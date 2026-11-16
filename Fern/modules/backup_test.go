package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportLeavesOutSecrets(t *testing.T) {
	useTempProfiles(t)
	UpdateConfig(func(c *Config) {
		c.AssistOptions.OpenAIKey = "sealed-openai"
		c.AssistOptions.AnthropicKey = "sealed-anthropic"
		c.STTOptions.Key = "sealed-stt"
		c.HeartRate.PulsoidToken = "pulsoid-token"
		c.HeartRate.HypeRateKey = "hyperate-key"
		c.HeartRate.CustomURL = "https://example.invalid/?token=abc"
		c.TimeOptions.Label = "exported"
	})
	_ = CreateProfile("P")
	data, err := ExportBackup()
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, leak := range []string{"sealed-openai", "sealed-anthropic", "sealed-stt", "pulsoid-token", "hyperate-key", "token=abc"} {
		if strings.Contains(text, leak) {
			t.Errorf("export contains %q", leak)
		}
	}
	if !strings.Contains(text, "exported") || !strings.Contains(text, `"name": "P"`) {
		t.Error("export is missing the settings or the profiles")
	}
}

func TestImportRoundTripKeepsSecrets(t *testing.T) {
	dir := useTempProfiles(t)
	UpdateConfig(func(c *Config) { c.TimeOptions.Label = "before" })
	_ = CreateProfile("Old")
	data, err := ExportBackup()
	if err != nil {
		t.Fatal(err)
	}

	_ = DeleteProfile("Old")
	UpdateConfig(func(c *Config) {
		c.TimeOptions.Label = "after"
		c.AssistOptions.OpenAIKey = "sealed-key"
		c.HeartRate.PulsoidToken = "my-token"
	})
	if err := ImportBackup(data); err != nil {
		t.Fatal(err)
	}
	got := GetConfig()
	if got.TimeOptions.Label != "before" {
		t.Errorf("label = %q, want before", got.TimeOptions.Label)
	}
	if got.AssistOptions.OpenAIKey != "sealed-key" || got.HeartRate.PulsoidToken != "my-token" {
		t.Error("import cleared the secrets")
	}
	if ActiveProfile() != "Old" || len(ListProfiles()) != 1 {
		t.Errorf("profiles = %+v active %q", ListProfiles(), ActiveProfile())
	}
	list := Backups()
	if len(list) != 1 || list[0].Reason != BackupImport {
		t.Fatalf("backups = %+v", list)
	}
	saved, err := os.ReadFile(filepath.Join(dir, "backups", list[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "after") {
		t.Error("the automatic backup does not hold the settings from before the import")
	}

	// Restoring the automatic backup brings "after" back.
	if err := RestoreBackup(list[0].Name); err != nil {
		t.Fatal(err)
	}
	if GetConfig().TimeOptions.Label != "after" {
		t.Errorf("restore: label = %q", GetConfig().TimeOptions.Label)
	}
	if err := RestoreBackup("../config.json"); err == nil {
		t.Error("restored a file outside the backups folder")
	}
}

func TestImportValidates(t *testing.T) {
	useTempProfiles(t)
	UpdateConfig(func(c *Config) { c.TimeOptions.Label = "keep" })
	for name, in := range map[string]string{
		"not json":   "hello",
		"array":      "[1]",
		"appearance": `{"app":"fern","kind":"appearance","hue":3}`,
		"other":      `{"foo":1}`,
		"no config":  `{"app":"fern","kind":"backup","version":1}`,
		"newer":      `{"app":"fern","kind":"backup","version":9,"config":{}}`,
		"bad types":  `{"app":"fern","kind":"backup","version":1,"config":{"display_options":"x"}}`,
	} {
		if err := ImportBackup([]byte(in)); err == nil {
			t.Errorf("%s: imported", name)
		}
	}
	if GetConfig().TimeOptions.Label != "keep" {
		t.Error("a failed import changed the settings")
	}
	if len(Backups()) != 0 {
		t.Error("a failed import made a backup")
	}

	// A hand-copied config.json works, and out-of-range values are fixed.
	if err := ImportBackup([]byte(`{"display_options":{"max_chars":99999,"time":false},"OSC":{"port":-4}}`)); err != nil {
		t.Fatal(err)
	}
	got := GetConfig()
	if got.DisplayOptions.MaxChars == 99999 || got.OSCOptions.Port != 9000 || got.DisplayOptions.Time {
		t.Errorf("sanitize did not run: max %d port %d time %v", got.DisplayOptions.MaxChars, got.OSCOptions.Port, got.DisplayOptions.Time)
	}
}

func TestAutoBackupKeepsTen(t *testing.T) {
	useTempProfiles(t)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	n := 0
	backupNow = func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	defer func() { backupNow = time.Now }()
	for i := 0; i < KeepBackups+3; i++ {
		if _, err := AutoBackup(BackupUpdate); err != nil {
			t.Fatal(err)
		}
	}
	list := Backups()
	if len(list) != KeepBackups {
		t.Fatalf("kept %d backups, want %d", len(list), KeepBackups)
	}
	if !(list[0].Created > list[len(list)-1].Created) {
		t.Error("backups are not newest first")
	}
	entries, _ := os.ReadDir(BackupDir())
	if len(entries) != KeepBackups {
		t.Errorf("%d files on disk", len(entries))
	}
	var file backupFile
	data, _ := os.ReadFile(filepath.Join(BackupDir(), list[0].Name))
	if err := json.Unmarshal(data, &file); err != nil || file.Kind != "backup" {
		t.Errorf("backup file: %v %+v", err, file.Kind)
	}
	if _, err := AutoBackup("weird"); err == nil {
		t.Error("an unknown reason was accepted")
	}
}
