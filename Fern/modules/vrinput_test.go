package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Fern/modules/openvr"
)

func TestVRInputDefaultsSurviveSanitize(t *testing.T) {
	c := DefaultConfig()
	want := c.VRInput
	c.sanitize()
	if c.VRInput != want {
		t.Fatalf("sanitize changed the defaults:\n%+v\n%+v", c.VRInput, want)
	}
	if c.VRInput.Haptics.Enabled || c.VRInput.Input.Enabled {
		t.Error("haptics and input must start off")
	}
}

func TestVRInputSanitizeBounds(t *testing.T) {
	var o VRInputOptions // a config written before the section existed
	o.sanitize()
	def := defaultVRInputOptions()
	if o.Haptics.Heartbeat.Strength != def.Haptics.Heartbeat.Strength || o.Haptics.Joined.Pulses != def.Haptics.Joined.Pulses ||
		o.Input.RateHz != def.Input.RateHz || o.Input.Source != VRInputAuto || o.Haptics.ParamMode != HapticParamOn {
		t.Fatalf("zero values not defaulted: %+v", o)
	}
	o.Haptics.Heartbeat = HapticPattern{Hand: "feet", Strength: 900, DurationMS: -5, Pulses: 99}
	o.Haptics.ParamName = " /avatar/parameters/My Param!/x "
	o.Haptics.ParamMode = "sometimes"
	o.Input = ControllerInputOptions{Source: "magic", Prefix: "/In put/", RateHz: 999, Deadzone: 1}
	o.sanitize()
	hb := o.Haptics.Heartbeat
	if hb.Hand != def.Haptics.Heartbeat.Hand || hb.Strength != 100 || hb.DurationMS != hapticMinMS || hb.Pulses != hapticMaxPulses {
		t.Errorf("pattern not bounded: %+v", hb)
	}
	if o.Haptics.ParamName != "MyParam/x" || o.Haptics.ParamMode != HapticParamOn {
		t.Errorf("param %q mode %q", o.Haptics.ParamName, o.Haptics.ParamMode)
	}
	if o.Input.Source != VRInputAuto || o.Input.Prefix != "Input/" || o.Input.RateHz != 30 || o.Input.Deadzone != 10 {
		t.Errorf("input not bounded: %+v", o.Input)
	}
}

