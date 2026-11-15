package modules

import (
	"math"
	"strings"
	"testing"
	"time"
)

func sampleDevices() []VRDevice {
	devices := []VRDevice{
		{Index: 0, Serial: "HMD1", Model: "Index", Kind: KindHeadset},
		{Index: 1, Serial: "L1", Model: "Knuckles Left", Kind: KindLeft, HasBattery: true, Battery: 64},
		{Index: 2, Serial: "R1", Model: "Knuckles Right", Kind: KindRight, HasBattery: true, Battery: 15, Charging: true},
		{Index: 3, Serial: "T1", Model: "VIVE Tracker 3.0", Kind: KindTracker, HasBattery: true, Battery: 42},
		{Index: 4, Serial: "T2", Model: "VIVE Tracker 3.0", Kind: KindTracker, HasBattery: true, Battery: 8},
	}
	defaultDeviceNames(devices)
	return devices
}

func TestBatteryTextDefaults(t *testing.T) {
	o := DefaultConfig().BatteryOptions
	got := batteryText(sampleDevices(), o)
	want := "🎮 Left 64% | 🎮 Right 15%⚡ LOW | 📍 Tracker 1 42% | 📍 Tracker 2 8% LOW"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestBatteryTextOptions(t *testing.T) {
	base := DefaultConfig().BatteryOptions
	cases := []struct {
		name string
		edit func(*BatteryOptions)
		want string
	}{
		{"only low", func(o *BatteryOptions) { o.OnlyLow = true }, "🎮 Right 15%⚡ LOW | 📍 Tracker 2 8% LOW"},
		{"only lowest", func(o *BatteryOptions) { o.OnlyLowest = true }, "📍 Tracker 2 8% LOW"},
		{"sorted low to high", func(o *BatteryOptions) { o.Sort = BatterySortLow; o.Template = "{name} {batt}" },
			"Tracker 2 8 | Right 15 ⚡ | Tracker 1 42 | Left 64"},
		{"sorted high to low, capped", func(o *BatteryOptions) {
			o.Sort = BatterySortHigh
			o.Template = "{name}"
			o.MaxDevices = 2
		}, "Left | Tracker 1"},
		{"sorted by name", func(o *BatteryOptions) { o.Sort = BatterySortName; o.Template = "{name}" },
			"Left | Right ⚡ | Tracker 1 | Tracker 2"},
		{"no trackers", func(o *BatteryOptions) { o.ShowTrackers = false }, "🎮 Left 64% | 🎮 Right 15%⚡ LOW"},
		{"renamed and hidden by serial", func(o *BatteryOptions) {
			o.Template = "{name} {batt}%"
			o.Devices = []BatteryDevice{{Serial: "T1", Name: "Hip"}, {Serial: "T2", Hidden: true}, {Serial: "L1", Hidden: true}}
		}, "Right 15% ⚡ | Hip 42%"},
		{"other placeholders", func(o *BatteryOptions) {
			o.Template = "{kind}:{model}:{serial}"
			o.ShowTrackers = false
		}, "Controller:Knuckles Left:L1 | Controller:Knuckles Right:R1 ⚡"},
		{"charging mark off", func(o *BatteryOptions) { o.ChargingMark = ""; o.ShowTrackers = false },
			"🎮 Left 64% | 🎮 Right 15% LOW"},
		{"label and separator", func(o *BatteryOptions) {
			o.Label = "bat"
			o.Separator = " / "
			o.Template = "{name}"
			o.ShowTrackers = false
		}, "bat Left / Right ⚡"},
		{"nothing low", func(o *BatteryOptions) { o.OnlyLow = true; o.LowThreshold = 5 }, ""},
	}
	for _, tc := range cases {
		o := base
		tc.edit(&o)
		if got := batteryText(sampleDevices(), o); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.name, got, tc.want)
		}
	}
}

func TestBatteryHeadsetShownOnlyWithBattery(t *testing.T) {
	o := DefaultConfig().BatteryOptions
	o.ShowControllers, o.ShowTrackers = false, false
	devices := sampleDevices()
	if got := batteryText(devices, o); got != "" {
		t.Errorf("headset without a battery shown: %q", got)
	}
	devices[0].HasBattery, devices[0].Battery = true, 77
	if got := batteryText(devices, o); got != "🥽 Headset 77%" {
		t.Errorf("got %q", got)
	}
}

