package modules

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Chatbox motion: per-line effects (scroll, typewriter, blink) and pages
// that take turns showing named groups of lines.
//
// VRChat drops chatbox updates sent faster than about one a second, so an
// animation can only move one step per send. Effects therefore advance in
// frames: the send loop calls AdvanceMotion before every send and a frame
// passes at most once per FrameMS. The panel preview composes the same
// frames, so it shows exactly what VRChat gets.

// Line effects.
const (
	EffectNone       = ""
	EffectScroll     = "scroll"
	EffectTypewriter = "typewriter"
	EffectBlink      = "blink"
)

// Limits of the motion settings.
const (
	MaxChatPages     = 12
	MinMotionFrameMS = 1000
	MaxMotionFrameMS = 5000
	MinPageSeconds   = 3
	MaxPageSeconds   = 600
)

// scrollGap separates the end of a scrolling line from its start.
const scrollGap = "   "

// MotionOptions animates the chatbox.
type MotionOptions struct {
	// FrameMS is how long one animation step stays up. It is also how often
	// the chatbox is sent while something moves.
	FrameMS int                   `json:"frame_ms"`
	Effects map[string]LineEffect `json:"effects"` // by line id; missing ids stand still
	PagesOn bool                  `json:"pages_on"`
	Pages   []ChatPage            `json:"pages"`
}

// LineEffect is how one line moves.
type LineEffect struct {
	Effect string `json:"effect"` // scroll, typewriter or blink; "" for none
	Width  int    `json:"width"`  // scroll: characters shown at once
	Step   int    `json:"step"`   // scroll and typewriter: characters per frame
}

// ChatPage is a named group of lines shown for a while before the next one.
// Lines in no page show on every page.
type ChatPage struct {
	Name    string   `json:"name"`
	Lines   []string `json:"lines"`
	Seconds int      `json:"seconds"`
}

func defaultMotionOptions() MotionOptions {
	return MotionOptions{FrameMS: 1500, Effects: map[string]LineEffect{}, Pages: []ChatPage{}}
}

func (o MotionOptions) clone() MotionOptions {
	effects := make(map[string]LineEffect, len(o.Effects))
	for id, e := range o.Effects {
		effects[id] = e
	}
	o.Effects = effects
	pages := make([]ChatPage, len(o.Pages))
	for i, p := range o.Pages {
		p.Lines = append([]string{}, p.Lines...)
		pages[i] = p
	}
	o.Pages = pages
	return o
}

func (o *MotionOptions) sanitize() {
	if o.FrameMS == 0 {
		o.FrameMS = 1500
	}
	o.FrameMS = clampInt(o.FrameMS, MinMotionFrameMS, MaxMotionFrameMS)
	effects := make(map[string]LineEffect, len(o.Effects))
	for id, e := range o.Effects {
		if !knownModules[id] {
			continue
		}
		switch e.Effect {
		case EffectScroll, EffectTypewriter, EffectBlink:
		default:
			continue
		}
		if e.Width == 0 {
			e.Width = 30
		}
		e.Width = clampInt(e.Width, 8, 140)
		if e.Step == 0 {
			e.Step = 3
		}
		e.Step = clampInt(e.Step, 1, 40)
		effects[id] = e
	}
	o.Effects = effects

	pages := make([]ChatPage, 0, len(o.Pages))
	seen := map[string]bool{}
	for _, p := range o.Pages {
		if len(pages) >= MaxChatPages {
			break
		}
		p.Name = RuneLimit(strings.Join(strings.Fields(p.Name), " "), 24)
		if p.Name == "" {
			p.Name = "Page " + strconv.Itoa(len(pages)+1)
		}
		if key := strings.ToLower(p.Name); seen[key] {
			p.Name = RuneLimit(p.Name, 20) + " " + strconv.Itoa(len(pages)+1)
		}
		seen[strings.ToLower(p.Name)] = true
		lines := make([]string, 0, len(p.Lines))
		for _, id := range p.Lines {
			if knownModules[id] && !slices.Contains(lines, id) {
				lines = append(lines, id)
			}
		}
		p.Lines = lines
		if p.Seconds == 0 {
			p.Seconds = 10
		}
		p.Seconds = clampInt(p.Seconds, MinPageSeconds, MaxPageSeconds)
		pages = append(pages, p)
	}
	o.Pages = pages
}

// pagesActive reports whether pages take turns: switched on, with at least
// one page that has a line.
func (o MotionOptions) pagesActive() bool {
	if !o.PagesOn {
		return false
	}
	for _, p := range o.Pages {
		if len(p.Lines) > 0 {
			return true
		}
	}
	return false
}

