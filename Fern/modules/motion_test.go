package modules

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMotionDefaultsAndSanitize(t *testing.T) {
	d := DefaultConfig()
	if d.Motion.FrameMS != 1500 || d.Motion.Effects == nil || d.Motion.Pages == nil || d.Motion.PagesOn {
		t.Fatalf("defaults %+v", d.Motion)
	}
	if motionRateMS(d) != 0 {
		t.Error("nothing moves by default, so the send rate should not change")
	}

	c := DefaultConfig()
	c.Motion = MotionOptions{
		FrameMS: 200,
		Effects: map[string]LineEffect{
			ModuleText:   {Effect: EffectScroll, Width: 999, Step: 0},
			ModuleTime:   {Effect: "spin"},
			"bogus":      {Effect: EffectBlink},
			ModuleMemory: {Effect: EffectTypewriter, Step: 100},
		},
	}
	for i := 0; i < MaxChatPages+3; i++ {
		c.Motion.Pages = append(c.Motion.Pages, ChatPage{Name: "  Music  ", Lines: []string{ModuleMedia, "nope", ModuleMedia}, Seconds: 1})
	}
	c.sanitize()
	m := c.Motion
	if m.FrameMS != MinMotionFrameMS {
		t.Errorf("frame %d", m.FrameMS)
	}
	if len(m.Effects) != 2 {
		t.Fatalf("effects %+v", m.Effects)
	}
	if e := m.Effects[ModuleText]; e.Width != 140 || e.Step != 3 {
		t.Errorf("scroll %+v", e)
	}
	if e := m.Effects[ModuleMemory]; e.Step != 40 {
		t.Errorf("typewriter %+v", e)
	}
	if len(m.Pages) != MaxChatPages {
		t.Fatalf("%d pages", len(m.Pages))
	}
	names := map[string]bool{}
	for _, p := range m.Pages {
		if names[strings.ToLower(p.Name)] {
			t.Errorf("duplicate page name %q", p.Name)
		}
		names[strings.ToLower(p.Name)] = true
		if !reflect.DeepEqual(p.Lines, []string{ModuleMedia}) || p.Seconds != MinPageSeconds {
			t.Errorf("page %+v", p)
		}
	}
	if m.Pages[0].Name != "Music" {
		t.Errorf("name %q", m.Pages[0].Name)
	}
}

func TestMotionRateFollowsFrames(t *testing.T) {
	c := DefaultConfig()
	c.TimeoutMS = 5000
	c.DisplayOptions.Loading = false
	c.Motion.Effects = map[string]LineEffect{ModuleMemory: {Effect: EffectBlink}}
	c.DisplayOptions.Memory = false
	if got := EffectiveRateMS(c); got != 5000 {
		t.Errorf("an effect on a line that is off changed the rate: %d", got)
	}
	c.DisplayOptions.Memory = true
	if got := EffectiveRateMS(c); got != 1500 {
		t.Errorf("rate %d, want the frame length", got)
	}
	c.Motion.Effects = nil
	c.Motion.PagesOn = true
	c.Motion.Pages = []ChatPage{{Name: "A"}}
	if got := EffectiveRateMS(c); got != 5000 {
		t.Errorf("an empty page changed the rate: %d", got)
	}
	c.Motion.Pages[0].Lines = []string{ModuleTime}
	if got := EffectiveRateMS(c); got != 1500 {
		t.Errorf("pages rate %d", got)
	}
}

func TestScrollText(t *testing.T) {
	if got := scrollText("short", 10, 2, 7); got != "short" {
		t.Errorf("text that fits moved: %q", got)
	}
	text := "abcdefghij"
	if got := scrollText(text, 4, 1, 0); got != "abcd" {
		t.Errorf("frame 0 %q", got)
	}
	if got := scrollText(text, 4, 3, 1); got != "defg" {
		t.Errorf("frame 1 %q", got)
	}
	// Wraps through the gap back to the start.
	if got := scrollText(text, 6, 1, 8); got != "ij   a" {
		t.Errorf("wrap %q", got)
	}
	loop := uint64(len(text) + len(scrollGap))
	if scrollText(text, 4, 1, loop) != scrollText(text, 4, 1, 0) {
		t.Error("a full loop should come back to the start")
	}
	if got := scrollText("ab\ncdefgh", 4, 1, 1); strings.Contains(got, "\n") || utf8.RuneCountInString(got) != 4 {
		t.Errorf("multi-line %q", got)
	}
	if got := scrollText("♪♫♪♫♪♫♪♫", 3, 1, 1); got != "♫♪♫" {
		t.Errorf("runes %q", got)
	}
}

func TestTypewriter(t *testing.T) {
	resetMotion()
	defer resetMotion()
	text := "hello world"
	if got := typewrite("t", text, 3, 1); got != "hel" {
		t.Fatalf("first frame %q", got)
	}
	if got := typewrite("t", text, 3, 1); got != "hel" {
		t.Errorf("same frame moved on: %q", got)
	}
	if got := typewrite("t", text, 3, 2); got != "hello" {
		t.Errorf("second frame %q", got)
	}
	if got := typewrite("t", text, 3, 3); got != "hello wor" {
		t.Errorf("third frame %q", got)
	}
	if got := typewrite("t", text, 3, 4); got != text {
		t.Errorf("done %q", got)
	}
	// A clock ticking over retypes only from the digit that changed.
	if got := typewrite("t", "hello wxyzq", 3, 4); got != "hello w" {
		t.Errorf("changed same frame %q", got)
	}
	if got := typewrite("t", "hello wxyzq", 3, 5); got != "hello wxyz" {
		t.Errorf("changed next frame %q", got)
	}
	// Off screen for a frame or more: typed from the start again.
	if got := typewrite("t", "hello wxyzq", 3, 9); got != "hel" {
		t.Errorf("came back %q", got)
	}
	if got := typewrite("x", "ab", 5, 1); got != "ab" {
		t.Errorf("short %q", got)
	}
}

