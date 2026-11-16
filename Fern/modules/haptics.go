package modules

// What makes the controllers buzz: the heartbeat, someone joining, an avatar
// parameter, and the "Buzz the controllers" rule action. The buzz itself is
// played by the SteamVR worker (vrinput_worker.go).

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Rule IDs for SteamVR haptics and input.
const (
	ActionHaptic     = "vr.haptic"
	TriggerVRControl = "vr.control"
)

// hapticOpts is the haptic section as of the last check, so OSC handlers
// never copy the whole config.
var hapticOpts atomic.Pointer[HapticOptions]

func currentHapticOpts() HapticOptions {
	if p := hapticOpts.Load(); p != nil {
		return *p
	}
	return HapticOptions{}
}

func refreshHapticOpts(c Config) {
	h := c.VRInput.Haptics
	hapticOpts.Store(&h)
}

var startHapticsOnce sync.Once

// startHaptics starts the heartbeat loop, the parameter watch and the
// controller parameter sender.
func startHaptics() {
	startHapticsOnce.Do(func() {
		refreshHapticOpts(GetConfig())
		go vrParamSender()
		go heartbeatHaptics()
		SubscribeOSCPrefix(AvatarParamPrefix, func(msg *osc.Message) {
			hapticParamMessage(msg)
		})
		OnAvatarChange(func(string) {
			vrResend.Store(true)
			hapticParam.reset()
		})
	})
}

// heartbeatHaptics buzzes once per beat at the current heart rate, and
// keeps hapticOpts fresh.
func heartbeatHaptics() {
	var next time.Time
	for {
		now := time.Now()
		cfg := GetConfig()
		refreshHapticOpts(cfg)
		h := cfg.VRInput.Haptics
		wait := 250 * time.Millisecond
		if h.Enabled && h.Heartbeat.On {
			samples, last := heartSnapshot()
			r := readHeart(cfg.HeartRate, samples, last, now)
			if period, ok := beatPeriod(r); ok {
				if next.IsZero() || next.Sub(now) > period {
					next = now
				}
				if !now.Before(next) {
					next = now.Add(period)
					QueueHaptic(h.Heartbeat, "heartbeat")
				}
				wait = min(wait, max(next.Sub(now), 10*time.Millisecond))
			} else {
				next = time.Time{}
			}
		} else {
			next = time.Time{}
		}
		time.Sleep(wait)
	}
}

// beatPeriod is the time between beats, for a live reading of 30 bpm or more.
func beatPeriod(r heartReading) (time.Duration, bool) {
	if !r.ok || r.bpm < 30 {
		return 0, false
	}
	return time.Minute / time.Duration(r.bpm), true
}

// hapticPlayerJoined buzzes for a join seen live in the log. Your own join
// never buzzes. It is called with logMu held and never blocks.
func hapticPlayerJoined(data map[string]string) {
	if data["self"] == "1" {
		return
	}
	h := currentHapticOpts()
	if h.Enabled && h.Joined.On {
		QueueHaptic(h.Joined, "player joined")
	}
}

// hapticParam remembers the watched parameter's last value.
var hapticParam paramWatch

type paramWatch struct {
	mu    sync.Mutex
	name  string
	known bool
	last  float64
}

func (w *paramWatch) reset() {
	w.mu.Lock()
	w.known = false
	w.mu.Unlock()
}

// fire reports whether a new value of the watched parameter should buzz.
// The first value after an avatar change only teaches it the value.
func (w *paramWatch) fire(name, mode string, v float64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !strings.EqualFold(w.name, name) {
		w.name, w.known = name, false
	}
	prev, known := w.last, w.known
	w.last, w.known = v, true
	if !known {
		return false
	}
	switch mode {
	case HapticParamChange:
		return math.Abs(v-prev) > 1e-6
	default: // on
		return prev <= 0 && v > 0
	}
}

func hapticParamMessage(msg *osc.Message) {
	h := currentHapticOpts()
	if !h.Enabled || !h.Param.On || h.ParamName == "" || len(msg.Arguments) == 0 {
		return
	}
	name := strings.TrimPrefix(msg.Address, AvatarParamPrefix)
	if !strings.EqualFold(name, h.ParamName) {
		return
	}
	v, ok := oscNumber(msg.Arguments[0])
	if !ok {
		return
	}
	if hapticParam.fire(name, h.ParamMode, v) {
		QueueHaptic(h.Param, "parameter "+name)
	}
}

// TestHapticFromPanel plays a pattern for the panel's test button and
// reports the result as the page's notice.
func TestHapticFromPanel(which string) {
	if err := TestHaptic(which); err != nil {
		setVRInputNotice(err.Error(), true)
		return
	}
	setVRInputNotice("Buzzing the controllers", false)
}