// motionRateMS is how often the chatbox has to be sent for the motion to
// show, or 0 when nothing moves.
func motionRateMS(cfg Config) int64 {
	m := cfg.Motion
	if m.pagesActive() {
		return int64(m.FrameMS)
	}
	for id, e := range m.Effects {
		if e.Effect != EffectNone && ModuleEnabled(id, cfg.DisplayOptions) {
			return int64(m.FrameMS)
		}
	}
	return 0
}

type typeState struct {
	text  string
	shown int    // runes revealed
	frame uint64 // frame shown was last advanced on
	seen  uint64 // last frame the line was on screen
}

var (
	motionMu      sync.Mutex
	motionFrame   uint64 = 1
	motionLastAt  time.Time
	motionTyped   = map[string]*typeState{}
	motionPage    int
	motionSince   time.Time
	motionPageSig string
	motionNow     = time.Now
)

// AdvanceMotion moves the animations one frame on, at most once per frame
// length. The send loop calls it before each send; extra calls, such as a
// send woken early by a config change, do not speed anything up.
func AdvanceMotion() {
	rate := time.Duration(GetConfig().Motion.FrameMS) * time.Millisecond
	if rate < MinMotionFrameMS*time.Millisecond {
		rate = MinMotionFrameMS * time.Millisecond
	}
	now := motionNow()
	motionMu.Lock()
	defer motionMu.Unlock()
	// A little slack so a send that wakes a few ms early still advances.
	if !motionLastAt.IsZero() && now.Sub(motionLastAt) < rate-150*time.Millisecond {
		return
	}
	motionLastAt = now
	motionFrame++
}

// MotionFrame is the current animation frame.
func MotionFrame() uint64 {
	motionMu.Lock()
	defer motionMu.Unlock()
	return motionFrame
}

// scrollText shows width runes of text, moved step runes along per frame and
// wrapping around through a short gap. Text that fits is left alone.
func scrollText(text string, width, step int, frame uint64) string {
	runes := []rune(strings.ReplaceAll(text, "\n", " "))
	if width <= 0 || len(runes) <= width {
		return text
	}
	loop := append(runes, []rune(scrollGap)...)
	n := uint64(len(loop))
	off := (frame * uint64(max(step, 1))) % n
	out := make([]rune, width)
	for i := range out {
		out[i] = loop[(off+uint64(i))%n]
	}
	return string(out)
}

// commonPrefix is how many leading runes a and b share.
func commonPrefix(a, b string) int {
	n := 0
	for a != "" && b != "" {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if ra != rb {
			break
		}
		n++
		a, b = a[sa:], b[sb:]
	}
	return n
}

// typewrite reveals text step runes per frame. A line that changes is
// retyped from the first character that differs, so a ticking clock only
// retypes its last digits; a line coming back after being off screen is
// typed from the start. Call with motionMu held.
func typewrite(id, text string, step int, frame uint64) string {
	step = max(step, 1)
	total := utf8.RuneCountInString(text)
	st := motionTyped[id]
	if st == nil || st.seen+1 < frame {
		// One frame back, so this frame already shows the first step.
		st = &typeState{text: text, frame: frame - 1}
		motionTyped[id] = st
	}
	if text != st.text {
		st.shown = min(st.shown, commonPrefix(st.text, text))
		st.text = text
	}
	if frame > st.frame {
		st.shown += step * int(frame-st.frame)
		st.frame = frame
	}
	st.shown = min(st.shown, total)
	st.seen = frame
	n := max(st.shown, min(step, total))
	if n >= total {
		return text
	}
	return strings.TrimRight(RuneLimit(text, n), " ")
}

// applyEffect gives a line this frame's look. It returns false when the line
// is off this frame (a blink).
func applyEffect(line *FitLine, e LineEffect, frame uint64) bool {
	if len(line.Variants) == 0 {
		return true
	}
	full := line.Variants[0]
	switch e.Effect {
	case EffectBlink:
		return frame%2 == 0
	case EffectScroll:
		if utf8.RuneCountInString(full) > e.Width {
			line.Variants = []string{scrollText(full, e.Width, e.Step, frame)}
			line.Trim = nil
		}
	case EffectTypewriter:
		if typed := typewrite(line.ID, full, e.Step, frame); typed != full {
			line.Variants = []string{typed}
			line.Trim = nil
		}
	}
	return true
}

// PageState says which page is up.
type PageState struct {
	Page  string   `json:"page"`  // name of the page showing; "" with pages off
	Index int      `json:"index"` // its position in the list
	Count int      `json:"count"` // pages in the list
	Until int64    `json:"until"` // unix ms the next page is due
	Paged []string `json:"paged"` // lines waiting for another page
}

