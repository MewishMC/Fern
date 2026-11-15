package modules

import (
	"testing"
	"time"
)

func TestProgressBarStyles(t *testing.T) {
	half, dur := 50*time.Second, 100*time.Second
	cases := []struct {
		name string
		o    MediaOptions
		pos  time.Duration
		want string
	}{
		{"default", MediaOptions{BarWidth: 4, BarFilled: "▰", BarEmpty: "▱"}, half, "▰▰▱▱"},
		{"empty characters fall back", MediaOptions{BarWidth: 4}, half, "▰▰▱▱"},
		{"head at start", MediaOptions{BarWidth: 5, BarFilled: "━", BarEmpty: "─", BarHead: "●"}, 0, "●────"},
		{"head in middle", MediaOptions{BarWidth: 5, BarFilled: "━", BarEmpty: "─", BarHead: "●"}, half, "━━●──"},
		{"head at end", MediaOptions{BarWidth: 5, BarFilled: "━", BarEmpty: "─", BarHead: "●"}, dur, "━━━━●"},
		{"caps", MediaOptions{BarWidth: 4, BarFilled: "=", BarEmpty: "-", BarHead: ">", BarLeft: "[", BarRight: "]"}, half, "[==>-]"},
		{"two-rune cells", MediaOptions{BarWidth: 2, BarFilled: "ab", BarEmpty: "cd"}, half, "abcd"},
		{"too narrow", MediaOptions{BarWidth: 1}, half, ""},
	}
	for _, c := range cases {
		if got := progressBar(c.pos, dur, c.o); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSanitizeLimitsBarCharacters(t *testing.T) {
	c := DefaultConfig()
	c.MediaOptions.BarFilled = "abcdef"
	c.MediaOptions.BarEmpty = " "
	c.sanitize()
	if c.MediaOptions.BarFilled != "ab" || c.MediaOptions.BarEmpty != " " {
		t.Errorf("got %q / %q", c.MediaOptions.BarFilled, c.MediaOptions.BarEmpty)
	}
}
