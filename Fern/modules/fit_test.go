package modules

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func plain(id, text string) FitLine { return FitLine{ID: id, Variants: []string{text}} }

func TestFitLines(t *testing.T) {
	media := FitLine{ID: ModuleMedia, Variants: []string{
		"♪ Song — Artist ▰▰▱▱ 1:00/3:00",
		"♪ Song — Artist 1:00/3:00",
		"♪ Song — Artist",
	}}
	cases := []struct {
		name        string
		lines       []FitLine
		drop        []string
		sep, pre    string
		suf         string
		chars, rows int
		want        string
		dropped     []string
		shrunk      []string
		cut         bool
	}{
		{
			name:  "fits untouched",
			lines: []FitLine{plain("time", "3:04 PM"), plain("text", "hi")},
			drop:  DefaultDropOrder, sep: "\n", chars: 143, rows: 9,
			want: "3:04 PM\nhi",
		},
		{
			name:  "exactly at the limit",
			lines: []FitLine{plain("time", "12345"), plain("text", "6789")},
			drop:  DefaultDropOrder, sep: "\n", chars: 10, rows: 9,
			want: "12345\n6789",
		},
		{
			name:  "time goes first",
			lines: []FitLine{plain("time", "3:04 PM"), plain("cpu", "cpu 5%"), plain("text", "hello")},
			drop:  DefaultDropOrder, sep: "\n", chars: 12, rows: 9,
			want: "cpu 5%\nhello", dropped: []string{"time"},
		},
		{
			name:  "several lines dropped in order",
			lines: []FitLine{plain("time", "3:04 PM"), plain("cpu", "cpu 5%"), plain("memory", "mem 3GB"), plain("text", "hello")},
			drop:  DefaultDropOrder, sep: "\n", chars: 13, rows: 9,
			want: "mem 3GB\nhello", dropped: []string{"time", "cpu"},
		},
		{
			name:  "custom drop order",
			lines: []FitLine{plain("time", "3:04 PM"), plain("text", "hello")},
			drop:  []string{"text", "time"}, sep: "\n", chars: 10, rows: 9,
			want: "3:04 PM", dropped: []string{"text"},
		},
		{
			name:  "display order kept for what is left",
			lines: []FitLine{plain("text", "hello"), plain("cpu", "cpu 5%"), plain("time", "3:04 PM")},
			drop:  DefaultDropOrder, sep: " | ", chars: 16, rows: 9,
			want: "hello | cpu 5%", dropped: []string{"time"},
		},
		{
			name:  "last line is cut, never dropped",
			lines: []FitLine{plain("time", "3:04 PM"), plain("text", "a very long message indeed")},
			drop:  DefaultDropOrder, sep: "\n", chars: 10, rows: 9,
			want: "a very lo…", dropped: []string{"time"}, shrunk: []string{"text"}, cut: true,
		},
		{
			name:  "media loses its bar before anything is dropped",
			lines: []FitLine{plain("time", "3:04 PM"), media},
			drop:  DefaultDropOrder, sep: "\n", chars: 33, rows: 9,
			want: "3:04 PM\n♪ Song — Artist 1:00/3:00", shrunk: []string{ModuleMedia},
		},
		{
			name:  "media loses the time next",
			lines: []FitLine{plain("time", "3:04 PM"), media},
			drop:  DefaultDropOrder, sep: "\n", chars: 23, rows: 9,
			want: "3:04 PM\n♪ Song — Artist", shrunk: []string{ModuleMedia},
		},
		{
			name:  "then lines are dropped",
			lines: []FitLine{plain("time", "3:04 PM"), media},
			drop:  DefaultDropOrder, sep: "\n", chars: 20, rows: 9,
			want: "♪ Song — Artist", dropped: []string{"time"}, shrunk: []string{ModuleMedia},
		},
		{
			name:  "room freed by a drop gives media back its time",
			lines: []FitLine{plain("time", "Monday 3:04:05 PM EST"), media, plain("text", "hello")},
			drop:  DefaultDropOrder, sep: "\n", chars: 33, rows: 9,
			want: "♪ Song — Artist 1:00/3:00\nhello", dropped: []string{"time"}, shrunk: []string{ModuleMedia},
		},
		{
			name: "trim before drop",
			lines: []FitLine{plain("text", "hello"), {ID: ModuleMedia, Variants: []string{"♪ A long song title here"},
				Trim: func(max int) string { return ellipsize("♪ A long song title here", max) }}},
			drop: []string{ModuleMedia, "text"}, sep: "\n", chars: 18, rows: 9,
			want: "hello\n♪ A long so…", shrunk: []string{ModuleMedia},
		},
		{
			name: "trim refused means drop",
			lines: []FitLine{plain("text", "hello"), {ID: ModuleMedia, Variants: []string{"♪ A long song title here"},
				Trim: func(max int) string { return "" }}},
			drop: []string{ModuleMedia, "text"}, sep: "\n", chars: 18, rows: 9,
			want: "hello", dropped: []string{ModuleMedia},
		},
		{
			name: "trim that is still too long is not trusted",
			lines: []FitLine{plain("text", "hello"), {ID: ModuleMedia, Variants: []string{"♪ A long song title here"},
				Trim: func(max int) string { return "♪ A long song title" }}},
			drop: []string{ModuleMedia, "text"}, sep: "\n", chars: 18, rows: 9,
			want: "hello", dropped: []string{ModuleMedia},
		},
		{
			name:  "too many rows drops by priority without shrinking",
			lines: []FitLine{plain("time", "t"), plain("cpu", "c"), media, plain("text", "x")},
			drop:  DefaultDropOrder, sep: "\n", chars: 143, rows: 3,
			want: "c\n♪ Song — Artist ▰▰▱▱ 1:00/3:00\nx", dropped: []string{"time"},
		},
		{
			name:  "prefix and suffix count",
			lines: []FitLine{plain("time", "3:04"), plain("text", "hi")},
			drop:  DefaultDropOrder, sep: "\n", pre: "» ", suf: " «", chars: 12, rows: 9,
			want: "» hi «", dropped: []string{"time"},
		},
		{
			name:  "suffix survives the ellipsis",
			lines: []FitLine{plain("text", "abcdefghijkl")},
			drop:  DefaultDropOrder, sep: "\n", pre: "[", suf: "]", chars: 8,
			want: "[abcde…]", shrunk: []string{"text"}, cut: true,
		},
		{
			name:  "prefix wider than the limit",
			lines: []FitLine{plain("text", "abc")},
			drop:  DefaultDropOrder, sep: "\n", pre: "123456", suf: "789", chars: 5,
			want: "12345", cut: true,
		},
		{
			name:  "empty lines are skipped",
			lines: []FitLine{plain("time", ""), {ID: "cpu"}, plain("text", "hi")},
			drop:  DefaultDropOrder, sep: "\n", chars: 143, rows: 9,
			want: "hi",
		},
		{
			name:  "ids missing from the drop order go bottom up",
			lines: []FitLine{plain("a", "aaaa"), plain("b", "bbbb"), plain("c", "cccc")},
			drop:  nil, sep: "\n", chars: 9, rows: 9,
			want: "aaaa\nbbbb", dropped: []string{"c"},
		},
		{
			name:  "emoji count as one character",
			lines: []FitLine{plain("time", "♡♡♡"), plain("text", "♡♡♡♡")},
			drop:  DefaultDropOrder, sep: "\n", chars: 8, rows: 9,
			want: "♡♡♡\n♡♡♡♡",
		},
		{
			name:  "one line with too many rows keeps the first ones",
			lines: []FitLine{plain("text", "1\n2\n3\n4")},
			drop:  DefaultDropOrder, sep: "\n", chars: 143, rows: 2,
			want: "1\n2", shrunk: []string{"text"},
		},
		{
			name:  "no limits",
			lines: []FitLine{plain("time", "3:04"), plain("text", "hi")},
			drop:  DefaultDropOrder, sep: " · ",
			want: "3:04 · hi",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FitLines(c.lines, c.drop, c.sep, c.pre, c.suf, c.chars, c.rows)
			if got.Text != c.want {
				t.Errorf("text %q, want %q", got.Text, c.want)
			}
			if c.chars > 0 && utf8.RuneCountInString(got.Text) > c.chars {
				t.Errorf("%d characters, limit %d", utf8.RuneCountInString(got.Text), c.chars)
			}
			if !reflect.DeepEqual(got.Dropped, c.dropped) {
				t.Errorf("dropped %v, want %v", got.Dropped, c.dropped)
			}
			if !reflect.DeepEqual(got.Shrunk, c.shrunk) {
				t.Errorf("shrunk %v, want %v", got.Shrunk, c.shrunk)
			}
			if got.Cut != c.cut {
				t.Errorf("cut %v, want %v", got.Cut, c.cut)
			}
		})
	}
}

