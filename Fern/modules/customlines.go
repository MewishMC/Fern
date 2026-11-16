package modules

// Custom lines are chatbox lines the user fills from outside Fern: from the
// local control API (or a rule), from a command or script run every few
// seconds, or from a URL, optionally picking one value out of a JSON reply.
//
// There are MaxCustomLines fixed slots (custom1, custom2, ...), each a normal
// chatbox line with its own switch, order, drop priority and VR / desktop
// setting. The definitions are kept per PC, like the OSC settings: a profile
// or an imported backup never brings a command along, so opening someone's
// file can't make Fern run a program.

import (
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
)

// IDs of the custom lines.
const (
	ModuleCustom1 = "custom1"
	ModuleCustom2 = "custom2"
	ModuleCustom3 = "custom3"
)

// MaxCustomLines is how many custom lines there are.
const MaxCustomLines = 3

// CustomLineIDs lists the custom line IDs in slot order.
var CustomLineIDs = []string{ModuleCustom1, ModuleCustom2, ModuleCustom3}

// Where a custom line's text comes from.
const (
	CustomSourceAPI     = "api"     // the control API or a rule sets it
	CustomSourceCommand = "command" // first line a command prints
	CustomSourceURL     = "url"     // a web address, optionally a JSON field of it
)

// Limits of the custom line settings.
const (
	customMinInterval = 2
	customMaxInterval = 3600
	customMaxTimeout  = 60
	customMaxCommand  = 1024
	customMaxURL      = 2048
	customMaxPath     = 256
	customMaxText     = 143
)

// CustomLine is one custom line's settings.
type CustomLine struct {
	Name        string `json:"name"`   // shown in the panel and the rule editor
	Source      string `json:"source"` // api, command or url
	Label       string `json:"label"`  // optional prefix
	Command     string `json:"command"`
	URL         string `json:"url"`
	JSONPath    string `json:"json_path"`    // e.g. data.viewers or items[0].title; empty takes the first line of the reply
	IntervalSec int    `json:"interval_sec"` // how often the command or URL runs
	TimeoutSec  int    `json:"timeout_sec"`  // how long one run may take
	MaxChars    int    `json:"max_chars"`    // 0 means no limit beyond the chatbox's
	Fallback    string `json:"fallback"`     // shown while there is nothing else; empty hides the line
}

// CustomLinesOptions holds every custom line, always MaxCustomLines of them.
type CustomLinesOptions struct {
	Lines []CustomLine `json:"lines"`
}

func defaultCustomLine(i int) CustomLine {
	return CustomLine{
		Name:        "Custom " + string(rune('1'+i)),
		Source:      CustomSourceAPI,
		IntervalSec: 30,
		TimeoutSec:  10,
	}
}

func defaultCustomLines() CustomLinesOptions {
	o := CustomLinesOptions{Lines: make([]CustomLine, MaxCustomLines)}
	for i := range o.Lines {
		o.Lines[i] = defaultCustomLine(i)
	}
	return o
}

// Line is slot i's settings, or its defaults when the config has none.
func (o CustomLinesOptions) Line(i int) CustomLine {
	if i >= 0 && i < len(o.Lines) {
		return o.Lines[i]
	}
	return defaultCustomLine(i)
}

func (o CustomLinesOptions) clone() CustomLinesOptions {
	o.Lines = append([]CustomLine{}, o.Lines...)
	return o
}

// plainLine removes control characters, newlines included, and trims.
func plainLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

func (o *CustomLinesOptions) sanitize() {
	lines := make([]CustomLine, MaxCustomLines)
	for i := range lines {
		l := defaultCustomLine(i)
		if i < len(o.Lines) {
			l = o.Lines[i]
		}
		l.Name = RuneLimit(plainLine(l.Name), 24)
		if l.Name == "" {
			l.Name = defaultCustomLine(i).Name
		}
		switch l.Source {
		case CustomSourceAPI, CustomSourceCommand, CustomSourceURL:
		default:
			l.Source = CustomSourceAPI
		}
		l.Label = affixLine(strings.TrimSpace(l.Label), 16)
		l.Command = RuneLimit(plainLine(l.Command), customMaxCommand)
		l.URL = RuneLimit(plainLine(l.URL), customMaxURL)
		l.JSONPath = RuneLimit(plainLine(l.JSONPath), customMaxPath)
		if l.IntervalSec == 0 {
			l.IntervalSec = 30
		}
		l.IntervalSec = clampInt(l.IntervalSec, customMinInterval, customMaxInterval)
		if l.TimeoutSec == 0 {
			l.TimeoutSec = 10
		}
		l.TimeoutSec = clampInt(l.TimeoutSec, 1, customMaxTimeout)
		l.MaxChars = clampInt(l.MaxChars, 0, customMaxText)
		l.Fallback = RuneLimit(plainLine(l.Fallback), customMaxText)
		lines[i] = l
	}
	o.Lines = lines
}

// keepCommands puts back the commands of cur, for an imported backup: a
// file from elsewhere may name the line, never the program it runs.
func (o *CustomLinesOptions) keepCommands(cur CustomLinesOptions) {
	for i := range o.Lines {
		o.Lines[i].Command = ""
		if i < len(cur.Lines) {
			o.Lines[i].Command = cur.Lines[i].Command
		}
	}
}

