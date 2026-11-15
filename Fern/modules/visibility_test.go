package modules

import (
	"reflect"
	"testing"
)

func TestLineVisibility(t *testing.T) {
	d := DisplayOptions{Visibility: map[string]string{ModuleTime: ShowVR, ModuleCPU: ShowDesktop}}
	cases := []struct {
		id   string
		vr   bool
		want bool
	}{
		{ModuleTime, true, true},
		{ModuleTime, false, false},
		{ModuleCPU, true, false},
		{ModuleCPU, false, true},
		{ModuleText, true, true},
		{ModuleText, false, true},
	}
	for _, c := range cases {
		if got := lineVisible(c.id, d, c.vr); got != c.want {
			t.Errorf("%s in VR=%v: visible %v, want %v", c.id, c.vr, got, c.want)
		}
	}
}

func TestBuildLinesHidesByMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisplayOptions.Time, cfg.DisplayOptions.Memory = true, true
	cfg.DisplayOptions.Visibility = map[string]string{ModuleTime: ShowVR, ModuleMemory: ShowDesktop}
	ids := func(lines []FitLine) map[string]bool {
		out := map[string]bool{}
		for _, l := range lines {
			out[l.ID] = true
		}
		return out
	}
	lines, hidden := buildLines(cfg, false)
	if got := ids(lines); got[ModuleTime] || !reflect.DeepEqual(hidden, []string{ModuleTime}) {
		t.Errorf("desktop: lines %v hidden %v", got, hidden)
	}
	lines, hidden = buildLines(cfg, true)
	if got := ids(lines); !got[ModuleTime] || !reflect.DeepEqual(hidden, []string{ModuleMemory}) {
		t.Errorf("VR: lines %v hidden %v", got, hidden)
	}
}

func TestSanitizeVisibility(t *testing.T) {
	c := DefaultConfig()
	c.DisplayOptions.Visibility = map[string]string{ModuleTime: ShowVR, ModuleCPU: "always", "bogus": ShowVR, ModuleText: "weird"}
	c.sanitize()
	if want := map[string]string{ModuleTime: ShowVR}; !reflect.DeepEqual(c.DisplayOptions.Visibility, want) {
		t.Errorf("visibility %v, want %v", c.DisplayOptions.Visibility, want)
	}
	c.DisplayOptions.Visibility = nil
	c.sanitize()
	if c.DisplayOptions.Visibility == nil {
		t.Error("nil visibility should become an empty map")
	}
}
