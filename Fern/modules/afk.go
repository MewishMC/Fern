package modules

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Activity sources. Each one keeps its own last-seen time, and the AFK timer
// runs from the newest time among the sources the user picked, so changing
// the selection applies straight away instead of only to new activity.
const (
	AfkSourceMovement = "movement" // VRChat OSC: Velocity and AngularY
	AfkSourceVoice    = "voice"    // VRChat OSC: Voice
	AfkSourceOSC      = "osc"      // other VRChat OSC activity (AngularX, Input)
	AfkSourceWindow   = "window"   // switching the focused window
	AfkSourceInput    = "input"    // keyboard and mouse, from GetLastInputInfo

	AfkInputAlways  = "always"
	AfkInputDesktop = "desktop" // only while no VR runtime is running
	AfkInputOff     = "off"
)

var afkSourceOrder = []string{AfkSourceMovement, AfkSourceVoice, AfkSourceOSC, AfkSourceWindow, AfkSourceInput}

var (
	activityMu      sync.RWMutex
	activityAt      = startActivity()
	prevWindowTitle string
	voiceActive     bool

	// inputIdleFn and inVRFn are swapped out by tests.
	inputIdleFn = inputIdle
	inVRFn      = InVR
)

func startActivity() map[string]time.Time {
	now := time.Now()
	at := make(map[string]time.Time, len(afkSourceOrder))
	for _, id := range afkSourceOrder {
		at[id] = now
	}
	return at
}

func markActivity(source string) {
	activityMu.Lock()
	activityAt[source] = time.Now()
	activityMu.Unlock()
}

// afkSources reports which sources count as activity for these options.
// "Only OSC movement" keeps its old meaning and wins over the rest.
func afkSources(o AfkOptions, inVR bool) map[string]bool {
	if o.MovementOnly {
		return map[string]bool{AfkSourceMovement: true}
	}
	input := o.InputMode == AfkInputAlways || (o.InputMode == AfkInputDesktop && !inVR)
	return map[string]bool{
		AfkSourceMovement: o.UseMovement,
		AfkSourceVoice:    o.UseVoice,
		AfkSourceOSC:      o.UseOSC,
		AfkSourceWindow:   o.UseWindow,
		AfkSourceInput:    input,
	}
}

// sourceTimes is the last activity per source right now. Keyboard and mouse
// are read live from Windows rather than polled.
func sourceTimes(now time.Time) map[string]time.Time {
	activityMu.RLock()
	at := make(map[string]time.Time, len(activityAt)+1)
	for k, v := range activityAt {
		at[k] = v
	}
	activityMu.RUnlock()
	if idle, ok := inputIdleFn(); ok {
		at[AfkSourceInput] = now.Add(-idle)
	}
	return at
}

// afkStatus decides whether someone counts as AFK. A manual AFK is on until it
// is turned off, whatever the detection says. Without any source to listen
// to, nobody is ever detected as AFK.
func afkStatus(o AfkOptions, now time.Time, at map[string]time.Time, inVR bool) (active bool, idle time.Duration) {
	if o.Manual {
		idle = now.Sub(time.UnixMilli(o.ManualSince))
		if idle < 0 {
			idle = 0
		}
		return true, idle
	}
	if o.Bypass || !o.AutoDetect {
		return false, 0
	}
	var last time.Time
	listening := false
	for id, on := range afkSources(o, inVR) {
		if !on {
			continue
		}
		listening = true
		if t := at[id]; t.After(last) {
			last = t
		}
	}
	if !listening {
		return false, 0
	}
	idle = now.Sub(last)
	if idle < 0 {
		idle = 0
	}
	return idle.Seconds() >= o.AFKTime, idle
}

// AfkInfo reports whether the user counts as afk and for how long.
func AfkInfo() (active bool, idle time.Duration) {
	o := GetConfig().AfkOptions
	now := time.Now()
	return afkStatus(o, now, sourceTimes(now), inVRFn())
}

// AfkSourceState is one activity source as the panel shows it.
type AfkSourceState struct {
	ID      string `json:"id"`
	Counted bool   `json:"counted"`
	AgoMS   int64  `json:"agoMs"`
}

// AfkSourcesNow lists every source, whether it counts with the current
// options and how long ago it last saw activity.
func AfkSourcesNow() []AfkSourceState {
	o := GetConfig().AfkOptions
	now := time.Now()
	at := sourceTimes(now)
	counted := afkSources(o, inVRFn())
	out := make([]AfkSourceState, 0, len(afkSourceOrder))
	for _, id := range afkSourceOrder {
		out = append(out, AfkSourceState{ID: id, Counted: counted[id], AgoMS: now.Sub(at[id]).Milliseconds()})
	}
	return out
}

// Talking reports whether the user spoke recently.
func Talking() bool {
	activityMu.RLock()
	defer activityMu.RUnlock()
	return voiceActive
}

// afkLine renders the afk module line using the configured options.
func afkLine(cfg Config) string {
	active, duration := AfkInfo()
	if !active {
		return ""
	}
	return renderAfk(cfg.AfkOptions, duration, time.Now(), cfg.TimeOptions.Use24h)
}