// pageSig changes whenever the pages are edited, which starts them over.
func pageSig(o MotionOptions) string {
	var b strings.Builder
	for _, p := range o.Pages {
		b.WriteString(p.Name)
		b.WriteByte(0)
		b.WriteString(strings.Join(p.Lines, ","))
		b.WriteByte(1)
	}
	return b.String()
}

// pageLines keeps the lines of the page that is up, plus lines in no page.
// A page whose lines are all empty right now is skipped for the next one, so
// the chatbox never sits on a blank page. Call with motionMu held.
func pageLines(o MotionOptions, lines []FitLine, now time.Time) ([]FitLine, PageState) {
	if !o.pagesActive() {
		return lines, PageState{}
	}
	if sig := pageSig(o); sig != motionPageSig || motionPage >= len(o.Pages) {
		motionPageSig, motionPage, motionSince = sig, 0, now
	}
	if motionSince.IsZero() {
		motionSince = now
	}
	paged := map[string]bool{}
	for _, p := range o.Pages {
		for _, id := range p.Lines {
			paged[id] = true
		}
	}
	has := map[string]bool{}
	for _, l := range lines {
		has[l.ID] = true
	}
	hasContent := func(p ChatPage) bool {
		for _, id := range p.Lines {
			if has[id] {
				return true
			}
		}
		return false
	}
	if now.Sub(motionSince) >= time.Duration(o.Pages[motionPage].Seconds)*time.Second {
		before := motionPage
		for i := 1; i <= len(o.Pages); i++ {
			next := (motionPage + i) % len(o.Pages)
			if hasContent(o.Pages[next]) {
				motionPage = next
				break
			}
		}
		motionSince = now
		if motionPage != before {
			FireTrigger(TriggerPageShown, map[string]string{"page": o.Pages[motionPage].Name, "name": o.Pages[motionPage].Name})
		}
	}
	// The page that is due may have gone blank since; show the next one
	// with something on it without restarting its timer.
	idx := motionPage
	if !hasContent(o.Pages[idx]) {
		for i := 1; i < len(o.Pages); i++ {
			if next := (idx + i) % len(o.Pages); hasContent(o.Pages[next]) {
				idx = next
				break
			}
		}
	}
	page := o.Pages[idx]
	st := PageState{
		Page: page.Name, Index: idx, Count: len(o.Pages),
		Until: motionSince.Add(time.Duration(o.Pages[motionPage].Seconds) * time.Second).UnixMilli(),
	}
	out := make([]FitLine, 0, len(lines))
	for _, l := range lines {
		if !paged[l.ID] || slices.Contains(page.Lines, l.ID) {
			out = append(out, l)
		} else {
			st.Paged = append(st.Paged, l.ID)
		}
	}
	return out, st
}

// applyMotion picks the page and gives each line its effect for this frame.
func applyMotion(o MotionOptions, lines []FitLine) ([]FitLine, PageState) {
	motionMu.Lock()
	defer motionMu.Unlock()
	lines, page := pageLines(o, lines, motionNow())
	if len(o.Effects) == 0 {
		return lines, page
	}
	out := lines[:0:0]
	for _, l := range lines {
		if e, ok := o.Effects[l.ID]; ok && e.Effect != EffectNone {
			l.Variants = append([]string{}, l.Variants...)
			if !applyEffect(&l, e, motionFrame) {
				continue
			}
		}
		out = append(out, l)
	}
	return out, page
}

// ShowPage jumps to the page called name (any case) and restarts its timer.
// "" or "next" moves to the next page.
func ShowPage(name string) bool {
	o := GetConfig().Motion
	if len(o.Pages) == 0 {
		return false
	}
	motionMu.Lock()
	if sig := pageSig(o); sig != motionPageSig || motionPage >= len(o.Pages) {
		motionPageSig, motionPage = sig, 0
	}
	idx := -1
	if name == "" || strings.EqualFold(name, "next") {
		idx = (motionPage + 1) % len(o.Pages)
	} else {
		for i, p := range o.Pages {
			if strings.EqualFold(p.Name, strings.TrimSpace(name)) {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		motionMu.Unlock()
		return false
	}
	motionPage, motionSince = idx, motionNow()
	page := o.Pages[idx].Name
	motionMu.Unlock()
	FireTrigger(TriggerPageShown, map[string]string{"page": page, "name": page})
	NotifyChange()
	return true
}

// resetMotion clears the animation state, for tests.
func resetMotion() {
	motionMu.Lock()
	motionFrame, motionLastAt = 1, time.Time{}
	motionTyped = map[string]*typeState{}
	motionPage, motionSince, motionPageSig = 0, time.Time{}, ""
	motionMu.Unlock()
}
