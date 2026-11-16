package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPathUsesLocalAppData(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	if got, want := configPath(), filepath.Join(dir, "Fern", "config.json"); got != want {
		t.Errorf("configPath() = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "Fern")); err != nil {
		t.Errorf("config folder not created: %v", err)
	}
}

func TestMigrateConfigCopiesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "old", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"timeout_ms":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")

	migrateConfig(path, []string{filepath.Join(dir, "missing.json"), legacy})
	if data, err := os.ReadFile(path); err != nil || string(data) != `{"timeout_ms":5}` {
		t.Fatalf("migrated config = %q, %v", data, err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("legacy file should be renamed away, stat err = %v", err)
	}
	if _, err := os.Stat(legacy + ".old"); err != nil {
		t.Errorf("legacy file should be kept as config.json.old: %v", err)
	}

	// An existing config is never overwritten.
	if err := os.WriteFile(legacy, []byte(`{"timeout_ms":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	migrateConfig(path, []string{legacy})
	if data, _ := os.ReadFile(path); string(data) != `{"timeout_ms":5}` {
		t.Errorf("existing config overwritten: %q", data)
	}
}

func TestSaveNowBeforeLoadWritesNothing(t *testing.T) {
	oldPath := cfgPath
	cfgPath = ""
	t.Cleanup(func() { cfgPath = oldPath })
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := SaveNow(); err == nil {
		t.Error("SaveNow with no config path: want an error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("SaveNow left files behind: %v", entries)
	}
}