// renderAfk writes the AFK line. The timer text is a template: {time} is the
// idle time and {since} the clock time AFK started; without either, the
// timer goes at the end. The text without a timer falls back to the timer
// text with its placeholders taken out.
func renderAfk(o AfkOptions, idle time.Duration, now time.Time, use24h bool) string {
	timerText := strings.TrimSpace(o.AFKMessage)
	if timerText == "" {
		timerText = "AFK"
	}
	var text string
	if o.ShowTimer {
		if !strings.Contains(timerText, "{time}") && !strings.Contains(timerText, "{since}") {
			timerText += " {time}"
		}
		layout := "3:04 PM"
		if use24h {
			layout = "15:04"
		}
		text = strings.NewReplacer(
			"{time}", StyleText(formatIdle(idle, o.TimerStyle), o.TimerFont),
			"{since}", StyleText(now.Add(-idle).Format(layout), o.TimerFont),
		).Replace(timerText)
	} else {
		text = strings.TrimSpace(o.PlainMessage)
		if text == "" {
			text = strings.NewReplacer("{time}", "", "{since}", "").Replace(timerText)
			text = strings.Trim(strings.Join(strings.Fields(text), " "), " ·-|:")
		}
		if text == "" {
			text = "AFK"
		}
	}
	return withLabel(o.Icon, strings.TrimSpace(text))
}

// formatIdle renders the idle time in one of the styles the options offer.
func formatIdle(d time.Duration, style string) string {
	total := int(d.Seconds())
	if total < 0 {
		total = 0
	}
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	switch style {
	case "short":
		switch {
		case hours > 0:
			return fmt.Sprintf("%dh %02dm", hours, minutes)
		case minutes > 0:
			return fmt.Sprintf("%dm %02ds", minutes, secs)
		default:
			return fmt.Sprintf("%ds", secs)
		}
	case "minutes":
		if total < 60 {
			return fmt.Sprintf("%ds", total)
		}
		return fmt.Sprintf("%dm", total/60)
	case "compact":
		switch {
		case hours > 0 && minutes > 0:
			return fmt.Sprintf("%dh %dm", hours, minutes)
		case hours > 0:
			return fmt.Sprintf("%dh", hours)
		case minutes > 0:
			return fmt.Sprintf("%dm", minutes)
		default:
			return fmt.Sprintf("%ds", secs)
		}
	case "words":
		unit := func(n int, name string) string {
			if n == 1 {
				return "1 " + name
			}
			return fmt.Sprintf("%d %ss", n, name)
		}
		switch {
		case hours > 0 && minutes > 0:
			return unit(hours, "hour") + " " + unit(minutes, "minute")
		case hours > 0:
			return unit(hours, "hour")
		case minutes > 0:
			return unit(minutes, "minute")
		default:
			return unit(secs, "second")
		}
	default:
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	}
}

// oscSource sorts a VRChat OSC address into an activity source.
func oscSource(address string) (string, bool) {
	switch {
	case containsAny(address, "/Velocity", "/AngularY"):
		return AfkSourceMovement, true
	case strings.Contains(address, "/Voice"):
		return AfkSourceVoice, true
	case containsAny(address, "/AngularX", "/Input"):
		return AfkSourceOSC, true
	}
	return "", false
}

func handleAddress(address string) {
	source, ok := oscSource(address)
	if !ok {
		return
	}
	markActivity(source)
	if source == AfkSourceVoice {
		activityMu.Lock()
		voiceActive = true
		activityMu.Unlock()
	}
}

func handlePackage(pkt osc.Packet) {
	noteMuteSelf(pkt)
	switch pk := pkt.(type) {
	case *osc.Message:
		handleAddress(pk.Address)
	case *osc.Bundle:
		for _, m := range pk.Messages {
			handleAddress(m.Address)
		}
	}
}

func containsAny(target string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(target, sub) {
			return true
		}
	}
	return false
}

func startAfkWorker() {
	go listenOSC()
	go watchWindowActivity()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			activityMu.Lock()
			voiceActive = false
			activityMu.Unlock()
		}
	}()
}

// listenOSC listens for VRChat's outgoing OSC so activity can be detected.
// A busy port is not fatal (another OSC app may own it), but the panel shows
// it, and the port is retried in case it frees up.
func listenOSC() {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9001}
	buf := make([]byte, 2048)
	for {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			setOSCListener(false, err)
			time.Sleep(5 * time.Second)
			continue
		}
		setOSCListener(true, nil)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			noteOSCPacket()
			pkt, err := osc.ParsePacket(string(buf[:n]))
			if err != nil {
				continue
			}
			handlePackage(pkt)
		}
		conn.Close()
		setOSCListener(false, nil)
		time.Sleep(time.Second)
	}
}

// watchWindowActivity treats switching windows as activity, but only samples
// once every 1.5s instead of on every OSC packet.
func watchWindowActivity() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if !GetConfig().AfkOptions.AutoDetect {
			continue
		}
		title := currentForeground().Title
		activityMu.Lock()
		if title != "" && title != prevWindowTitle {
			prevWindowTitle = title
			activityAt[AfkSourceWindow] = time.Now()
		}
		activityMu.Unlock()
	}
}

