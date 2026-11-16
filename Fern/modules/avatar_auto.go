package modules

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// The avatar worker: everything Fern does with the avatar you wear apart
// from rules. It
//   - remembers avatars and switches to the profile picked for one (A2),
//   - turns avatar parameters into media keys (A6),
//   - sends VRChat events, the time and the PC's load as parameters (A8, A9),
//   - saves picked parameter values and puts them back (A10).

// Media actions avatar parameters can trigger.
const (
	MediaPlayPause  = "playpause"
	MediaNext       = "next"
	MediaPrev       = "prev"
	MediaVolumeUp   = "volup"
	MediaVolumeDown = "voldown"
	MediaMute       = "mute"
)

// avatarSettle is how long after an avatar change Fern waits before sending
// its own parameters, so VRChat's burst of the avatar's defaults goes first.
const avatarSettle = time.Second

// AvatarSentValue is the last value Fern sent to a parameter.
type AvatarSentValue struct {
	Value any   `json:"value"`
	At    int64 `json:"at"` // unix ms
}

// AvatarAutoStatus is what the Avatar page shows about the automation.
type AvatarAutoStatus struct {
	Profile    string `json:"profile"`    // last profile switched to for an avatar
	ProfileErr string `json:"profileErr"` // why the last switch failed
	ProfileAt  int64  `json:"profileAt"`
	Restored   int    `json:"restored"` // values put back on the last change
	RestoredAt int64  `json:"restoredAt"`
}

var (
	avatarOnce sync.Once
	avatarOpts atomic.Pointer[AvatarOptions]

	avMu         sync.Mutex
	avChangedAt  time.Time
	avChangeID   string
	avNameAtSwap string
	avSettleTill time.Time       // auto-save ignores values until then
	avRestore    *time.Timer     // pending restore after a change
	avMediaOn    map[string]bool // last on/off of each media parameter
	avState      map[string]any  // state parameters as last sent
	avSent       = map[string]AvatarSentValue{}
	avPulses     = map[string]*time.Timer{}
	avStatus     AvatarAutoStatus
	avAutoSave   map[string]bool // auto-saved parameters of the current avatar

	// Swapped by tests.
	avatarNow      = time.Now
	avatarMediaFn  = runMediaAction
	avatarSwitchFn = SwitchProfile
	avatarLogQueue = make(chan avatarLogEvent, 64)
)

type avatarLogEvent struct {
	kind string
	self bool
}

func currentAvatarOpts() AvatarOptions {
	if o := avatarOpts.Load(); o != nil {
		return *o
	}
	o := GetConfig().Avatar
	avatarOpts.Store(&o)
	return o
}

// startAvatarWorker hooks the avatar automation up to the OSC listener and
// starts its once-a-second loop.
func startAvatarWorker() {
	avatarOnce.Do(func() {
		OnAvatarChange(func(id string) { go handleAvatarChange(id) })
		SubscribeOSCPrefix(AvatarParamPrefix, onAvatarParamMsg)
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case ev := <-avatarLogQueue:
					handleAvatarLogEvent(ev)
				case <-tick.C:
					tickAvatar(GetConfig())
				}
			}
		}()
	})
}

// noteVRCEvent reports a live VRChat log event to the avatar automation and
// the rules. The log reader calls it with logMu held, so it never blocks.
func noteVRCEvent(kind string, data map[string]string) {
	switch kind {
	case EventScreenshot:
		FireTrigger(TriggerScreenshot, nil)
	case EventLoaded:
		FireTrigger(TriggerWorldLoaded, nil)
	}
	select {
	case avatarLogQueue <- avatarLogEvent{kind: kind, self: data["self"] == "1"}:
	default:
	}
}

// sendAvatarOut sends a parameter and remembers it for the Avatar page.
func sendAvatarOut(name string, v any) error {
	if err := SendAvatarParam(name, v); err != nil {
		return err
	}
	avMu.Lock()
	if len(avSent) > 512 {
		avSent = map[string]AvatarSentValue{}
	}
	avSent[name] = AvatarSentValue{Value: v, At: avatarNow().UnixMilli()}
	avMu.Unlock()
	return nil
}

