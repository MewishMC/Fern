package modules

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// spotifyFake is a small Web API and accounts server with one player.
type spotifyFake struct {
	mu       sync.Mutex
	srv      *httptest.Server
	playing  bool // false answers /me/player with 204
	liked    bool
	shuffle  bool
	repeat   string
	volume   int
	device   string
	log      []string // "METHOD /path?query"
	premium  bool     // false answers controls with 403 Premium required
	tokens   url.Values
	verifier func(string) bool
}

func newSpotifyFake(t *testing.T) *spotifyFake {
	f := &spotifyFake{playing: true, liked: true, repeat: "off", volume: 40, device: "d2", premium: true}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *spotifyFake) client(token string) spotifyClient {
	return spotifyClient{http: f.srv.Client(), api: f.srv.URL + "/v1", accounts: f.srv.URL, clientID: "cid", token: token}
}

func (f *spotifyFake) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.log...)
}

func (f *spotifyFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/api/token" {
		_ = r.ParseForm()
		f.tokens = r.PostForm
		if r.PostForm.Get("client_id") != "cid" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"Invalid client"}`))
			return
		}
		if r.PostForm.Get("grant_type") == "authorization_code" && (f.verifier == nil || !f.verifier(r.PostForm.Get("code_verifier"))) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code_verifier was incorrect"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"good","refresh_token":"r2","expires_in":3600}`))
		return
	}
	if r.Header.Get("Authorization") != "Bearer good" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"status":401,"message":"The access token expired"}}`))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1")
	entry := r.Method + " " + path
	if r.URL.RawQuery != "" {
		entry += "?" + r.URL.RawQuery
	}
	if r.Method == http.MethodPut && path == "/me/player" {
		body, _ := io.ReadAll(r.Body)
		entry += " " + string(body)
	}
	f.log = append(f.log, entry)
	q := r.URL.Query()
	control := r.Method != http.MethodGet && strings.HasPrefix(path, "/me/player")
	if control && !f.premium {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"status":403,"message":"Player command failed: Premium required","reason":"PREMIUM_REQUIRED"}}`))
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/me/player":
		if !f.playing {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		name := map[string]string{"d1": "Desktop", "d2": "Phone"}[f.device]
		fmt.Fprintf(w, `{"device":{"id":%q,"name":%q,"type":"Computer","is_active":true,"volume_percent":%d,"supports_volume":true},
			"shuffle_state":%v,"repeat_state":%q,"is_playing":true,
			"context":{"type":"playlist","uri":"spotify:playlist:pl1"},
			"item":{"id":"t1","uri":"spotify:track:t1","name":"Song","artists":[{"name":"A"},{"name":"B"}]}}`,
			f.device, name, f.volume, f.shuffle, f.repeat)
	case r.Method == http.MethodGet && path == "/me/tracks/contains":
		fmt.Fprintf(w, `[%v]`, f.liked)
	case path == "/me/tracks":
		f.liked = r.Method == http.MethodPut
		w.WriteHeader(http.StatusOK)
	case path == "/playlists/pl1":
		_, _ = w.Write([]byte(`{"name":"Chill"}`))
	case path == "/me/player/devices":
		_, _ = w.Write([]byte(`{"devices":[
			{"id":"d2","name":"Phone","type":"Smartphone","is_active":true,"volume_percent":40,"supports_volume":true},
			{"id":"d1","name":"Desktop","type":"Computer","is_active":false,"volume_percent":70,"supports_volume":true},
			{"id":"d3","name":"Locked","type":"Speaker","is_restricted":true}]}`))
	case path == "/me/player/shuffle":
		f.shuffle = q.Get("state") == "true"
		w.WriteHeader(http.StatusNoContent)
	case path == "/me/player/repeat":
		f.repeat = q.Get("state")
		w.WriteHeader(http.StatusNoContent)
	case path == "/me/player/volume":
		_, _ = fmt.Sscan(q.Get("volume_percent"), &f.volume)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && path == "/me/player":
		f.device = "d1"
		w.WriteHeader(http.StatusNoContent)
	case path == "/me/player/queue":
		w.WriteHeader(http.StatusNoContent)
	case path == "/search":
		_, _ = w.Write([]byte(`{"tracks":{"items":[{"uri":"spotify:track:4uLU6hMCjMI75M1A2tKUQC","name":"Found","artists":[{"name":"Someone"}]}]}}`))
	default:
		http.NotFound(w, r)
	}
}

