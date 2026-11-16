package modules

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := encodeFrame(&buf, ipcOpFrame, map[string]string{"cmd": "PING"}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	if raw[0] != 1 || raw[1] != 0 || raw[4] != byte(len(`{"cmd":"PING"}`)) {
		t.Fatalf("header = % x", raw[:8])
	}
	op, body, err := decodeFrame(&buf)
	if err != nil || op != ipcOpFrame || string(body) != `{"cmd":"PING"}` {
		t.Fatalf("decode = %d %q %v", op, body, err)
	}

	huge := []byte{1, 0, 0, 0, 0xff, 0xff, 0xff, 0x7f}
	if _, _, err := decodeFrame(bytes.NewReader(huge)); err != errFrameTooLarge {
		t.Fatalf("oversized frame: %v", err)
	}
	short := []byte{1, 0, 0, 0, 10, 0, 0, 0, '{'}
	if _, _, err := decodeFrame(bytes.NewReader(short)); err == nil {
		t.Fatal("truncated frame decoded")
	}
}

// fakeDiscord plays the Discord client's side of the pipe.
type fakeDiscord struct {
	mu      sync.Mutex
	out     bytes.Buffer // frames waiting for Fern to read
	got     []rpcMessage
	shake   map[string]any
	respond func(f *fakeDiscord, m rpcMessage)
}

func (f *fakeDiscord) Write(b []byte) (int, error) {
	op, body, err := decodeFrame(bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if op == ipcOpHandshake {
		_ = json.Unmarshal(body, &f.shake)
		f.push(rpcMessage{Cmd: "DISPATCH", Evt: "READY", Data: json.RawMessage(`{"v":1,"user":{"id":"1","username":"fern","global_name":"Fern"}}`)})
		return len(b), nil
	}
	var m rpcMessage
	_ = json.Unmarshal(body, &m)
	f.got = append(f.got, m)
	if f.respond != nil {
		f.respond(f, m)
	}
	return len(b), nil
}

// push queues a frame for Fern; the caller holds f.mu.
func (f *fakeDiscord) push(m rpcMessage) { _ = encodeFrame(&f.out, ipcOpFrame, m) }

func (f *fakeDiscord) reply(m rpcMessage, data string) {
	f.push(rpcMessage{Cmd: m.Cmd, Nonce: m.Nonce, Data: json.RawMessage(data)})
}

func (f *fakeDiscord) Read(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out.Read(b)
}

func (f *fakeDiscord) Available() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.out.Len(), nil
}

func (f *fakeDiscord) Close() error { return nil }

func (f *fakeDiscord) sent(cmd string) []rpcMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []rpcMessage
	for _, m := range f.got {
		if m.Cmd == cmd {
			out = append(out, m)
		}
	}
	return out
}

func (f *fakeDiscord) event(evt, data string) {
	f.mu.Lock()
	f.push(rpcMessage{Cmd: "DISPATCH", Evt: evt, Data: json.RawMessage(data)})
	f.mu.Unlock()
}

