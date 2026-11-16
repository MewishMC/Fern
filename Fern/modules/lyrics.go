package modules

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LyricsStatus is what the panel shows about the lyrics lookup.
type LyricsStatus struct {
	State  string `json:"state"`  // see the lyrics* state constants
	Source string `json:"source"` // the .lrc file name, or LRCLIB
	Track  string `json:"track"`
	Line   string `json:"line"` // what the chatbox shows right now
	Lines  int    `json:"lines"`
}

// Lyrics lookup states.
const (
	lyricsOff          = "off"
	lyricsIdle         = "idle"
	lyricsSearching    = "searching"
	lyricsFound        = "found"
	lyricsNotFound     = "notfound"
	lyricsUnsynced     = "unsynced"
	lyricsInstrumental = "instrumental"
	lyricsOffline      = "offline"
	lyricsLimited      = "limited"
)

// lrclibBase is a variable so tests can point it at a local server.
var lrclibBase = "https://lrclib.net/api/"

type lyricsResult struct {
	state  string
	source string
	lines  []LyricLine
}

// lyricsQuery is the track to look up, already tidied.
type lyricsQuery struct {
	key, title, artist, album string
	seconds                   int
	display                   string
}

var (
	lyricsMu       sync.RWMutex
	lyricsKey      string // track the current result belongs to
	lyricsTrack    string
	lyricsLocalSig string // folder and online switch the result was found with
	lyricsCur      lyricsResult
	lyricsRetryAt  time.Time
	lyricsFails    int
	lyricsPausedTo time.Time // after a 429, LRCLIB is left alone until then
	// lyricsCache keeps LRCLIB's answers, found or not, for this run so a
	// track is asked about once.
	lyricsCache = map[string]lyricsResult{}
)

func startLyricsWorker() {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			lyricsTick()
		}
	}()
}

// lyricsQueryFor builds the lookup for the playing track. Titles are tidied
// first: "(Official Video)" is never part of the song's name.
func lyricsQueryFor(info MediaInfo) (lyricsQuery, bool) {
	if !info.Available || strings.TrimSpace(info.Title) == "" {
		return lyricsQuery{}, false
	}
	title, artist := TidyTrack(info.Title, info.Artist)
	q := lyricsQuery{
		title:   title,
		artist:  artist,
		album:   strings.TrimSpace(info.Album),
		seconds: int((info.DurationMS + 500) / 1000),
		key:     squashName(artist) + "\x00" + squashName(title),
		display: joinNonEmpty(" — ", title, artist),
	}
	return q, true
}

// lyricsTick looks up lyrics when the track changes, and retries a failed
// lookup after a growing pause. It runs on the lyrics worker only.
func lyricsTick() {
	cfg := GetConfig()
	if !cfg.DisplayOptions.Lyrics {
		return
	}
	q, ok := lyricsQueryFor(CurrentMedia())
	if !ok {
		lyricsMu.Lock()
		lyricsKey, lyricsTrack, lyricsCur = "", "", lyricsResult{state: lyricsIdle}
		lyricsMu.Unlock()
		return
	}
	o := cfg.LyricsOptions
	sig := o.Folder + "\x00" + fmt.Sprint(o.Online)

	lyricsMu.Lock()
	same := q.key == lyricsKey && sig == lyricsLocalSig
	retry := (lyricsCur.state == lyricsOffline || lyricsCur.state == lyricsLimited) && time.Now().After(lyricsRetryAt)
	if same && !retry {
		lyricsMu.Unlock()
		return
	}
	if !same {
		lyricsFails = 0
	}
	lyricsKey, lyricsTrack, lyricsLocalSig = q.key, q.display, sig
	lyricsCur = lyricsResult{state: lyricsSearching}
	lyricsMu.Unlock()

	res, wait := lookupLyrics(q, o)

	lyricsMu.Lock()
	defer lyricsMu.Unlock()
	if lyricsKey != q.key {
		return // the track changed meanwhile; the next tick looks that one up
	}
	lyricsCur = res
	if res.state == lyricsOffline || res.state == lyricsLimited {
		lyricsFails++
		backoff := 30 * time.Second << min(lyricsFails-1, 4) // 30s up to 8m
		if wait > backoff {
			backoff = wait
		}
		lyricsRetryAt = time.Now().Add(backoff)
	}
}

