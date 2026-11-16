package modules

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCompactCount(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1000: "1k", 1234: "1.2k", 1299: "1.2k", 12345: "12k",
		999_949: "999k", 999_999: "1M", 1_500_000: "1.5M", 23_000_000: "23M", -5: "0"}
	for n, want := range cases {
		if got := compactCount(n); got != want {
			t.Errorf("compactCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFillTemplate(t *testing.T) {
	got := fillTemplate(`🔊 {channel} {missing} ({count})\n🎙️ {speaking}  {x}`, map[string]string{
		"channel": "Lobby", "count": "3", "speaking": "", "x": "",
	})
	if got != "🔊 Lobby {missing} (3)\n🎙️" {
		t.Errorf("fillTemplate = %q", got)
	}
	if fillTemplate(`{a}\n{b}`, map[string]string{"a": "", "b": "x"}) != "x" {
		t.Error("empty line kept")
	}
}

func TestPresenceWorldJoinURL(t *testing.T) {
	w := PresenceWorld{WorldID: "wrld_a", InstanceID: "123~region(eu)", Type: "Public"}
	if got := w.JoinURL(); got != "https://vrchat.com/home/launch?worldId=wrld_a&instanceId=123~region%28eu%29" {
		t.Errorf("JoinURL = %q", got)
	}
	w.Type = "Friends+"
	if w.JoinURL() != "" {
		t.Error("join link for a friends instance")
	}
}

func TestPresenceWorldFromLog(t *testing.T) {
	reset := func() { selfName, world, inWorld, roomName = "", WorldInfo{}, false, "" }
	reset()
	t.Cleanup(reset)
	at := time.Date(2024, 1, 1, 12, 0, 0, 0, time.Local)
	parseWorldLine("2024.01.01 12:00:00 Log        -  [Behaviour] Joining wrld_4432:83442~hidden(usr_1)~region(eu)", at)
	parseWorldLine("2024.01.01 12:00:01 Log        -  [Behaviour] Joining or Creating Room: The Black Cat", at.Add(time.Second))
	w := currentPresenceWorld()
	if w.Name != "The Black Cat" || w.WorldID != "wrld_4432" || w.InstanceID != "83442~hidden(usr_1)~region(eu)" || w.Type != "Friends+" || w.Since == 0 {
		t.Errorf("world = %+v", w)
	}
	parseWorldLine("2024.01.01 12:10:00 Log        -  [Behaviour] OnLeftRoom", at.Add(10*time.Minute))
	if currentPresenceWorld().Name != "" {
		t.Error("world kept after leaving")
	}
}

func TestSocialSanitize(t *testing.T) {
	c := DefaultConfig()
	c.DiscordOptions.ClientID = " 12a34 "
	c.DiscordOptions.MaxSpeakers = 99
	c.DiscordOptions.DebounceMS = 1
	c.DiscordOptions.Template = ""
	c.DiscordOptions.ButtonURL = "javascript:alert(1)"
	c.TwitchOptions.Channel = "https://www.twitch.tv/Some_User"
	c.TwitchOptions.IntervalSec = 1
	c.TwitchOptions.Separator = ""
	c.SoundpadOptions.DurationSec = 999
	c.VoicemodOptions.SoundSec = 0
	c.sanitize()
	d := c.DiscordOptions
	if d.ClientID != "1234" || d.MaxSpeakers != 10 || d.DebounceMS != 200 || d.Template == "" || d.ButtonURL != "" {
		t.Errorf("discord = %+v", d)
	}
	if c.TwitchOptions.Channel != "some_user" || c.TwitchOptions.IntervalSec != 15 || c.TwitchOptions.Separator != " | " {
		t.Errorf("twitch = %+v", c.TwitchOptions)
	}
	if c.SoundpadOptions.DurationSec != 60 || c.VoicemodOptions.SoundSec != 2 {
		t.Error("durations not clamped")
	}
	def := DefaultConfig()
	if def.DisplayOptions.Discord || def.DisplayOptions.Twitch || def.DisplayOptions.Soundpad ||
		def.DisplayOptions.Voicemod || def.DiscordOptions.Presence || def.VoicemodOptions.Enabled {
		t.Error("an integration is on by default")
	}
}

func TestSecretsRoundTrip(t *testing.T) {
	useTempSecrets(t)
	if err := SetSecret("a", "one"); err != nil {
		t.Fatal(err)
	}
	if err := SetPanelSecret(SecretTwitchToken, "oauth:abc"); err != nil {
		t.Fatal(err)
	}
	if err := SetPanelSecret("discord_access", "x"); err == nil {
		t.Error("panel wrote an internal secret")
	}
	secretsMu.Lock()
	secretsLoaded = false // force a reload from disk
	raw := secretsFile
	secretsMu.Unlock()
	if GetSecret("a") != "one" || GetSecret(SecretTwitchToken) != "abc" {
		t.Error("secrets did not survive a reload")
	}
	if set := SecretsSet(); !set["a"] || set["missing"] {
		t.Errorf("SecretsSet = %v", set)
	}
	data, _ := os.ReadFile(raw)
	if bytes.Contains(data, []byte("one")) || bytes.Contains(data, []byte("abc")) {
		t.Error("secrets.dat holds plain text")
	}
	_ = SetSecret("a", "")
	if GetSecret("a") != "" {
		t.Error("secret not cleared")
	}
}

// twitchFake is a minimal Helix + OAuth server.
func twitchFake(t *testing.T, live bool, tokens *sync.Map) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		valid := auth == "Bearer good" || auth == "OAuth good"
		switch r.URL.Path {
		case "/oauth2/validate":
			if !valid {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"user_id":"42","login":"fern"}`))
		case "/oauth2/token":
			_ = r.ParseForm()
			tokens.Store(r.PostForm.Get("grant_type"), r.PostForm)
			if r.PostForm.Get("grant_type") == "refresh_token" {
				_, _ = w.Write([]byte(`{"access_token":"good","refresh_token":"r2"}`))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":400,"message":"authorization_pending"}`))
		case "/helix/streams":
			if !valid || r.Header.Get("Client-Id") != "cid" || r.URL.Query().Get("user_id") != "42" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if live {
				_, _ = w.Write([]byte(`{"data":[{"type":"live","viewer_count":1234,"game_name":"VRChat","title":"chilling"}]}`))
			} else {
				_, _ = w.Write([]byte(`{"data":[]}`))
			}
		case "/helix/channels/followers":
			_, _ = w.Write([]byte(`{"total":8420,"data":[]}`))
		case "/helix/users":
			_, _ = w.Write([]byte(`{"data":[{"id":"42","display_name":"Fern"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTwitchPollAndLine(t *testing.T) {
	srv := twitchFake(t, true, &sync.Map{})
	defer srv.Close()
	t.Cleanup(func() { resetTwitchUser() })
	c := twitchClient{http: srv.Client(), api: srv.URL + "/helix", id: srv.URL + "/oauth2", clientID: "cid", token: "good"}
	cfg := DefaultConfig()
	cfg.TwitchOptions.ClientID = "cid"
	cfg.TwitchOptions.ShowFollowers = true
	resetTwitchUser()
	if err := twitchPollOnce(c, cfg.TwitchOptions); err != nil {
		t.Fatal(err)
	}
	if got := twitchLine(cfg); got != "🔴 LIVE | 1.2k viewers | playing VRChat | 8.4k followers" {
		t.Errorf("line = %q", got)
	}
	cfg.TwitchOptions.Compact = false
	cfg.TwitchOptions.Template = "{live} {viewerCount}👀 {channel}: {title}"
	if got := twitchLine(cfg); got != "🔴 LIVE 1234👀 fern: chilling" {
		t.Errorf("template line = %q", got)
	}

	// A named channel is looked up through /users.
	cfg.TwitchOptions.Channel = "fern"
	if err := twitchPollOnce(c, cfg.TwitchOptions); err != nil || CurrentTwitch().Channel != "Fern" {
		t.Errorf("named channel: %v %+v", err, CurrentTwitch())
	}

	c.token = "bad"
	resetTwitchUser()
	if err := twitchPollOnce(c, cfg.TwitchOptions); err != errTwitchAuth {
		t.Errorf("bad token: %v", err)
	}
}

func TestTwitchOffline(t *testing.T) {
	srv := twitchFake(t, false, &sync.Map{})
	defer srv.Close()
	t.Cleanup(func() { resetTwitchUser() })
	c := twitchClient{http: srv.Client(), api: srv.URL + "/helix", id: srv.URL + "/oauth2", clientID: "cid", token: "good"}
	cfg := DefaultConfig()
	resetTwitchUser()
	if err := twitchPollOnce(c, cfg.TwitchOptions); err != nil {
		t.Fatal(err)
	}
	if got := twitchLine(cfg); got != "" {
		t.Errorf("offline with no text = %q", got)
	}
	cfg.TwitchOptions.OfflineText = "offline"
	if got := twitchLine(cfg); got != "offline" {
		t.Errorf("offline line = %q", got)
	}
}

func TestTwitchRefresh(t *testing.T) {
	tokens := &sync.Map{}
	srv := twitchFake(t, true, tokens)
	defer srv.Close()
	c := twitchClient{http: srv.Client(), api: srv.URL + "/helix", id: srv.URL + "/oauth2", clientID: "cid"}
	got, err := c.refresh("r1")
	if err != nil || got.AccessToken != "good" || got.RefreshToken != "r2" {
		t.Fatalf("refresh = %+v %v", got, err)
	}
	form, _ := tokens.Load("refresh_token")
	if form.(url.Values)["refresh_token"][0] != "r1" {
		t.Errorf("refresh form = %v", form)
	}
}

func TestTwitchDeviceFlow(t *testing.T) {
	useTempSecrets(t)
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/oauth2/device":
			if r.PostForm.Get("client_id") != "cid" || r.PostForm.Get("scopes") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD-EFGH","verification_uri":"https://www.twitch.tv/activate?device-code=ABCD","expires_in":60,"interval":1}`))
		case "/oauth2/token":
			if polls.Add(1) < 2 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":400,"message":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"granted","refresh_token":"rt"}`))
		}
	}))
	defer srv.Close()
	t.Cleanup(TwitchCancelSignIn)
	c := twitchClient{http: srv.Client(), api: srv.URL + "/helix", id: srv.URL + "/oauth2", clientID: "cid"}
	gen := twitchGen.Add(1)
	go twitchDeviceFlow(c, gen)
	waitFor(t, "user code", func() bool { d := CurrentTwitch().Device; return d != nil && d.Code == "ABCD-EFGH" })
	waitFor(t, "token", func() bool { return GetSecret(SecretTwitchToken) == "granted" && CurrentTwitch().Device == nil })
	if GetSecret(SecretTwitchRefresh) != "rt" || CurrentTwitch().Device != nil {
		t.Error("device flow did not finish cleanly")
	}
}

