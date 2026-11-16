package modules

// The counter line: counters that go up when an avatar parameter turns on
// (a headpat contact, a boop) or changes, or by hand. Each keeps a session
// count, since Fern started, and an all-time total that is saved to
// counters.json next to config.json, so the panel's config pushes never
// carry stale numbers back.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Ways a counter counts its parameter.
const (
	CounterRising = "rising" // each time the value goes above the threshold
	CounterChange = "change" // each time the value changes
)

// MaxCounters is how many counters there can be.
const MaxCounters = 10

// CounterOptions controls the counter line.
type CounterOptions struct {
	Counters  []CounterDef `json:"counters"`
	Separator string       `json:"separator"` // between counters on the line
}

// CounterDef is one counter.
type CounterDef struct {
	ID        string  `json:"id"` // stable; the saved total belongs to it
	Name      string  `json:"name"`
	Param     string  `json:"param"`     // avatar parameter; empty counts by hand only
	Mode      string  `json:"mode"`      // rising or change
	Threshold float64 `json:"threshold"` // rising: counts when the value goes above this
	Cooldown  float64 `json:"cooldown"`  // seconds after a count when the parameter is ignored
	// Template is the counter's part of the line: {name}, {session}, {total}.
	Template string `json:"template"`
	Show     bool   `json:"show"` // on the line at all
	// OnlyAfterChange shows the counter for ShowSec seconds after it goes up
	// or down, then hides it until the next change.
	OnlyAfterChange bool   `json:"only_after_change"`
	ShowSec         int    `json:"show_sec"`
	Hotkey          string `json:"hotkey"` // adds one from any app
}

func defaultCounterOptions() CounterOptions {
	return CounterOptions{Counters: []CounterDef{}, Separator: " | "}
}

// DefaultCounterTemplate is a new counter's template.
const DefaultCounterTemplate = "{name}: {session} ({total})"

func (o CounterOptions) clone() CounterOptions {
	o.Counters = append([]CounterDef{}, o.Counters...)
	return o
}

// newTallyID is a short random ID for a counter or scheduled message.
func newTallyID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// cleanTallyID keeps IDs short and simple, and gives a missing or repeated
// one a fresh ID.
func cleanTallyID(id string, seen map[string]bool) string {
	id = strings.TrimSpace(id)
	ok := id != "" && len(id) <= 32
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			ok = false
			break
		}
	}
	for !ok || seen[id] {
		id, ok = newTallyID(), true
	}
	seen[id] = true
	return id
}

func (o *CounterOptions) sanitize() {
	if len(o.Counters) > MaxCounters {
		o.Counters = o.Counters[:MaxCounters]
	}
	seen := map[string]bool{}
	list := make([]CounterDef, 0, len(o.Counters))
	for _, c := range o.Counters {
		c.ID = cleanTallyID(c.ID, seen)
		c.Name = oneLine(c.Name, 24)
		c.Param = tallyParam(c.Param)
		if c.Mode != CounterChange {
			c.Mode = CounterRising
		}
		if math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) {
			c.Threshold = 0
		}
		c.Threshold = math.Max(-1, math.Min(255, c.Threshold))
		if math.IsNaN(c.Cooldown) || c.Cooldown < 0 {
			c.Cooldown = 0
		}
		c.Cooldown = math.Min(3600, math.Round(c.Cooldown*10)/10)
		c.Template = oneLine(c.Template, 80)
		if c.Template == "" {
			c.Template = DefaultCounterTemplate
		}
		if c.ShowSec <= 0 {
			c.ShowSec = 15
		}
		c.ShowSec = clampInt(c.ShowSec, 1, 3600)
		c.Hotkey = canonicalHotkey(c.Hotkey)
		list = append(list, c)
	}
	o.Counters = list
	o.Separator = RuneLimit(strings.NewReplacer("\r", "", "\n", "").Replace(o.Separator), 8)
	if o.Separator == "" {
		o.Separator = " | "
	}
}

// counterCount is one counter's numbers.
type counterCount struct {
	Session int64
	Total   int64
	Changed time.Time // last time it went up or down
	hit     time.Time // last count from the parameter, for the cooldown
}