// customSlot is the slot of a custom line ID, or -1.
func customSlot(id string) int {
	for i, c := range CustomLineIDs {
		if c == id {
			return i
		}
	}
	return -1
}

// IsCustomLine reports whether id is one of the custom lines.
func IsCustomLine(id string) bool { return customSlot(id) >= 0 }

// customValue is what a custom line shows right now, from whichever source.
type customValue struct {
	text  string
	until time.Time // API text only; zero keeps it until cleared
	err   string
	at    time.Time // when text or err last changed
	key   string    // the command or URL settings that produced it
	runs  int64
	busy  bool
	next  time.Time // when the command or URL runs next
	force bool      // run now, even with the line off
}

var (
	customMu   sync.Mutex
	customAPI  [MaxCustomLines]customValue // set through the API
	customPoll [MaxCustomLines]customValue // from the command or URL
	customNow  = time.Now
)

// SetCustomLineText shows text on a custom line, for d or, with d of zero,
// until it is replaced or cleared. Empty text clears it. It works whatever
// the line's source; the line only shows it while its source is the API.
func SetCustomLineText(id, text string, d time.Duration) error {
	i := customSlot(id)
	if i < 0 {
		return errors.New("unknown custom line " + id + "; use custom1 to custom" + string(rune('0'+MaxCustomLines)))
	}
	text = RuneLimit(plainLine(text), customMaxText)
	customMu.Lock()
	v := &customAPI[i]
	v.text, v.at, v.until = text, customNow(), time.Time{}
	if text != "" && d > 0 {
		v.until = customNow().Add(d)
	}
	customMu.Unlock()
	NotifyChange()
	return nil
}

// CustomLineText is the text a custom line was given through the API, if it
// is still showing.
func CustomLineText(id string) string {
	i := customSlot(id)
	if i < 0 {
		return ""
	}
	customMu.Lock()
	defer customMu.Unlock()
	return customAPI[i].current(customNow())
}

func (v customValue) current(now time.Time) string {
	if !v.until.IsZero() && now.After(v.until) {
		return ""
	}
	return v.text
}

// customText is the text of slot i before its label and limit: the source's
// value, or the fallback when the source has none.
func customText(cfg Config, i int) string {
	if i < 0 || i >= MaxCustomLines {
		return ""
	}
	l := cfg.CustomLines.Line(i)
	now := customNow()
	customMu.Lock()
	text := ""
	switch l.Source {
	case CustomSourceAPI:
		text = customAPI[i].current(now)
	default:
		if customPoll[i].key == customKey(l) {
			text = customPoll[i].text
		}
	}
	customMu.Unlock()
	if text == "" {
		text = l.Fallback
	}
	return text
}

// customLine renders custom line id for the chatbox.
func customLine(cfg Config, id string) string {
	i := customSlot(id)
	text := customText(cfg, i)
	if text == "" {
		return ""
	}
	l := cfg.CustomLines.Line(i)
	if l.MaxChars > 0 {
		text = ellipsize(text, l.MaxChars)
	}
	return withLabel(l.Label, text)
}

// customFitLine is a custom line that can be cut down with an ellipsis
// before it is dropped to make room.
func customFitLine(cfg Config, id string) FitLine {
	full := strings.TrimSpace(customLine(cfg, id))
	return FitLine{
		ID:       id,
		Variants: []string{full},
		Trim: func(max int) string {
			if max < 8 {
				return ""
			}
			return ellipsize(full, max)
		},
	}
}

// CustomLineStatus is a custom line's state for the panel.
type CustomLineStatus struct {
	ID        string `json:"id"`
	Text      string `json:"text"`      // the source's value, before the label and limit
	Error     string `json:"error"`     // why the last run failed
	UpdatedMS int64  `json:"updatedMs"` // when the value last changed (unix ms)
	UntilMS   int64  `json:"untilMs"`   // API text: when it goes away; 0 for never
	Runs      int64  `json:"runs"`
	Busy      bool   `json:"busy"`
}

// CustomLineStatuses reports every custom line, in slot order.
func CustomLineStatuses() []CustomLineStatus {
	cfg := GetConfig()
	now := customNow()
	customMu.Lock()
	defer customMu.Unlock()
	out := make([]CustomLineStatus, 0, MaxCustomLines)
	for i, id := range CustomLineIDs {
		s := CustomLineStatus{ID: id}
		l := cfg.CustomLines.Line(i)
		if l.Source == CustomSourceAPI {
			v := customAPI[i]
			s.Text = v.current(now)
			if s.Text != "" {
				s.UpdatedMS = v.at.UnixMilli()
				if !v.until.IsZero() {
					s.UntilMS = v.until.UnixMilli()
				}
			}
		} else if v := customPoll[i]; v.key == customKey(l) {
			s.Text, s.Error, s.Runs, s.Busy = v.text, v.err, v.runs, v.busy
			if !v.at.IsZero() {
				s.UpdatedMS = v.at.UnixMilli()
			}
		}
		out = append(out, s)
	}
	return out
}

// customLineName is the user's name for a custom line, for LineLabel.
func customLineName(id string) (string, bool) {
	i := customSlot(id)
	if i < 0 {
		return "", false
	}
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if i < len(cfg.CustomLines.Lines) && cfg.CustomLines.Lines[i].Name != "" {
		return cfg.CustomLines.Lines[i].Name, true
	}
	return defaultCustomLine(i).Name, true
}