// TestHaptic plays one of the configured patterns from the panel, even when
// it is switched off.
func TestHaptic(which string) error {
	h := GetConfig().VRInput.Haptics
	var p HapticPattern
	switch which {
	case "heartbeat":
		p = h.Heartbeat
	case "joined":
		p = h.Joined
	case "param":
		p = h.Param
	default:
		return errors.New("unknown haptic")
	}
	if !SteamVRState().Connected {
		return errors.New("SteamVR is not connected. Turn haptics or controller input on while SteamVR runs.")
	}
	if !QueueHaptic(p, "test") {
		return errors.New("too many buzzes queued")
	}
	return nil
}

/* ---------------- rules ---------------- */

var hapticHands = []RuleOption{{HandBoth, "Both"}, {HandLeft, "Left"}, {HandRight, "Right"}}

func init() {
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionHaptic, Label: "Buzz the controllers", Category: "SteamVR",
		Description: "Vibrates your VR controllers. Needs SteamVR running and haptics or controller input on.",
		Fields: []RuleField{
			{Key: "hand", Label: "Controller", Type: FieldSelect, Options: hapticHands},
			{Key: "strength", Label: "Strength %", Type: FieldNumber, Min: 1, Max: 100, Step: 1, Default: 60.0},
			{Key: "duration", Label: "Length (ms)", Type: FieldNumber, Min: hapticMinMS, Max: hapticMaxMS, Step: 1, Default: 120.0},
			{Key: "pulses", Label: "Pulses", Type: FieldNumber, Min: 1, Max: hapticMaxPulses, Step: 1, Default: 1.0},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			pat := HapticPattern{
				On:         true,
				Hand:       p.String("hand"),
				Strength:   int(p.Number("strength")),
				DurationMS: int(p.Number("duration")),
				Pulses:     int(p.Number("pulses")),
			}
			if !QueueHaptic(pat, "rule") {
				return nil, errors.New("too many buzzes queued")
			}
			return nil, nil
		},
	}))
	dirs := make([]RuleOption, 0, len(vrDirNames8))
	for _, n := range vrDirNames8 {
		dirs = append(dirs, RuleOption{Value: n, Label: strings.ToUpper(n[:1]) + n[1:]})
	}
	thumbs := []RuleOption{{"none", "Nothing"}, {"a", "A / X"}, {"b", "B / Y"}, {"pad", "Trackpad"}, {"stick", "Thumbstick"}}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerVRControl, Label: "Controller input", Category: "SteamVR",
		Description: "While a thumbstick or trackpad points a way, or your thumb rests on a button. Needs controller input on.",
		Fields: []RuleField{
			{Key: "hand", Label: "Controller", Type: FieldSelect, Options: []RuleOption{{HandLeft, "Left"}, {HandRight, "Right"}, {"either", "Either"}}},
			{Key: "control", Label: "Control", Type: FieldSelect, Options: []RuleOption{{"stick", "Thumbstick direction"}, {"pad", "Trackpad direction"}, {"thumb", "Thumb rests on"}}},
			{Key: "dir", Label: "Direction", Type: FieldSelect, Options: dirs, Default: "up", ShowIf: &RuleShowIf{Key: "control", Values: []string{"stick", "pad"}}},
			{Key: "thumb", Label: "Rests on", Type: FieldSelect, Options: thumbs, Default: "a", ShowIf: &RuleShowIf{Key: "control", Values: []string{"thumb"}}},
		},
		Check: func(p RuleParams) bool { return checkVRControl(p, time.Now()) },
	}))
}

var thumbValues = map[string]int{"none": ThumbNone, "a": ThumbA, "b": ThumbB, "pad": ThumbPad, "stick": ThumbStick}

// checkVRControl tests the latest controller reading against a rule.
// Directions are always eight-way here, so "up" means within 22.5 degrees of
// straight up whatever the parameters use. A reading older than two seconds
// counts as nothing.
func checkVRControl(p RuleParams, now time.Time) bool {
	vrDerived.Lock()
	dirs, thumbs, at := vrDerived.dirs, vrDerived.thumbs, vrDerived.at
	vrDerived.Unlock()
	if at.IsZero() || now.Sub(at) > 2*time.Second {
		return false
	}
	hands := []int{0, 1}
	switch p.String("hand") {
	case HandLeft:
		hands = []int{0}
	case HandRight:
		hands = []int{1}
	}
	for _, h := range hands {
		switch p.String("control") {
		case "thumb":
			if want, ok := thumbValues[p.String("thumb")]; ok && thumbs[h] == want {
				return true
			}
		default:
			c := 0
			if p.String("control") == "pad" {
				c = 1
			}
			if d := dirs[h][c]; d >= 0 && d < len(vrDirNames8) && vrDirNames8[d] == p.String("dir") {
				return true
			}
		}
	}
	return false
}
