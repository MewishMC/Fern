package modules

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MediaSource is one media session Windows currently exposes, used by the
// "detect from" picker so Fern can follow a chosen app instead of the current
// session.
type MediaSource struct {
	AppID   string `json:"appId"`
	App     string `json:"app"`
	Title   string `json:"title"`
	Playing bool   `json:"playing"`
}

// MediaInfo is the sanitized media state shared with the UI and the chatbox.
type MediaInfo struct {
	Available  bool   `json:"available"`
	AppID      string `json:"appId"`
	App        string `json:"app"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	Status     string `json:"status"`
	Playing    bool   `json:"playing"`
	PositionMS int64  `json:"positionMs"`
	DurationMS int64  `json:"durationMs"`
	Cover      string `json:"cover"` // changes with the artwork; empty when there is none
	Source     string `json:"source"`
	SampledAt  int64  `json:"sampledAt"`
}

var (
	mediaMu      sync.RWMutex
	mediaCurrent rawMedia
	lastFallback time.Time

	sourcesMu sync.RWMutex
	sources   []MediaSource
	sourcesAt time.Time

	// The position Windows reports lags behind actual playback, so the same
	// track can report a lower position than we already showed. Track the last
	// value per track and never let the clock run backwards.
	mediaLastKey string
	mediaLastPos time.Duration
)

func setMedia(raw rawMedia) {
	mediaMu.Lock()
	mediaCurrent = raw
	mediaMu.Unlock()
}

// CurrentMedia returns the media state with the playback position advanced to
// "now". The clock is extrapolated from the timestamp Windows reports for the
// position (not from when we polled), and clamped so it never runs backwards.
func CurrentMedia() MediaInfo {
	mediaMu.Lock()
	raw := mediaCurrent
	now := time.Now()

	pos := raw.Position
	if raw.Available && raw.Status == "playing" {
		base := raw.Updated
		if base.IsZero() || base.After(now) {
			base = raw.Sampled
		}
		if !base.IsZero() && now.After(base) {
			pos += now.Sub(base)
		}
	}
	if raw.Duration > 0 && pos > raw.Duration {
		pos = raw.Duration
	}
	if pos < 0 {
		pos = 0
	}

	key := trackKey(raw)
	switch {
	case !raw.Available:
		mediaLastKey, mediaLastPos = "", 0
	case raw.Status == "playing":
		if key == mediaLastKey && mediaLastPos > 0 && pos < mediaLastPos {
			// A large jump backwards is the user seeking, so trust it.
			if pos+2500*time.Millisecond >= mediaLastPos {
				pos = mediaLastPos
			}
		}
		mediaLastKey, mediaLastPos = key, pos
	}

	sampledAt := now.UnixMilli()
	mediaMu.Unlock()

	cfg := GetConfig()
	info := MediaInfo{
		Available:  raw.Available,
		AppID:      raw.AppID,
		App:        appNameFromID(raw.AppID),
		Title:      raw.Title,
		Artist:     raw.Artist,
		Album:      raw.Album,
		Status:     raw.Status,
		Playing:    raw.Status == "playing",
		Source:     "windows",
		SampledAt:  sampledAt,
		DurationMS: raw.Duration.Milliseconds(),
		PositionMS: pos.Milliseconds(),
	}
	if raw.Sampled.IsZero() {
		info.Source = ""
	}
	if c := CurrentCover(); c.Key != "" && coverTrack() == key {
		info.Cover = c.Key
	}

	if filter := strings.TrimSpace(cfg.MediaOptions.AppFilter); filter != "" && info.Available {
		haystack := strings.ToLower(info.AppID + " " + info.App + " " + info.Title + " " + info.Artist)
		if !strings.Contains(haystack, strings.ToLower(filter)) {
			info.Available = false
		}
	}
	if !info.Available {
		info.Title, info.Artist, info.Status = "", "", ""
	}

	// Windows hands back the app's own name as the media title before a track is
	// loaded ("Spotify Premium", or just "Spotify"). That is not a song, and
	// showing it made the now playing card flash an app name before the track.
	if isAppNameText(info.Title, info.App) {
		info.Title = ""
	}
	if isAppNameText(info.Artist, info.App) {
		info.Artist = ""
	}
	if isAppNameText(info.Album, info.App) {
		info.Album = ""
	}
	return info
}

// trackKey identifies a track within one app.
func trackKey(raw rawMedia) string {
	return raw.AppID + "\x00" + raw.Title + "\x00" + raw.Artist
}

// isAppNameText reports whether a media string is really the app or its branding
// rather than track information.
func isAppNameText(s, app string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "" {
		return false
	}
	if isSpotifyBranding(t) {
		return true
	}
	if a := strings.ToLower(strings.TrimSpace(app)); a != "" && t == a {
		return true
	}
	return false
}

func appNameFromID(id string) string {
	if id == "" {
		return ""
	}
	name := id
	if idx := strings.LastIndexAny(name, "\\/"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimSuffix(name, ".exe")
	name = strings.TrimSuffix(name, ".EXE")
	if name == "" {
		return id
	}
	// Firefox registers its media session under a 16-digit hex hash of its
	// install path instead of a readable name.
	if isHexID(name) {
		return "Firefox"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func isHexID(s string) bool {
	if len(s) != 16 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789ABCDEFabcdef", r) {
			return false
		}
	}
	return true
}

func startMediaWorker() {
	go smtcLoop()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sampleMedia()
			// The session list only feeds the picker, so it can refresh slower
			// than the playback position.
			if sourcesStale() {
				refreshSources()
			}
		}
	}()
}

// sampleMedia reads the chosen session (or the current one) once.
func sampleMedia() {
	cfg := GetConfig()
	preferred := strings.TrimSpace(cfg.MediaOptions.PreferredApp)
	raw, err := smtcRead(preferred)
	if err == nil {
		setMedia(raw)
		return
	}
	// Fall back to the Spotify window title only when no particular app was
	// chosen; otherwise a different app's window would override the choice.
	if preferred == "" || strings.Contains(strings.ToLower(preferred), "spotify") {
		if time.Since(lastFallback) > 3*time.Second {
			lastFallback = time.Now()
			if fb, ok := windowTitleFallback(); ok {
				setMedia(fb)
				return
			}
		}
	}
	mediaMu.RLock()
	hadOne := mediaCurrent.Available
	mediaMu.RUnlock()
	if hadOne {
		setMedia(rawMedia{})
	}
}

// MediaSources returns the media sessions Windows is exposing right now.
func MediaSources() []MediaSource {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	return append([]MediaSource{}, sources...)
}

func refreshSources() {
	list, err := smtcSessions()
	if err != nil {
		return
	}
	sourcesMu.Lock()
	sources = list
	sourcesAt = time.Now()
	sourcesMu.Unlock()
}

func sourcesStale() bool {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	return time.Since(sourcesAt) > 3*time.Second
}

// windowTitleFallback is the legacy "read the Spotify window title" path. It is
// slower and has no playback position, so it is only used when SMTC is empty.
func windowTitleFallback() (rawMedia, bool) {
	title, found := findSpotifyWindow()
	if !found {
		return rawMedia{}, false
	}
	artist, song, ok := parseSpotifyTitle(title)
	if !ok {
		return rawMedia{Available: true, AppID: "Spotify.exe", Status: "paused", Sampled: time.Now()}, true
	}

	return rawMedia{
		Available: true,
		AppID:     "Spotify.exe",
		Status:    "playing",
		Title:     song,
		Artist:    artist,
		Sampled:   time.Now(),
	}, true
}

// parseSpotifyTitle splits a real "Artist - Song" window title. It reports false
// for everything else, because while nothing is playing Spotify titles its
// window with the branding ("Spotify Premium", "Spotify Free") and announcing
// that as the current song is wrong.
func parseSpotifyTitle(title string) (artist, song string, ok bool) {
	title = strings.TrimSpace(title)
	if title == "" || isSpotifyBranding(title) {
		return "", "", false
	}
	artist, song = parseMediaInfo(title)
	if artist == "" || song == "" {
		return "", "", false
	}
	return artist, song, true
}

// isSpotifyBranding reports whether a window title is one of Spotify's own
// idle titles rather than a track.
func isSpotifyBranding(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	t = strings.TrimSuffix(t, " - spotify")
	return t == "spotify" || t == "spotify premium" || t == "spotify free" ||
		t == "spotify web player" || t == "advertisement"
}

// mediaLine renders the now playing module for the chatbox.
func mediaLine(cfg Config) string {
	return mediaFitLine(cfg).Variants[0]
}

// mediaFitLine renders the now playing line with its shorter forms for
// fitting the chatbox: without the bar, without the time, without album and
// feat. credits, and finally with the track cut to fit.
func mediaFitLine(cfg Config) FitLine {
	return mediaFit(CurrentMedia(), cfg.MediaOptions)
}

func mediaFit(info MediaInfo, o MediaOptions) FitLine {
	line := FitLine{ID: ModuleMedia, Variants: []string{""}}
	if !info.Available {
		return line
	}
	if !info.Playing && !o.ShowWhenPaused {
		return line
	}

	title := strings.TrimSpace(info.Title)
	artist := ""
	if o.ShowArtist {
		artist = strings.TrimSpace(info.Artist)
	}
	album := ""
	if o.ShowAlbum {
		album = strings.TrimSpace(info.Album)
	}
	full := trackText(title, artist, album)
	if full == "" {
		return line
	}
	full = strings.TrimSpace(RuneLimit(full, o.MaxChars))
	short := strings.TrimSpace(RuneLimit(trackText(stripFeat(title), firstArtist(artist), ""), o.MaxChars))
	if short == "" {
		short = full
	}

	icon := strings.TrimSpace(o.Icon)
	if !info.Playing {
		icon = strings.TrimSpace(o.PausedIcon)
	}

	// The progress switch wins over the style: with it off the line is just
	// the track, which is what the switch promises in the UI.
	clock, bar := "", ""
	if o.ShowProgress && info.DurationMS > 0 {
		pos := time.Duration(info.PositionMS) * time.Millisecond
		dur := time.Duration(info.DurationMS) * time.Millisecond
		if o.ProgressStyle == "time" || o.ProgressStyle == "both" {
			clock = shortClock(pos) + "/" + shortClock(dur)
		}
		if o.ProgressStyle == "bar" || o.ProgressStyle == "both" {
			bar = progressBar(pos, dur, o)
		}
	}

	compose := func(track, bar, clock string) string {
		out := track
		if detail := strings.TrimSpace(bar + " " + clock); detail != "" {
			out += " " + detail
		}
		if icon != "" {
			out = icon + " " + out
		}
		return out
	}
	line.Variants = []string{compose(full, bar, clock)}
	for _, v := range []string{compose(full, "", clock), compose(full, "", ""), compose(short, "", "")} {
		if v != line.Variants[len(line.Variants)-1] {
			line.Variants = append(line.Variants, v)
		}
	}
	line.Trim = func(max int) string {
		lead := ""
		if icon != "" {
			lead = icon + " "
		}
		budget := max - len([]rune(lead))
		// Fewer than a handful of letters of a title says nothing; drop the
		// line instead.
		if budget < 8 || budget >= len([]rune(short)) {
			return ""
		}
		return lead + ellipsize(short, budget)
	}
	return line
}

// trackText joins title, artist and album the way the media line shows them.
func trackText(title, artist, album string) string {
	track := title
	if artist != "" {
		if track != "" {
			track = track + " — " + artist
		} else {
			track = artist
		}
	}
	if album != "" {
		if track != "" {
			track = track + " · " + album
		} else {
			track = album
		}
	}
	return track
}

var (
	featBracket = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s[^)\]]*[)\]]`)
	featTail    = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring)\s.*$`)
)

// stripFeat removes "(feat. Someone)" style credits from a title.
func stripFeat(title string) string {
	out := featTail.ReplaceAllString(featBracket.ReplaceAllString(title, ""), "")
	if out = strings.TrimSpace(out); out == "" {
		return title
	}
	return out
}

// firstArtist keeps the main artist of "A, B" or "A feat. B".
func firstArtist(artist string) string {
	artist = stripFeat(artist)
	if i := strings.Index(artist, ", "); i > 0 {
		artist = artist[:i]
	}
	return strings.TrimSpace(artist)
}

func shortClock(d time.Duration) string {
	total := int(d.Seconds())
	if total < 0 {
		total = 0
	}
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// progressBar draws the bar with the configured characters. With a head set,
// the head takes one of the cells and marks the position, like ━━━●──────.
func progressBar(pos, dur time.Duration, o MediaOptions) string {
	width := o.BarWidth
	if width < 2 || dur <= 0 {
		return ""
	}
	full, empty := o.BarFilled, o.BarEmpty
	if full == "" {
		full = "▰"
	}
	if empty == "" {
		empty = "▱"
	}
	ratio := float64(pos) / float64(dur)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	var bar string
	if o.BarHead != "" {
		at := int(ratio*float64(width-1) + 0.5)
		bar = strings.Repeat(full, at) + o.BarHead + strings.Repeat(empty, width-1-at)
	} else {
		filled := int(ratio * float64(width))
		if filled > width {
			filled = width
		}
		bar = strings.Repeat(full, filled) + strings.Repeat(empty, width-filled)
	}
	return o.BarLeft + bar + o.BarRight
}