func TestSoundpadParsing(t *testing.T) {
	xml1 := `<?xml version="1.0"?><Soundlist>
		<Sound index="1" url="C:\sounds\airhorn.mp3" artist="" title="" duration="0:02" addedOn="2023-01-01" lastPlayedOn="2023-05-01T10:00:00" playCount="3"/>
		<Category name="memes"><Sound index="2" url="C:\sounds\bruh.wav" title="Bruh" lastPlayedOn="2023-05-02T10:00:00" playCount="1"/></Category>
		<Sound index="3" url="C:\sounds\never.ogg" title="Never" playCount="0"/>
	</Soundlist>`
	list, err := parseSoundlist([]byte(xml1))
	if err != nil || len(list) != 3 {
		t.Fatalf("parse = %v %v", list, err)
	}
	if list[0].name() != "airhorn" || list[1].name() != "Bruh" {
		t.Errorf("names = %q %q", list[0].name(), list[1].name())
	}
	if s, ok := playedSound(nil, list); !ok || s.Index != 2 {
		t.Errorf("no history: picked %+v", s)
	}
	prev := playCounts(list)
	if _, ok := playedSound(prev, list); ok {
		t.Error("nothing changed but a sound was picked")
	}
	list[0].PlayCount++
	list[0].LastPlayedOn = "2023-05-03T10:00:00"
	if s, ok := playedSound(prev, list); !ok || s.name() != "airhorn" {
		t.Errorf("replay picked %+v", s)
	}
	if _, err := parseSoundlist([]byte("<Soundlist><Sound")); err == nil {
		t.Error("broken XML parsed")
	}
}

