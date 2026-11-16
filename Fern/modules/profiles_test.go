package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useTempProfiles points the config, profiles and backups at a temp folder
// and starts from the defaults with no profiles.
func useTempProfiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldCfg, oldPath := GetConfig(), cfgPath
	cfgPath = filepath.Join(dir, "config.json")
	profilesMu.Lock()
	profiles, profilesLoaded, profilesFile = profileStore{}, false, ""
	profilesMu.Unlock()
	backupMu.Lock()
	backupList, backupListed, backupDirPath = nil, false, ""
	backupMu.Unlock()
	SetConfig(DefaultConfig())
	t.Cleanup(func() {
		saveMu.Lock()
		if saveAt != nil {
			saveAt.Stop()
		}
		saveMu.Unlock()
		cfgPath = oldPath
		profilesMu.Lock()
		profiles, profilesLoaded = profileStore{}, false
		profilesMu.Unlock()
		backupMu.Lock()
		backupList, backupListed = nil, false
		backupMu.Unlock()
		SetConfig(oldCfg)
		saveMu.Lock()
		if saveAt != nil {
			saveAt.Stop()
		}
		saveMu.Unlock()
	})
	return dir
}

func TestProfileSettingsLeaveOutMachineParts(t *testing.T) {
	c := DefaultConfig()
	c.AppOptions.MicHotkey = "Alt+V"
	c.OSCOptions.Host, c.OSCOptions.Port = "10.0.0.5", 9100
	c.AssistOptions.OpenAIKey = "sealed-openai-key"
	c.STTOptions.Key = "sealed-stt-key"
	c.HeartRate.PulsoidToken = "pulsoid-secret-token"
	c.HeartRate.HypeRateKey = "hyperate-secret-key"
	c.ChatOptions.History = []string{"private message"}
	c.DisplayOptions.Time = false
	c.TimeOptions.Label = "clock:"
	raw, err := profileSettings(c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, leak := range []string{"Alt+V", "10.0.0.5", "sealed-openai-key", "sealed-stt-key", "pulsoid-secret-token", "hyperate-secret-key", "private message"} {
		if strings.Contains(text, leak) {
			t.Errorf("snapshot contains %q", leak)
		}
	}
	var sections map[string]json.RawMessage
	_ = json.Unmarshal(raw, &sections)
	for _, key := range profileSkipSections {
		if _, ok := sections[key]; ok {
			t.Errorf("snapshot has section %s", key)
		}
	}
	if !strings.Contains(text, "clock:") {
		t.Error("snapshot lost a line setting")
	}

	cur := DefaultConfig()
	cur.OSCOptions.Host = "192.168.1.2"
	cur.AppOptions.Tray = true
	cur.HeartRate.PulsoidToken = "mine"
	cur.DisplayOptions.Time = true
	got, err := applyProfileSettings(cur, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.OSCOptions.Host != "192.168.1.2" || !got.AppOptions.Tray || got.HeartRate.PulsoidToken != "mine" {
		t.Errorf("applying a profile replaced per-PC settings: %+v %+v", got.OSCOptions, got.AppOptions)
	}
	if got.DisplayOptions.Time || got.TimeOptions.Label != "clock:" {
		t.Error("applying a profile did not bring its line settings")
	}
}

func TestProfileLifecycle(t *testing.T) {
	dir := useTempProfiles(t)
	var events []ProfileEvent
	remove := OnProfileChange(func(ev ProfileEvent) { events = append(events, ev) })
	defer remove()

	UpdateConfig(func(c *Config) { c.TimeOptions.Label = "home" })
	if err := CreateProfile("  Home  "); err != nil {
		t.Fatal(err)
	}
	if ActiveProfile() != "Home" {
		t.Fatalf("active = %q, want Home", ActiveProfile())
	}
	if err := CreateProfile("home"); err == nil {
		t.Error("a second profile with the same name (ignoring case) was allowed")
	}
	if err := DuplicateProfile("Home", "Club"); err != nil {
		t.Fatal(err)
	}
	if ActiveProfile() != "Home" {
		t.Error("duplicating switched profiles")
	}

	// Editing while Club is active saves into Club, not Home.
	if err := SwitchProfile("club"); err != nil {
		t.Fatal(err)
	}
	UpdateConfig(func(c *Config) { c.TimeOptions.Label = "club" })
	if err := SaveNow(); err != nil {
		t.Fatal(err)
	}
	if err := SwitchProfile("Home"); err != nil {
		t.Fatal(err)
	}
	if got := GetConfig().TimeOptions.Label; got != "home" {
		t.Errorf("Home label = %q, want home", got)
	}
	if err := SwitchProfile("Club"); err != nil {
		t.Fatal(err)
	}
	if got := GetConfig().TimeOptions.Label; got != "club" {
		t.Errorf("Club label = %q, want club (edit was not saved to the profile)", got)
	}

	// Unsaved edits go into the profile being left.
	UpdateConfig(func(c *Config) { c.TimeOptions.Label = "club2" })
	if err := SwitchProfile("Home"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchProfile("Club"); err != nil {
		t.Fatal(err)
	}
	if got := GetConfig().TimeOptions.Label; got != "club2" {
		t.Errorf("Club label = %q, want club2", got)
	}

	if err := RenameProfile("Club", "Party"); err != nil {
		t.Fatal(err)
	}
	if ActiveProfile() != "Party" {
		t.Errorf("rename lost the active profile: %q", ActiveProfile())
	}
	if err := RenameProfile("Party", "home"); err == nil {
		t.Error("renaming onto an existing name was allowed")
	}
	if err := DeleteProfile("Party"); err != nil {
		t.Fatal(err)
	}
	if ActiveProfile() != "" {
		t.Error("deleting the active profile left it active")
	}
	if got := GetConfig().TimeOptions.Label; got != "club2" {
		t.Error("deleting the active profile changed the settings")
	}
	if err := SwitchProfile("Party"); err == nil {
		t.Error("switched to a deleted profile")
	}

	switched := 0
	for _, ev := range events {
		if ev.Kind == ProfileSwitched {
			switched++
		}
	}
	if switched < 5 {
		t.Errorf("got %d switch events, want at least 5: %+v", switched, events)
	}

	// Everything survives a restart.
	profilesMu.Lock()
	profiles, profilesLoaded = profileStore{}, false
	profilesMu.Unlock()
	list := ListProfiles()
	if len(list) != 1 || list[0].Name != "Home" {
		t.Fatalf("after reload: %+v", list)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"AppOptions\"") || strings.Contains(string(data), "\"OSC\"") {
		t.Error("profiles.json holds per-PC sections")
	}
}

func TestOverwriteAndDeactivate(t *testing.T) {
	useTempProfiles(t)
	if err := CreateProfile("A"); err != nil {
		t.Fatal(err)
	}
	if err := DeactivateProfile(); err != nil {
		t.Fatal(err)
	}
	UpdateConfig(func(c *Config) { c.DisplayOptions.Memory = !c.DisplayOptions.Memory })
	want := GetConfig().DisplayOptions.Memory
	if err := SaveNow(); err != nil {
		t.Fatal(err)
	}
	// With no profile active, A keeps its old settings.
	if err := SwitchProfile("A"); err != nil {
		t.Fatal(err)
	}
	if GetConfig().DisplayOptions.Memory == want {
		t.Error("an edit with no profile active changed profile A")
	}
	if err := DeactivateProfile(); err != nil {
		t.Fatal(err)
	}
	UpdateConfig(func(c *Config) { c.DisplayOptions.Memory = want })
	if err := OverwriteProfile("A"); err != nil {
		t.Fatal(err)
	}
	if ActiveProfile() != "A" {
		t.Error("overwriting did not make the profile active")
	}
	UpdateConfig(func(c *Config) { c.DisplayOptions.Memory = !want })
	if err := SwitchProfile("A"); err != nil {
		t.Fatal(err)
	}
	// The unsaved edit belongs to A now, since it is active.
	if GetConfig().DisplayOptions.Memory != !want {
		t.Error("switching to the active profile dropped an edit")
	}
}

func TestProfileHotkeys(t *testing.T) {
	useTempProfiles(t)
	UpdateConfig(func(c *Config) { c.AppOptions.MicHotkey = "Alt+V" })
	_ = CreateProfile("A")
	_ = CreateProfile("B")
	if err := SetProfileHotkey("A", "ctrl+shift+1"); err != nil {
		t.Fatal(err)
	}
	if err := SetProfileHotkey("B", "Shift+Ctrl+1"); err == nil {
		t.Error("two profiles got the same shortcut")
	}
	if err := SetProfileHotkey("B", "alt+v"); err == nil {
		t.Error("a profile got the mic shortcut")
	}
	if err := SetProfileHotkey("B", "q"); err == nil {
		t.Error("a bare letter was accepted")
	}
	keys := ProfileHotkeys()
	if len(keys) != 1 || keys[0].Name != "A" || keys[0].Hotkey != "Ctrl+Shift+1" {
		t.Errorf("hotkeys = %+v", keys)
	}
	if err := SetProfileHotkey("A", ""); err != nil || len(ProfileHotkeys()) != 0 {
		t.Errorf("clearing: %v %+v", err, ProfileHotkeys())
	}
}

func TestProfileAppearanceAndNames(t *testing.T) {
	useTempProfiles(t)
	if err := CreateProfile(""); err == nil {
		t.Error("an empty name was accepted")
	}
	if err := CreateProfile(strings.Repeat("x", maxProfileName+1)); err == nil {
		t.Error("an overlong name was accepted")
	}
	if err := CreateProfile("tab\there"); err != nil {
		t.Errorf("whitespace should fold to spaces: %v", err)
	}
	if ListProfiles()[0].Name != "tab here" {
		t.Errorf("name = %q", ListProfiles()[0].Name)
	}
	if err := SetProfileAppearance("tab here", json.RawMessage(`[1,2]`)); err == nil {
		t.Error("a non-object appearance was accepted")
	}
	if err := SetProfileAppearance("tab here", json.RawMessage(`{"kind":"appearance","hue":200}`)); err != nil {
		t.Fatal(err)
	}
	if raw, ok := ProfileAppearance("TAB HERE"); !ok || !strings.Contains(string(raw), "200") {
		t.Errorf("appearance = %s %v", raw, ok)
	}
	if !ListProfiles()[0].HasAppearance {
		t.Error("HasAppearance not set")
	}
	_ = SetProfileAppearance("tab here", nil)
	if _, ok := ProfileAppearance("tab here"); ok {
		t.Error("appearance not cleared")
	}
}

func TestNormalizeStoreDropsBadEntries(t *testing.T) {
	good, _ := profileSettings(DefaultConfig())
	in := profileStore{
		Active: "missing",
		Profiles: []Profile{
			{Name: "One", Settings: good, Hotkey: "ctrl+f1"},
			{Name: "one", Settings: good},                        // duplicate name
			{Name: "", Settings: good},                           // no name
			{Name: "Bad", Settings: json.RawMessage(`"nope"`)},   // damaged
			{Name: "Two", Settings: good, Hotkey: "Ctrl+F1"},     // duplicate key
			{Name: "Three", Settings: good, Hotkey: "nonsense+"}, // bad key
			{Name: "Leak", Settings: json.RawMessage(`{"OSC":{"host":"9.9.9.9"},"HeartRateOptions":{"pulsoid_token":"leaktoken123"}}`)},
		},
	}
	out := normalizeStore(in)
	var names []string
	for _, p := range out.Profiles {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "One,Two,Three,Leak" {
		t.Errorf("names = %v", names)
	}
	if out.Active != "" {
		t.Errorf("active = %q, want none", out.Active)
	}
	if out.Profiles[0].Hotkey != "Ctrl+F1" || out.Profiles[1].Hotkey != "" || out.Profiles[2].Hotkey != "" {
		t.Errorf("hotkeys = %q %q %q", out.Profiles[0].Hotkey, out.Profiles[1].Hotkey, out.Profiles[2].Hotkey)
	}
	if s := string(out.Profiles[3].Settings); strings.Contains(s, "9.9.9.9") || strings.Contains(s, "leaktoken123") {
		t.Errorf("normalized settings kept per-PC values: %s", s)
	}
}