// lookupLyrics tries the local folder, then LRCLIB. The duration is how long
// LRCLIB asked us to wait, if it did.
func lookupLyrics(q lyricsQuery, o LyricsOptions) (lyricsResult, time.Duration) {
	if o.Folder != "" {
		if res, ok := localLyrics(o.Folder, q); ok {
			return res, 0
		}
	}
	if !o.Online {
		return lyricsResult{state: lyricsNotFound}, 0
	}
	lyricsMu.RLock()
	cached, hit := lyricsCache[q.key]
	pausedTo := lyricsPausedTo
	lyricsMu.RUnlock()
	if hit {
		return cached, 0
	}
	if time.Now().Before(pausedTo) {
		return lyricsResult{state: lyricsLimited}, time.Until(pausedTo)
	}
	res, wait := lrclibLookup(q)
	lyricsMu.Lock()
	switch res.state {
	case lyricsLimited:
		lyricsPausedTo = time.Now().Add(wait)
	case lyricsOffline:
	default:
		if len(lyricsCache) >= 200 {
			lyricsCache = map[string]lyricsResult{}
		}
		lyricsCache[q.key] = res
	}
	lyricsMu.Unlock()
	return res, wait
}

// lrclibTrack is the part of an LRCLIB record Fern uses.
type lrclibTrack struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

// lrclibLookup asks LRCLIB with at most two requests: an exact match when the
// album and length are known, then a search.
func lrclibLookup(q lyricsQuery) (lyricsResult, time.Duration) {
	var plain, instrumental bool
	if q.album != "" && q.seconds > 0 && q.artist != "" {
		v := url.Values{}
		v.Set("artist_name", q.artist)
		v.Set("track_name", q.title)
		v.Set("album_name", q.album)
		v.Set("duration", fmt.Sprint(q.seconds))
		var t lrclibTrack
		status, wait, err := getJSON(lrclibBase+"get?"+v.Encode(), &t)
		if res, failed := lyricsFailure(err); failed {
			return res, wait
		}
		if status == 200 {
			if res := lrclibResult(t); res.state == lyricsFound {
				return res, 0
			}
			plain, instrumental = t.PlainLyrics != "", t.Instrumental
		}
	}

	v := url.Values{}
	v.Set("track_name", q.title)
	if q.artist != "" {
		v.Set("artist_name", q.artist)
	}
	var found []lrclibTrack
	_, wait, err := getJSON(lrclibBase+"search?"+v.Encode(), &found)
	if res, failed := lyricsFailure(err); failed {
		return res, wait
	}
	if t, ok := pickLrclib(found, q); ok {
		return lrclibResult(t), 0
	}
	for _, t := range found {
		plain = plain || t.PlainLyrics != ""
		instrumental = instrumental || (t.Instrumental && len(found) == 1)
	}
	switch {
	case instrumental:
		return lyricsResult{state: lyricsInstrumental, source: "LRCLIB"}, 0
	case plain:
		return lyricsResult{state: lyricsUnsynced, source: "LRCLIB"}, 0
	}
	return lyricsResult{state: lyricsNotFound}, 0
}

// lyricsFailure turns a request error into a state worth retrying later.
func lyricsFailure(err error) (lyricsResult, bool) {
	switch {
	case err == nil:
		return lyricsResult{}, false
	case err == errRateLimited:
		return lyricsResult{state: lyricsLimited}, true
	}
	return lyricsResult{state: lyricsOffline}, true
}

func lrclibResult(t lrclibTrack) lyricsResult {
	if t.Instrumental {
		return lyricsResult{state: lyricsInstrumental, source: "LRCLIB"}
	}
	lines := ParseLRC(t.SyncedLyrics)
	if !hasWords(lines) {
		if t.PlainLyrics != "" {
			return lyricsResult{state: lyricsUnsynced, source: "LRCLIB"}
		}
		return lyricsResult{state: lyricsNotFound}
	}
	return lyricsResult{state: lyricsFound, source: "LRCLIB", lines: lines}
}