// AvatarSent is the last value Fern sent to each parameter.
func AvatarSent() map[string]AvatarSentValue {
	avMu.Lock()
	defer avMu.Unlock()
	out := make(map[string]AvatarSentValue, len(avSent))
	for k, v := range avSent {
		out[k] = v
	}
	return out
}

// CurrentAvatarAuto is the automation's status for the panel.
func CurrentAvatarAuto() AvatarAutoStatus {
	avMu.Lock()
	defer avMu.Unlock()
	return avStatus
}

// handleAvatarChange runs off the listener goroutine after /avatar/change.
func handleAvatarChange(id string) {
	cfg := GetConfig()
	o := cfg.Avatar
	avatarOpts.Store(&o)
	now := avatarNow()
	noteAvatarWorn(id)
	entry, _ := AvatarInfo(id)

	avMu.Lock()
	avChangedAt, avChangeID, avNameAtSwap = now, id, CurrentAvatarName()
	avSettleTill = now.Add(time.Duration(o.Sync.DelayMS)*time.Millisecond + avatarSettle)
	avMediaOn = nil
	avState = nil // the new avatar starts from its defaults: send everything again
	if avRestore != nil {
		avRestore.Stop()
		avRestore = nil
	}
	avAutoSave = autoSaveSet(entry)
	if o.Sync.Restore && entry.Restore && len(entry.Values) > 0 {
		values := avatarParamValues(entry)
		avRestore = time.AfterFunc(time.Duration(o.Sync.DelayMS)*time.Millisecond, func() {
			if CurrentAvatarID() != id {
				return
			}
			n, _ := sendAvatarValues(values)
			avMu.Lock()
			avStatus.Restored, avStatus.RestoredAt = n, avatarNow().UnixMilli()
			avMu.Unlock()
		})
	}
	avMu.Unlock()

	if o.AutoProfile {
		target := entry.Profile
		if target == "" {
			target = o.FallbackProfile
		}
		if target != "" && foldName(target) != foldName(ActiveProfile()) {
			err := avatarSwitchFn(target)
			avMu.Lock()
			avStatus.ProfileAt = avatarNow().UnixMilli()
			if err != nil {
				avStatus.Profile, avStatus.ProfileErr = target, err.Error()
				fmt.Println("avatar: could not switch profile:", err)
			} else {
				avStatus.Profile, avStatus.ProfileErr = target, ""
			}
			avMu.Unlock()
		}
	}
}

func autoSaveSet(e AvatarEntry) map[string]bool {
	if !e.AutoSave {
		return nil
	}
	set := make(map[string]bool, len(e.Params))
	for _, p := range e.Params {
		set[p] = true
	}
	return set
}

// refreshAutoSave picks up changes to the current avatar's sync settings.
func refreshAutoSave() {
	id := CurrentAvatarID()
	entry, _ := AvatarInfo(id)
	set := autoSaveSet(entry)
	avMu.Lock()
	avAutoSave = set
	avMu.Unlock()
}

// paramOn reads a parameter as a button: bool true, int above 0, float above
// half.
func paramOn(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case int32:
		return n > 0
	case float32:
		return n > 0.5
	}
	return false
}

// mediaParamAction is the media action a parameter is set up for, or "".
func mediaParamAction(o AvatarMediaOptions, name string) string {
	if !o.Enabled || name == "" {
		return ""
	}
	switch name {
	case o.PlayPause:
		return MediaPlayPause
	case o.Next:
		return MediaNext
	case o.Previous:
		return MediaPrev
	case o.VolumeUp:
		return MediaVolumeUp
	case o.VolumeDown:
		return MediaVolumeDown
	case o.Mute:
		return MediaMute
	}
	return ""
}