// AfkPreset is a named look for the AFK line. Applying one sets the icon,
// both texts and the timer format.
type AfkPreset struct {
	ID         string
	Name       string
	Icon       string
	Message    string
	Plain      string
	TimerStyle string
	TimerFont  string
}

// AfkPresets are the built in AFK styles, in picker order.
var AfkPresets = []AfkPreset{
	{"classic", "Classic", "💤", "AFK", "", "full", ""},
	{"minimal", "Minimal", "", "AFK", "", "compact", ""},
	{"sleepy", "Sleepy", "💤", "away for {time}", "away", "words", ""},
	{"smallcaps", "Small caps", "", StyleText("AFK", "smallcaps") + " · {time}", StyleText("AFK", "smallcaps"), "compact", ""},
	{"tiny", "Tiny timer", "💤", "AFK {time}", "AFK", "short", "superscript"},
	{"brb", "Be right back", "🌙", StyleText("brb", "smallcaps") + " · {time}", StyleText("brb", "smallcaps"), "compact", ""},
	{"since", "Away since", "⏾", "away since {since}", "away", "compact", ""},
	{"bold", "Bold", "", StyleText("AFK", "sans") + " {time}", StyleText("AFK", "sans"), "short", "sans"},
	{"fullwidth", "Fullwidth", "", StyleText("AFK", "fullwidth") + " {time}", StyleText("AFK", "fullwidth"), "compact", "fullwidth"},
	{"break", "On a break", "☕", "on a break · {time}", "on a break", "words", ""},
	{"brackets", "Brackets", "", "[AFK] {time}", "[AFK]", "full", ""},
	{"mono", "Monospace", "⌛", StyleText("away", "mono") + " {time}", StyleText("away", "mono"), "short", "mono"},
	{"zzz", "Zzz", "", StyleText("zzz", "superscript") + " {time}", StyleText("zzz", "superscript"), "compact", "superscript"},
	{"double", "Starry", "✦", StyleText("AFK", "double") + " {time}", StyleText("AFK", "double"), "compact", "double"},
}

func (p AfkPreset) apply(o *AfkOptions) {
	o.Icon = p.Icon
	o.AFKMessage = p.Message
	o.PlainMessage = p.Plain
	o.TimerStyle = p.TimerStyle
	o.TimerFont = p.TimerFont
	o.ShowTimer = true
}

// ApplyAfkPreset switches the AFK line to a built in style.
func ApplyAfkPreset(id string) bool {
	for _, p := range AfkPresets {
		if p.ID == id {
			UpdateConfig(func(c *Config) { p.apply(&c.AfkOptions) })
			return true
		}
	}
	return false
}

// AfkPresetMatch is the id of the preset the options look like, or "".
func AfkPresetMatch(o AfkOptions) string {
	for _, p := range AfkPresets {
		if o.ShowTimer && o.Icon == p.Icon && o.AFKMessage == p.Message && o.PlainMessage == p.Plain &&
			o.TimerStyle == p.TimerStyle && o.TimerFont == p.TimerFont {
			return p.ID
		}
	}
	return ""
}

// afkSampleIdle is the idle time previews are drawn with.
const afkSampleIdle = 5*time.Minute + 12*time.Second

// AfkSample is a preview of one AFK look.
type AfkSample struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Sample string `json:"sample"`
}

// AfkPresetSamples previews every preset, plus the current settings under
// the id "current", as they would look after five minutes away.
func AfkPresetSamples(cfg Config) []AfkSample {
	now := time.Now()
	use24h := cfg.TimeOptions.Use24h
	out := make([]AfkSample, 0, len(AfkPresets)+1)
	out = append(out, AfkSample{ID: "current", Name: "Current", Sample: renderAfk(cfg.AfkOptions, afkSampleIdle, now, use24h)})
	for _, p := range AfkPresets {
		o := cfg.AfkOptions
		p.apply(&o)
		out = append(out, AfkSample{ID: p.ID, Name: p.Name, Sample: renderAfk(o, afkSampleIdle, now, use24h)})
	}
	return out
}

// sanitize keeps the AFK options inside what renderAfk handles. The texts
// are one chatbox line each. Spaces are kept as typed (they are trimmed when
// rendering), so a space typed at the end of a field does not vanish.
func (o *AfkOptions) sanitize() {
	line := func(s string, n int) string {
		return RuneLimit(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s), n)
	}
	o.AFKMessage = line(o.AFKMessage, 80)
	o.PlainMessage = line(o.PlainMessage, 80)
	o.Icon = line(o.Icon, 8)
	if _, ok := textStyleTables[o.TimerFont]; !ok || o.TimerFont == "plain" {
		o.TimerFont = ""
	}
	switch o.InputMode {
	case AfkInputAlways, AfkInputDesktop, AfkInputOff:
	default:
		o.InputMode = AfkInputAlways
	}
	if !o.Manual {
		o.ManualSince = 0
	} else if o.ManualSince <= 0 || o.ManualSince > time.Now().UnixMilli() {
		o.ManualSince = time.Now().UnixMilli()
	}
}