// fakePipe answers Soundpad requests; a sound list comes back in pieces.
type fakePipe struct {
	pending bytes.Buffer
	chunks  [][]byte
	asked   []string
}

func (p *fakePipe) Write(b []byte) (int, error) {
	cmd := string(b)
	p.asked = append(p.asked, cmd)
	switch cmd {
	case "GetPlayStatus()":
		p.chunks = [][]byte{[]byte("PLAYING")}
	case "GetSoundlist()":
		p.chunks = [][]byte{[]byte(`<Soundlist><Sound index="1" title="A" playCount="1"/>`), []byte(`</Soundlist>`)}
	}
	return len(b), nil
}

func (p *fakePipe) Read(b []byte) (int, error) {
	if p.pending.Len() == 0 && len(p.chunks) > 0 {
		p.pending.Write(p.chunks[0])
		p.chunks = p.chunks[1:]
	}
	return p.pending.Read(b)
}

func (p *fakePipe) Available() (int, error) {
	if p.pending.Len() > 0 {
		return p.pending.Len(), nil
	}
	if len(p.chunks) > 0 {
		return len(p.chunks[0]), nil
	}
	return 0, nil
}

func (p *fakePipe) Close() error { return nil }

func TestSoundpadRequestAndLine(t *testing.T) {
	t.Cleanup(func() { soundpadLast, soundpadLastAt = "", time.Time{} })
	conn := &soundpadConn{rw: &fakePipe{}}
	if got, err := conn.request("GetPlayStatus()"); err != nil || got != "PLAYING" {
		t.Fatalf("status = %q %v", got, err)
	}
	var counts map[int]int
	if err := soundpadCheckList(conn, &counts, true); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	if got := soundpadLine(cfg); got != "🎶 'A'" {
		t.Errorf("line = %q", got)
	}
	soundpadLastAt = time.Now().Add(-time.Duration(cfg.SoundpadOptions.DurationSec+1) * time.Second)
	if got := soundpadLine(cfg); got != "" {
		t.Errorf("line after its time = %q", got)
	}
}

