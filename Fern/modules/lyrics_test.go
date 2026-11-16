package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const sampleLRC = `[ar:Rick Astley]
[ti:Never Gonna Give You Up]
[length: 03:33]
[00:18.50]We're no strangers to love
[00:22.71]You know the rules and so do I
[00:27.00]
[00:43.10][01:30.00]Never gonna give you up
[01:00.5]Tenths work too
[01:05:25]So do colons
[01:10.123]<01:10.123>Word <01:11.00>timings   go
`

func TestParseLRC(t *testing.T) {
	lines := ParseLRC("\uFEFF" + sampleLRC)
	want := []LyricLine{
		{18500 * time.Millisecond, "We're no strangers to love"},
		{22710 * time.Millisecond, "You know the rules and so do I"},
		{27 * time.Second, ""},
		{43100 * time.Millisecond, "Never gonna give you up"},
		{60500 * time.Millisecond, "Tenths work too"},
		{65250 * time.Millisecond, "So do colons"},
		{70123 * time.Millisecond, "Word timings go"},
		{90 * time.Second, "Never gonna give you up"},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i, lines[i], want[i])
		}
	}
}

func TestParseLRCOffsetAndPlainText(t *testing.T) {
	lines := ParseLRC("[offset:+500]\r\n[00:01.00]one\r\n[00:10.00]two\r\n")
	if len(lines) != 2 || lines[0].At != 500*time.Millisecond || lines[1].At != 9500*time.Millisecond {
		t.Errorf("offset not applied: %+v", lines)
	}
	if got := ParseLRC("just some\nplain lyrics\n"); len(got) != 0 {
		t.Errorf("plain text gave lines: %+v", got)
	}
}

func TestLyricAt(t *testing.T) {
	s := time.Second
	lines := []LyricLine{
		{10 * s, "first"},
		{13 * s, "second"},
		{30 * s, "after a long pause"},
		{32 * s, ""},
		{50 * s, "last"},
	}
	hold, gap, mark := 6*s, 8*s, "♪"
	cases := []struct {
		pos  time.Duration
		want string
	}{
		{0, "♪"},           // a 10s intro gets the marker
		{10 * s, "first"},  // on time
		{12 * s, "first"},  // still before the next line
		{19 * s, "second"}, // held for 6s
		{20 * s, "♪"},      // then a 17s pause shows the marker
		{31 * s, "after a long pause"},
		{40 * s, "♪"}, // an empty line is a break
		{55 * s, "last"},
		{57 * s, ""}, // nothing after the last line has been held
	}
	for _, c := range cases {
		if got := lyricAt(lines, c.pos, hold, gap, mark); got != c.want {
			t.Errorf("at %v: got %q, want %q", c.pos, got, c.want)
		}
	}
	// A short pause keeps the line instead of flashing the marker.
	short := []LyricLine{{0, "a"}, {7 * s, "b"}}
	if got := lyricAt(short, 6500*time.Millisecond, 2*s, gap, mark); got != "a" {
		t.Errorf("short pause: got %q", got)
	}
	if got := lyricAt([]LyricLine{{3 * s, "x"}}, 0, hold, gap, mark); got != "" {
		t.Errorf("short intro: got %q", got)
	}
}

func TestLocalLyrics(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Rick Astley - Never Gonna Give You Up.LRC"), []byte(sampleLRC), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "Plain.lrc"), []byte("no timings here"), 0o644)
	q, _ := lyricsQueryFor(MediaInfo{Available: true, Title: "Never Gonna Give You Up (Official Music Video)", Artist: "Rick Astley"})
	res, ok := localLyrics(dir, q)
	if !ok || res.state != lyricsFound || res.source != "Rick Astley - Never Gonna Give You Up.LRC" || len(res.lines) == 0 {
		t.Fatalf("got %+v, %v", res, ok)
	}
	q, _ = lyricsQueryFor(MediaInfo{Available: true, Title: "Plain", Artist: "x"})
	if _, ok := localLyrics(dir, q); ok {
		t.Error("a file without timings counted as lyrics")
	}
	// Once the track's length is known, the plain file is timed from it.
	q.seconds = 120
	if res, ok := localLyrics(dir, q); !ok || !res.estimated || len(res.lines) != 1 {
		t.Errorf("plain file with a length: %+v, %v", res, ok)
	}
}