// onAvatarParamMsg runs on the OSC listener goroutine for every parameter
// VRChat sends, so it only looks things up and hands work to goroutines.
func onAvatarParamMsg(msg *osc.Message) {
	if len(msg.Arguments) == 0 {
		return
	}
	name := strings.TrimPrefix(msg.Address, AvatarParamPrefix)
	value := msg.Arguments[0]
	o := currentAvatarOpts()
	now := avatarNow()

	avMu.Lock()
	var action string
	if a := mediaParamAction(o.Media, name); a != "" {
		on := paramOn(value)
		if avMediaOn == nil {
			avMediaOn = map[string]bool{}
		}
		// Values in VRChat's burst after a change are the avatar's
		// defaults, not presses.
		if on && !avMediaOn[name] && !now.Before(avChangedAt.Add(avatarSettle)) {
			action = a
		}
		avMediaOn[name] = on
	}
	save := avAutoSave[name] && !now.Before(avSettleTill)
	id := avChangeID
	avMu.Unlock()

	if action != "" {
		go avatarMediaFn(action)
	}
	if save && id != "" && id == CurrentAvatarID() {
		go storeAvatarValue(id, name, value)
	}
}

// runMediaAction does a media action: the media session's controls for
// play, pause and skipping, the volume keys for the volume.
func runMediaAction(action string) {
	var err error
	switch action {
	case MediaVolumeUp:
		err = tapKeys([]uint16{vkVolumeUp})
	case MediaVolumeDown:
		err = tapKeys([]uint16{vkVolumeDown})
	case MediaMute:
		err = tapKeys([]uint16{vkVolumeMute})
	default:
		err = MediaControl(action)
	}
	if err != nil {
		fmt.Println("avatar: media", action+":", err)
	}
}

// handleAvatarLogEvent pulses the parameter set up for a VRChat event.
func handleAvatarLogEvent(ev avatarLogEvent) {
	o := currentAvatarOpts().Events
	if !o.Enabled {
		return
	}
	name := ""
	switch ev.kind {
	case EventJoin:
		if !ev.self {
			name = o.PlayerJoined
		}
	case EventLeave:
		name = o.PlayerLeft
	case EventLoaded:
		name = o.WorldLoaded
	case EventScreenshot:
		name = o.Screenshot
	}
	if name != "" {
		pulseParam(name, time.Duration(o.PulseMS)*time.Millisecond)
	}
}

// pulseParam makes a bool true for d. Another pulse meanwhile makes it last
// longer instead of flickering.
func pulseParam(name string, d time.Duration) {
	if err := sendAvatarOut(name, true); err != nil {
		return
	}
	avMu.Lock()
	defer avMu.Unlock()
	if t := avPulses[name]; t != nil {
		t.Stop()
	}
	avPulses[name] = time.AfterFunc(d, func() {
		avMu.Lock()
		delete(avPulses, name)
		avMu.Unlock()
		_ = sendAvatarOut(name, false)
	})
}

// stateParam is one parameter the loop keeps up to date.
type stateParam struct {
	Name  string
	Value any
}

// avatarStateParams works out the parameters that follow a state: AFK, the
// player count, whether media plays, the time and the PC's load.
func avatarStateParams(cfg Config, now time.Time, read avatarReadings) []stateParam {
	o := cfg.Avatar
	var out []stateParam
	add := func(name string, v any) {
		if name != "" {
			out = append(out, stateParam{name, v})
		}
	}
	if o.Events.Enabled {
		add(o.Events.AFK, read.afk)
		add(o.Events.PlayerCount, clampInt(read.players, 0, 255))
	}
	if o.Media.Enabled {
		add(o.Media.Playing, read.playing)
	}
	if o.Stats.Enabled {
		t := now.In(zone(cfg.TimeOptions.Zone))
		h, m, s := t.Hour(), t.Minute(), t.Second()
		hour, dial := h, 24.0
		if o.Stats.Hour12 {
			hour, dial = h%12, 12
			if hour == 0 {
				hour = 12
			}
		}
		add(o.Stats.Hour, hour)
		add(o.Stats.Minute, m)
		add(o.Stats.Second, s)
		add(o.Stats.HourFloat, round4((float64(h%int(dial))+float64(m)/60)/dial))
		add(o.Stats.MinuteFloat, round4((float64(m)+float64(s)/60)/60))
		add(o.Stats.SecondFloat, round4(float64(s)/60))
		add(o.Stats.CPU, round4(unit(read.cpu)))
		if read.hasGPU {
			add(o.Stats.GPU, round4(unit(read.gpu)))
		}
		add(o.Stats.RAM, round4(unit(read.ram)))
	}
	return out
}

