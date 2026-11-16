package modules

import (
	"encoding/json"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	loadingStep   atomic.Int32
	loadingLastAt atomic.Int64 // unix nano of the last step
)

// AdvanceLoading moves the dots on by one, but never faster than paceMS. Config
// and media changes wake the send loop early, and advancing on every wake raced
// through several dots between two looks at the screen, so the line appeared to
// jump from ".." to "......". The pace gate makes one dot mean one update at the
// rate the chatbox is really being refreshed at.
func AdvanceLoading(paceMS int) {
	if paceMS < 400 {
		paceMS = 400
	}
	now := time.Now().UnixNano()
	last := loadingLastAt.Load()
	if last != 0 && now-last < int64(paceMS)*int64(time.Millisecond) {
		return
	}
	if loadingLastAt.CompareAndSwap(last, now) {
		loadingStep.Add(1)
	}
}

// Loading renders the loading animation: by default a growing run of dots,
// ".", "..", "..." and so on, looping back to a single dot after the
// configured longest run. An animation text replaces the dots and is typed
// out a character at a time instead, so "test" reads "t", "te", "tes",
// "test". A custom loading text is shown in front, so "brb" reads "brb.",
// "brb.." and so on; with the animation off the line is just that text.
func Loading() string {
	o := GetConfig().LoadingOptions
	text := strings.TrimRight(strings.TrimSpace(o.Text), ".")
	if !o.ShowDots {
		return text
	}
	frames := loadingFrames(o)
	return text + frames[int(loadingStep.Load())%len(frames)]
}

// Loading animation styles.
const (
	loadingRestart = "restart" // type out, then start again from one character
	loadingBounce  = "bounce"  // type out, then erase back to one character
)

// loadingFrames lists every step of the animation once, in order. There is
// always at least one frame.
func loadingFrames(o LoadingOptions) []string {
	units := textUnits(o.Anim)
	if len(units) == 0 {
		// sanitize keeps Dots between 1 and 6; the guard is for raw values.
		units = textUnits(strings.Repeat(".", max(o.Dots, 1)))
	}
	n := len(units)
	frame := func(k int) string {
		return strings.Join(units[:k], "") + strings.Repeat(o.Fill, n-k)
	}
	frames := make([]string, 0, 2*n+o.Hold)
	for k := 1; k <= n; k++ {
		frames = append(frames, frame(k))
	}
	for i := 0; i < o.Hold; i++ {
		frames = append(frames, frame(n))
	}
	if o.Style == loadingBounce {
		for k := n - 1; k >= 2; k-- {
			frames = append(frames, frame(k))
		}
	}
	return frames
}

// textUnits splits s into the characters a reader sees, so an animation never
// shows half of one. It is a close approximation of grapheme clusters, not
// the full Unicode rules: combining marks, variation selectors, skin tones,
// emoji tags and keycaps stay with the character before them, a zero width
// joiner glues the next character on, and flags stay in pairs.
func textUnits(s string) []string {
	var units []string
	var cur []rune
	join := false // the last rune was a zero width joiner
	for _, r := range s {
		attach := len(cur) > 0 && (join || extendsUnit(r) ||
			(isRegionalIndicator(r) && len(cur) == 1 && isRegionalIndicator(cur[0])))
		if !attach && len(cur) > 0 {
			units = append(units, string(cur))
			cur = cur[:0]
		}
		cur = append(cur, r)
		join = r == '\u200d'
	}
	if len(cur) > 0 {
		units = append(units, string(cur))
	}
	return units
}

// extendsUnit reports whether r belongs to the character before it.
func extendsUnit(r rune) bool {
	switch {
	case r == '\u200d', // zero width joiner
		r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
		r >= 0xE0100 && r <= 0xE01EF, // more variation selectors
		r >= 0x1F3FB && r <= 0x1F3FF, // skin tones
		r >= 0xE0020 && r <= 0xE007F: // emoji tags
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
}

func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// unitLimit shortens s to whole characters of at most n runes in total.
func unitLimit(s string, n int) string {
	var b strings.Builder
	used := 0
	for _, u := range textUnits(s) {
		c := utf8.RuneCountInString(u)
		if used+c > n {
			break
		}
		used += c
		b.WriteString(u)
	}
	return b.String()
}

// sanitizeLoading keeps the loading options in range. Old configs have no
// animation text, fill, style or hold, which gives exactly the old dots.
func sanitizeLoading(o *LoadingOptions) {
	// VRChat accepts roughly one chatbox update per second and silently drops
	// the rest, so sending faster does not make the animation smoother: it makes
	// the dots skip. One dot per second is the pace VRChat can actually show.
	o.RateMS = clampInt(o.RateMS, 1000, 3000)
	// The loading text is one chatbox line: no newlines, no runaway length.
	trimmed := strings.Join(strings.Fields(o.Text), " ")
	o.Text = RuneLimit(strings.TrimRight(trimmed, "."), 40)
	if o.Dots == 0 {
		o.Dots = 6
	}
	o.Dots = clampInt(o.Dots, 1, 6)
	o.Anim = unitLimit(strings.Join(strings.Fields(o.Anim), " "), 40)
	// The fill is one character, spaces included: a blank is the point.
	// Line breaks and other control characters are dropped.
	fill := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, o.Fill)
	if units := textUnits(fill); len(units) > 0 && utf8.RuneCountInString(units[0]) <= 8 {
		o.Fill = units[0]
	} else {
		o.Fill = ""
	}
	if o.Style != loadingBounce {
		o.Style = loadingRestart
	}
	o.Hold = clampInt(o.Hold, 0, 5)
}