func TestVoicemodParsing(t *testing.T) {
	m, err := parseVoicemodMessage([]byte(`{"actionType":"registerClient","actionID":"x1","payload":{"status":{"code":200,"description":"Connected"}}}`))
	if err != nil || m.Action != "registerClient" || m.Status != 200 || m.ID != "x1" {
		t.Errorf("register = %+v %v", m, err)
	}
	m, _ = parseVoicemodMessage([]byte(`{"actionType":"getVoices","actionObject":{"voices":[
		{"id":"nofx","friendlyName":"Clean","enabled":true},
		{"id":"robot","friendlyName":"Robot","enabled":false}],"currentVoice":"robot"}}`))
	if !m.HasVoices || len(m.Voices) != 2 || m.Voices[1].Enabled || m.VoiceID != "robot" {
		t.Errorf("voices = %+v", m)
	}
	m, _ = parseVoicemodMessage([]byte(`{"actionType":"getAllSoundboard","actionObject":{"soundboards":[
		{"id":"b1","name":"Memes","sounds":[{"id":"s1","name":"Airhorn","isEnabled":true}]}]}}`))
	if !m.HasSounds || len(m.Sounds) != 1 || m.Sounds[0].Board != "Memes" || !m.Sounds[0].Enabled {
		t.Errorf("sounds = %+v", m)
	}
	m, _ = parseVoicemodMessage([]byte(`{"actionType":"voiceLoadedEvent","actionObject":{"voiceID":"robot"}}`))
	if m.VoiceID != "robot" || m.HasVoices {
		t.Errorf("event = %+v", m)
	}
	if _, err := parseVoicemodMessage([]byte(`nope`)); err == nil {
		t.Error("garbage parsed")
	}
}