func TestPickLrclib(t *testing.T) {
	q := lyricsQuery{title: "Song", artist: "Band", seconds: 200}
	found := []lrclibTrack{
		{ArtistName: "Band", Duration: 200, PlainLyrics: "plain only"},
		{ArtistName: "Cover Act", Duration: 201, SyncedLyrics: "[00:01.00]cover"},
		{ArtistName: "Band", Duration: 260, SyncedLyrics: "[00:01.00]long version"},
		{ArtistName: "Band", Duration: 198, SyncedLyrics: "[00:01.00]right one"},
	}
	got, ok := pickLrclib(found, q)
	if !ok || got.SyncedLyrics != "[00:01.00]right one" {
		t.Errorf("got %+v", got)
	}
	got, ok = pickLrclib(found[:3], q)
	if !ok || got.ArtistName != "Cover Act" {
		t.Errorf("fallback by length: got %+v", got)
	}
	if _, ok := pickLrclib(found[:1], q); ok {
		t.Error("picked a result without synced lyrics")
	}
}

// fakeLrclib serves LRCLIB's two endpoints and counts the requests.
func fakeLrclib(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if ua := r.Header.Get("User-Agent"); ua != "Fern" {
			t.Errorf("User-Agent %q", ua)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	old := lrclibBase
	lrclibBase = srv.URL + "/api/"
	t.Cleanup(func() { lrclibBase = old })
	lyricsMu.Lock()
	lyricsCache = map[string]lyricsResult{}
	lyricsPausedTo = time.Time{}
	lyricsMu.Unlock()
	return &hits
}

func TestLookupLyricsOnline(t *testing.T) {
	hits := fakeLrclib(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/get":
			if r.URL.Query().Get("duration") != "213" || r.URL.Query().Get("album_name") != "Whenever" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":404,"name":"TrackNotFound"}`))
		case "/api/search":
			_ = json.NewEncoder(w).Encode([]lrclibTrack{{ArtistName: "Rick Astley", Duration: 212, SyncedLyrics: sampleLRC}})
		}
	})
	q, _ := lyricsQueryFor(MediaInfo{Available: true, Title: "Never Gonna Give You Up", Artist: "Rick Astley", Album: "Whenever", DurationMS: 212600})
	o := DefaultConfig().LyricsOptions
	res, _ := lookupLyrics(q, o)
	if res.state != lyricsFound || res.source != "LRCLIB" || len(res.lines) != 8 {
		t.Fatalf("got %+v", res)
	}
	if hits.Load() != 2 {
		t.Errorf("%d requests, want 2", hits.Load())
	}
	// The answer is cached for the track.
	if res, _ := lookupLyrics(q, o); res.state != lyricsFound || hits.Load() != 2 {
		t.Errorf("cache missed: %+v after %d requests", res, hits.Load())
	}
	// Offline mode never asks.
	o.Online = false
	q.key = "other"
	if res, _ := lookupLyrics(q, o); res.state != lyricsNotFound || hits.Load() != 2 {
		t.Errorf("offline: %+v after %d requests", res, hits.Load())
	}
}

