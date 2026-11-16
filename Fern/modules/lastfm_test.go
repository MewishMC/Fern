package modules

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// lastfmFake answers user.getrecenttracks with recent and track.getInfo
// with a play count of 12.
func lastfmFake(t *testing.T, recent *atomic.Value, infoCalls *atomic.Int32) lastfmClient {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api_key") != "key" || q.Get("format") != "json" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":10,"message":"Invalid API key - You must be granted a valid key by last.fm"}`))
			return
		}
		switch q.Get("method") {
		case "user.getrecenttracks":
			if q.Get("user") != "fern" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":6,"message":"User not found"}`))
				return
			}
			_, _ = w.Write([]byte(recent.Load().(string)))
		case "track.getinfo", "track.getInfo":
			infoCalls.Add(1)
			if q.Get("username") != "fern" || q.Get("artist") != "Artist" || q.Get("track") != "Song" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"track":{"name":"Song","userplaycount":"12","userloved":"0"}}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(resetLastFM)
	return lastfmClient{http: srv.Client(), base: srv.URL + "/2.0/", user: "fern", key: "key"}
}

const lastfmNowPlaying = `{"recenttracks":{"track":[
	{"artist":{"name":"Artist","url":"x"},"name":"Song","album":{"#text":"Album"},"loved":"1","@attr":{"nowplaying":"true"}},
	{"artist":{"name":"Old"},"name":"Before","album":{"#text":""},"loved":"0","date":{"uts":"1"}}],
	"@attr":{"user":"fern","total":"12345"}}}`

const lastfmIdle = `{"recenttracks":{"track":{"artist":{"name":"Artist"},"name":"Song","album":{"#text":"Album"},"loved":"0","date":{"uts":"1"}},
	"@attr":{"user":"fern","total":"12346"}}}`

func TestLastFMPollAndLine(t *testing.T) {
	var recent atomic.Value
	var infos atomic.Int32
	recent.Store(lastfmNowPlaying)
	c := lastfmFake(t, &recent, &infos)
	o := defaultLastFMOptions()
	resetLastFM()
	if err := lastfmPollOnce(c, o); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.LastFMOptions = o
	if got := lastfmLine(cfg); got != "🎧 Scrobbling Song — Artist ♥ · 12 plays" {
		t.Errorf("line = %q", got)
	}
	info := CurrentLastFM()
	if !info.NowPlaying || info.Plays != 12 || !info.Loved || info.Total != 12345 || info.Album != "Album" {
		t.Errorf("info = %+v", info)
	}

	// The play count is asked once per song.
	if err := lastfmPollOnce(c, o); err != nil || infos.Load() != 1 {
		t.Errorf("second poll: %v, %d track.getInfo calls", err, infos.Load())
	}

	o.ShowTotal, o.ShowAlbum, o.Compact = true, true, true
	if got := formatLastFMLine(o, lastfmNow); got != "🎧 Scrobbling Song — Artist · Album ♥ · 12 plays · 12k scrobbles" {
		t.Errorf("full line = %q", got)
	}
	o.Template = "{icon} {title} by {artist} {loved} ({playCount}x, {totalCount})"
	if got := formatLastFMLine(o, lastfmNow); got != "🎧 Song by Artist ♥ (12x, 12k)" {
		t.Errorf("template = %q", got)
	}

	// Nothing playing: hidden, or the last scrobble when asked for. A single
	// track comes as an object rather than a list.
	recent.Store(lastfmIdle)
	o = defaultLastFMOptions()
	if err := lastfmPollOnce(c, o); err != nil {
		t.Fatal(err)
	}
	if got := formatLastFMLine(o, lastfmNow); got != "" {
		t.Errorf("idle line = %q", got)
	}
	o.ShowLast, o.ShowPlays = true, false
	if got := formatLastFMLine(o, lastfmNow); got != "🎧 Last played Song — Artist" {
		t.Errorf("last played = %q", got)
	}
}

func TestLastFMErrors(t *testing.T) {
	var recent atomic.Value
	var infos atomic.Int32
	recent.Store(lastfmNowPlaying)
	c := lastfmFake(t, &recent, &infos)
	bad := c
	bad.key = "secret-key-value"
	err := lastfmPollOnce(bad, defaultLastFMOptions())
	if err == nil || !lastfmBad(err) || strings.Contains(err.Error(), "secret-key-value") {
		t.Errorf("bad key: %v", err)
	}
	stranger := c
	stranger.user = "nobody"
	if err := lastfmPollOnce(stranger, defaultLastFMOptions()); err == nil || err.Error() != "Last.fm has no user by that name" {
		t.Errorf("unknown user: %v", err)
	}
	offline := c
	offline.base = "http://127.0.0.1:1/"
	if err := lastfmPollOnce(offline, defaultLastFMOptions()); err == nil || strings.Contains(err.Error(), "key") || lastfmBad(err) {
		t.Errorf("offline: %v", err)
	}
	if got := lastfmLine(DefaultConfig()); got != "" {
		t.Errorf("line after errors = %q", got)
	}
}

func TestLastFMSanitize(t *testing.T) {
	useTempSecrets(t)
	c := DefaultConfig()
	if c.DisplayOptions.LastFM || c.LastFMOptions.IntervalSec != 15 || !c.LastFMOptions.ShowPlays {
		t.Errorf("defaults = %+v", c.LastFMOptions)
	}
	c.LastFMOptions = LastFMOptions{IntervalSec: 1, Icon: "  🎧🎧🎧🎧🎧 ", Separator: "\n", Text: " now \n playing ", MaxChars: 9999}
	c.sanitize()
	o := c.LastFMOptions
	if o.IntervalSec != 10 || o.Icon != "🎧🎧🎧🎧" || o.Separator != " — " || o.Text != "now playing" || o.MaxChars != 144 {
		t.Errorf("sanitized = %+v", o)
	}
	if !knownModules[ModuleLastFM] || LineLabel(ModuleLastFM) != "Last.fm" {
		t.Error("the Last.fm line is not registered")
	}
	if err := SetPanelSecret(SecretLastFMKey, ""); err != nil {
		t.Errorf("panel cannot set the Last.fm key: %v", err)
	}
}

func TestLastFMTrigger(t *testing.T) {
	tr := lookupTrigger(TriggerLastFMTrack)
	if tr == nil {
		t.Fatal("trigger missing")
	}
	p := SanitizeRuleParams(tr.Fields, RuleParams{"loved": true})
	if tr.Match(p, RuleEvent{Data: map[string]string{"title": "Song", "loved": "0"}}) ||
		!tr.Match(p, RuleEvent{Data: map[string]string{"title": "Song", "loved": "1"}}) {
		t.Error("loved filter")
	}
}