// voicemodFake is a Control API server that requires the client key.
func voicemodFake(t *testing.T, got chan<- map[string]any) *httptest.Server {
	up := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req map[string]any
			if conn.ReadJSON(&req) != nil {
				return
			}
			got <- req
			payload, _ := req["payload"].(map[string]any)
			reply := func(s string) { _ = conn.WriteMessage(websocket.TextMessage, []byte(s)) }
			switch req["action"] {
			case "registerClient":
				code := 200
				if payload["clientKey"] != "key" {
					code = 401
				}
				b, _ := json.Marshal(map[string]any{"actionType": "registerClient", "actionID": req["id"], "payload": map[string]any{"status": map[string]any{"code": code}}})
				reply(string(b))
			case "getVoices":
				reply(`{"actionType":"getVoices","actionObject":{"voices":[{"id":"nofx","friendlyName":"Clean"},{"id":"robot","friendlyName":"Robot"}],"currentVoice":"nofx"}}`)
			case "getAllSoundboard":
				reply(`{"actionType":"getAllSoundboard","actionObject":{"soundboards":[{"name":"Memes","sounds":[{"id":"s1","name":"Airhorn"}]}]}}`)
			case "loadVoice":
				reply(`{"actionType":"voiceLoadedEvent","actionObject":{"voiceID":"` + payload["voiceID"].(string) + `"}}`)
			}
		}
	}))
}

func TestVoicemodSession(t *testing.T) {
	got := make(chan map[string]any, 64)
	srv := voicemodFake(t, got)
	defer srv.Close()
	t.Cleanup(func() { clearVoicemod(); vmLast, vmLastAt = "", time.Time{}; setVoicemodLink(linkOff, "") })
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Dead ports are skipped.
	conn := dialVoicemod([]string{"ws://127.0.0.1:1/v1", url})
	if conn == nil {
		t.Fatal("could not dial the fake")
	}
	var wanted atomic.Bool
	wanted.Store(true)
	done := make(chan error, 1)
	go func() { done <- voicemodSession(conn, "key", wanted.Load) }()

	waitFor(t, "lists", func() bool { i := CurrentVoicemod(); return len(i.Voices) == 2 && len(i.Sounds) == 1 })
	if i := CurrentVoicemod(); i.Link.State != linkConnected || i.Voice != "" {
		t.Errorf("after connect: %+v", i)
	}
	VoicemodCommand("voice", "robot")
	waitFor(t, "voice", func() bool { return CurrentVoicemod().Voice == "Robot" })
	VoicemodCommand("play", "s1")
	cfg := DefaultConfig()
	waitFor(t, "sound", func() bool { return voicemodLine(cfg) == "🎶 'Airhorn' · 🎙️ 'Robot'" })

	sawPlay := false
	for len(got) > 0 {
		req := <-got
		if req["action"] == "playMeme" {
			p := req["payload"].(map[string]any)
			sawPlay = p["FileName"] == "s1" && p["IsKeyDown"] == true
		}
	}
	if !sawPlay {
		t.Error("playMeme not sent with the sound id")
	}
	wanted.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("session end: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("session did not stop when switched off")
	}
	conn.Close()

	// A wrong key is reported, not retried silently.
	conn = dialVoicemod([]string{url})
	err := voicemodSession(conn, "wrong", func() bool { return true })
	conn.Close()
	if err == nil || !strings.Contains(err.Error(), "client key") {
		t.Errorf("wrong key: %v", err)
	}
}