func useTempSecrets(t *testing.T) {
	t.Helper()
	secretsMu.Lock()
	secretsFile = filepath.Join(t.TempDir(), "secrets.dat")
	secretsLoaded = false
	secretsMu.Unlock()
	t.Cleanup(func() {
		secretsMu.Lock()
		secretsFile, secretsLoaded, secretsCache = "", false, nil
		secretsMu.Unlock()
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runSession starts a session against fake and returns a stop function.
func runSession(t *testing.T, fake *fakeDiscord, cfg Config) (*discordSession, func()) {
	t.Helper()
	var stopped atomic.Bool
	s := newDiscordSession(fake, func() Config {
		if stopped.Load() {
			c := cfg
			c.DiscordOptions.Presence, c.DisplayOptions.Discord = false, false
			return c
		}
		return cfg
	})
	s.world = func() PresenceWorld {
		return PresenceWorld{Name: "The Black Cat", WorldID: "wrld_1", InstanceID: "123~region(eu)", Type: "Public", Region: "Europe", Since: 1000}
	}
	s.running = func() bool { return true }
	s.players = func() int { return 12 }
	s.inVR = func() bool { return true }
	done := make(chan error, 1)
	go func() { done <- s.run() }()
	return s, func() {
		stopped.Store(true)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("session did not stop")
		}
		clearDiscordVoice()
	}
}

func TestDiscordPresence(t *testing.T) {
	useTempSecrets(t)
	fake := &fakeDiscord{respond: func(f *fakeDiscord, m rpcMessage) { f.reply(m, `{}`) }}
	cfg := DefaultConfig()
	cfg.DiscordOptions.ClientID = "123456"
	cfg.DiscordOptions.Presence = true
	cfg.DiscordOptions.JoinButton = true
	cfg.DiscordOptions.LargeImage = "vrchat"
	_, stop := runSession(t, fake, cfg)
	defer stop()

	waitFor(t, "SET_ACTIVITY", func() bool { return len(fake.sent("SET_ACTIVITY")) > 0 })
	if fake.shake["client_id"] != "123456" || fake.shake["v"] != float64(1) {
		t.Errorf("handshake = %v", fake.shake)
	}
	args, _ := json.Marshal(fake.sent("SET_ACTIVITY")[0].Args)
	var got struct {
		PID      int `json:"pid"`
		Activity struct {
			Details    string `json:"details"`
			State      string `json:"state"`
			Timestamps struct {
				Start int64 `json:"start"`
			} `json:"timestamps"`
			Assets  map[string]string   `json:"assets"`
			Buttons []map[string]string `json:"buttons"`
		} `json:"activity"`
	}
	_ = json.Unmarshal(args, &got)
	a := got.Activity
	if got.PID == 0 || a.Details != "In The Black Cat" || a.State != "Public • 12 players" || a.Timestamps.Start != 1000 {
		t.Errorf("activity = %s", args)
	}
	if a.Assets["large_image"] != "vrchat" || len(a.Buttons) != 1 || !strings.Contains(a.Buttons[0]["url"], "worldId=wrld_1") {
		t.Errorf("assets/buttons = %s", args)
	}
}

func TestDiscordVoiceLine(t *testing.T) {
	useTempSecrets(t)
	_ = SetSecret(SecretDiscordAccess, "stored-token")
	channel := `{"id":"77","name":"Hangout","voice_states":[
		{"nick":"","user":{"id":"1","username":"fern","global_name":"Fern"}},
		{"nick":"Ali","user":{"id":"2","username":"alice"}},
		{"user":{"id":"3","username":"bob"}}]}`
	fake := &fakeDiscord{respond: func(f *fakeDiscord, m rpcMessage) {
		switch m.Cmd {
		case "AUTHENTICATE":
			f.reply(m, `{"user":{"id":"1"}}`)
		case "GET_SELECTED_VOICE_CHANNEL":
			f.reply(m, channel)
		case "GET_VOICE_SETTINGS":
			f.reply(m, `{"mute":true,"deaf":false}`)
		default:
			f.reply(m, `{}`)
		}
	}}
	cfg := DefaultConfig()
	cfg.DiscordOptions.ClientID = "123456"
	cfg.DisplayOptions.Discord = true
	cfg.DiscordOptions.HideSelf = true
	_, stop := runSession(t, fake, cfg)
	defer stop()

	waitFor(t, "channel", func() bool { return strings.Contains(discordLine(cfg), "Hangout") })
	auth := fake.sent("AUTHENTICATE")
	if b, _ := json.Marshal(auth[0].Args); !strings.Contains(string(b), "stored-token") {
		t.Errorf("AUTHENTICATE args = %s", b)
	}
	subs := map[string]bool{}
	for _, m := range fake.sent("SUBSCRIBE") {
		subs[m.Evt] = true
	}
	for _, evt := range []string{"VOICE_CHANNEL_SELECT", "SPEAKING_START", "SPEAKING_STOP", "VOICE_STATE_CREATE", "VOICE_STATE_DELETE"} {
		if !subs[evt] {
			t.Errorf("not subscribed to %s", evt)
		}
	}
	waitFor(t, "mute state", func() bool { return strings.HasSuffix(discordLine(cfg), "ᵐᵘᵗᵉᵈ") })
	if got := discordLine(cfg); got != "🔊 Hangout (3) | 🎙️ Quiet... ᵐᵘᵗᵉᵈ" {
		t.Errorf("quiet line = %q", got)
	}

	fake.event("SPEAKING_START", `{"user_id":"1","channel_id":"77"}`) // self, hidden
	fake.event("SPEAKING_START", `{"user_id":"3","channel_id":"77"}`)
	waitFor(t, "first speaker", func() bool { return strings.Contains(discordLine(cfg), "bob") })
	fake.event("SPEAKING_START", `{"user_id":"2","channel_id":"77"}`)
	waitFor(t, "speakers", func() bool { return strings.Contains(discordLine(cfg), "bob, Ali") })

	fake.event("VOICE_STATE_CREATE", `{"user":{"id":"4","username":"cat"}}`)
	time.Sleep(60 * time.Millisecond)
	fake.event("SPEAKING_START", `{"user_id":"4","channel_id":"77"}`)
	waitFor(t, "overflow", func() bool { return discordLine(cfg) == "🔊 Hangout (4) | 🎙️ bob, Ali +1 ᵐᵘᵗᵉᵈ" })

	fake.event("SPEAKING_STOP", `{"user_id":"3","channel_id":"77"}`)
	// Still shown during the debounce, gone after it.
	if !strings.Contains(discordLine(cfg), "bob") {
		t.Error("speaker dropped before the debounce ran out")
	}
	waitFor(t, "debounce drop", func() bool { return strings.Contains(discordLine(cfg), "Ali, cat") })

	fake.event("VOICE_CHANNEL_SELECT", `{"channel_id":null}`)
	waitFor(t, "left channel", func() bool { return discordLine(cfg) == "" })
}

func TestDiscordAuthorizeExchangesCode(t *testing.T) {
	useTempSecrets(t)
	_ = SetSecret(SecretDiscordClientSecret, "shh")
	var form atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form.Store(r.PostForm)
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":604800}`))
	}))
	defer srv.Close()
	oldURL := discordTokenURL
	discordTokenURL = srv.URL
	defer func() { discordTokenURL = oldURL }()

	fake := &fakeDiscord{respond: func(f *fakeDiscord, m rpcMessage) {
		switch m.Cmd {
		case "AUTHORIZE":
			f.reply(m, `{"code":"the-code"}`)
		case "AUTHENTICATE":
			f.reply(m, `{"user":{"id":"1"}}`)
		case "GET_SELECTED_VOICE_CHANNEL":
			f.reply(m, `null`)
		default:
			f.reply(m, `{}`)
		}
	}}
	cfg := DefaultConfig()
	cfg.DiscordOptions.ClientID = "123456"
	cfg.DisplayOptions.Discord = true
	DiscordAuthorize()
	_, stop := runSession(t, fake, cfg)
	defer stop()

	waitFor(t, "AUTHENTICATE", func() bool { return len(fake.sent("AUTHENTICATE")) > 0 })
	b, _ := json.Marshal(fake.sent("AUTHORIZE")[0].Args)
	if !strings.Contains(string(b), `"rpc.voice.read"`) || !strings.Contains(string(b), `"123456"`) {
		t.Errorf("AUTHORIZE args = %s", b)
	}
	f := form.Load().(url.Values)
	if f["code"][0] != "the-code" || f["client_secret"][0] != "shh" || f["redirect_uri"][0] != "http://localhost" {
		t.Errorf("token form = %v", f)
	}
	if GetSecret(SecretDiscordAccess) != "new-access" || GetSecret(SecretDiscordRefresh) != "new-refresh" {
		t.Error("tokens not stored")
	}
	waitFor(t, "signed in", func() bool { return CurrentDiscord(cfg).Voice.State == linkConnected })
	if discordLine(cfg) != "" {
		t.Errorf("not in a call, empty NotInVCText: line = %q", discordLine(cfg))
	}
	cfg.DiscordOptions.NotInVCText = "not in a call"
	if discordLine(cfg) != "not in a call" {
		t.Errorf("NotInVCText line = %q", discordLine(cfg))
	}
}

func TestPostTokenHidesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","echo":"secret-value"}`))
	}))
	defer srv.Close()
	_, _, err := postToken(srv.Client(), srv.URL, map[string][]string{"client_secret": {"secret-value"}})
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "secret-value") {
		t.Errorf("err = %v", err)
	}
}

func TestPresenceTextLimits(t *testing.T) {
	if got := presenceText("a"); len(got) != 2 {
		t.Errorf("one char presence = %q", got)
	}
	if got := presenceText(strings.Repeat("x", 300)); len([]rune(got)) != 128 {
		t.Errorf("long presence = %d runes", len([]rune(got)))
	}
	cfg := DefaultConfig()
	if presenceActivity(cfg, PresenceWorld{}, true, 0, false) != nil {
		t.Error("presence shown while switched off")
	}
	cfg.DiscordOptions.Presence = true
	if presenceActivity(cfg, PresenceWorld{}, false, 0, false) != nil {
		t.Error("presence shown while VRChat is closed")
	}
	act := presenceActivity(cfg, PresenceWorld{}, true, 0, false)
	if act["details"] != "In VRChat" || act["state"] != "Desktop" {
		t.Errorf("unknown world presence = %v", act)
	}
	cfg.DiscordOptions.JoinButton = true
	act = presenceActivity(cfg, PresenceWorld{WorldID: "wrld_1", InstanceID: "1~private(usr_1)", Type: "Invite"}, true, 3, true)
	if _, ok := act["buttons"]; ok {
		t.Error("join button offered for an invite-only instance")
	}
}