// useSpotifyFake points the package at f and starts from a clean snapshot.
func useSpotifyFake(t *testing.T, f *spotifyFake) {
	t.Helper()
	oldAPI, oldAcc, oldHTTP := spotifyAPIBase, spotifyAccountsBase, spotifyHTTP
	spotifyAPIBase, spotifyAccountsBase, spotifyHTTP = f.srv.URL+"/v1", f.srv.URL, f.srv.Client()
	resetSpotifyState()
	spotifyMu.Lock()
	clear(spotifyPlaylists)
	spotifyMu.Unlock()
	t.Cleanup(func() {
		spotifyAPIBase, spotifyAccountsBase, spotifyHTTP = oldAPI, oldAcc, oldHTTP
		resetSpotifyState()
	})
}

func spotifyTestConfig() Config {
	c := DefaultConfig()
	c.SpotifyOptions.Enabled = true
	c.SpotifyOptions.ClientID = "cid"
	return c
}

func TestSpotifySanitize(t *testing.T) {
	c := DefaultConfig()
	o := c.SpotifyOptions
	if o.Enabled || o.RedirectPort != DefaultSpotifyRedirectPort || o.IntervalSec != 5 || !o.ShowLiked || o.Params {
		t.Errorf("defaults = %+v", o)
	}
	c.SpotifyOptions = SpotifyOptions{
		ClientID: " ABC-def 123 ", RedirectPort: 80, IntervalSec: 9999,
		LikedIcon: "  ♥♥♥♥♥♥  ", DevicePrefix: "  playing \n on  ", ParamLike: " /avatar/parameters/Like ",
		QueueText: strings.Repeat("x", 300),
	}
	c.sanitize()
	o = c.SpotifyOptions
	if o.ClientID != "abcdef123" || o.RedirectPort != 1024 || o.IntervalSec != 60 || o.LikedIcon != "♥♥♥♥" ||
		o.DevicePrefix != "playing on" || o.ParamLike != "Like" || len(o.QueueText) != 200 {
		t.Errorf("sanitized = %+v", o)
	}
	c.SpotifyOptions.RedirectPort = 0
	c.sanitize()
	if c.SpotifyOptions.RedirectPort != DefaultSpotifyRedirectPort {
		t.Errorf("missing port = %d", c.SpotifyOptions.RedirectPort)
	}
	if got := SpotifyRedirectURI(1931); got != "http://127.0.0.1:1931/callback" {
		t.Errorf("redirect = %q", got)
	}
}

func TestSpotifyPollReadsPlayer(t *testing.T) {
	f := newSpotifyFake(t)
	useSpotifyFake(t, f)
	if err := spotifyPollOnce(f.client("good")); err != nil {
		t.Fatal(err)
	}
	p := spotifySnapshot()
	if !p.active || p.title != "Song" || p.artist != "A, B" || !p.likedKnown || !p.liked ||
		p.playlist != "Chill" || p.device.Name != "Phone" || p.device.Volume != 40 || p.repeat != "off" {
		t.Fatalf("snapshot = %+v", p)
	}
	devs := SpotifyDevices()
	if len(devs) != 2 || devs[0].Name != "Desktop" || devs[1].Name != "Phone" || !devs[1].Active || devs[0].Active {
		t.Errorf("devices = %+v", devs)
	}

	// The liked state, the playlist and the devices are asked once, not per poll.
	before := len(f.calls())
	if err := spotifyPollOnce(f.client("good")); err != nil {
		t.Fatal(err)
	}
	if got := f.calls()[before:]; len(got) != 1 || !strings.HasPrefix(got[0], "GET /me/player?") {
		t.Errorf("second poll asked %q", got)
	}

	if err := spotifyPollOnce(f.client("bad")); err != errSpotifyAuth {
		t.Errorf("bad token: %v", err)
	}

	f.mu.Lock()
	f.playing = false
	f.mu.Unlock()
	if err := spotifyPollOnce(f.client("good")); err != nil {
		t.Fatal(err)
	}
	if p := spotifySnapshot(); p.active || p.title != "" {
		t.Errorf("nothing playing = %+v", p)
	}
}