func TestVRInputConfigRoundTrip(t *testing.T) {
	c := DefaultConfig()
	c.VRInput.Haptics.Enabled = true
	c.VRInput.Input.Prefix = "Ctl/"
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"SteamVRInput":{"haptics":{"enabled":true`) {
		t.Fatalf("section missing from JSON")
	}
	back := DefaultConfig()
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	back.sanitize()
	if !back.VRInput.Haptics.Enabled || back.VRInput.Input.Prefix != "Ctl/" {
		t.Errorf("lost settings: %+v", back.VRInput)
	}
	if !vrInputWanted(back) || !heartHapticsWanted(back) {
		t.Error("enabled haptics should want SteamVR and the heart rate source")
	}
}

func TestVRDirection(t *testing.T) {
	cases := []struct {
		x, y  float64
		eight bool
		want  int
	}{
		{0, 0, false, 0},
		{0.2, 0.2, false, 0}, // inside the dead zone
		{0, 1, false, 1},
		{1, 0, false, 2},
		{0, -1, false, 3},
		{-1, 0, false, 4},
		{0.6, 0.7, false, 1}, // mostly up
		{0.7, 0.7, true, 2},  // up-right
		{0.7, -0.7, true, 4}, // down-right
		{-0.7, 0.7, true, 8}, // up-left
		{-0.1, 0.99, true, 1},
	}
	for _, c := range cases {
		if got := vrDirection(c.x, c.y, 0.5, c.eight); got != c.want {
			t.Errorf("vrDirection(%v, %v, eight=%v) = %d, want %d", c.x, c.y, c.eight, got, c.want)
		}
	}
}

func TestThumbPositionAndParams(t *testing.T) {
	h := vrHand{Present: true, ATouch: true, PadTouch: true, Stick: [2]float64{0, 1}, Pad: [2]float64{1, 0}, Trigger: 2, Grip: 0.5}
	if thumbPosition(h) != ThumbA {
		t.Error("A beats the trackpad")
	}
	h.ATouch = false
	if thumbPosition(h) != ThumbPad {
		t.Error("trackpad")
	}
	o := defaultVRInputOptions().Input
	o.Prefix = "X/"
	o.Fingers = true
	got := map[string]any{}
	for _, p := range vrHandParams(o, "Left", h) {
		got[p.Name] = p.Value
	}
	want := map[string]any{
		"X/LeftStickX": 0.0, "X/LeftStickY": 1.0, "X/LeftPadX": 1.0, "X/LeftPadY": 0.0, "X/LeftPadTouch": true,
		"X/LeftTrigger": 1.0, "X/LeftGrip": 0.5, "X/LeftThumb": ThumbPad, "X/LeftStickDir": 1, "X/LeftPadDir": 2,
		"X/LeftThumbCurl": 0.0, "X/LeftPinkyCurl": 0.0,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != 15 {
		t.Errorf("%d parameters: %v", len(got), got)
	}
	// An untouched pad has no direction.
	h.PadTouch = false
	if padDirection(h, 0.5, false) != 0 {
		t.Error("untouched pad pointed somewhere")
	}
	o = ControllerInputOptions{}
	if len(vrInputParams(o, h, h)) != 0 {
		t.Error("everything off still sends")
	}
}

func TestVRParamDiff(t *testing.T) {
	var d vrParamDiff
	first := d.changed([]vrParam{{"a", 0.5}, {"b", true}, {"c", 3}})
	if len(first) != 3 {
		t.Fatalf("first read sends everything, got %v", first)
	}
	if got := d.changed([]vrParam{{"a", 0.505}, {"b", true}, {"c", 3}}); len(got) != 0 {
		t.Errorf("tiny change sent: %v", got)
	}
	if got := d.changed([]vrParam{{"a", 0.6}, {"b", false}, {"c", 3}}); len(got) != 2 {
		t.Errorf("changes not sent: %v", got)
	}
	d.changed([]vrParam{{"a", 0.004}})
	if got := d.changed([]vrParam{{"a", 0.0}}); len(got) != 1 {
		t.Errorf("returning to exactly zero must be sent: %v", got)
	}
	d.reset()
	if got := d.changed([]vrParam{{"a", 0.0}, {"c", 3}}); len(got) != 2 {
		t.Errorf("reset did not resend: %v", got)
	}
}

func TestVRManifestBindingsUseKnownActions(t *testing.T) {
	dir := t.TempDir()
	path, err := writeVRManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("manifest path %q is not absolute", path)
	}
	var m struct {
		Defaults []struct {
			Type string `json:"controller_type"`
			URL  string `json:"binding_url"`
		} `json:"default_bindings"`
		Actions []struct{ Name, Type string } `json:"actions"`
	}
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	known := map[string]string{}
	for _, a := range m.Actions {
		known[a.Name] = a.Type
	}
	if known[vrHapticAction] != "vibration" || known[vrActionName("left", "skeleton")] != "skeleton" {
		t.Fatalf("actions: %v", known)
	}
	if len(m.Defaults) != len(vrBindingTypes) {
		t.Fatalf("defaults: %v", m.Defaults)
	}
	for _, d := range m.Defaults {
		raw, err := os.ReadFile(filepath.Join(dir, d.URL))
		if err != nil {
			t.Fatal(err)
		}
		// Every output a binding names must be an action in the manifest.
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if out, ok := x["output"].(string); ok {
					if _, ok := known[out]; !ok {
						t.Errorf("%s binds unknown action %s", d.Type, out)
					}
				}
				for _, y := range x {
					walk(y)
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			}
		}
		var b any
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("%s: %v", d.URL, err)
		}
		walk(b)
	}
	// Writing again leaves unchanged files alone.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	if _, err := writeVRManifest(dir); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Error("unchanged manifest was rewritten")
	}
}

func TestLegacyHand(t *testing.T) {
	// Index under the legacy binding: thumbstick, trigger, grip, trackpad.
	m := legacyAxes([openvr.ControllerAxisCount]int32{openvr.AxisJoystick, openvr.AxisTrigger, openvr.AxisTrigger, openvr.AxisTrackPad, openvr.AxisNone})
	if m != (vrAxisMap{stick: 0, pad: 3, trigger: 1, grip: 2}) {
		t.Fatalf("axis map %+v", m)
	}
	var c openvr.ControllerState
	c.PacketNum = 5
	c.Axis[0] = openvr.ControllerAxis{X: 0.5, Y: -0.5}
	c.Axis[1].X = 0.75
	c.Axis[2].X = 0.25
	c.Axis[3] = openvr.ControllerAxis{X: -1, Y: 0}
	c.Touched = 1<<(openvr.ButtonTouchpad+3) | 1<<openvr.ButtonApplicationMenu
	h := legacyHand(c, m)
	if !h.HasStick || h.Stick != [2]float64{0.5, -0.5} || h.StickTouch {
		t.Errorf("stick %+v", h)
	}
	if !h.HasPad || h.Pad != [2]float64{-1, 0} || !h.PadTouch {
		t.Errorf("pad %+v", h)
	}
	if h.Trigger != 0.75 || h.Grip != 0.25 || !h.BTouch || h.ATouch || h.HasCurl {
		t.Errorf("trigger/grip/buttons %+v", h)
	}
	// A Vive wand: trackpad and trigger, grip is a button.
	m = legacyAxes([openvr.ControllerAxisCount]int32{openvr.AxisTrackPad, openvr.AxisTrigger})
	c = openvr.ControllerState{PacketNum: 1, Pressed: 1 << openvr.ButtonGrip}
	if h := legacyHand(c, m); h.Grip != 1 || h.HasStick {
		t.Errorf("wand %+v", h)
	}
}

func TestSchedulePulses(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := schedulePulses(HapticPattern{Hand: HandRight, Strength: 40, DurationMS: 100, Pulses: 3}, now)
	if len(ps) != 3 {
		t.Fatalf("%d pulses", len(ps))
	}
	if ps[0].at != now || ps[1].at != now.Add(160*time.Millisecond) || ps[2].at != now.Add(320*time.Millisecond) {
		t.Errorf("timing %v %v %v", ps[0].at.Sub(now), ps[1].at.Sub(now), ps[2].at.Sub(now))
	}
	if ps[0].hands != [2]bool{false, true} || ps[0].strength != 0.4 || ps[0].dur != 100*time.Millisecond {
		t.Errorf("pulse %+v", ps[0])
	}
	if both := schedulePulses(HapticPattern{Hand: HandBoth, Strength: 1, DurationMS: 10, Pulses: 1}, now); both[0].hands != [2]bool{true, true} {
		t.Error("both hands")
	}
}

func TestParamWatch(t *testing.T) {
	var w paramWatch
	if w.fire("Glasses", HapticParamOn, 1) {
		t.Error("the first value only teaches the watch")
	}
	if w.fire("Glasses", HapticParamOn, 1) {
		t.Error("staying on is not turning on")
	}
	w.fire("Glasses", HapticParamOn, 0)
	if !w.fire("glasses", HapticParamOn, 1) {
		t.Error("turning on should fire")
	}
	if !w.fire("glasses", HapticParamChange, 0.5) || w.fire("glasses", HapticParamChange, 0.5) {
		t.Error("change mode")
	}
	w.reset()
	if w.fire("glasses", HapticParamChange, 0.9) {
		t.Error("after an avatar change the first value only teaches the watch")
	}
}

func TestBeatPeriod(t *testing.T) {
	if d, ok := beatPeriod(heartReading{ok: true, bpm: 60}); !ok || d != time.Second {
		t.Errorf("60 bpm: %v %v", d, ok)
	}
	if _, ok := beatPeriod(heartReading{ok: true, bpm: 20}); ok {
		t.Error("too slow to be real")
	}
	if _, ok := beatPeriod(heartReading{bpm: 80}); ok {
		t.Error("stale reading")
	}
}

// fakeVR stands in for a SteamVR session.
type fakeVR struct {
	input      bool
	manifest   string
	live       bool // actions report data
	analog     map[uint64]openvr.AnalogActionData
	digital    map[uint64]openvr.DigitalActionData
	curl       [5]float32
	legacy     map[uint32]openvr.ControllerState
	axisTypes  map[uint32][openvr.ControllerAxisCount]int32
	vibrations []string
	pulses     map[uint32]int
	priority   int32
	handles    map[string]uint64
}

func newFakeVR() *fakeVR {
	return &fakeVR{input: true, analog: map[uint64]openvr.AnalogActionData{}, digital: map[uint64]openvr.DigitalActionData{},
		legacy: map[uint32]openvr.ControllerState{}, axisTypes: map[uint32][openvr.ControllerAxisCount]int32{},
		pulses: map[uint32]int{}, handles: map[string]uint64{}}
}

func (f *fakeVR) handle(name string) uint64 {
	if h, ok := f.handles[name]; ok {
		return h
	}
	h := uint64(len(f.handles) + 1)
	f.handles[name] = h
	return h
}

func (f *fakeVR) HasInput() bool { return f.input }
func (f *fakeVR) SetActionManifestPath(p string) error {
	f.manifest = p
	return nil
}
func (f *fakeVR) ActionSetHandle(n string) (uint64, error)   { return f.handle(n), nil }
func (f *fakeVR) ActionHandle(n string) (uint64, error)      { return f.handle(n), nil }
func (f *fakeVR) InputSourceHandle(p string) (uint64, error) { return f.handle(p), nil }
func (f *fakeVR) UpdateActionState(set uint64, prio int32) error {
	f.priority = prio
	return nil
}
func (f *fakeVR) DigitalAction(a, _ uint64) (openvr.DigitalActionData, error) {
	d := f.digital[a]
	d.Active = f.live
	return d, nil
}
func (f *fakeVR) AnalogAction(a, _ uint64) (openvr.AnalogActionData, error) {
	d := f.analog[a]
	d.Active = f.live
	return d, nil
}
func (f *fakeVR) SkeletalSummary(uint64, int32) (openvr.SkeletalSummary, error) {
	if !f.live {
		return openvr.SkeletalSummary{}, openvr.InputNoData
	}
	return openvr.SkeletalSummary{Curl: f.curl}, nil
}
func (f *fakeVR) Vibrate(a uint64, dur, freq, amp float32, hand uint64) error {
	for name, h := range f.handles {
		if h == hand {
			f.vibrations = append(f.vibrations, name)
		}
	}
	return nil
}
func (f *fakeVR) ControllerState(i uint32) (openvr.ControllerState, bool) {
	c, ok := f.legacy[i]
	return c, ok
}
func (f *fakeVR) HapticPulse(i uint32, micros uint16) { f.pulses[i]++ }
func (f *fakeVR) Int32Prop(i uint32, prop int32) (int32, bool) {
	n := int(prop - openvr.PropAxis0Type)
	if t, ok := f.axisTypes[i]; ok && n >= 0 && n < openvr.ControllerAxisCount {
		return t[n], true
	}
	return 0, false
}

var fakeDevices = []VRDevice{{Index: 3, Kind: KindLeft}, {Index: 4, Kind: KindRight}}

func drainHapticQueue() {
	for {
		select {
		case <-hapticQueue:
		default:
			return
		}
	}
}

func TestVRInputTickReadsActions(t *testing.T) {
	drainHapticQueue()
	f := newFakeVR()
	f.live = true
	var sent []vrParam
	r := &vrInputRun{dir: t.TempDir(), emit: func(p []vrParam) { sent = append(sent, p...) }}
	o := defaultVRInputOptions()
	o.Input.Enabled = true
	o.Input.Fingers = true
	o.Input.Global = true
	// Push the left stick up and curl the right index finger.
	f.analog[f.handle(vrActionName("left", "stick"))] = openvr.AnalogActionData{Y: 1}
	f.curl = [5]float32{0, 0.8}
	now := time.Unix(2000, 0)
	r.tick(f, now, o, fakeDevices)
	if f.manifest == "" || !r.actions.ready {
		t.Fatalf("manifest not set up: %+v", r.actions)
	}
	if r.mode != VRInputActions || f.priority != openvr.ActionSetOverlayGlobalPriority {
		t.Errorf("mode %q priority %d", r.mode, f.priority)
	}
	got := map[string]any{}
	for _, p := range sent {
		got[p.Name] = p.Value
	}
	if got["LeftStickY"] != 1.0 || got["LeftStickDir"] != 1 || got["RightIndexCurl"] != float64(float32(0.8)) {
		t.Errorf("sent %v", got)
	}
	// Nothing changed: the next read sends nothing.
	sent = nil
	r.tick(f, now.Add(time.Second), o, fakeDevices)
	if len(sent) != 0 {
		t.Errorf("resent unchanged values: %v", sent)
	}
	// An avatar change sends everything again.
	vrResend.Store(true)
	r.tick(f, now.Add(2*time.Second), o, fakeDevices)
	if len(sent) == 0 {
		t.Error("avatar change did not resend")
	}
	// Haptics go through SteamVR Input, one vibration per hand.
	QueueHaptic(HapticPattern{Hand: HandBoth, Strength: 50, DurationMS: 50, Pulses: 1}, "test")
	r.tick(f, now.Add(3*time.Second), o, fakeDevices)
	if strings.Join(f.vibrations, ",") != "/user/hand/left,/user/hand/right" {
		t.Errorf("vibrations %v", f.vibrations)
	}
}

func TestVRInputTickFallsBackToLegacy(t *testing.T) {
	drainHapticQueue()
	f := newFakeVR()
	f.live = false // SteamVR Input gives nothing while a game has focus
	f.axisTypes[4] = [openvr.ControllerAxisCount]int32{openvr.AxisJoystick, openvr.AxisTrigger}
	c := openvr.ControllerState{PacketNum: 9}
	c.Axis[0] = openvr.ControllerAxis{X: 1}
	c.Axis[1].X = 0.5
	f.legacy[4] = c
	var sent []vrParam
	r := &vrInputRun{dir: t.TempDir(), emit: func(p []vrParam) { sent = append(sent, p...) }}
	o := defaultVRInputOptions()
	o.Input.Enabled = true
	now := time.Unix(3000, 0)
	r.tick(f, now, o, fakeDevices)
	if r.mode != VRInputLegacy {
		t.Fatalf("mode %q", r.mode)
	}
	got := map[string]any{}
	for _, p := range sent {
		got[p.Name] = p.Value
	}
	if got["RightStickX"] != 1.0 || got["RightStickDir"] != 2 || got["RightTrigger"] != 0.5 {
		t.Errorf("sent %v", got)
	}

	// With SteamVR Input only, there is no fallback.
	o.Input.Source = VRInputActions
	r2 := &vrInputRun{dir: t.TempDir(), emit: func([]vrParam) {}}
	r2.tick(f, now, o, fakeDevices)
	if r2.mode != "" {
		t.Errorf("actions-only fell back to %q", r2.mode)
	}

	// Legacy haptics repeat short pulses on the controller's slot.
	o.Input.Source = VRInputLegacy
	r3 := &vrInputRun{emit: func([]vrParam) {}}
	QueueHaptic(HapticPattern{Hand: HandLeft, Strength: 100, DurationMS: 20, Pulses: 1}, "test")
	wait := r3.tick(f, now, o, fakeDevices)
	if f.pulses[3] != 1 || wait > legacyPulseEvery {
		t.Fatalf("pulses %v wait %v", f.pulses, wait)
	}
	r3.tick(f, now.Add(10*time.Millisecond), o, fakeDevices)
	r3.tick(f, now.Add(30*time.Millisecond), o, fakeDevices) // past the end
	if f.pulses[3] != 2 || f.pulses[4] != 0 {
		t.Errorf("pulses %v", f.pulses)
	}
	if r3.actions.ready || r3.tried {
		t.Error("legacy mode should not register the manifest")
	}
}

func TestVRInputNoInterface(t *testing.T) {
	f := newFakeVR()
	f.input = false
	a := setupActions(f, t.TempDir())
	if a.ready || a.err == "" {
		t.Errorf("setup without IVRInput: %+v", a)
	}
}

func TestCheckVRControl(t *testing.T) {
	hands := [2]vrHand{{Stick: [2]float64{0, 1}}, {ATouch: true, PadTouch: true, Pad: [2]float64{-1, 0}}}
	noteVRDerived(hands, 0.5)
	now := time.Now()
	p := func(kv ...string) RuleParams {
		out := RuleParams{}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	if !checkVRControl(p("hand", "left", "control", "stick", "dir", "up"), now) {
		t.Error("left stick up")
	}
	if checkVRControl(p("hand", "right", "control", "stick", "dir", "up"), now) {
		t.Error("right stick is centred")
	}
	if !checkVRControl(p("hand", "either", "control", "pad", "dir", "left"), now) {
		t.Error("right pad left")
	}
	if !checkVRControl(p("hand", "right", "control", "thumb", "thumb", "a"), now) {
		t.Error("right thumb on A")
	}
	if checkVRControl(p("hand", "left", "control", "stick", "dir", "up"), now.Add(5*time.Second)) {
		t.Error("stale reading still matched")
	}
}

func TestHapticRuleRegistered(t *testing.T) {
	drainHapticQueue()
	var act *RuleAction
	for _, a := range RuleActions() {
		if a.ID == ActionHaptic {
			a := a
			act = &a
		}
	}
	if act == nil {
		t.Fatal("haptic action not registered")
	}
	params := SanitizeRuleParams(act.Fields, RuleParams{"hand": "left", "strength": 500.0})
	if params.Number("strength") != 100 || params.Number("duration") != 120 {
		t.Errorf("params %v", params)
	}
	if _, err := act.Run(RuleContext{}, params); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-hapticQueue:
		if req.Pattern.Hand != HandLeft || req.Pattern.Strength != 100 || req.Pattern.DurationMS != 120 {
			t.Errorf("queued %+v", req.Pattern)
		}
	default:
		t.Error("nothing queued")
	}
	found := false
	for _, tr := range RuleTriggers() {
		if tr.ID == TriggerVRControl && tr.Check != nil {
			found = true
		}
	}
	if !found {
		t.Error("controller trigger not registered as a state trigger")
	}
}

func TestQueueHapticFull(t *testing.T) {
	drainHapticQueue()
	defer drainHapticQueue()
	p := HapticPattern{Hand: HandBoth, Strength: 10, DurationMS: 10, Pulses: 1}
	n := 0
	for QueueHaptic(p, "test") {
		n++
		if n > 1000 {
			t.Fatal("queue never fills")
		}
	}
	if n != cap(hapticQueue) {
		t.Errorf("queued %d, cap %d", n, cap(hapticQueue))
	}
	if err := TestHaptic("nope"); err == nil {
		t.Error("unknown pattern accepted")
	}
}
