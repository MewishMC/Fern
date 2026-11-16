package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParseVoicemodMute(t *testing.T) {
	cases := []struct {
		msg  string
		want string // "", "true" or "false"
	}{
		{`{"actionType":"toggleMuteMic","actionObject":{"value":true}}`, "true"},
		{`{"actionType":"getMuteMicStatus","actionID":"x","actionObject":{"value":false}}`, "false"},
		{`{"actionType":"muteMicrophoneEnabledEvent","actionObject":{}}`, "true"},
		{`{"actionType":"muteMicrophoneDisabledEvent"}`, "false"},
		{`{"action":"getMuteMicStatus","payload":{"muted":true}}`, "true"},
		{`{"actionType":"getVoices","actionObject":{"value":true}}`, ""},
		{`{"actionType":"toggleMuteMic","actionObject":{}}`, ""},
	}
	for _, c := range cases {
		m, err := parseVoicemodMessage([]byte(c.msg))
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if m.Muted != nil {
			got = map[bool]string{true: "true", false: "false"}[*m.Muted]
		}
		if got != c.want {
			t.Errorf("%s: muted %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestVoicemodMicSanitize(t *testing.T) {
	def := DefaultConfig().VoicemodOptions
	if def.BleepMaxSec != 10 || def.MuteText == "" || def.AnnounceMute || def.MuteHotkey != "" {
		t.Errorf("defaults: %+v", def)
	}
	c := DefaultConfig()
	c.VoicemodOptions.BleepMaxSec = 500
	c.VoicemodOptions.MuteHotkey = "ctrl+shift+m"
	c.VoicemodOptions.BleepHotkey = "Ctrl+Shift+M"
	c.sanitize()
	v := c.VoicemodOptions
	if v.BleepMaxSec != 60 || v.MuteHotkey != "Ctrl+Shift+M" || v.BleepHotkey != "" {
		t.Errorf("sanitized: %+v", v)
	}
	c.VoicemodOptions.MuteHotkey = "nonsense+q"
	c.VoicemodOptions.BleepMaxSec = -3
	c.sanitize()
	if c.VoicemodOptions.MuteHotkey != "" || c.VoicemodOptions.BleepMaxSec != 1 {
		t.Errorf("invalid: %+v", c.VoicemodOptions)
	}
}

// micFake answers registration, the mute calls and the beep.
func micFake(t *testing.T, got chan<- map[string]any, muted *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var wmu sync.Mutex
		reply := func(v any) {
			wmu.Lock()
			defer wmu.Unlock()
			b, _ := json.Marshal(v)
			_ = conn.WriteMessage(websocket.TextMessage, b)
		}
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			got <- req
			switch req["action"] {
			case "registerClient":
				reply(map[string]any{"actionType": "registerClient", "actionID": req["id"], "payload": map[string]any{"status": map[string]any{"code": 200}}})
			case "getMuteMicStatus":
				reply(map[string]any{"actionType": "getMuteMicStatus", "actionID": req["id"], "actionObject": map[string]any{"value": muted.Load()}})
			case "toggleMuteMic":
				muted.Store(!muted.Load())
				reply(map[string]any{"actionType": "toggleMuteMic", "actionObject": map[string]any{"value": muted.Load()}})
			}
		}
	}))
}

func TestVoicemodMuteAndBleep(t *testing.T) {
	got := make(chan map[string]any, 256)
	var muted atomic.Bool
	srv := micFake(t, got, &muted)
	defer srv.Close()
	vmBleepLimit = func() time.Duration { return 300 * time.Millisecond }
	t.Cleanup(func() {
		vmBleepLimit = func() time.Duration { return time.Duration(GetConfig().VoicemodOptions.BleepMaxSec) * time.Second }
		clearVoicemod()
		bleepLeftOn()
		setVoicemodLink(linkOff, "")
	})
	waitReq := func(action string, check func(p map[string]any) bool) {
		t.Helper()
		deadline := time.After(4 * time.Second)
		for {
			select {
			case req := <-got:
				p, _ := req["payload"].(map[string]any)
				if req["action"] == action && (check == nil || check(p)) {
					return
				}
			case <-deadline:
				t.Fatalf("no %s request", action)
			}
		}
	}
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn := dialVoicemod([]string{url})
	if conn == nil {
		t.Fatal("could not dial the fake")
	}
	var wanted atomic.Bool
	wanted.Store(true)
	done := make(chan error, 1)
	go func() { done <- voicemodSession(conn, "key", wanted.Load) }()

	waitFor(t, "mute state", func() bool { _, ok := VoicemodMuted(); return ok })
	if m, _ := VoicemodMuted(); m {
		t.Error("starts muted")
	}
	VoicemodCommand("mute", "")
	waitReq("toggleMuteMic", nil)
	waitFor(t, "muted", func() bool { m, _ := VoicemodMuted(); return m && CurrentVoicemod().Muted })

	cfg := DefaultConfig()
	cfg.VoicemodOptions.AnnounceMute = true
	cfg.VoicemodOptions.AnnounceVoice = false
	if line := voicemodLine(cfg); line != "🔇 Muted" {
		t.Errorf("line while muted: %q", line)
	}

	// Setting the state it already has sends no toggle.
	VoicemodCommand("muteSet", "1")
	waitReq("getMuteMicStatus", nil)
	VoicemodCommand("muteSet", "0")
	waitReq("toggleMuteMic", nil)
	waitFor(t, "unmuted", func() bool { m, _ := VoicemodMuted(); return !m })
	if muted.Load() {
		t.Error("the fake is still muted")
	}

	// A held bleep starts once and stops on release.
	VoicemodCommand("bleepOn", "")
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(1) })
	waitFor(t, "bleeping", vmBleeping)
	VoicemodCommand("bleepOff", "")
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(0) })
	waitFor(t, "bleep over", func() bool { return !vmBleeping() })

	// A bleep whose release never comes stops at the limit.
	VoicemodCommand("bleepOn", "")
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(1) })
	start := time.Now()
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(0) })
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("auto stop took %v", d)
	}

	// A short timed bleep from a rule.
	VoicemodCommand("bleepOn", "100")
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(1) })
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(0) })

	// Ending the session mid-bleep switches it off on the next one.
	VoicemodCommand("bleepOn", "")
	waitFor(t, "bleeping again", vmBleeping)
	wanted.Store(false)
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("session did not stop")
	}
	conn.Close()
	clearVoicemod()
	if vmBleeping() {
		t.Error("still bleeping after the session")
	}
	conn = dialVoicemod([]string{url})
	wanted.Store(true)
	go func() { done <- voicemodSession(conn, "key", wanted.Load) }()
	waitReq("setBeepSound", func(p map[string]any) bool { return p["badLanguage"] == float64(0) })
	wanted.Store(false)
	<-done
	conn.Close()
}

func TestVoicemodMuteRuleNeedsConnection(t *testing.T) {
	setVoicemodLink(linkWaiting, "")
	t.Cleanup(func() { setVoicemodLink(linkOff, "") })
	for _, a := range RuleActions() {
		if a.ID == ActionVoicemodMute || a.ID == ActionVoicemodBleep {
			if _, err := a.Run(RuleContext{}, SanitizeRuleParams(a.Fields, nil)); err == nil {
				t.Errorf("%s ran without Voicemod", a.ID)
			}
		}
	}
}
