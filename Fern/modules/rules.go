package modules

// Rules: "when <trigger> [and conditions] then <actions>".
//
// Triggers and actions live in a registry so other parts of Fern can add
// their own. Each one describes its settings as a list of RuleField, and the
// panel draws the editor from that list, so a newly registered trigger or
// action shows up on the Rules page without any change to the page.
//
// There are two kinds of trigger:
//
//   - State triggers have Check: they hold for a while ("in a world called
//     X", "between 22:00 and 06:00"). A rule with one fires when the trigger
//     and its conditions start to hold, and with Undo on it puts back what its
//     actions changed once they stop. State triggers double as conditions.
//   - Event triggers have no Check: something happened ("a player joined").
//     Whoever notices it calls FireTrigger, and every enabled rule with that
//     trigger whose Match accepts the event and whose conditions hold fires.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// Field types of RuleField.
const (
	FieldText   = "text"   // a string; Source, if set, offers suggestions
	FieldNumber = "number" // a float64 between Min and Max when Max > Min
	FieldBool   = "bool"
	FieldSelect = "select" // one of Options, or of Source's list
	FieldMulti  = "multi"  // any of Options, stored as []string
	FieldTime   = "time"   // "HH:MM", 24-hour
)

// RuleOption is one choice of a select or multi field.
type RuleOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// RuleField describes one setting of a trigger or action. The panel draws an
// input for it, and sanitize coerces the stored value to its type.
type RuleField struct {
	Key         string       `json:"key"`
	Label       string       `json:"label"`
	Type        string       `json:"type"`
	Hint        string       `json:"hint,omitempty"`
	Placeholder string       `json:"placeholder,omitempty"`
	Options     []RuleOption `json:"options,omitempty"`
	// Source names a list registered with RegisterRuleSource, used as the
	// options of a select or the suggestions of a text field. A select with
	// a Source accepts any value, since the list changes (profiles come and go).
	Source  string  `json:"source,omitempty"`
	Default any     `json:"default,omitempty"`
	Min     float64 `json:"min,omitempty"`
	Max     float64 `json:"max,omitempty"`
	Step    float64 `json:"step,omitempty"` // 1 or more rounds to whole steps
	MaxLen  int     `json:"maxLen,omitempty"`
	// ShowIf dims the field unless another field of the same step has one of
	// these values, e.g. a value that only matters for "equals".
	ShowIf *RuleShowIf `json:"showIf,omitempty"`
}

// RuleShowIf ties a field to another field's value.
type RuleShowIf struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// RuleEvent is something that happened, handed to FireTrigger.
type RuleEvent struct {
	Type string            `json:"type"` // the trigger's ID
	Data map[string]string `json:"data,omitempty"`
}

// RuleTrigger is a registered trigger. Exactly one of Check (state trigger)
// or event semantics applies: with Check nil it is an event trigger.
type RuleTrigger struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description,omitempty"`
	Category    string      `json:"category,omitempty"`
	Fields      []RuleField `json:"fields"`
	// State is filled in by the registry from Check, for the panel.
	State bool `json:"state"`
	// Check reports whether a state trigger holds right now. It runs on the
	// rules goroutine about once a second per rule that uses it, so it must
	// be quick and must not call the rules API.
	Check func(p RuleParams) bool `json:"-"`
	// Match reports whether an event fired for this trigger matches a rule's
	// settings. nil matches every event.
	Match func(p RuleParams, ev RuleEvent) bool `json:"-"`
}

// RuleContext tells an action why it runs.
type RuleContext struct {
	RuleID   string
	RuleName string
	Event    RuleEvent // the event that fired the rule; Type is "" for state triggers
	Test     bool      // fired with the Test button
	depth    int       // nesting of "run another rule"
	eng      *rulesEngine
}

// RuleAction is a registered action.
type RuleAction struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description,omitempty"`
	Category    string      `json:"category,omitempty"`
	Fields      []RuleField `json:"fields"`
	// Run does the action on the rules goroutine and returns an optional
	// function that puts things back, called when the rule's state trigger
	// stops holding and the rule has Undo on. Slow work belongs in a
	// goroutine. Run must not call TestRule.
	Run func(ctx RuleContext, p RuleParams) (undo func(), err error) `json:"-"`
}

