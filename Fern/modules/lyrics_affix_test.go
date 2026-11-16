package modules

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// playLyrics makes "Song" by "Band" the playing track, two seconds in, with
// res as its looked up lyrics.
func playLyrics(t *testing.T, res lyricsResult) {
	t.Helper()
	now := time.Now()
	setMedia(rawMedia{Available: true, AppID: "a", Title: "Song", Artist: "Band", Status: "playing",
		Position: 2 * time.Second, Duration: 200 * time.Second, Sampled: now, Updated: now})
	q, ok := lyricsQueryFor(CurrentMedia())
	if !ok {
		t.Fatal("no lyrics query for the playing track")
	}
	lyricsMu.Lock()
	oldKey, oldCur := lyricsKey, lyricsCur
	lyricsKey, lyricsCur = q.key, res
	lyricsMu.Unlock()
	t.Cleanup(func() {
		setMedia(rawMedia{})
		lyricsMu.Lock()
		lyricsKey, lyricsCur = oldKey, oldCur
		lyricsMu.Unlock()
	})
}

func lyricsAffixConfig(replace bool) Config {
	c := DefaultConfig()
	c.DisplayOptions.Lyrics = true
	c.DisplayOptions.Media = true
	c.LyricsOptions.ReplaceTrack = replace
	c.LyricsOptions.Prefix = "🎤 "
	c.LyricsOptions.Suffix = " 🎶"
	c.sanitize()
	return c
}

func TestLyricAffixesOnBothLines(t *testing.T) {
	playLyrics(t, lyricsResult{state: lyricsFound, lines: []LyricLine{{0, "hello world"}, {60 * time.Second, "next"}}})

	own := lyricsAffixConfig(false)
	if got, want := lyricsLine(own), "🎤 hello world 🎶"; got != want {
		t.Errorf("own line = %q, want %q", got, want)
	}
	own.LyricsOptions.Label = "Lyrics:"
	if got, want := lyricsLine(own), "Lyrics: 🎤 hello world 🎶"; got != want {
		t.Errorf("own line with label = %q, want %q", got, want)
	}

	media := lyricsAffixConfig(true)
	if got := lyricsLine(media); got != "" {
		t.Errorf("lyrics line shown while lyrics replace the track: %q", got)
	}
	want := strings.TrimSpace(media.MediaOptions.Icon) + " 🎤 hello world 🎶"
	if got := mediaFitLine(media).Variants[0]; got != want {
		t.Errorf("media line = %q, want %q", got, want)
	}

	// The global per-line affixes go on once, outside the lyric's own.
	fit := FitLines([]FitLine{mediaFitLine(media)}, DefaultDropOrder, "\n", "» ", " «", 143, 9)
	if got := fit.Text; got != "» "+want+" «" {
		t.Errorf("chatbox with global affixes = %q", got)
	}
}

func TestLyricAffixesWrapBreakMarker(t *testing.T) {
	playLyrics(t, lyricsResult{state: lyricsInstrumental})
	if got, want := lyricsLine(lyricsAffixConfig(false)), "🎤 ♪ ♪ ♪ 🎶"; got != want {
		t.Errorf("instrumental = %q, want %q", got, want)
	}
	c := lyricsAffixConfig(false)
	c.LyricsOptions.GapMarker = ""
	if got := lyricsLine(c); got != "" {
		t.Errorf("no marker still showed the affixes: %q", got)
	}
}

func TestLyricAffixesNothingToShow(t *testing.T) {
	// The first line is still a minute away and too short an intro for a marker.
	playLyrics(t, lyricsResult{state: lyricsFound, lines: []LyricLine{{5 * time.Second, "later"}}})
	c := lyricsAffixConfig(true)
	if got := currentLyric(c); got != "" {
		t.Errorf("affixes shown with no lyric: %q", got)
	}
}

func TestAffixLyricLength(t *testing.T) {
	o := DefaultConfig().LyricsOptions
	o.MaxChars = 20
	o.Prefix, o.Suffix = "🎤 ", " 🎶"
	got := affixLyric(o, "a line that is far longer than twenty characters")
	if n := utf8.RuneCountInString(got); n > 20 || !strings.HasPrefix(got, "🎤 a line") || !strings.HasSuffix(got, " 🎶") {
		t.Errorf("limited = %q (%d runes)", got, n)
	}
	// Long affixes never squeeze the lyric below a few characters.
	o.Prefix, o.Suffix = "<<<<<<<<<<<<<<<<", ">>>>>>>>>>>>>>>>"
	got = affixLyric(o, "abcdefghijklmnop")
	if want := o.Prefix + "abcdefghij" + o.Suffix; got != want {
		t.Errorf("long affixes = %q, want %q", got, want)
	}
	// Without affixes the old limit applies unchanged.
	o.Prefix, o.Suffix = "", ""
	if got := affixLyric(o, "a line that is far longer than twenty characters"); got != "a line that is far l" {
		t.Errorf("no affixes = %q", got)
	}
}

func TestSanitizeLyricAffixes(t *testing.T) {
	c := DefaultConfig()
	if c.LyricsOptions.Prefix != "" || c.LyricsOptions.Suffix != "" {
		t.Fatal("affixes on by default")
	}
	c.LyricsOptions.Prefix = "🎤\r\n "
	c.LyricsOptions.Suffix = " 🎶 and a lot more than sixteen"
	c.sanitize()
	if got := c.LyricsOptions.Prefix; got != "🎤 " {
		t.Errorf("prefix = %q", got)
	}
	if got := c.LyricsOptions.Suffix; utf8.RuneCountInString(got) != 16 || !strings.HasPrefix(got, " 🎶 and") {
		t.Errorf("suffix = %q", got)
	}
}