// TextMessage is one custom message. On is the heart: only messages that are
// on take part in the rotation. Group is optional; the rotation can be
// limited to one group.
type TextMessage struct {
	Text  string `json:"text"`
	On    bool   `json:"on"`
	Group string `json:"group,omitempty"`
}

// UnmarshalJSON also reads the old format, where each message was a plain
// string, as a message that is on and in no group. A message object without
// "on" is on as well.
func (m *TextMessage) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*m = TextMessage{Text: text, On: true}
		return nil
	}
	type plain TextMessage
	v := plain{On: true}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*m = TextMessage(v)
	return nil
}

// Messages builds a list of messages that are all on, for defaults and tests.
func Messages(texts ...string) []TextMessage {
	out := make([]TextMessage, len(texts))
	for i, t := range texts {
		out[i] = TextMessage{Text: t, On: true}
	}
	return out
}

const (
	maxMessages   = 200
	maxGroups     = 24
	maxGroupRunes = 24
	maxIcons      = 40
)

// sanitizeTexts keeps messages, groups and icons within bounds. Messages keep
// exactly what was typed, blanks included: editing a message briefly empties
// it, and dropping blank entries here made rows vanish mid-edit and shifted
// every index after them. Blank entries are skipped when rendering.
func sanitizeTexts(c *Config) {
	if c.TextMessages != nil {
		if len(c.TextMessages) > maxMessages {
			c.TextMessages = c.TextMessages[:maxMessages]
		}
		for i := range c.TextMessages {
			m := &c.TextMessages[i]
			m.Text = RuneLimit(m.Text, 144)
			m.Group = RuneLimit(strings.TrimSpace(m.Group), maxGroupRunes)
		}
	}

	// Groups are kept as their own list so an empty group survives, and any
	// group a message names is added to it.
	o := &c.TextOptions
	seen := map[string]bool{}
	groups := make([]string, 0, len(o.Groups))
	add := func(g string) {
		g = RuneLimit(strings.TrimSpace(g), maxGroupRunes)
		if g == "" || seen[strings.ToLower(g)] || len(groups) >= maxGroups {
			return
		}
		seen[strings.ToLower(g)] = true
		groups = append(groups, g)
	}
	for _, g := range o.Groups {
		add(g)
	}
	for _, m := range c.TextMessages {
		add(m.Group)
	}
	o.Groups = groups
	o.Group = strings.TrimSpace(o.Group)
	if !seen[strings.ToLower(o.Group)] {
		o.Group = ""
	}

	icons := make([]string, 0, len(o.Icons))
	for _, icon := range o.Icons {
		icon = RuneLimit(strings.TrimSpace(icon), 8)
		if icon != "" && len(icons) < maxIcons {
			icons = append(icons, icon)
		}
	}
	o.Icons = icons
}