var (
	ruleRegMu    sync.RWMutex
	ruleTriggers = map[string]*RuleTrigger{}
	ruleActions  = map[string]*RuleAction{}
	ruleOrder    = map[string]int{} // registration order, for a stable list
	ruleSources  = map[string]func() []RuleOption{}
	ruleSeq      int
)

var ruleIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,47}$`)

func checkRuleDef(id, label string, fields []RuleField) error {
	if !ruleIDPattern.MatchString(id) {
		return fmt.Errorf("rules: bad id %q (lower-case letters, digits, _ . -)", id)
	}
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("rules: %s needs a label", id)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if f.Key == "" || seen[f.Key] {
			return fmt.Errorf("rules: %s has an empty or repeated field key %q", id, f.Key)
		}
		seen[f.Key] = true
		switch f.Type {
		case FieldText, FieldNumber, FieldBool, FieldSelect, FieldMulti, FieldTime:
		default:
			return fmt.Errorf("rules: %s field %s has unknown type %q", id, f.Key, f.Type)
		}
	}
	return nil
}

// RegisterRuleTrigger adds a trigger. It fails on a bad or repeated ID.
// Register from an init function or at startup, before Load, so rules
// using the trigger keep their settings when config.json is read; rules
// whose trigger is unknown are kept untouched and simply never fire.
func RegisterRuleTrigger(t RuleTrigger) error {
	if err := checkRuleDef(t.ID, t.Label, t.Fields); err != nil {
		return err
	}
	ruleRegMu.Lock()
	defer ruleRegMu.Unlock()
	if _, dup := ruleTriggers[t.ID]; dup {
		return fmt.Errorf("rules: trigger %s is already registered", t.ID)
	}
	t.State = t.Check != nil
	t.Fields = append([]RuleField(nil), t.Fields...)
	ruleTriggers[t.ID] = &t
	ruleSeq++
	ruleOrder["t:"+t.ID] = ruleSeq
	return nil
}

// RegisterRuleAction adds an action. It fails on a bad or repeated ID or a
// missing Run.
func RegisterRuleAction(a RuleAction) error {
	if err := checkRuleDef(a.ID, a.Label, a.Fields); err != nil {
		return err
	}
	if a.Run == nil {
		return fmt.Errorf("rules: action %s has no Run", a.ID)
	}
	ruleRegMu.Lock()
	defer ruleRegMu.Unlock()
	if _, dup := ruleActions[a.ID]; dup {
		return fmt.Errorf("rules: action %s is already registered", a.ID)
	}
	a.Fields = append([]RuleField(nil), a.Fields...)
	ruleActions[a.ID] = &a
	ruleSeq++
	ruleOrder["a:"+a.ID] = ruleSeq
	return nil
}

// RegisterRuleSource names a list of choices for fields with that Source.
// fn is called for every panel update, so keep it cheap. Registering a name
// again replaces it.
func RegisterRuleSource(name string, fn func() []RuleOption) {
	ruleRegMu.Lock()
	defer ruleRegMu.Unlock()
	ruleSources[name] = fn
}

func mustRegister(err error) {
	if err != nil {
		panic(err)
	}
}

func lookupTrigger(id string) *RuleTrigger {
	ruleRegMu.RLock()
	defer ruleRegMu.RUnlock()
	return ruleTriggers[id]
}

func lookupAction(id string) *RuleAction {
	ruleRegMu.RLock()
	defer ruleRegMu.RUnlock()
	return ruleActions[id]
}

// RuleTriggers lists the registered triggers by category, then in the order
// they were registered.
func RuleTriggers() []RuleTrigger {
	ruleRegMu.RLock()
	defer ruleRegMu.RUnlock()
	out := make([]RuleTrigger, 0, len(ruleTriggers))
	for _, t := range ruleTriggers {
		out = append(out, *t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryRank(out[i].Category) < categoryRank(out[j].Category)
		}
		return ruleOrder["t:"+out[i].ID] < ruleOrder["t:"+out[j].ID]
	})
	return out
}

// RuleActions lists the registered actions like RuleTriggers.
func RuleActions() []RuleAction {
	ruleRegMu.RLock()
	defer ruleRegMu.RUnlock()
	out := make([]RuleAction, 0, len(ruleActions))
	for _, a := range ruleActions {
		out = append(out, *a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryRank(out[i].Category) < categoryRank(out[j].Category)
		}
		return ruleOrder["a:"+out[i].ID] < ruleOrder["a:"+out[j].ID]
	})
	return out
}

// Categories Fern's own triggers and actions use, in the order the panel
// lists them. Others sort after these, alphabetically.
var ruleCategories = []string{"VRChat", "Avatar", "You", "Time", "Fern", "Chatbox"}

func categoryRank(c string) string {
	for i, known := range ruleCategories {
		if known == c {
			return fmt.Sprintf("%02d", i)
		}
	}
	return "99" + strings.ToLower(c)
}

// RuleSources returns the current choices of every registered source.
func RuleSources() map[string][]RuleOption {
	ruleRegMu.RLock()
	fns := make(map[string]func() []RuleOption, len(ruleSources))
	for name, fn := range ruleSources {
		fns[name] = fn
	}
	ruleRegMu.RUnlock()
	out := make(map[string][]RuleOption, len(fns))
	for name, fn := range fns {
		list := fn()
		if list == nil {
			list = []RuleOption{}
		}
		out[name] = list
	}
	return out
}

// RuleParams are a step's settings, keyed by RuleField.Key. After sanitize
// the values are string, float64, bool or []string.
type RuleParams map[string]any

// String returns a text, select or time value.
func (p RuleParams) String(key string) string {
	s, _ := p[key].(string)
	return s
}

// Number returns a number value; a numeric string also works.
func (p RuleParams) Number(key string) float64 {
	switch v := p[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

// Bool returns a bool value.
func (p RuleParams) Bool(key string) bool {
	b, _ := p[key].(bool)
	return b
}

// List returns a multi value.
func (p RuleParams) List(key string) []string {
	switch v := p[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (p RuleParams) clone() RuleParams {
	if p == nil {
		return RuleParams{}
	}
	out := make(RuleParams, len(p))
	for k, v := range p {
		switch x := v.(type) {
		case []string:
			out[k] = append([]string{}, x...)
		case []any:
			out[k] = append([]any{}, x...)
		default:
			out[k] = v
		}
	}
	return out
}

// MatchText reports whether s matches a user's pattern: empty matches
// anything, /regex/ is a case-insensitive regular expression, and anything
// else is a case-insensitive "contains". A broken regex matches nothing.
func MatchText(pattern, s string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	if len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
		re, err := cachedRegexp(pattern[1 : len(pattern)-1])
		return err == nil && re.MatchString(s)
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(pattern))
}

var (
	ruleReMu    sync.Mutex
	ruleReCache = map[string]*regexp.Regexp{}
	ruleReBad   = map[string]error{}
)

func cachedRegexp(expr string) (*regexp.Regexp, error) {
	ruleReMu.Lock()
	defer ruleReMu.Unlock()
	if re, ok := ruleReCache[expr]; ok {
		return re, nil
	}
	if err, ok := ruleReBad[expr]; ok {
		return nil, err
	}
	re, err := regexp.Compile("(?i)" + expr)
	if len(ruleReCache)+len(ruleReBad) > 256 {
		ruleReCache, ruleReBad = map[string]*regexp.Regexp{}, map[string]error{}
	}
	if err != nil {
		ruleReBad[expr] = err
		return nil, err
	}
	ruleReCache[expr] = re
	return re, nil
}

/* ---------------- config ---------------- */

// Limits of the rules section.
const (
	MaxRules          = 100
	MaxRuleConditions = 8
	MaxRuleActions    = 10
	MaxRuleCooldown   = 86400
)

// RuleStep is a rule's trigger, one of its conditions or one of its actions.
type RuleStep struct {
	Type   string     `json:"type"`
	Not    bool       `json:"not,omitempty"` // conditions only: holds when the trigger does not
	Params RuleParams `json:"params"`
}

// Rule is one "when ... then ..." rule.
type Rule struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Enabled    bool       `json:"enabled"`
	Trigger    RuleStep   `json:"trigger"`
	Conditions []RuleStep `json:"conditions"`
	Actions    []RuleStep `json:"actions"`
	Cooldown   int        `json:"cooldown"` // seconds before it may fire again
	// Undo puts back what the actions changed once a state trigger stops
	// holding. It does nothing for event triggers.
	Undo bool `json:"undo"`
}

// RulesOptions is the rules section of the config. It is kept per PC, so
// switching profiles never changes the rules (a rule may be what switched).
type RulesOptions struct {
	Enabled bool   `json:"enabled"`
	List    []Rule `json:"list"`
}

func defaultRulesOptions() RulesOptions {
	return RulesOptions{Enabled: true, List: []Rule{}}
}

func (s RuleStep) clone() RuleStep {
	s.Params = s.Params.clone()
	return s
}

func (r Rule) clone() Rule {
	r.Trigger = r.Trigger.clone()
	conds := make([]RuleStep, len(r.Conditions))
	for i, c := range r.Conditions {
		conds[i] = c.clone()
	}
	r.Conditions = conds
	acts := make([]RuleStep, len(r.Actions))
	for i, a := range r.Actions {
		acts[i] = a.clone()
	}
	r.Actions = acts
	return r
}

func (o RulesOptions) clone() RulesOptions {
	list := make([]Rule, len(o.List))
	for i, r := range o.List {
		list[i] = r.clone()
	}
	o.List = list
	return o
}

func newRuleID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("r%d", ruleSeq)
	}
	return "r" + hex.EncodeToString(b[:])
}

var ruleRowID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// cleanText removes control characters and limits s to n runes.
func cleanText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return RuneLimit(s, n)
}

func (o *RulesOptions) sanitize() {
	if o.List == nil {
		o.List = []Rule{}
	}
	if len(o.List) > MaxRules {
		o.List = o.List[:MaxRules]
	}
	seen := map[string]bool{}
	for i := range o.List {
		r := &o.List[i]
		if !ruleRowID.MatchString(r.ID) || seen[r.ID] {
			r.ID = newRuleID()
		}
		seen[r.ID] = true
		r.Name = strings.TrimSpace(cleanText(r.Name, 60))
		r.Cooldown = clampInt(r.Cooldown, 0, MaxRuleCooldown)
		r.Trigger.sanitize(stepTrigger)
		r.Trigger.Not = false
		if r.Conditions == nil {
			r.Conditions = []RuleStep{}
		}
		if len(r.Conditions) > MaxRuleConditions {
			r.Conditions = r.Conditions[:MaxRuleConditions]
		}
		for j := range r.Conditions {
			r.Conditions[j].sanitize(stepTrigger)
		}
		if r.Actions == nil {
			r.Actions = []RuleStep{}
		}
		if len(r.Actions) > MaxRuleActions {
			r.Actions = r.Actions[:MaxRuleActions]
		}
		for j := range r.Actions {
			r.Actions[j].sanitize(stepAction)
			r.Actions[j].Not = false
		}
	}
}

const (
	stepTrigger = iota
	stepAction
)

func (s *RuleStep) sanitize(kind int) {
	s.Type = strings.TrimSpace(RuneLimit(s.Type, 64))
	var fields []RuleField
	known := false
	if kind == stepTrigger {
		if t := lookupTrigger(s.Type); t != nil {
			fields, known = t.Fields, true
		}
	} else if a := lookupAction(s.Type); a != nil {
		fields, known = a.Fields, true
	}
	if !known {
		// Keep the settings of a trigger or action this build does not
		// have, bounded, so they come back if it is registered later.
		s.Params = looseParams(s.Params)
		return
	}
	s.Params = SanitizeRuleParams(fields, s.Params)
}

// looseParams keeps plain values only, with bounded sizes.
func looseParams(in RuleParams) RuleParams {
	out := RuleParams{}
	for k, v := range in {
		if len(out) >= 32 || len(k) > 64 {
			break
		}
		switch x := v.(type) {
		case string:
			out[k] = cleanText(x, 500)
		case float64:
			if !math.IsNaN(x) && !math.IsInf(x, 0) {
				out[k] = x
			}
		case bool:
			out[k] = x
		case []string, []any:
			list := RuleParams{k: x}.List(k)
			if len(list) > 50 {
				list = list[:50]
			}
			for i := range list {
				list[i] = cleanText(list[i], 200)
			}
			out[k] = list
		}
	}
	return out
}

// SanitizeRuleParams coerces in to fields: every field gets a value of its
// type (its Default when missing or invalid), and unknown keys are dropped.
func SanitizeRuleParams(fields []RuleField, in RuleParams) RuleParams {
	out := make(RuleParams, len(fields))
	for _, f := range fields {
		out[f.Key] = sanitizeField(f, in[f.Key])
	}
	return out
}

func fieldDefault(f RuleField) any {
	switch f.Type {
	case FieldNumber:
		if d, ok := toNumber(f.Default); ok {
			return clampField(f, d)
		}
		if f.Max > f.Min {
			return clampField(f, 0)
		}
		return 0.0
	case FieldBool:
		b, _ := f.Default.(bool)
		return b
	case FieldMulti:
		switch d := f.Default.(type) {
		case []string:
			return append([]string{}, d...)
		}
		return []string{}
	case FieldSelect:
		if d, ok := f.Default.(string); ok {
			return d
		}
		if f.Source == "" && len(f.Options) > 0 {
			return f.Options[0].Value
		}
		return ""
	case FieldTime:
		if d, ok := f.Default.(string); ok && validClock(d) {
			return d
		}
		return "00:00"
	}
	d, _ := f.Default.(string)
	return d
}

func toNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return float64(x), !math.IsNaN(float64(x))
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func clampField(f RuleField, v float64) float64 {
	if f.Step >= 1 {
		v = math.Round(v/f.Step) * f.Step
	}
	if f.Max > f.Min {
		v = math.Max(f.Min, math.Min(f.Max, v))
	}
	return v
}

func hasOption(opts []RuleOption, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

func sanitizeField(f RuleField, v any) any {
	switch f.Type {
	case FieldNumber:
		if n, ok := toNumber(v); ok {
			return clampField(f, n)
		}
	case FieldBool:
		if b, ok := v.(bool); ok {
			return b
		}
	case FieldSelect:
		if s, ok := v.(string); ok {
			s = cleanText(s, 200)
			if f.Source != "" || hasOption(f.Options, s) {
				return s
			}
		}
	case FieldMulti:
		list := RuleParams{"v": v}.List("v")
		if list == nil {
			break
		}
		out := []string{}
		seen := map[string]bool{}
		for _, s := range list {
			if seen[s] || len(out) >= 50 {
				continue
			}
			if len(f.Options) > 0 && !hasOption(f.Options, s) {
				continue
			}
			seen[s] = true
			out = append(out, cleanText(s, 200))
		}
		return out
	case FieldTime:
		if s, ok := v.(string); ok && validClock(s) {
			return s
		}
	default:
		if s, ok := v.(string); ok {
			n := f.MaxLen
			if n <= 0 {
				n = 200
			}
			return cleanText(s, n)
		}
	}
	return fieldDefault(f)
}

// validClock accepts "HH:MM" in 24-hour time.
func validClock(s string) bool {
	_, ok := parseClock(s)
	return ok
}

// parseClock returns minutes after midnight for "HH:MM".
func parseClock(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, m := 0, 0
	for i, c := range s {
		if i == 2 {
			continue
		}
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	h = int(s[0]-'0')*10 + int(s[1]-'0')
	m = int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