var (
	counterMu     sync.Mutex
	counterNow    = time.Now
	counterCounts = map[string]*counterCount{}
	counterLoaded bool
	counterFile   string // set by tests; empty uses DataDir
	counterSave   *time.Timer
	// counterPrev holds each parameter's last value; counterQuiet is when
	// the burst of values VRChat sends after an avatar change is over.
	counterPrev  = map[string]float64{}
	counterQuiet time.Time
)

// countersPath is where the totals are kept.
func countersPath() string {
	if counterFile != "" {
		return counterFile
	}
	if dir := DataDir(); dir != "" {
		return filepath.Join(dir, "counters.json")
	}
	return "counters.json"
}

type counterFileData struct {
	Totals map[string]int64 `json:"totals"`
}

// loadCounterTotalsLocked reads the saved totals once. The caller holds counterMu.
func loadCounterTotalsLocked() {
	if counterLoaded {
		return
	}
	counterLoaded = true
	data, err := os.ReadFile(countersPath())
	if err != nil {
		return
	}
	var f counterFileData
	if err := json.Unmarshal(data, &f); err != nil {
		fmt.Println("counters: could not read the totals:", err)
		return
	}
	for id, n := range f.Totals {
		if n < 0 {
			n = 0
		}
		counterCountLocked(id).Total = n
	}
}

func counterCountLocked(id string) *counterCount {
	c := counterCounts[id]
	if c == nil {
		c = &counterCount{}
		counterCounts[id] = c
	}
	return c
}

// scheduleCounterSaveLocked writes the totals a moment after the last change.
func scheduleCounterSaveLocked() {
	if counterSave != nil {
		counterSave.Stop()
	}
	counterSave = time.AfterFunc(2*time.Second, func() {
		if err := SaveCounters(); err != nil {
			fmt.Println("counters: save failed:", err)
		}
	})
}

// SaveCounters writes every total to counters.json now.
func SaveCounters() error {
	counterMu.Lock()
	f := counterFileData{Totals: make(map[string]int64, len(counterCounts))}
	for id, c := range counterCounts {
		if c.Total > 0 {
			f.Totals[id] = c.Total
		}
	}
	counterMu.Unlock()
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	path := countersPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// counterEdge reports whether a new value v counts, given the previous one.
// Without a previous value a rising counter treats it as off.
func counterEdge(def CounterDef, prev float64, hadPrev bool, v float64) bool {
	if def.Mode == CounterChange {
		return hadPrev && math.Abs(v-prev) > 1e-4
	}
	if !hadPrev {
		prev = math.Min(0, def.Threshold)
	}
	return prev <= def.Threshold && v > def.Threshold
}

// counterParam counts one received parameter value for every counter that
// uses it, and returns the events to fire.
func counterParam(defs []CounterDef, name string, v float64, now time.Time) []map[string]string {
	counterMu.Lock()
	defer counterMu.Unlock()
	loadCounterTotalsLocked()
	prev, hadPrev := counterPrev[name]
	counterPrev[name] = v
	if now.Before(counterQuiet) {
		// The values VRChat sends right after an avatar change are not changes.
		return nil
	}
	var events []map[string]string
	for _, def := range defs {
		if def.Param != name || !counterEdge(def, prev, hadPrev, v) {
			continue
		}
		c := counterCountLocked(def.ID)
		if def.Cooldown > 0 && !c.hit.IsZero() && now.Sub(c.hit) < time.Duration(def.Cooldown*float64(time.Second)) {
			continue
		}
		c.hit = now
		events = append(events, bumpLocked(def, c, 1, now, "param"))
	}
	if len(events) > 0 {
		scheduleCounterSaveLocked()
	}
	return events
}

// bumpLocked adds delta to a counter, never going below zero.
func bumpLocked(def CounterDef, c *counterCount, delta int64, now time.Time, source string) map[string]string {
	c.Session = max(0, c.Session+delta)
	c.Total = max(0, c.Total+delta)
	c.Changed = now
	return map[string]string{
		"counter": def.ID, "name": def.Name, "session": fmt.Sprint(c.Session),
		"total": fmt.Sprint(c.Total), "delta": fmt.Sprint(delta), "source": source,
	}
}

func onCounterParam(msg *osc.Message) {
	if len(msg.Arguments) == 0 {
		return
	}
	name := strings.TrimPrefix(msg.Address, AvatarParamPrefix)
	defs := tallyOptions().counters
	used := false
	for _, d := range defs {
		if d.Param == name {
			used = true
			break
		}
	}
	if !used {
		return
	}
	v, ok := oscNumber(msg.Arguments[0])
	if !ok {
		return
	}
	events := counterParam(defs, name, v, counterNow())
	for _, ev := range events {
		FireTrigger(TriggerCounterChanged, ev)
	}
	if len(events) > 0 {
		NotifyChange()
	}
}

// counterAvatarChanged forgets the last values: the next avatar sends its own.
func counterAvatarChanged(string) {
	counterMu.Lock()
	clear(counterPrev)
	counterQuiet = counterNow().Add(2 * time.Second)
	counterMu.Unlock()
}

var errNoCounter = errors.New("that counter no longer exists")

func findCounter(id string) (CounterDef, bool) {
	for _, d := range GetConfig().CounterOptions.Counters {
		if d.ID == id {
			return d, true
		}
	}
	return CounterDef{}, false
}

// CounterAdd changes a counter's session and total by delta.
func CounterAdd(id string, delta int64) error {
	def, ok := findCounter(id)
	if !ok {
		return errNoCounter
	}
	counterMu.Lock()
	loadCounterTotalsLocked()
	ev := bumpLocked(def, counterCountLocked(id), delta, counterNow(), "manual")
	scheduleCounterSaveLocked()
	counterMu.Unlock()
	FireTrigger(TriggerCounterChanged, ev)
	NotifyChange()
	return nil
}

// CounterReset sets the session count to zero, and with all the total too.
func CounterReset(id string, all bool) error {
	if _, ok := findCounter(id); !ok {
		return errNoCounter
	}
	counterMu.Lock()
	loadCounterTotalsLocked()
	c := counterCountLocked(id)
	c.Session = 0
	if all {
		c.Total = 0
	}
	c.Changed = time.Time{}
	scheduleCounterSaveLocked()
	counterMu.Unlock()
	NotifyChange()
	return nil
}

// CounterStatus is one counter as the panel shows it.
type CounterStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Session int64  `json:"session"`
	Total   int64  `json:"total"`
	Changed int64  `json:"changed"` // unix ms, 0 when not this session
	Text    string `json:"text"`    // its part of the line
}

