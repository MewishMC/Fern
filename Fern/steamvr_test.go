package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSteamVRManifest(t *testing.T) {
	data, err := buildManifest(`C:\Games\Fern\Fern.exe`)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Source       string `json:"source"`
		Applications []map[string]any
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Source != "builtin" || len(m.Applications) != 1 {
		t.Fatalf("manifest = %s", data)
	}
	app := m.Applications[0]
	want := map[string]any{
		"app_key":              steamVRAppKey,
		"launch_type":          "binary",
		"binary_path_windows":  `C:\Games\Fern\Fern.exe`,
		"arguments":            "--steamvr",
		"is_dashboard_overlay": true,
	}
	for k, v := range want {
		if app[k] != v {
			t.Errorf("%s = %v, want %v", k, app[k], v)
		}
	}
	if app["strings"].(map[string]any)["en_us"].(map[string]any)["name"] != "Fern" {
		t.Errorf("strings = %v", app["strings"])
	}
}

func TestWriteManifestStaysInFernFolder(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	path, err := writeManifest()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(os.Getenv("LOCALAPPDATA"), "Fern", "fern.vrmanifest") {
		t.Errorf("path = %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
	if got := currentSteamVR().Manifest; got != path {
		t.Errorf("state manifest = %q", got)
	}
}