func TestFitLinesNothing(t *testing.T) {
	if got := FitLines(nil, DefaultDropOrder, "\n", "", "", 143, 9); got.Text != "" || got.Kept != nil {
		t.Errorf("got %+v", got)
	}
}

// Whatever goes in, what comes out fits.
func TestFitLinesAlwaysFits(t *testing.T) {
	long := strings.Repeat("word ", 40)
	lines := []FitLine{}
	for _, id := range DefaultOrder {
		lines = append(lines, plain(id, id+" "+long))
	}
	for limit := 1; limit < 200; limit += 7 {
		for rows := 1; rows < 10; rows += 4 {
			got := FitLines(lines, DefaultDropOrder, "\n", "> ", " <", limit, rows)
			if n := utf8.RuneCountInString(got.Text); n > limit {
				t.Fatalf("limit %d: %d characters", limit, n)
			}
			if n := strings.Count(got.Text, "\n") + 1; n > rows {
				t.Fatalf("rows %d: %d rows", rows, n)
			}
			if len(got.Kept) != 1 || got.Kept[0] != ModuleText {
				t.Fatalf("limit %d: kept %v, want only the text line", limit, got.Kept)
			}
		}
	}
}

func TestEllipsize(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 7, "hello…"},
		{"a — b — c", 5, "a…"},
		{"hello", 1, "…"},
		{"hello", 0, ""},
		{"♡♡♡♡", 3, "♡♡…"},
	}
	for _, c := range cases {
		if got := ellipsize(c.in, c.n); got != c.want {
			t.Errorf("ellipsize(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestStripFeat(t *testing.T) {
	cases := map[string]string{
		"Song (feat. Someone)":     "Song",
		"Song [ft. A & B]":         "Song",
		"Song (with Someone) Live": "Song Live",
		"Song feat. Someone":       "Song",
		"Song Featuring Someone":   "Song",
		"Feather":                  "Feather",
		"Left (Remix)":             "Left (Remix)",
		"(feat. Only)":             "(feat. Only)",
	}
	for in, want := range cases {
		if got := stripFeat(in); got != want {
			t.Errorf("stripFeat(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"A, B":              "A",
		"A feat. B":         "A",
		"Simon & Garfunkel": "Simon & Garfunkel",
		"":                  "",
	} {
		if got := firstArtist(in); got != want {
			t.Errorf("firstArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMediaFitVariants(t *testing.T) {
	o := DefaultConfig().MediaOptions
	o.ShowAlbum = true
	info := MediaInfo{Available: true, Playing: true, Title: "Song (feat. B)", Artist: "A, C", Album: "LP",
		PositionMS: 60000, DurationMS: 120000}
	line := mediaFit(info, o)
	want := []string{
		"♪ Song (feat. B) — A, C · LP ▰▰▰▰▱▱▱▱ 1:00/2:00",
		"♪ Song (feat. B) — A, C · LP 1:00/2:00",
		"♪ Song (feat. B) — A, C · LP",
		"♪ Song — A",
	}
	if !reflect.DeepEqual(line.Variants, want) {
		t.Fatalf("variants\n%q\nwant\n%q", line.Variants, want)
	}
	if got := line.Trim(10); got != "" {
		t.Errorf("Trim(10) = %q, want a refusal since the short form fits", got)
	}

	line = mediaFit(MediaInfo{Available: true, Playing: true, Title: "A rather long song name", Artist: "X"}, o)
	if got := line.Trim(14); got != "♪ A rather lo…" {
		t.Errorf("Trim(14) = %q", got)
	}
	if got := line.Trim(9); got != "" {
		t.Errorf("Trim(9) = %q, want a refusal", got)
	}
	if got := line.Trim(40); got != "" {
		t.Errorf("Trim(40) = %q, want a refusal since it already fits", got)
	}

	// Nothing to take away: one variant.
	o.ShowProgress, o.ShowAlbum = false, false
	if line := mediaFit(MediaInfo{Available: true, Playing: true, Title: "Song", Artist: "A"}, o); len(line.Variants) != 1 {
		t.Errorf("variants %q", line.Variants)
	}
	if line := mediaFit(MediaInfo{Available: true, Playing: false, Title: "Song"}, o); line.Variants[0] != "" {
		t.Errorf("paused line %q while paused is hidden", line.Variants[0])
	}
}

func TestSanitizeDropOrder(t *testing.T) {
	c := DefaultConfig()
	c.DisplayOptions.DropOrder = []string{"text", "bogus", "text", "time"}
	c.sanitize()
	got := c.DisplayOptions.DropOrder
	if got[0] != "text" || got[1] != "time" || len(got) != len(DefaultOrder) {
		t.Errorf("drop order %v", got)
	}
	c.DisplayOptions.DropOrder = nil
	c.sanitize()
	if !reflect.DeepEqual(c.DisplayOptions.DropOrder, DefaultDropOrder) {
		t.Errorf("empty drop order became %v", c.DisplayOptions.DropOrder)
	}
}
