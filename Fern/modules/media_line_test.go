package modules

import (
	"testing"
	"time"
)

func TestOnlyShowOnChange(t *testing.T) {
	o := DefaultConfig().MediaOptions
	if mediaHiddenByChange(o, time.Hour) {
		t.Error("hidden while the option is off")
	}
	o.OnlyOnChange = true
	if mediaHiddenByChange(o, 10*time.Second) {
		t.Error("hidden 10s after a change with a 25s window")
	}
	if !mediaHiddenByChange(o, 25*time.Second) {
		t.Error("still shown 25s after a change")
	}

	defer setMedia(rawMedia{})
	setMedia(rawMedia{Available: true, AppID: "a", Title: "One", Status: "playing"})
	mediaMu.Lock()
	mediaChangedAt = time.Now().Add(-time.Minute)
	mediaMu.Unlock()
	setMedia(rawMedia{Available: true, AppID: "a", Title: "One", Status: "playing", Position: time.Second})
	if mediaChangedSince() < 50*time.Second {
		t.Error("a new position alone counted as a change")
	}
	setMedia(rawMedia{Available: true, AppID: "a", Title: "One", Status: "paused"})
	if mediaChangedSince() > time.Second {
		t.Error("pausing did not count as a change")
	}
}

func TestFormatMediaLine(t *testing.T) {
	base := DefaultConfig().MediaOptions
	base.ShowProgress = false
	playing := MediaInfo{Available: true, Playing: true, Status: "playing", Title: "Song", Artist: "Band"}
	paused := MediaInfo{Available: true, Status: "paused", Title: "Song", Artist: "Band"}
	stopped := MediaInfo{Available: true, Status: "stopped", Title: "Song", Artist: "Band"}

	with := func(edit func(o *MediaOptions)) MediaOptions {
		o := base
		edit(&o)
		return o
	}
	cases := []struct {
		name string
		o    MediaOptions
		info MediaInfo
		want string
	}{
		{"default", base, playing, "♪ Song — Band"},
		{"state text", with(func(o *MediaOptions) { o.PlayingText = "Listening to" }), playing, "♪ Listening to Song — Band"},
		{"separator", with(func(o *MediaOptions) { o.Separator = " ᵇʸ " }), playing, "♪ Song ᵇʸ Band"},
		{"uppercase", with(func(o *MediaOptions) { o.Uppercase = true; o.PlayingText = "Now" }), playing, "♪ NOW SONG — BAND"},
		{"paused hidden", base, paused, ""},
		{"paused shown", with(func(o *MediaOptions) { o.ShowWhenPaused = true; o.PausedText = "Paused" }), paused, "❚❚ Paused Song — Band"},
		{"stopped hidden", with(func(o *MediaOptions) { o.ShowWhenPaused = true }), stopped, ""},
		{"stopped shown", with(func(o *MediaOptions) { o.ShowWhenStopped = true; o.StoppedText = "Stopped" }), stopped, "⏹ Stopped Song — Band"},
		{"no icon", with(func(o *MediaOptions) { o.Icon = "" }), playing, "Song — Band"},
	}
	for _, c := range cases {
		if got := formatMediaLine(c.o, c.info); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatMediaLineProgress(t *testing.T) {
	o := DefaultConfig().MediaOptions
	o.ProgressStyle = "time"
	info := MediaInfo{Available: true, Playing: true, Title: "Song", PositionMS: 84000, DurationMS: 198000}
	if got := formatMediaLine(o, info); got != "♪ Song 1:24/3:18" {
		t.Errorf("got %q", got)
	}
	o.SuperscriptTime = true
	if got := formatMediaLine(o, info); got != "♪ Song ¹˸²⁴ᐟ³˸¹⁸" {
		t.Errorf("superscript: got %q", got)
	}
	o.ShowWhenStopped = true
	info.Playing, info.Status = false, "stopped"
	if got := formatMediaLine(o, info); got != "⏹ Song" {
		t.Errorf("stopped has no progress: got %q", got)
	}
}

func TestSanitizeMediaText(t *testing.T) {
	c := DefaultConfig()
	c.MediaOptions.Separator = "   "
	c.MediaOptions.PlayingText = "  Listening\nto  "
	c.sanitize()
	if c.MediaOptions.Separator != " — " {
		t.Errorf("blank separator: got %q", c.MediaOptions.Separator)
	}
	if c.MediaOptions.PlayingText != "Listening to" {
		t.Errorf("playing text: got %q", c.MediaOptions.PlayingText)
	}
	c.MediaOptions.Separator = " ᵇʸ "
	c.sanitize()
	if c.MediaOptions.Separator != " ᵇʸ " {
		t.Errorf("separator lost its spaces: %q", c.MediaOptions.Separator)
	}
}
