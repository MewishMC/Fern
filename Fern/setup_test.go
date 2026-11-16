package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"Fern/modules"

	"github.com/hypebeast/go-osc/osc"
)

func TestSetupTestSendsWithTheDisplayOff(t *testing.T) {
	if modules.GetConfig().DisplayOptions.Show {
		t.Skip("needs the display off, which is the unloaded config in tests")
	}
	var got []*osc.Message
	old := setupSend
	setupSend = func(_ modules.OSCOptions, msg *osc.Message) int {
		got = append(got, msg)
		return 1
	}
	t.Cleanup(func() { setupSend = old })

	before := readNotice(&setupNotice).Seq
	handleCommand(command{Type: "setup", Action: "test"})
	if len(got) != 1 || got[0].Address != "/chatbox/input" {
		t.Fatalf("sent %v", got)
	}
	if len(got[0].Arguments) != 3 || got[0].Arguments[0] != setupTestText || got[0].Arguments[1] != true {
		t.Errorf("arguments %v: want the text, sent at once", got[0].Arguments)
	}
	st := currentSetup()
	if st.TestMS == 0 || st.Notice.Seq != before+1 || st.Notice.Bad {
		t.Errorf("state after the test: %+v", st)
	}
	if len([]rune(setupTestText)) > 144 {
		t.Error("the test message does not fit the chatbox")
	}

	setupSend = func(modules.OSCOptions, *osc.Message) int { return 0 }
	handleCommand(command{Type: "setup", Action: "test"})
	if n := readNotice(&setupNotice); !n.Bad {
		t.Errorf("a failed send should say so: %+v", n)
	}
}

func TestSetupLinesReportsAnEmptyChoice(t *testing.T) {
	before := readNotice(&setupNotice).Seq
	handleCommand(command{Type: "setup", Action: "lines", Order: []string{"nonsense"}})
	if n := readNotice(&setupNotice); n.Seq != before+1 || !n.Bad {
		t.Errorf("notice %+v", n)
	}
}

func TestSetupStateJSON(t *testing.T) {
	data, err := json.Marshal(currentSetup())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"done", "oscFolder", "oscFolderPath", "avatarConfigs", "oscSetting", "testMs", "testTargets", "notice"} {
		if _, ok := st[key]; !ok {
			t.Errorf("setup state has no %q: %s", key, data)
		}
	}
	if st["testTargets"] == nil {
		t.Error("testTargets should be a list, not null")
	}
}

func TestSessionExportDownload(t *testing.T) {
	for _, c := range []struct {
		query, host, origin string
		want                int
		kind                string
	}{
		{"what=all&format=txt", "127.0.0.1:1932", "", http.StatusOK, "text/plain"},
		{"what=activity&format=csv", "localhost:1932", "http://localhost:1932", http.StatusOK, "text/csv"},
		{"what=messages&format=csv", "127.0.0.1:1932", "https://example.com", http.StatusForbidden, ""},
		{"what=messages&format=csv", "rebound.example.com:1932", "", http.StatusForbidden, ""},
		{"what=secrets&format=txt", "127.0.0.1:1932", "", http.StatusBadRequest, ""},
		{"what=all&format=exe", "127.0.0.1:1932", "", http.StatusBadRequest, ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "/export/session?"+c.query, nil)
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		rec := httptest.NewRecorder()
		handleSessionExport(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s from %s: status %d, want %d", c.query, c.host, rec.Code, c.want)
			continue
		}
		if c.want != http.StatusOK {
			continue
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), c.kind) {
			t.Errorf("%s: content type %q", c.query, rec.Header().Get("Content-Type"))
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="fern-`) {
			t.Errorf("%s: not a download: %q", c.query, cd)
		}
	}

	post := httptest.NewRequest(http.MethodPost, "/export/session?what=all&format=txt", nil)
	post.Host = "127.0.0.1:1932"
	rec := httptest.NewRecorder()
	handleSessionExport(rec, post)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d", rec.Code)
	}
}
