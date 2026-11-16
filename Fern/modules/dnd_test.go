package modules

import (
	"reflect"
	"testing"
)

func TestDNDHidesAllButAllowed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DisplayOptions.Time, cfg.DisplayOptions.Text, cfg.DisplayOptions.Memory = true, true, true
	cfg.DND = DNDOptions{On: true, Allow: []string{ModuleTime}}
	lines, _ := buildLines(cfg, false)
	for _, l := range lines {
		if l.ID != ModuleTime {
			t.Errorf("line %s shown during do not disturb", l.ID)
		}
	}
	quiet := dndQuieted(cfg)
	for _, id := range quiet {
		if id == ModuleTime || !ModuleEnabled(id, cfg.DisplayOptions) {
			t.Errorf("quiet lists %s", id)
		}
	}
	if len(quiet) == 0 {
		t.Error("nothing quieted")
	}
	cfg.DND.On = false
	if dndQuieted(cfg) != nil || dndHides(cfg.DND, ModuleText) {
		t.Error("off should hide nothing")
	}
}

func TestDNDSanitize(t *testing.T) {
	c := DefaultConfig()
	if c.DND.On || c.DND.Allow == nil {
		t.Fatalf("default %+v", c.DND)
	}
	c.DND = DNDOptions{Allow: []string{ModuleTime, "bogus", ModuleTime, ModuleAFK}, Hotkey: "ctrl + shift + d"}
	c.sanitize()
	if !reflect.DeepEqual(c.DND.Allow, []string{ModuleTime, ModuleAFK}) || c.DND.Hotkey != "Ctrl+Shift+D" {
		t.Errorf("sanitized %+v", c.DND)
	}
	c.DND.Hotkey = "nonsense+++"
	c.sanitize()
	if c.DND.Hotkey != "" {
		t.Errorf("bad hotkey kept: %q", c.DND.Hotkey)
	}
}

func TestDNDKeptAcrossProfiles(t *testing.T) {
	useTempProfiles(t)
	if err := CreateProfile("A"); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("B"); err != nil {
		t.Fatal(err)
	}
	SetDND(true)
	if err := SwitchProfile("A"); err != nil {
		t.Fatal(err)
	}
	if !DNDActive() {
		t.Error("switching profiles turned do not disturb off")
	}
}
