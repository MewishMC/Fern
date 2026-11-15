package modules

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hypebeast/go-osc/osc"
)

func series(now time.Time, bpms ...float64) []hrSample {
	// One reading per second, the last one at now.
	out := make([]hrSample, len(bpms))
	for i, b := range bpms {
		out[i] = hrSample{at: now.Add(-time.Duration(len(bpms)-1-i) * time.Second), bpm: b}
	}
	return out
}

func TestSmoothBPMAveragesTheWindow(t *testing.T) {
	now := time.Now()
	s := series(now, 100, 60, 70, 80)
	if got := smoothBPM(s, now, 0); got != 80 {
		t.Errorf("no smoothing = %v, want the latest 80", got)
	}
	// Readings at -2s, -1s and 0s fall inside a 2s window.
	if got := smoothBPM(s, now, 2*time.Second); got != 70 {
		t.Errorf("2s window = %v, want 70", got)
	}
	// Everything stale: fall back to the latest reading instead of 0.
	if got := smoothBPM(s, now.Add(time.Hour), 2*time.Second); got != 80 {
		t.Errorf("stale window = %v, want 80", got)
	}
	if got := smoothBPM(nil, now, time.Second); got != 0 {
		t.Errorf("empty = %v", got)
	}
}

func TestHeartTrendUsesSensitivity(t *testing.T) {
	now := time.Now()
	rising := series(now, 60, 60, 60, 66, 66, 66)
	falling := series(now, 90, 90, 90, 80, 80, 80)
	steady := series(now, 70, 71, 70, 71, 70, 71)
	w := 3 * time.Second
	for name, c := range map[string]struct {
		s    []hrSample
		sens float64
		want int
	}{
		"rising":         {rising, 3, 1},
		"rise too small": {rising, 8, 0},
		"falling":        {falling, 3, -1},
		"steady":         {steady, 1, 0},
		"one window":     {series(now, 60, 90), 1, 0},
	} {
		if got := heartTrend(c.s, now, w, c.sens); got != c.want {
			t.Errorf("%s: trend = %d, want %d", name, got, c.want)
		}
	}
	if got := heartTrend(series(now, 90), now, w, 1); got != 0 {
		t.Errorf("single reading trend = %d, want 0", got)
	}
}

func TestHeartZonesAndIcons(t *testing.T) {
	o := DefaultConfig().HeartRate
	for bpm, want := range map[int]int{59: -1, 60: 0, 99: 0, 100: 1, 180: 1} {
		if got := heartZone(bpm, o.LowBPM, o.HighBPM); got != want {
			t.Errorf("zone(%d) = %d, want %d", bpm, got, want)
		}
	}
	if heartZone(10, 0, 0) != 0 {
		t.Error("zero thresholds should disable the zones")
	}
	if got := heartIcon(o, 50); got != o.IconLow {
		t.Errorf("low icon = %q", got)
	}
	if got := heartIcon(o, 120); got != o.IconHigh {
		t.Errorf("high icon = %q", got)
	}
	o.ZoneIcons = false
	if got := heartIcon(o, 120); got != o.Icon {
		t.Errorf("icon without zones = %q", got)
	}
}

func TestFormatHeartLine(t *testing.T) {
	o := DefaultConfig().HeartRate
	o.Label = "hr"
	o.ShowZoneText = true
	o.ShowStats = true
	r := heartReading{ok: true, bpm: 110, trend: 1, last: time.Now()}
	got := formatHeartLine(o, r, [3]int{58, 72, 120})
	want := "hr 💖 110 bpm ↑ · hot · 58–120 avg 72"
	if got != want {
		t.Errorf("line = %q, want %q", got, want)
	}

	o = DefaultConfig().HeartRate
	o.ShowIcon, o.ShowSuffix, o.ShowTrend = false, false, false
	if got := formatHeartLine(o, heartReading{ok: true, bpm: 70, last: time.Now()}, [3]int{}); got != "70" {
		t.Errorf("bare line = %q", got)
	}

	stale := heartReading{last: time.Now().Add(-time.Hour)}
	if got := formatHeartLine(o, stale, [3]int{}); got != "" {
		t.Errorf("offline without text = %q, want hidden", got)
	}
	o.OfflineText = "no pulse"
	if got := formatHeartLine(o, stale, [3]int{}); got != "no pulse" {
		t.Errorf("offline text = %q", got)
	}
}

func TestReadHeartAppliesOffsetAndOffline(t *testing.T) {
	o := DefaultConfig().HeartRate
	o.SmoothSec = 0
	o.Offset = -5
	now := time.Now()
	s := series(now, 80)
	r := readHeart(o, s, now, now)
	if !r.ok || r.bpm != 75 || r.raw != 80 {
		t.Errorf("reading = %+v, want ok 75 (raw 80)", r)
	}
	late := now.Add(time.Duration(o.OfflineSec+1) * time.Second)
	if readHeart(o, s, now, late).ok {
		t.Error("reading still fresh after the offline timeout")
	}
}