func TestSpotifyExtras(t *testing.T) {
	o := defaultSpotifyOptions()
	o.Enabled = true
	p := spotifyPlayer{active: true, title: "Song", likedKnown: true, liked: true, shuffle: true, repeat: "track",
		device: SpotifyDevice{Name: "Desktop"}, playlist: "Chill"}
	if got := formatSpotifyExtras(o, p, "Song"); got != "♥" {
		t.Errorf("defaults = %q", got)
	}
	o.ShowShuffle, o.ShowRepeat, o.ShowDevice, o.ShowPlaylist = true, true, true, true
	if got := formatSpotifyExtras(o, p, "song"); got != "♥ 🔀 🔂 · on Desktop · from Chill" {
		t.Errorf("everything = %q", got)
	}
	p.liked, p.shuffle, p.repeat = false, false, "context"
	o.UnlikedIcon = "♡"
	if got := formatSpotifyExtras(o, p, "Song"); got != "♡ 🔁 · on Desktop · from Chill" {
		t.Errorf("unliked = %q", got)
	}
	if got := formatSpotifyExtras(o, p, "Another song entirely"); got != "" {
		t.Errorf("other track = %q", got)
	}
	p.active = false
	if got := formatSpotifyExtras(o, p, "Song"); got != "" {
		t.Errorf("inactive = %q", got)
	}
	if !spotifyTitlesMatch("Song (Remastered)", "Song") || spotifyTitlesMatch("", "Song") {
		t.Error("title matching")
	}
}

func TestMediaLineWithSpotifyExtras(t *testing.T) {
	o := DefaultConfig().MediaOptions
	info := MediaInfo{Available: true, Playing: true, Title: "Song", Artist: "A", PositionMS: 60000, DurationMS: 120000}
	line := mediaFitExtras(info, o, "♥ · on Desktop")
	want := []string{
		"♪ Song — A ♥ · on Desktop ▰▰▰▰▱▱▱▱ 1:00/2:00",
		"♪ Song — A ♥ · on Desktop 1:00/2:00",
		"♪ Song — A 1:00/2:00",
		"♪ Song — A",
	}
	if !reflect.DeepEqual(line.Variants, want) {
		t.Fatalf("variants\n%q\nwant\n%q", line.Variants, want)
	}
	if got := mediaFitExtras(info, o, ""); !reflect.DeepEqual(got.Variants, mediaFit(info, o).Variants) {
		t.Errorf("no extras changed the line: %q", got.Variants)
	}
}