func TestRememberDevices(t *testing.T) {
	saved := []BatteryDevice{{Serial: "T1", Name: "Hip", Kind: KindController, Model: "old"}}
	next, changed := rememberDevices(saved, sampleDevices())
	if !changed {
		t.Fatal("expected a change")
	}
	if len(next) != 4 { // the headset has no battery
		t.Fatalf("remembered %d devices: %+v", len(next), next)
	}
	if next[0].Name != "Hip" || next[0].Kind != KindTracker || next[0].Model != "VIVE Tracker 3.0" {
		t.Errorf("known device not refreshed: %+v", next[0])
	}
	if _, changed := rememberDevices(next, sampleDevices()); changed {
		t.Error("nothing new, but reported a change")
	}
}

func TestSanitizeSteamVR(t *testing.T) {
	c := DefaultConfig()
	b := &c.BatteryOptions
	b.Template = "  \n "
	b.Separator = ""
	b.LowThreshold = 500
	b.Sort = "random"
	b.MaxDevices = -3
	b.HeadsetIcon = "🥽🥽🥽🥽🥽🥽"
	b.Devices = []BatteryDevice{{Serial: " A "}, {Serial: "A"}, {Serial: ""}, {Serial: "B", Kind: "toaster", Name: "x\ny"}}
	p := &c.VRPerfOptions
	p.Separator = "\n"
	p.BadReprojection = -1
	p.BadFPSPercent = 400
	p.ClearSeconds = 9999
	c.sanitize()

	def := DefaultConfig()
	if b.Template != def.BatteryOptions.Template || b.Separator != def.BatteryOptions.Separator {
		t.Errorf("template %q separator %q", b.Template, b.Separator)
	}
	if b.LowThreshold != 100 || b.Sort != BatterySortDetected || b.MaxDevices != 0 {
		t.Errorf("threshold %d sort %q max %d", b.LowThreshold, b.Sort, b.MaxDevices)
	}
	if len([]rune(b.HeadsetIcon)) != 4 {
		t.Errorf("icon %q", b.HeadsetIcon)
	}
	if len(b.Devices) != 2 || b.Devices[0].Serial != "A" || b.Devices[1].Kind != KindController || b.Devices[1].Name != "x y" {
		t.Errorf("devices %+v", b.Devices)
	}
	if p.Separator != " " || p.BadReprojection != 0 || p.BadFPSPercent != 100 || p.ClearSeconds != 120 {
		t.Errorf("perf %+v", *p)
	}
	for _, id := range []string{ModuleBattery, ModuleVRPerf} {
		if !knownModules[id] {
			t.Errorf("%s is not a known module", id)
		}
	}
}