func TestHeartFloatConventions(t *testing.T) {
	for _, c := range []struct {
		bpm    int
		signed bool
		want   float32
	}{
		{0, true, -1}, {255, true, 1}, {0, false, 0}, {255, false, 1}, {300, true, 1},
	} {
		if got := heartFloat(c.bpm, c.signed); got != c.want {
			t.Errorf("heartFloat(%d, %v) = %v, want %v", c.bpm, c.signed, got, c.want)
		}
	}
	if got := heartFloat(51, false); got < 0.199 || got > 0.201 {
		t.Errorf("51 bpm unsigned = %v, want 0.2", got)
	}
}

func TestHeartSparkBuckets(t *testing.T) {
	now := time.Now()
	s := []hrSample{{now.Add(-9 * time.Second), 60}, {now.Add(-1 * time.Second), 70}, {now, 80}}
	got := heartSpark(s, now, 10*time.Second, 2*time.Second, 0)
	if len(got) != 5 || got[0] != 60 || got[1] != 0 || got[4] != 75 {
		t.Errorf("spark = %v", got)
	}
}

func TestExtractBPM(t *testing.T) {
	cases := []struct {
		body, path string
		want       float64
		fail       bool
	}{
		{"72", "", 72, false},
		{" 72.5\n", "anything", 72.5, false},
		{`{"measured_at":1,"data":{"heart_rate":81}}`, "data.heart_rate", 81, false},
		{`{"data":{"heart_rate":81}}`, "", 81, false},
		{`{"HR":64}`, "", 64, false},
		{`{"payload":{"hr":"77"}}`, "payload.hr", 77, false},
		{`{"items":[{"bpm":1},{"bpm":99}]}`, "items[1].bpm", 99, false},
		{`{"items":[{"bpm":1},{"bpm":99}]}`, "items.1.bpm", 99, false},
		{`{"a":{"B":5}}`, "$.a.b", 5, false},
		{`{"a":1}`, "b", 0, true},
		{`{"a":"x"}`, "a", 0, true},
		{`{"a":[1]}`, "a.4", 0, true},
		{`<html>`, "", 0, true},
		{`{"nothing":"here"}`, "", 0, true},
	}
	for _, c := range cases {
		got, err := extractBPM([]byte(c.body), c.path)
		if (err != nil) != c.fail || (!c.fail && got != c.want) {
			t.Errorf("extractBPM(%s, %q) = %v, %v", c.body, c.path, got, err)
		}
	}
}

func TestCleanHeartErrorHidesSecrets(t *testing.T) {
	secret := "abcd-1234-secret"
	errs := []error{
		errors.New(`Get "https://host/x?access_token=` + secret + `": dial tcp: refused`),
		errors.New("bad token " + secret),
		errors.New("Authorization: Bearer " + secret),
	}
	for _, err := range errs {
		msg := cleanHeartError(err, []string{secret, ""})
		if strings.Contains(msg, secret) || strings.Contains(msg, "1234") {
			t.Errorf("message leaks the secret: %q", msg)
		}
	}
	if got := cleanHeartError(heartError{msg: "Pulsoid refused the credentials (HTTP 401)"}, nil); !strings.Contains(got, "401") {
		t.Errorf("friendly message lost: %q", got)
	}
}

func TestPulsoidTokenFromRedirectURL(t *testing.T) {
	for in, want := range map[string]string{
		"  tok-123  ": "tok-123",
		"https://pulsoid.net/oauth2/authorize#access_token=tok-123&token_type=bearer": "tok-123",
	} {
		if got := pulsoidTokenFrom(in); got != want {
			t.Errorf("pulsoidTokenFrom(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeHeartOptions(t *testing.T) {
	o := HeartOptions{Source: "nope", TrendSensitivity: 99, Offset: -500, OfflineSec: 1,
		ParamInt: " /avatar/HR ", FloatRange: "x", BeatMode: "y", LowText: "a\nb"}
	o.sanitize()
	if o.Source != HeartPulsoid || o.TrendSensitivity != 30 || o.Offset != -50 || o.OfflineSec != 5 ||
		o.ParamInt != "avatarHR" || o.FloatRange != "signed" || o.BeatMode != "toggle" || o.LowText != "a b" || o.PollMS != 1000 {
		t.Errorf("sanitized = %+v", o)
	}
}

// waitSample runs one source against a fake server until it reports a reading.
func waitSample(t *testing.T, o HeartOptions, want float64) {
	t.Helper()
	clearHeartSamples()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	run := heartRun{gen: hrGen.Add(1)}
	go runHeartSource(ctx, run, o)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := heartSnapshot(); len(s) > 0 {
			if s[len(s)-1].bpm != want {
				t.Fatalf("reading = %v, want %v", s[len(s)-1].bpm, want)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	hrMu.RLock()
	msg := hrMessage
	hrMu.RUnlock()
	t.Fatalf("no reading arrived (status message %q)", msg)
}

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func TestCustomWebsocketSourceEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"hello":true}`))
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"reading":{"bpm":91}}`))
		time.Sleep(time.Second)
	}))
	defer srv.Close()
	o := DefaultConfig().HeartRate
	o.Source = HeartCustom
	o.CustomURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	o.CustomPath = "reading.bpm"
	waitSample(t, o, 91)
}

func TestCustomHTTPSourceEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"heartRate": 64}`))
	}))
	defer srv.Close()
	o := DefaultConfig().HeartRate
	o.Source = HeartCustom
	o.CustomURL = srv.URL + "/hr?key=secret"
	o.PollMS = 250
	waitSample(t, o, 64)
}

func TestPulsoidFallsBackToRESTWithBearerToken(t *testing.T) {
	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path == "/ws" {
			http.NotFound(w, r) // no websocket here: Fern must poll instead
			return
		}
		if strings.Contains(r.URL.RawQuery, "tok") {
			t.Error("token sent in the URL")
		}
		_, _ = w.Write([]byte(`{"measured_at":1,"data":{"heart_rate":77}}`))
	}))
	defer srv.Close()
	oldWS, oldREST := pulsoidWSURL, pulsoidRESTURL
	pulsoidWSURL = "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	pulsoidRESTURL = srv.URL + "/latest"
	t.Cleanup(func() { pulsoidWSURL, pulsoidRESTURL = oldWS, oldREST })

	o := DefaultConfig().HeartRate
	o.PulsoidToken = "tok-xyz"
	waitSample(t, o, 77)
	mu.Lock()
	defer mu.Unlock()
	for _, a := range auths {
		if a != "Bearer tok-xyz" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

func TestPulsoidWebsocketEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-xyz" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"measured_at":1,"data":{"heart_rate":83}}`))
		time.Sleep(time.Second)
	}))
	defer srv.Close()
	old := pulsoidWSURL
	pulsoidWSURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	t.Cleanup(func() { pulsoidWSURL = old })

	o := DefaultConfig().HeartRate
	o.PulsoidToken = "tok-xyz"
	waitSample(t, o, 83)
	if got := CurrentHeartRate().Via; got != "websocket" {
		t.Errorf("via = %q, want websocket", got)
	}

	// A refused token is reported without the token itself.
	o.PulsoidToken = "wrong-token-value"
	clearHeartSamples()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runHeartSource(ctx, heartRun{gen: hrGen.Add(1)}, o)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		hrMu.RLock()
		state, msg := hrState, hrMessage
		hrMu.RUnlock()
		if state == HRStatusError {
			if !strings.Contains(msg, "401") || strings.Contains(msg, "wrong-token") {
				t.Errorf("message = %q", msg)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("refused token never reported")
}

func TestHypeRatePhoenixEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "key-1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var join phxMessage
		if err := conn.ReadJSON(&join); err != nil || join.Event != "phx_join" || join.Topic != "hr:ABCD" {
			t.Errorf("join = %+v, %v", join, err)
			return
		}
		_ = conn.WriteJSON(phxMessage{Topic: "hr:ABCD", Event: "phx_reply", Payload: json.RawMessage(`{"status":"ok"}`)})
		_ = conn.WriteJSON(phxMessage{Topic: "hr:ABCD", Event: "hr_update", Payload: json.RawMessage(`{"hr":69}`)})
		time.Sleep(time.Second)
	}))
	defer srv.Close()
	old := hypeRateWSURL
	hypeRateWSURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	t.Cleanup(func() { hypeRateWSURL = old })

	o := DefaultConfig().HeartRate
	o.Source = HeartHypeRate
	o.HypeRateKey = "key-1"
	o.HypeRateID = "ABCD"
	waitSample(t, o, 69)
}

func TestSendParamUsesAvatarAddress(t *testing.T) {
	var got []*osc.Message
	old := avatarSend
	avatarSend = func(_ OSCOptions, m *osc.Message) error { got = append(got, m); return nil }
	t.Cleanup(func() { avatarSend = old })
	sendParam(OSCOptions{}, "HR", int32(70))
	sendParam(OSCOptions{}, "", int32(70))
	if len(got) != 1 || got[0].Address != "/avatar/parameters/HR" || got[0].Arguments[0] != int32(70) {
		t.Errorf("sent = %v", got)
	}
}