func TestLookupLyricsNotFoundAndRateLimit(t *testing.T) {
	limited := false
	hits := fakeLrclib(t, func(w http.ResponseWriter, r *http.Request) {
		if limited {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	})
	o := DefaultConfig().LyricsOptions
	res, _ := lookupLyrics(lyricsQuery{key: "a", title: "Unknown Song", artist: "Nobody"}, o)
	if res.state != lyricsNotFound || hits.Load() != 1 {
		t.Fatalf("not found: %+v after %d requests", res, hits.Load())
	}
	limited = true
	res, wait := lookupLyrics(lyricsQuery{key: "b", title: "Other"}, o)
	if res.state != lyricsLimited || wait != 2*time.Minute {
		t.Fatalf("rate limit: %+v, wait %v", res, wait)
	}
	// While paused, nothing is sent at all.
	if res, _ := lookupLyrics(lyricsQuery{key: "c", title: "Third"}, o); res.state != lyricsLimited || hits.Load() != 2 {
		t.Errorf("sent during the pause: %+v after %d requests", res, hits.Load())
	}
	// A limited answer is not cached as "not found".
	lyricsMu.RLock()
	_, cached := lyricsCache["b"]
	lyricsMu.RUnlock()
	if cached {
		t.Error("rate limited answer was cached")
	}
}

func TestLookupLyricsOffline(t *testing.T) {
	fakeLrclib(t, func(w http.ResponseWriter, r *http.Request) {})
	lrclibBase = "http://127.0.0.1:1/api/" // nothing listens there
	res, _ := lookupLyrics(lyricsQuery{key: "x", title: "Song"}, DefaultConfig().LyricsOptions)
	if res.state != lyricsOffline {
		t.Errorf("got %+v", res)
	}
}

func TestLyricsLines(t *testing.T) {
	defer setMedia(rawMedia{})
	setMedia(rawMedia{Available: true, AppID: "a", Title: "Song", Artist: "Band", Status: "playing",
		Position: 5 * time.Second, Duration: time.Minute, Sampled: time.Now(), Updated: time.Now()})
	q, _ := lyricsQueryFor(CurrentMedia())
	lyricsMu.Lock()
	lyricsKey = q.key
	lyricsCur = lyricsResult{state: lyricsFound, lines: []LyricLine{{4 * time.Second, "hello there"}, {30 * time.Second, "later"}}}
	lyricsMu.Unlock()
	defer func() {
		lyricsMu.Lock()
		lyricsKey, lyricsCur = "", lyricsResult{}
		lyricsMu.Unlock()
	}()

	c := DefaultConfig()
	c.DisplayOptions.Lyrics = true
	c.MediaOptions.ShowProgress = false
	c.LyricsOptions.Label = "🎤"
	if got := lyricsLine(c); got != "🎤 hello there" {
		t.Errorf("own line: got %q", got)
	}
	if got := mediaLine(c); got != "♪ Song — Band" {
		t.Errorf("media line without replace: got %q", got)
	}
	c.LyricsOptions.ReplaceTrack = true
	if got, line := mediaLine(c), lyricsLine(c); got != "♪ hello there" || line != "" {
		t.Errorf("replace: media %q, lyrics %q", got, line)
	}
	c.DisplayOptions.Media = false
	if got := lyricsLine(c); got != "🎤 hello there" {
		t.Errorf("replace with the media line off: got %q", got)
	}
	c.DisplayOptions.Lyrics = false
	if got := lyricsLine(c); got != "" {
		t.Errorf("module off: got %q", got)
	}
}

func TestSanitizeLyrics(t *testing.T) {
	c := DefaultConfig()
	c.LyricsOptions.OffsetMS = 99999
	c.LyricsOptions.HoldSeconds = 0
	c.LyricsOptions.Folder = `  "C:\Music\Lyrics"  `
	c.sanitize()
	o := c.LyricsOptions
	if o.OffsetMS != 10000 || o.HoldSeconds != 1 || o.Folder != `C:\Music\Lyrics` {
		t.Errorf("got %+v", o)
	}
}

func TestEstimateLRC(t *testing.T) {
	plain := "[ar:Band]\nFirst line here\nSecond line here\n\n\nNew verse starts\nLast"
	length := 200 * time.Second
	lines := EstimateLRC(plain, length)
	if len(lines) != 4 {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	if lines[0].Text != "First line here" || lines[3].Text != "Last" {
		t.Errorf("texts: %+v", lines)
	}
	// Singing starts after an intro and the last line leaves room for an outro.
	if lines[0].At != 14*time.Second || lines[3].At >= length-14*time.Second {
		t.Errorf("intro or outro missing: %+v", lines)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i].At <= lines[i-1].At {
			t.Fatalf("not in order: %+v", lines)
		}
	}
	// The verse break gives the line after it more room than a line change.
	if lines[2].At-lines[1].At <= lines[1].At-lines[0].At {
		t.Errorf("verse pause not longer: %+v", lines)
	}
	if EstimateLRC(plain, 0) != nil || EstimateLRC("\n[ti:x]\n", length) != nil {
		t.Error("estimated without a length or words")
	}
	// A very short track still gets every line inside it.
	short := EstimateLRC(plain, 4*time.Second)
	if len(short) != 4 || short[3].At >= 4*time.Second {
		t.Errorf("short track: %+v", short)
	}
}

func TestLookupLyricsPlainOnly(t *testing.T) {
	fakeLrclib(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]lrclibTrack{
			{ArtistName: "Someone Else", Duration: 300, PlainLyrics: "wrong song"},
			{ArtistName: "Alana Markel", Duration: 181, PlainLyrics: "Line one\nLine two"},
		})
	})
	q, _ := lyricsQueryFor(MediaInfo{Available: true, Title: "Restart", Artist: "Alana Markel", DurationMS: 180000})
	res, _ := lookupLyrics(q, DefaultConfig().LyricsOptions)
	if res.state != lyricsFound || !res.estimated || len(res.lines) != 2 || res.lines[0].Text != "Line one" {
		t.Fatalf("got %+v", res)
	}
	// Without a length from the player, LRCLIB's own length is used.
	q.key, q.seconds = "nolength", 0
	if res, _ := lookupLyrics(q, DefaultConfig().LyricsOptions); res.state != lyricsFound || !res.estimated {
		t.Errorf("no player length: %+v", res)
	}
}