// pickLrclib chooses the search result that best fits the track: synced, by
// the same artist and within a few seconds of the same length.
func pickLrclib(found []lrclibTrack, q lyricsQuery) (lrclibTrack, bool) {
	artist := squashName(q.artist)
	near := func(t lrclibTrack) bool {
		if q.seconds <= 0 || t.Duration <= 0 {
			return true
		}
		d := t.Duration - float64(q.seconds)
		return d > -5 && d < 5
	}
	sameArtist := func(t lrclibTrack) bool {
		a := squashName(t.ArtistName)
		return artist == "" || a == "" || strings.Contains(a, artist) || strings.Contains(artist, a)
	}
	var fallback *lrclibTrack
	for i, t := range found {
		if t.SyncedLyrics == "" || !near(t) {
			continue
		}
		if sameArtist(t) {
			return t, true
		}
		if fallback == nil && q.seconds > 0 {
			fallback = &found[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return lrclibTrack{}, false
}

// localLyrics looks for "Artist - Title.lrc", "Title - Artist.lrc" or
// "Title.lrc" in folder, ignoring case, spaces and punctuation.
func localLyrics(folder string, q lyricsQuery) (lyricsResult, bool) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return lyricsResult{}, false
	}
	want := []string{
		squashName(q.artist + q.title),
		squashName(q.title + q.artist),
		squashName(q.title),
	}
	for _, target := range want {
		if target == "" {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			ext := filepath.Ext(name)
			if e.IsDir() || !strings.EqualFold(ext, ".lrc") || squashName(strings.TrimSuffix(name, ext)) != target {
				continue
			}
			data, err := os.ReadFile(filepath.Join(folder, name))
			if err != nil || len(data) > 1<<20 {
				continue
			}
			if lines := ParseLRC(string(data)); hasWords(lines) {
				return lyricsResult{state: lyricsFound, source: name, lines: lines}, true
			}
		}
	}
	return lyricsResult{}, false
}

func hasWords(lines []LyricLine) bool {
	for _, l := range lines {
		if l.Text != "" {
			return true
		}
	}
	return false
}

// currentLyric is the lyric for the playing track at its current position,
// or the marker during breaks and for instrumentals.
func currentLyric(cfg Config) string {
	if !cfg.DisplayOptions.Lyrics {
		return ""
	}
	info := CurrentMedia()
	if !info.Playing {
		return ""
	}
	q, ok := lyricsQueryFor(info)
	if !ok {
		return ""
	}
	o := cfg.LyricsOptions
	lyricsMu.RLock()
	res := lyricsCur
	same := q.key == lyricsKey
	lyricsMu.RUnlock()
	if !same {
		return ""
	}
	text := ""
	switch res.state {
	case lyricsFound:
		pos := time.Duration(info.PositionMS-int64(o.OffsetMS)) * time.Millisecond
		text = lyricAt(res.lines, pos, time.Duration(o.HoldSeconds)*time.Second, time.Duration(o.GapSeconds)*time.Second, o.GapMarker)
	case lyricsInstrumental:
		text = o.GapMarker
	}
	return strings.TrimSpace(RuneLimit(text, o.MaxChars))
}

// lyricsLine is the lyrics module's own line. When lyrics replace the track,
// they appear on the media line instead, as long as that line is on.
func lyricsLine(cfg Config) string {
	o := cfg.LyricsOptions
	if o.ReplaceTrack && cfg.DisplayOptions.Media {
		return ""
	}
	return withLabel(o.Label, currentLyric(cfg))
}

// replacingLyric is the lyric the media line shows in place of the track.
func replacingLyric(cfg Config) string {
	if !cfg.LyricsOptions.ReplaceTrack {
		return ""
	}
	return currentLyric(cfg)
}

// CurrentLyricsStatus reports the lookup for the panel.
func CurrentLyricsStatus() LyricsStatus {
	cfg := GetConfig()
	if !cfg.DisplayOptions.Lyrics {
		return LyricsStatus{State: lyricsOff}
	}
	lyricsMu.RLock()
	st := LyricsStatus{State: lyricsCur.state, Source: lyricsCur.source, Track: lyricsTrack, Lines: len(lyricsCur.lines)}
	lyricsMu.RUnlock()
	if st.State == "" {
		st.State = lyricsIdle
	}
	st.Line = currentLyric(cfg)
	return st
}

// sanitizeLyrics keeps the lyrics options in range.
func sanitizeLyrics(o *LyricsOptions) {
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
	o.OffsetMS = clampInt(o.OffsetMS, -10000, 10000)
	o.HoldSeconds = clampInt(o.HoldSeconds, 1, 30)
	o.GapSeconds = clampInt(o.GapSeconds, 0, 60)
	o.GapMarker = RuneLimit(strings.Join(strings.Fields(o.GapMarker), " "), 16)
	o.Folder = RuneLimit(strings.Trim(strings.TrimSpace(o.Folder), `"`), 260)
	o.MaxChars = clampInt(o.MaxChars, 10, 144)
}