// inRotation lists the positions of the messages the rotation may show:
// hearted, not blank, and in the chosen group when one is set.
func inRotation(cfg Config) []int {
	var out []int
	for i, m := range cfg.TextMessages {
		if !m.On || strings.TrimSpace(m.Text) == "" {
			continue
		}
		if cfg.TextOptions.Group != "" && !strings.EqualFold(m.Group, cfg.TextOptions.Group) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// rotation remembers which message is up, when each one was last shown (for
// the weighted order) and which icon goes with it.
type rotation struct {
	mu        sync.Mutex
	epoch     time.Time
	step      int64 // time step the current message was picked in
	index     int   // position in the message list, or -1
	text      string
	changes   int // message changes so far, for the icon rotation
	iconPick  int // random icon position, when icons are shuffled
	lastShown map[string]time.Time
	perm      []int
	permPos   int
	permSig   string
	rng       *rand.Rand
}

func newRotation(now time.Time, seed int64) *rotation {
	return &rotation{
		epoch:     now,
		step:      -1,
		index:     -1,
		lastShown: map[string]time.Time{},
		rng:       rand.New(rand.NewSource(seed)),
	}
}

var textRotation = newRotation(time.Now(), time.Now().UnixNano())

func rotationSpeed(cfg Config) time.Duration {
	return time.Duration(cfg.TextOptions.Speed) * time.Millisecond
}

// current returns the position of the message to show and its icon, moving
// the rotation on when its time step has passed. The step is worked out from
// the clock, so asking twice in one step gives the same message.
func (r *rotation) current(cfg Config, now time.Time) (int, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pool := inRotation(cfg)
	if len(pool) == 0 {
		r.index, r.text = -1, ""
		return -1, ""
	}
	// Editing the message on screen keeps it there; it only moves on when
	// it leaves the rotation (unhearted, emptied, other group) or time is up.
	valid := false
	for _, i := range pool {
		if i == r.index {
			valid = true
			r.text = cfg.TextMessages[i].Text
			break
		}
	}
	if !cfg.TextOptions.Cycle {
		if r.index != pool[0] {
			r.show(cfg, pool[0], now, false)
		}
		return r.index, r.icon(cfg)
	}
	step := int64(0)
	if speed := rotationSpeed(cfg); speed > 0 {
		step = int64(now.Sub(r.epoch) / speed)
	}
	if !valid || step != r.step {
		// A new step moves the icons on even when only one message is in
		// the rotation, so a lone message still gets a fresh icon each time.
		r.show(cfg, r.next(cfg, pool, now), now, step != r.step)
		r.step = step
	}
	return r.index, r.icon(cfg)
}

func (r *rotation) show(cfg Config, index int, now time.Time, newStep bool) {
	if index != r.index || newStep {
		r.changes++
		if n := len(cfg.TextOptions.Icons); n > 1 {
			r.iconPick = (r.iconPick + 1 + r.rng.Intn(n-1)) % n
		}
	}
	r.index = index
	r.text = cfg.TextMessages[index].Text
	r.lastShown[r.text] = now
}

// next picks the message after the current one: the next in the list, the
// next in a shuffled pass over the list, or a random one weighted by how long
// each has gone unseen.
func (r *rotation) next(cfg Config, pool []int, now time.Time) int {
	if len(pool) == 1 {
		return pool[0]
	}
	switch {
	case cfg.TextOptions.Shuffle && cfg.TextOptions.Weighted:
		return r.weighted(cfg, pool, now)
	case cfg.TextOptions.Shuffle:
		return r.shuffled(pool)
	}
	for _, i := range pool {
		if i > r.index {
			return i
		}
	}
	return pool[0]
}

func (r *rotation) shuffled(pool []int) int {
	var sig strings.Builder
	for _, i := range pool {
		sig.WriteString(strconv.Itoa(i))
		sig.WriteByte(',')
	}
	if sig.String() != r.permSig || r.permPos >= len(r.perm) {
		r.permSig = sig.String()
		r.perm = make([]int, len(pool))
		for k, j := range r.rng.Perm(len(pool)) {
			r.perm[k] = pool[j]
		}
		// A new pass must not start with the message that just ended the last.
		if r.perm[0] == r.index {
			r.perm[0], r.perm[len(r.perm)-1] = r.perm[len(r.perm)-1], r.perm[0]
		}
		r.permPos = 0
	}
	pick := r.perm[r.permPos]
	r.permPos++
	return pick
}

// neverShown is how long a message that has not been shown yet counts as
// unseen, so new messages come up soon without always coming up first.
const neverShown = time.Hour

func (r *rotation) weighted(cfg Config, pool []int, now time.Time) int {
	weights := make([]float64, len(pool))
	total := 0.0
	for k, i := range pool {
		if i == r.index {
			continue // never the same message twice in a row
		}
		unseen := neverShown
		if at, ok := r.lastShown[cfg.TextMessages[i].Text]; ok {
			unseen = now.Sub(at)
		}
		w := unseen.Seconds() + 1
		if w < 1 {
			w = 1
		}
		weights[k] = w
		total += w
	}
	x := r.rng.Float64() * total
	for k, w := range weights {
		if w == 0 {
			continue
		}
		if x < w {
			return pool[k]
		}
		x -= w
	}
	for k := len(pool) - 1; k >= 0; k-- {
		if weights[k] > 0 {
			return pool[k]
		}
	}
	return pool[0]
}

// icon is the rotating prefix for the current message: the next icon each
// time the rotation moves on, in list order or shuffled.
func (r *rotation) icon(cfg Config) string {
	icons := cfg.TextOptions.Icons
	if len(icons) == 0 {
		return ""
	}
	if cfg.TextOptions.IconsRandom {
		return icons[r.iconPick%len(icons)]
	}
	return icons[(r.changes-1+len(icons))%len(icons)]
}

func (r *rotation) skip() {
	r.mu.Lock()
	r.epoch = r.epoch.Add(-rotationSpeed(GetConfig()))
	r.mu.Unlock()
}

// CurrentMessage returns the message that should be shown right now, with its
// rotating icon in front when icons are set.
func CurrentMessage() string {
	cfg := GetConfig()
	index, icon := textRotation.current(cfg, time.Now())
	if index < 0 {
		return ""
	}
	return withLabel(icon, strings.TrimSpace(cfg.TextMessages[index].Text))
}

// MessageIndex is the zero based position of the message being shown, or -1
// when no message is in the rotation.
func MessageIndex() int {
	cfg := GetConfig()
	index, _ := textRotation.current(cfg, time.Now())
	return index
}

// SkipMessage advances to the next custom message immediately.
func SkipMessage() {
	textRotation.skip()
	NotifyChange()
}