func unit(percent float64) float64 {
	if math.IsNaN(percent) {
		return 0
	}
	return math.Max(0, math.Min(1, percent/100))
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// avatarReadings are the numbers the state parameters come from.
type avatarReadings struct {
	afk     bool
	players int
	playing bool
	cpu     float64
	gpu     float64
	hasGPU  bool
	ram     float64
}

var avatarReadFn = func(cfg Config) avatarReadings {
	afk, _ := AfkInfo()
	_, _, ram := MemoryStats()
	r := avatarReadings{afk: afk, players: PlayerCount(), playing: CurrentMedia().Playing, cpu: CpuUsage(), ram: ram}
	if g, ok := pickGPU(GPUs(), cfg.GPUOptions.Adapter); ok {
		r.gpu, r.hasGPU = g.Usage, true
	}
	return r
}

// sameParamValue reports whether a state parameter would not change.
func sameParamValue(a, b any) bool {
	fa, okA := a.(float64)
	fb, okB := b.(float64)
	if okA && okB {
		return math.Abs(fa-fb) < 0.004
	}
	return a == b
}

// tickAvatar runs once a second.
func tickAvatar(cfg Config) {
	o := cfg.Avatar
	avatarOpts.Store(&o)
	now := avatarNow()
	id := CurrentAvatarID()
	refreshAutoSave()

	avMu.Lock()
	changedAt, nameAtSwap := avChangedAt, avNameAtSwap
	avMu.Unlock()
	// The log names the avatar around the time OSC reports the change; a
	// name that differs from the old one, or one that has stood a few
	// seconds, belongs to the new avatar.
	if id != "" {
		if name := CurrentAvatarName(); name != "" && (name != nameAtSwap || now.Sub(changedAt) > 4*time.Second) {
			noteAvatarName(id, name)
		}
	}
	if id == "" || now.Sub(changedAt) < avatarSettle {
		return
	}
	params := avatarStateParams(cfg, now, avatarReadFn(cfg))
	avMu.Lock()
	if avState == nil {
		avState = map[string]any{}
	}
	var send []stateParam
	for _, p := range params {
		if last, ok := avState[p.Name]; ok && sameParamValue(last, p.Value) {
			continue
		}
		send = append(send, p)
	}
	avMu.Unlock()
	for _, p := range send {
		if sendAvatarOut(p.Name, p.Value) == nil {
			avMu.Lock()
			if avState != nil {
				avState[p.Name] = p.Value
			}
			avMu.Unlock()
		}
	}
}

// AvatarParamRow is one parameter of the current avatar, for the panel.
type AvatarParamRow struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// CurrentAvatarParams lists the current avatar's parameters by name, up to
// limit.
func CurrentAvatarParams(limit int) []AvatarParamRow {
	params := AvatarParams()
	out := make([]AvatarParamRow, 0, len(params))
	for name, v := range params {
		row := AvatarParamRow{Name: name, Value: v}
		switch v.(type) {
		case bool:
			row.Type = "bool"
		case int32:
			row.Type = "int"
		case float32:
			row.Type = "float"
			f := float64(v.(float32))
			if math.IsNaN(f) || math.IsInf(f, 0) {
				f = 0
			}
			row.Value = round4(f)
		default:
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