func TestSpotifyControls(t *testing.T) {
	useTempSecrets(t)
	useTestConfig(t, spotifyTestConfig())
	f := newSpotifyFake(t)
	useSpotifyFake(t, f)
	// An expired token is refreshed once and the call goes through.
	_ = SetSecret(SecretSpotifyToken, "stale")
	_ = SetSecret(SecretSpotifyRefresh, "r1")
	if err := spotifyDo(spotifyPollOnce); err != nil {
		t.Fatal(err)
	}
	if GetSecret(SecretSpotifyToken) != "good" || GetSecret(SecretSpotifyRefresh) != "r2" ||
		f.tokens.Get("grant_type") != "refresh_token" || f.tokens.Get("refresh_token") != "r1" {
		t.Fatalf("refresh: token form %v", f.tokens)
	}

	if liked, err := SpotifySetLiked(spotifyToggle); err != nil || liked {
		t.Fatalf("toggle like = %v %v", liked, err)
	}
	if p := spotifySnapshot(); !p.likedKnown || p.liked {
		t.Error("snapshot did not follow the unlike")
	}
	if on, err := SpotifySetShuffle(spotifyToggle); err != nil || !on {
		t.Fatalf("shuffle = %v %v", on, err)
	}
	if r, err := SpotifySetRepeat(spotifyCycle); err != nil || r != "context" {
		t.Fatalf("repeat = %v %v", r, err)
	}
	if v, err := SpotifySetVolume(spotifyUp, 75); err != nil || v != 100 {
		t.Fatalf("volume = %v %v", v, err)
	}
	if name, err := SpotifyTransfer("desk"); err != nil || name != "Desktop" {
		t.Fatalf("transfer = %v %v", name, err)
	}
	if added, err := SpotifyQueue("https://open.spotify.com/intl-de/track/4uLU6hMCjMI75M1A2tKUQC?si=x"); err != nil || added == "" {
		t.Fatalf("queue link = %v %v", added, err)
	}
	if added, err := SpotifyQueue("found someone"); err != nil || added != "Found — Someone" {
		t.Fatalf("queue search = %v %v", added, err)
	}
	if _, err := SpotifyTransfer("Kitchen"); err == nil {
		t.Error("transfer to a missing device worked")
	}

	want := []string{
		"DELETE /me/tracks?ids=t1",
		"PUT /me/player/shuffle?state=true",
		"PUT /me/player/repeat?state=context",
		"PUT /me/player/volume?volume_percent=100",
		`PUT /me/player {"device_ids":["d1"]}`,
		"POST /me/player/queue?uri=spotify%3Atrack%3A4uLU6hMCjMI75M1A2tKUQC",
		"GET /search?limit=1&q=found+someone&type=track",
		"POST /me/player/queue?uri=spotify%3Atrack%3A4uLU6hMCjMI75M1A2tKUQC",
	}
	var got []string
	for _, c := range f.calls() {
		if !strings.HasPrefix(c, "GET /me/") && !strings.HasPrefix(c, "GET /playlists") {
			got = append(got, c)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls\n%q\nwant\n%q", got, want)
	}

	// Spotify Free: the error says why.
	f.mu.Lock()
	f.premium = false
	f.mu.Unlock()
	if _, err := SpotifySetShuffle(spotifyOff); err == nil || err.Error() != "This needs Spotify Premium" {
		t.Errorf("free account: %v", err)
	}
}

func TestSpotifyNeedsSetup(t *testing.T) {
	useTempSecrets(t)
	c := spotifyTestConfig()
	c.SpotifyOptions.ClientID = ""
	useTestConfig(t, c)
	if err := spotifyDo(spotifyPollOnce); err == nil || !strings.Contains(err.Error(), "client ID") {
		t.Errorf("no client id: %v", err)
	}
	useTestConfig(t, spotifyTestConfig())
	if err := spotifyDo(spotifyPollOnce); err == nil || !strings.Contains(err.Error(), "sign in") {
		t.Errorf("signed out: %v", err)
	}
	c = spotifyTestConfig()
	c.SpotifyOptions.Enabled = false
	useTestConfig(t, c)
	if err := spotifyReady(); err == nil {
		t.Error("rules ran with the link off")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestSpotifySignInPKCE(t *testing.T) {
	useTempSecrets(t)
	c := spotifyTestConfig()
	c.SpotifyOptions.RedirectPort = freePort(t)
	useTestConfig(t, c)
	f := newSpotifyFake(t)
	useSpotifyFake(t, f)
	t.Cleanup(SpotifyCancelSignIn)

	SpotifyStartSignIn()
	s := CurrentSpotify(c.SpotifyOptions).SignIn
	if s == nil || s.Error != "" || !strings.HasPrefix(s.URL, f.srv.URL+"/authorize?") {
		t.Fatalf("sign-in = %+v", s)
	}
	auth, _ := url.Parse(s.URL)
	q := auth.Query()
	redirect := SpotifyRedirectURI(c.SpotifyOptions.RedirectPort)
	if q.Get("client_id") != "cid" || q.Get("response_type") != "code" || q.Get("redirect_uri") != redirect ||
		q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || !strings.Contains(q.Get("scope"), "user-library-modify") {
		t.Fatalf("authorize query = %v", q)
	}
	challenge := q.Get("code_challenge")
	f.mu.Lock()
	f.verifier = func(v string) bool { return len(v) >= 43 && len(v) <= 128 && pkceChallenge(v) == challenge }
	f.mu.Unlock()

	// A callback with the wrong state is turned away and the sign-in waits on.
	resp, err := http.Get(redirect + "?code=abc&state=nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || CurrentSpotify(c.SpotifyOptions).SignIn == nil {
		t.Fatalf("wrong state: %d", resp.StatusCode)
	}

	resp, err = http.Get(redirect + "?code=abc&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "signed in") {
		t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	if GetSecret(SecretSpotifyToken) != "good" || GetSecret(SecretSpotifyRefresh) != "r2" ||
		f.tokens.Get("code") != "abc" || f.tokens.Get("redirect_uri") != redirect {
		t.Fatalf("tokens not stored; form %v", f.tokens)
	}
	if info := CurrentSpotify(c.SpotifyOptions); info.SignIn != nil || !info.SignedIn {
		t.Errorf("after sign-in = %+v", info)
	}
	waitFor(t, "the callback to close", func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", c.SpotifyOptions.RedirectPort), 200*time.Millisecond)
		if err != nil {
			return true
		}
		conn.Close()
		return false
	})

	SpotifySignOut()
	if GetSecret(SecretSpotifyToken) != "" || GetSecret(SecretSpotifyRefresh) != "" {
		t.Error("sign out kept the tokens")
	}
}

func TestSpotifySignInCancelled(t *testing.T) {
	useTempSecrets(t)
	c := spotifyTestConfig()
	c.SpotifyOptions.RedirectPort = freePort(t)
	useTestConfig(t, c)
	t.Cleanup(SpotifyCancelSignIn)
	SpotifyStartSignIn()
	s := CurrentSpotify(c.SpotifyOptions).SignIn
	auth, _ := url.Parse(s.URL)
	resp, err := http.Get(SpotifyRedirectURI(c.SpotifyOptions.RedirectPort) + "?error=access_denied&state=" + url.QueryEscape(auth.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s := CurrentSpotify(c.SpotifyOptions).SignIn; s == nil || !strings.Contains(s.Error, "cancelled") {
		t.Errorf("cancelled sign-in = %+v", s)
	}

	// The port being taken is reported, not ignored.
	var ln net.Listener
	waitFor(t, "the old callback to close", func() bool {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.SpotifyOptions.RedirectPort))
		return err == nil
	})
	defer ln.Close()
	SpotifyStartSignIn()
	if s := CurrentSpotify(c.SpotifyOptions).SignIn; s == nil || !strings.Contains(s.Error, "busy") {
		t.Errorf("busy port = %+v", s)
	}
}

func TestSpotifyItemURI(t *testing.T) {
	for in, want := range map[string]string{
		"spotify:track:4uLU6hMCjMI75M1A2tKUQC":                         "spotify:track:4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC?si=abc": "spotify:track:4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/episode/4uLU6hMCjMI75M1A2tKUQC":      "spotify:episode:4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/album/4uLU6hMCjMI75M1A2tKUQC":        "",
		"never gonna give you up":                                      "",
	} {
		got, ok := spotifyItemURI(in)
		if got != want || ok != (want != "") {
			t.Errorf("spotifyItemURI(%q) = %q %v", in, got, ok)
		}
	}
	if nextRepeat("off") != "context" || nextRepeat("context") != "track" || nextRepeat("track") != "off" {
		t.Error("repeat cycle")
	}
}

func TestSpotifyParamsBothWays(t *testing.T) {
	c := spotifyTestConfig()
	c.SpotifyOptions.Params = true
	c.SpotifyOptions.ParamDevice = ""
	o := c.SpotifyOptions

	// Fern -> avatar: the state is sent once, then only changes.
	var mu sync.Mutex
	var sent []string
	oldSend := avatarSend
	avatarSend = func(_ OSCOptions, m *osc.Message) error {
		mu.Lock()
		sent = append(sent, fmt.Sprint(m.Address, "=", m.Arguments[0]))
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() { avatarSend = oldSend })
	spotifyParamMu.Lock()
	clear(spotifySent)
	spotifyVolInAt = time.Time{}
	spotifyParamMu.Unlock()
	spotifyQuietUntil.Store(0)
	spotifyMu.Lock()
	spotifyNow = spotifyPlayer{active: true, trackID: "t1", trackURI: "spotify:track:t1", title: "Song", likedKnown: true, liked: true,
		shuffle: false, repeat: "track", device: SpotifyDevice{ID: "d1", Volume: 50, SupportsVolume: true}}
	spotifyMu.Unlock()
	t.Cleanup(resetSpotifyState)

	spotifySyncParams(o)
	mu.Lock()
	got := append([]string{}, sent...)
	sent = nil
	mu.Unlock()
	want := map[string]bool{
		"/avatar/parameters/SpotifyLike=true": true, "/avatar/parameters/SpotifyShuffle=false": true,
		"/avatar/parameters/SpotifyRepeat=2": true, "/avatar/parameters/SpotifyVolume=0.5": true,
	}
	if len(got) != len(want) {
		t.Fatalf("sent %q", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected send %q", s)
		}
	}
	spotifySyncParams(o)
	spotifyUpdate(func(p *spotifyPlayer) { p.shuffle = true })
	spotifySyncParams(o)
	mu.Lock()
	if len(sent) != 1 || sent[0] != "/avatar/parameters/SpotifyShuffle=true" {
		t.Errorf("after one change sent %q", sent)
	}
	mu.Unlock()

	// Avatar -> Fern: only configured names, queue on the rising edge only.
	startSpotifyParams()
	refreshSpotifyParamSubs(o)
	t.Cleanup(func() { refreshSpotifyParamSubs(SpotifyOptions{}) })
	var runs []string
	oldRun := spotifyParamRun
	spotifyParamRun = func(kind string, v any) {
		mu.Lock()
		runs = append(runs, fmt.Sprint(kind, "=", v))
		mu.Unlock()
	}
	t.Cleanup(func() { spotifyParamRun = oldRun })
	deliver := func(name string, v any) {
		deliverOSC(osc.NewMessage(AvatarParamPrefix+name, v))
	}
	deliver("SpotifyLike", false)
	deliver("SpotifyDevice", int32(1)) // not configured
	deliver("Unrelated", true)
	deliver("SpotifyQueue", true)
	deliver("SpotifyQueue", true)
	deliver("SpotifyQueue", false)
	deliver("SpotifyQueue", true)
	spotifyQuietUntil.Store(time.Now().Add(time.Hour).UnixNano())
	deliver("SpotifyShuffle", false) // right after an avatar change
	spotifyQuietUntil.Store(0)
	waitFor(t, "parameter runs", func() bool { mu.Lock(); defer mu.Unlock(); return len(runs) >= 3 })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	sortedRuns := append([]string{}, runs...)
	mu.Unlock()
	if len(sortedRuns) != 3 {
		t.Fatalf("runs %q", sortedRuns)
	}
	counts := map[string]int{}
	for _, r := range sortedRuns {
		counts[r]++
	}
	if counts["like=false"] != 1 || counts["queue=true"] != 2 {
		t.Errorf("runs %q", sortedRuns)
	}

	// A value matching the state does nothing (no request is made, so no
	// server is needed).
	spotifyParamAction(spotifyParamLike, true)
	spotifyParamAction(spotifyParamShuffle, true)
	spotifyParamAction(spotifyParamRepeat, int32(2))
}

func TestSpotifyParamValues(t *testing.T) {
	if repeatFromParam(int32(1)) != "context" || repeatFromParam(int32(2)) != "track" || repeatFromParam(true) != "context" ||
		repeatFromParam(float32(0)) != "off" || repeatToParam("track") != 2 {
		t.Error("repeat parameter mapping")
	}
	if volumeFromParam(float32(0.42)) != 42 || volumeFromParam(int32(70)) != 70 || volumeFromParam(float32(3)) != 100 || volumeFromParam(true) != 100 {
		t.Error("volume parameter mapping")
	}
	if !paramTruthy(true) || paramTruthy(false) || !paramTruthy(int32(1)) || paramTruthy(float32(0.2)) {
		t.Error("truthy")
	}
	names := spotifyParamNames(SpotifyOptions{Enabled: true, Params: true, ParamLike: "L", ParamQueue: "Q"})
	if len(names) != 2 || names["L"] != spotifyParamLike || names["Q"] != spotifyParamQueue {
		t.Errorf("names = %v", names)
	}
	if len(spotifyParamNames(SpotifyOptions{Params: true, ParamLike: "L"})) != 0 {
		t.Error("parameters listened to with the link off")
	}
}

func TestSpotifyRulesRegistered(t *testing.T) {
	ids := map[string]bool{}
	for _, a := range RuleActions() {
		ids[a.ID] = true
	}
	for _, id := range []string{ActionSpotifyLike, ActionSpotifyShuffle, ActionSpotifyRepeat, ActionSpotifyVolume, ActionSpotifyDevice, ActionSpotifyQueue} {
		if !ids[id] {
			t.Errorf("action %s not registered", id)
		}
	}
	tr := lookupTrigger(TriggerSpotifyTrack)
	if tr == nil || tr.Match == nil {
		t.Fatal("track trigger missing")
	}
	p := SanitizeRuleParams(tr.Fields, map[string]any{"artist": "/^a$/"})
	if !tr.Match(p, RuleEvent{Data: map[string]string{"title": "x", "artist": "A"}}) ||
		tr.Match(p, RuleEvent{Data: map[string]string{"title": "x", "artist": "AB"}}) {
		t.Error("track trigger matching")
	}
	spotifyMu.Lock()
	spotifyNow = spotifyPlayer{active: true, likedKnown: true, liked: true, device: SpotifyDevice{Name: "Desktop"}}
	spotifyMu.Unlock()
	t.Cleanup(resetSpotifyState)
	if !lookupTrigger(TriggerSpotifyLiked).Check(nil) {
		t.Error("liked trigger")
	}
	dev := lookupTrigger(TriggerSpotifyDevice)
	if !dev.Check(SanitizeRuleParams(dev.Fields, map[string]any{"device": "desk"})) ||
		dev.Check(SanitizeRuleParams(dev.Fields, map[string]any{"device": "phone"})) {
		t.Error("device trigger")
	}
	data, _ := json.Marshal(RuleTriggers())
	if !strings.Contains(string(data), `"Spotify"`) {
		t.Error("Spotify category missing from the schema")
	}
}