func TestBlinkAndEffectsKeepFit(t *testing.T) {
	resetMotion()
	defer resetMotion()
	o := MotionOptions{FrameMS: 1500, Effects: map[string]LineEffect{
		"a": {Effect: EffectBlink},
		"b": {Effect: EffectScroll, Width: 5, Step: 2},
	}}
	lines := []FitLine{
		{ID: "a", Variants: []string{"blinky"}},
		{ID: "b", Variants: []string{"a long scrolling line", "short"}, Trim: func(int) string { return "x" }},
		{ID: "c", Variants: []string{"still"}},
	}
	motionFrame = 2
	out, _ := applyMotion(o, lines)
	if len(out) != 3 || out[1].Variants[0] != "ng sc" || len(out[1].Variants) != 1 || out[1].Trim != nil {
		t.Fatalf("frame 2 %+v", out)
	}
	if lines[1].Variants[0] != "a long scrolling line" {
		t.Error("the effect changed the caller's line")
	}
	motionFrame = 3
	out, _ = applyMotion(o, lines)
	if len(out) != 2 || out[0].ID != "b" {
		t.Fatalf("blink off frame %+v", out)
	}
	fit := FitLines(out, nil, "\n", "", "", 143, 9)
	if fit.Text != " scro\nstill" {
		t.Errorf("fit %q", fit.Text)
	}
}

func TestAdvanceMotionPacing(t *testing.T) {
	resetMotion()
	defer resetMotion()
	now := time.Unix(1000, 0)
	motionNow = func() time.Time { return now }
	defer func() { motionNow = time.Now }()
	AdvanceMotion()
	f := MotionFrame()
	AdvanceMotion()
	if MotionFrame() != f {
		t.Error("a second call in the same frame advanced it")
	}
	now = now.Add(time.Duration(max(GetConfig().Motion.FrameMS, MinMotionFrameMS)-100) * time.Millisecond)
	AdvanceMotion()
	if MotionFrame() != f+1 {
		t.Error("a send just under a frame later should advance")
	}
}

func TestPagesCycle(t *testing.T) {
	resetMotion()
	defer resetMotion()
	o := MotionOptions{PagesOn: true, Pages: []ChatPage{
		{Name: "Music", Lines: []string{"media"}, Seconds: 10},
		{Name: "Stats", Lines: []string{"cpu", "memory"}, Seconds: 5},
		{Name: "Empty", Lines: []string{"gpu"}, Seconds: 5},
	}}
	lines := []FitLine{
		{ID: "time", Variants: []string{"12:00"}},
		{ID: "media", Variants: []string{"song"}},
		{ID: "cpu", Variants: []string{"cpu 5%"}},
		{ID: "memory", Variants: []string{"mem 2GB"}},
	}
	ids := func(ls []FitLine) string {
		var out []string
		for _, l := range ls {
			out = append(out, l.ID)
		}
		return strings.Join(out, ",")
	}
	start := time.Unix(5000, 0)
	out, st := pageLines(o, lines, start)
	if ids(out) != "time,media" || st.Page != "Music" || st.Index != 0 || st.Count != 3 || !reflect.DeepEqual(st.Paged, []string{"cpu", "memory"}) {
		t.Fatalf("first %s %+v", ids(out), st)
	}
	if st.Until != start.Add(10*time.Second).UnixMilli() {
		t.Errorf("until %d", st.Until)
	}
	out, st = pageLines(o, lines, start.Add(9*time.Second))
	if st.Page != "Music" {
		t.Errorf("moved on early: %s", st.Page)
	}
	out, st = pageLines(o, lines, start.Add(10*time.Second))
	if ids(out) != "time,cpu,memory" || st.Page != "Stats" {
		t.Fatalf("second %s %+v", ids(out), st)
	}
	// Empty has nothing to show, so it is skipped back to Music.
	_, st = pageLines(o, lines, start.Add(15*time.Second))
	if st.Page != "Music" {
		t.Errorf("after stats %+v", st)
	}
	// Editing the pages starts them over.
	o.Pages[0].Lines = []string{"media", "time"}
	_, st = pageLines(o, lines, start.Add(16*time.Second))
	if st.Index != 0 || st.Until != start.Add(26*time.Second).UnixMilli() {
		t.Errorf("after edit %+v", st)
	}
	o.PagesOn = false
	if out, st := pageLines(o, lines, start); len(out) != len(lines) || st.Page != "" {
		t.Error("pages off should change nothing")
	}
}

func TestPageGoneBlankShowsNext(t *testing.T) {
	resetMotion()
	defer resetMotion()
	o := MotionOptions{PagesOn: true, Pages: []ChatPage{
		{Name: "A", Lines: []string{"media"}, Seconds: 10},
		{Name: "B", Lines: []string{"cpu"}, Seconds: 10},
	}}
	now := time.Unix(100, 0)
	pageLines(o, []FitLine{{ID: "media", Variants: []string{"x"}}, {ID: "cpu", Variants: []string{"y"}}}, now)
	out, st := pageLines(o, []FitLine{{ID: "cpu", Variants: []string{"y"}}}, now.Add(time.Second))
	if st.Page != "B" || len(out) != 1 {
		t.Errorf("%+v %+v", st, out)
	}
}