func TestDiffCounts(t *testing.T) {
	a := compositorCounts{Pid: 1, Presents: 100, Reprojected: 10, Submits: 90, SumAppGPU: 500}
	b := compositorCounts{Pid: 1, Presents: 190, Reprojected: 19, Submits: 171, SumAppGPU: 1310}
	d, ok := diffCounts(a, b, 1)
	if !ok || d.Presents != 90 || d.Reprojected != 9 || d.Submits != 81 || d.SumAppGPU != 810 {
		t.Fatalf("delta %+v ok=%v", d, ok)
	}
	b.Pid = 2
	if _, ok := diffCounts(a, b, 1); ok {
		t.Error("compared counters from a different app")
	}
	b.Pid, b.Presents = 1, 50
	if _, ok := diffCounts(a, b, 1); ok {
		t.Error("compared counters that went backwards")
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestSummarizePerf(t *testing.T) {
	// Five seconds at 90 Hz where the game makes 81 frames a second, 10 ms each.
	var deltas []perfDelta
	for i := 0; i < 5; i++ {
		deltas = append(deltas, perfDelta{Seconds: 1, Presents: 90, Reprojected: 9, Submits: 81, SumAppGPU: 810, SumAppCPU: 405, Dropped: 1})
	}
	p := summarizePerf(deltas, 90, 0, true)
	if !p.Valid || !near(p.FPS, 81) || !near(p.ReprojectionPct, 10) || !near(p.GPUms, 10) || !near(p.CPUms, 5) {
		t.Fatalf("perf %+v", p)
	}
	if !near(p.BudgetPct, 90) { // 10 ms of an 11.1 ms frame
		t.Errorf("budget %v", p.BudgetPct)
	}
	if p.DroppedPerMin != 5 || !p.Smoothing {
		t.Errorf("dropped %v smoothing %v", p.DroppedPerMin, p.Smoothing)
	}

	// Without submit counts, fresh frames are presents minus reprojected, and
	// the GPU time falls back to the latest frame's.
	p = summarizePerf([]perfDelta{{Seconds: 1, Presents: 120, Reprojected: 60}}, 120, 7.5, false)
	if !near(p.FPS, 60) || !near(p.GPUms, 7.5) || p.CPUms != 0 {
		t.Errorf("fallback perf %+v", p)
	}
	if p := summarizePerf(nil, 90, 0, false); p.Valid {
		t.Error("no samples should not be valid")
	}
}

func TestPerfIsBad(t *testing.T) {
	o := DefaultConfig().VRPerfOptions // 10% reprojection, 5 dropped/min, 90% fps
	good := VRPerf{Valid: true, FPS: 90, Hz: 90, ReprojectionPct: 2, DroppedPerMin: 1}
	if perfIsBad(good, o) {
		t.Error("good frame counted as bad")
	}
	for name, p := range map[string]VRPerf{
		"reprojection": {Valid: true, FPS: 90, Hz: 90, ReprojectionPct: 10},
		"dropped":      {Valid: true, FPS: 90, Hz: 90, DroppedPerMin: 5},
		"fps":          {Valid: true, FPS: 80, Hz: 90},
	} {
		if !perfIsBad(p, o) {
			t.Errorf("%s not counted as bad", name)
		}
	}
	o.BadReprojection, o.BadDroppedPerMin, o.BadFPSPercent = 0, 0, 0
	if perfIsBad(VRPerf{Valid: true, FPS: 10, Hz: 90, ReprojectionPct: 90, DroppedPerMin: 90}, o) {
		t.Error("thresholds of 0 should be ignored")
	}
}

func TestPerfGateHysteresis(t *testing.T) {
	var g perfGate
	t0 := time.Unix(1000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	clear := 5 * time.Second
	steps := []struct {
		sec  int
		bad  bool
		want bool
	}{
		{0, false, false},
		{1, true, true},   // goes bad at once
		{2, false, true},  // fine for 0s
		{4, false, true},  // fine for 2s
		{5, true, true},   // a hitch restarts the wait
		{6, false, true},  // fine for 0s
		{10, false, true}, // fine for 4s
		{11, false, false},
		{12, false, false},
	}
	for _, s := range steps {
		if got := g.update(at(s.sec), s.bad, clear); got != s.want {
			t.Errorf("t=%ds bad=%v: gate %v, want %v", s.sec, s.bad, got, s.want)
		}
	}
	var instant perfGate
	instant.update(at(0), true, 0)
	if instant.update(at(0), false, 0) {
		t.Error("a zero clear time should clear at once")
	}
}

func TestVRPerfText(t *testing.T) {
	p := VRPerf{Valid: true, FPS: 80.6, Hz: 90, ReprojectionPct: 12.4, DroppedPerMin: 3, GPUms: 10.04, CPUms: 4.96, BudgetPct: 90.4, Smoothing: true}
	o := DefaultConfig().VRPerfOptions
	if got := vrPerfText(p, false, o); got != "81ᶠᵖˢ ¦ 12%ʳᵉᵖʳᵒʲ" {
		t.Errorf("default: %q", got)
	}
	o.ShowHz, o.ShowDropped, o.ShowGPU, o.ShowCPU, o.ShowBudget, o.ShowSmoothing = true, true, true, true, true, true
	o.SmallUnits = false
	o.Separator = " | "
	o.Label = "VR"
	want := "VR 81 fps | 90 Hz | 12% reproj | 3 dropped/min | 10.0 ms GPU | 5.0 ms CPU | 90% budget | smoothing"
	if got := vrPerfText(p, false, o); got != want {
		t.Errorf("all fields:\n got  %q\n want %q", got, want)
	}
	o.OnlyWhenBad = true
	if got := vrPerfText(p, false, o); got != "" {
		t.Errorf("shown while fine: %q", got)
	}
	if got := vrPerfText(p, true, o); !strings.HasPrefix(got, "VR 81 fps") {
		t.Errorf("hidden while bad: %q", got)
	}
	if got := vrPerfText(VRPerf{}, true, o); got != "" {
		t.Errorf("invalid sample shown: %q", got)
	}
}

func TestSteamVRLinesEmptyWithoutSteamVR(t *testing.T) {
	cfg := DefaultConfig()
	if SteamVRState().Connected {
		t.Skip("connected to SteamVR")
	}
	if got := batteryLine(cfg); got != "" {
		t.Errorf("battery line %q", got)
	}
	if got := vrPerfLine(cfg); got != "" {
		t.Errorf("vrperf line %q", got)
	}
}