// CurrentCounters reports every configured counter.
func CurrentCounters(o CounterOptions) []CounterStatus {
	counterMu.Lock()
	defer counterMu.Unlock()
	loadCounterTotalsLocked()
	out := make([]CounterStatus, 0, len(o.Counters))
	for _, d := range o.Counters {
		c := counterCounts[d.ID]
		if c == nil {
			c = &counterCount{}
		}
		st := CounterStatus{ID: d.ID, Name: d.Name, Session: c.Session, Total: c.Total, Text: counterPart(d, *c)}
		if !c.Changed.IsZero() {
			st.Changed = c.Changed.UnixMilli()
		}
		out = append(out, st)
	}
	return out
}

func counterPart(d CounterDef, c counterCount) string {
	return collapseSpaces(strings.NewReplacer(
		"{name}", d.Name,
		"{session}", fmt.Sprint(c.Session),
		"{total}", fmt.Sprint(c.Total),
	).Replace(d.Template))
}

// counterText renders the line from the given counts.
func counterLineText(o CounterOptions, counts map[string]counterCount, now time.Time) string {
	parts := make([]string, 0, len(o.Counters))
	for _, d := range o.Counters {
		if !d.Show {
			continue
		}
		c := counts[d.ID]
		if d.OnlyAfterChange && (c.Changed.IsZero() || now.Sub(c.Changed) >= time.Duration(d.ShowSec)*time.Second) {
			continue
		}
		if part := counterPart(d, c); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, o.Separator)
}

func counterLine(cfg Config) string {
	counterMu.Lock()
	loadCounterTotalsLocked()
	counts := make(map[string]counterCount, len(counterCounts))
	for id, c := range counterCounts {
		counts[id] = *c
	}
	counterMu.Unlock()
	return counterLineText(cfg.CounterOptions, counts, counterNow())
}
