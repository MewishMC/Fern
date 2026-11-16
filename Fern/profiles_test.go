package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Fern/modules"
)

func TestBackupExportOnlyForThePanel(t *testing.T) {
	for _, c := range []struct {
		host, origin string
		want         int
	}{
		{"127.0.0.1:1932", "", http.StatusOK},
		{"127.0.0.1:1932", "http://127.0.0.1:1932", http.StatusOK},
		{"localhost:1932", "", http.StatusOK},
		{"127.0.0.1:1932", "https://example.com", http.StatusForbidden},
		{"rebound.example.com:1932", "http://rebound.example.com:1932", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/backup/export", nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		handleBackupExport(rec, req)
		if rec.Code != c.want {
			t.Errorf("host %s origin %q: status %d, want %d", c.host, c.origin, rec.Code, c.want)
			continue
		}
		if c.want != http.StatusOK {
			continue
		}
		if !strings.Contains(rec.Header().Get("Content-Disposition"), "fern-backup-") {
			t.Error("export is not offered as a download")
		}
		var file map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &file); err != nil || file["config"] == nil {
			t.Errorf("export is not a backup: %v", err)
		}
	}

	post := httptest.NewRequest(http.MethodPost, "/backup/export", nil)
	post.Host = "127.0.0.1:1932"
	rec := httptest.NewRecorder()
	handleBackupExport(rec, post)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", rec.Code)
	}
}

func TestProfileCommandsReportOutcome(t *testing.T) {
	before := readNotice(&profileNotice).Seq
	handleProfileCommand(command{Type: "profile", Action: "switch", Module: "no such profile"})
	n := readNotice(&profileNotice)
	if n.Seq != before+1 || !n.Bad {
		t.Errorf("notice = %+v, want a new error", n)
	}

	handleProfileCommand(command{Type: "profile", Action: "create", Text: "Test profile"})
	defer func() { _ = modules.DeleteProfile("Test profile") }()
	if n := readNotice(&profileNotice); n.Bad || modules.ActiveProfile() != "Test profile" {
		t.Errorf("create: notice %+v, active %q", n, modules.ActiveProfile())
	}
	title, items := trayProfiles()
	if title != "Profile: Test profile" || len(items) == 0 || !items[len(items)-1].Checked {
		t.Errorf("tray: %q %+v", title, items)
	}

	handleProfileCommand(command{Type: "profile", Action: "hotkey", Module: "Test profile", Text: "Ctrl+Alt+F7"})
	keys := profileHotkeys()
	if len(keys) != 1 || keys[0].ID != profileKeyPrefix+"Test profile" || keys[0].Key != 0x76 {
		t.Errorf("hotkeys = %+v", keys)
	}
}
