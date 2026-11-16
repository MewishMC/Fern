package modules

import (
	"encoding/json"
	"slices"
	"testing"
)

// loadingOpts is a sanitized LoadingOptions built from the defaults.
func loadingOpts(edit func(*LoadingOptions)) LoadingOptions {
	o := DefaultConfig().LoadingOptions
	edit(&o)
	sanitizeLoading(&o)
	return o
}

func TestLoadingFramesDefaultDots(t *testing.T) {
	o := loadingOpts(func(*LoadingOptions) {})
	want := []string{".", "..", "...", "....", ".....", "......"}
	if got := loadingFrames(o); !slices.Equal(got, want) {
		t.Errorf("default frames = %q, want %q", got, want)
	}
	o = loadingOpts(func(o *LoadingOptions) { o.Dots = 3 })
	if got := loadingFrames(o); !slices.Equal(got, []string{".", "..", "..."}) {
		t.Errorf("three dots = %q", got)
	}
}

// A config saved before the animation options existed has none of their
// keys, and must still load to the same dots it always showed.
func TestLoadingOldConfigKeepsDots(t *testing.T) {
	c := DefaultConfig()
	old := `{"LoadingOptions":{"fast":true,"rate_ms":1000,"text":"brb","show_dots":true,"dots":3}}`
	if err := json.Unmarshal([]byte(old), &c); err != nil {
		t.Fatal(err)
	}
	c.sanitize()
	o := c.LoadingOptions
	if o.Anim != "" || o.Fill != "" || o.Style != loadingRestart || o.Hold != 0 {
		t.Fatalf("old config got new options: %+v", o)
	}
	if got := loadingFrames(o); !slices.Equal(got, []string{".", "..", "..."}) {
		t.Errorf("old config frames = %q", got)
	}
	// A config with every loading key missing, as from an old version.
	c = DefaultConfig()
	if err := json.Unmarshal([]byte(`{"LoadingOptions":{}}`), &c); err != nil {
		t.Fatal(err)
	}
	c.sanitize()
	if got := loadingFrames(c.LoadingOptions); len(got) != 6 || got[5] != "......" {
		t.Errorf("empty loading options frames = %q", got)
	}
}

func TestLoadingFramesText(t *testing.T) {
	o := loadingOpts(func(o *LoadingOptions) { o.Anim = "test" })
	if got, want := loadingFrames(o), []string{"t", "te", "tes", "test"}; !slices.Equal(got, want) {
		t.Errorf("restart = %q, want %q", got, want)
	}
	o.Fill = "_"
	if got, want := loadingFrames(o), []string{"t___", "te__", "tes_", "test"}; !slices.Equal(got, want) {
		t.Errorf("fill = %q, want %q", got, want)
	}
	o.Fill, o.Style, o.Hold = "", loadingBounce, 2
	if got, want := loadingFrames(o), []string{"t", "te", "tes", "test", "test", "test", "tes", "te"}; !slices.Equal(got, want) {
		t.Errorf("bounce with hold = %q, want %q", got, want)
	}
	o.Anim, o.Hold = "x", 0
	if got := loadingFrames(o); !slices.Equal(got, []string{"x"}) {
		t.Errorf("one character bounce = %q", got)
	}
}

func TestLoadingFramesKeepEmojiWhole(t *testing.T) {
	o := loadingOpts(func(o *LoadingOptions) {
		o.Anim = "a👍🏽🇯🇵👨‍👩‍👧é"
		o.Fill = "⠀"
	})
	want := []string{
		"a⠀⠀⠀⠀",
		"a👍🏽⠀⠀⠀",
		"a👍🏽🇯🇵⠀⠀",
		"a👍🏽🇯🇵👨‍👩‍👧⠀",
		"a👍🏽🇯🇵👨‍👩‍👧é",
	}
	if got := loadingFrames(o); !slices.Equal(got, want) {
		t.Errorf("emoji frames = %q, want %q", got, want)
	}
}

func TestSanitizeLoadingAnimation(t *testing.T) {
	o := loadingOpts(func(o *LoadingOptions) {
		o.Anim = "  lo\nad  ing  "
		o.Fill = "\r\n__"
		o.Style = "sideways"
		o.Hold = 99
	})
	if o.Anim != "lo ad ing" || o.Fill != "_" || o.Style != loadingRestart || o.Hold != 5 {
		t.Errorf("sanitized = %+v", o)
	}
	o = loadingOpts(func(o *LoadingOptions) { o.Fill = " "; o.Hold = -3; o.Style = loadingBounce })
	if o.Fill != " " || o.Hold != 0 || o.Style != loadingBounce {
		t.Errorf("a space fill, bounce and hold 0 must stay: %+v", o)
	}
	// 40 runes at most, cut between characters, never inside one.
	long := ""
	for range 20 {
		long += "👍🏽"
	}
	o = loadingOpts(func(o *LoadingOptions) { o.Anim = long + "x" })
	if want := long; o.Anim != want {
		t.Errorf("long anim kept %d units", len(textUnits(o.Anim)))
	}
	o = loadingOpts(func(o *LoadingOptions) { o.Anim = "a" + long })
	if got := textUnits(o.Anim); len(got) != 20 || got[19] != "👍🏽" {
		t.Errorf("cut inside an emoji: %q", got)
	}
}
