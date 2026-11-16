package modules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSetupStartsOnlyForNewConfigs(t *testing.T) {
	if DefaultConfig().AppOptions.SetupDone {
		t.Fatal("a new config should start the setup")
	}
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"config from before the setup", `{"AppOptions":{"tray":true}}`, true},
		{"no app section at all", `{"display_options":{"show":true}}`, true},
		{"setup still to do", `{"AppOptions":{"setup_done":false}}`, false},
		{"setup done", `{"AppOptions":{"setup_done":true}}`, true},
	}
	for _, tc := range cases {
		c := DefaultConfig()
		if tc.data != "" {
			if err := json.Unmarshal([]byte(tc.data), &c); err != nil {
				t.Fatal(err)
			}
		}
		keepSetupForOldConfig([]byte(tc.data), &c)
		if c.AppOptions.SetupDone != tc.want {
			t.Errorf("%s: done = %v, want %v", tc.name, c.AppOptions.SetupDone, tc.want)
		}
	}
}

func TestFinishSetup(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	if CurrentSetup().Done {
		t.Fatal("setup done before finishing")
	}
	FinishSetup()
	if !GetConfig().AppOptions.SetupDone || !CurrentSetup().Done {
		t.Error("setup not marked done")
	}
}

func TestApplyStarterLines(t *testing.T) {
	start := DefaultConfig()
	start.DisplayOptions.Show = false
	start.DisplayOptions.Discord = true // not a starter line: left alone
	useTestConfig(t, start)

	on, err := ApplyStarterLines([]string{" CPU ", "media", "bogus", ModuleDiscord})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{ModuleMedia, ModuleCPU}; !reflect.DeepEqual(on, want) {
		t.Errorf("turned on %q, want %q", on, want)
	}
	d := GetConfig().DisplayOptions
	if !d.Show || !d.Media || !d.CPU {
		t.Errorf("chosen lines or display not on: %+v", d)
	}
	if d.Time || d.Afk || d.Text || d.Loading {
		t.Errorf("lines left out of the choice are still on: %+v", d)
	}
	if !d.Discord {
		t.Error("a line the setup does not offer was changed")
	}

	if _, err := ApplyStarterLines([]string{"bogus"}); err == nil {
		t.Error("no known line should be an error")
	}
	if !GetConfig().DisplayOptions.Media {
		t.Error("a refused choice changed the lines")
	}
}

func TestStarterLinesAreKnownModules(t *testing.T) {
	var d DisplayOptions
	for _, id := range StarterLines {
		if moduleFlag(&d, id) == nil {
			t.Errorf("%q has no display switch", id)
		}
	}
}

func TestScanOSCFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OSC")
	if s := scanOSCFolder(dir); s.exists || s.configs != 0 {
		t.Fatalf("missing folder: %+v", s)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if s := scanOSCFolder(dir); !s.exists || s.configs != 0 {
		t.Fatalf("empty folder: %+v", s)
	}
	avatars := filepath.Join(dir, "usr_1234", "Avatars")
	if err := os.MkdirAll(avatars, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"avtr_a.json", "avtr_b.JSON", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(avatars, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(avatars, "avtr_b.JSON"), old, old); err != nil {
		t.Fatal(err)
	}
	// Folders that are not a user's are not VRChat's avatar files.
	if err := os.MkdirAll(filepath.Join(dir, "other", "Avatars"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other", "Avatars", "x.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s := scanOSCFolder(dir)
	if !s.exists || s.configs != 2 {
		t.Errorf("got %+v, want 2 avatar files", s)
	}
	if time.Since(s.newest) > time.Minute {
		t.Errorf("newest file is %v, want the one just written", s.newest)
	}
}

func TestOSCSettingFrom(t *testing.T) {
	values := map[string]uint64{"VRC_INPUT_OSC_h123456": 1, "VRC_INPUT_OSC_ENABLED_h1": 0, "Other_h2": 0}
	get := func(name string) (uint64, error) {
		v, ok := values[name]
		if !ok {
			return 0, errors.New("missing")
		}
		return v, nil
	}
	names := []string{"Other_h2", "VRC_INPUT_OSC_ENABLED_h1", "VRC_INPUT_OSC_h123456"}
	if got := oscSettingFrom(names, get); got != "on" {
		t.Errorf("got %q, want on", got)
	}
	values["VRC_INPUT_OSC_h123456"] = 0
	if got := oscSettingFrom(names, get); got != "off" {
		t.Errorf("got %q, want off", got)
	}
	if got := oscSettingFrom([]string{"Other_h2"}, get); got != "" {
		t.Errorf("no OSC value: got %q, want unknown", got)
	}
	if got := oscSettingFrom([]string{"VRC_INPUT_OSC_h9"}, get); got != "" {
		t.Errorf("unreadable value: got %q, want unknown", got)
	}
	if !strings.HasPrefix(CurrentSetup().OSCFolderPath, logDirectory()) {
		t.Error("the OSC folder should sit in VRChat's folder")
	}
}
