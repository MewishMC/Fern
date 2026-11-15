package modules

import "testing"

func TestModuleSamplesCoverEveryModule(t *testing.T) {
	cfg := GetConfig()
	cfg.DisplayOptions.Time = false // samples must not depend on the line being on
	samples := ModuleSamples(cfg)
	for _, id := range DefaultOrder {
		if _, ok := samples[id]; !ok {
			t.Errorf("no sample for module %q", id)
		}
	}
	if samples[ModuleTime] == "" {
		t.Error("time sample is empty while the time line is off")
	}
}
