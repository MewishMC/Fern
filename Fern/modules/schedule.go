package modules

// Scheduled and repeating messages: a message at a time of day on chosen
// weekdays, or every N minutes within an optional window. Each one is shown
// for a while on the schedule line, or, with Takeover, as a chatbox message
// like Send on the Live page.

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kinds of scheduled message.
const (
	ScheduleAt    = "at"    // once at a time of day
	ScheduleEvery = "every" // every N minutes
)

// MaxScheduled is how many scheduled messages there can be.
const MaxScheduled = 20

// ScheduleOptions controls scheduled messages and their line.
type ScheduleOptions struct {
	Messages  []ScheduledMessage `json:"messages"`
	Separator string             `json:"separator"` // between messages showing at once
}

// ScheduledMessage is one scheduled or repeating message.
type ScheduledMessage struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Text    string `json:"text"` // {time} is the clock, {world} and {profile} as in rules
	Kind    string `json:"kind"` // at or every
	At      string `json:"at"`   // "HH:MM" for at
	// EveryMin repeats the message, counted from From, while the time is
	// between From and To. From == To is all day.
	EveryMin int      `json:"every_min"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Days     []string `json:"days"` // mon..sun; empty is every day
	ShowSec  int      `json:"show_sec"`
	Takeover bool     `json:"takeover"` // replace the chatbox while it shows
}

func defaultScheduleOptions() ScheduleOptions {
	return ScheduleOptions{Messages: []ScheduledMessage{}, Separator: " · "}
}

func (o ScheduleOptions) clone() ScheduleOptions {
	o.Messages = append([]ScheduledMessage{}, o.Messages...)
	for i := range o.Messages {
		o.Messages[i].Days = append([]string{}, o.Messages[i].Days...)
	}
	return o
}

func (o *ScheduleOptions) sanitize() {
	if len(o.Messages) > MaxScheduled {
		o.Messages = o.Messages[:MaxScheduled]
	}
	seen := map[string]bool{}
	list := make([]ScheduledMessage, 0, len(o.Messages))
	for _, m := range o.Messages {
		m.ID = cleanTallyID(m.ID, seen)
		m.Text = oneLine(m.Text, 143)
		if m.Kind != ScheduleEvery {
			m.Kind = ScheduleAt
		}
		for _, t := range []*string{&m.At, &m.From, &m.To} {
			if _, ok := parseClock(*t); !ok {
				*t = "00:00"
			}
		}
		if m.EveryMin <= 0 {
			m.EveryMin = 30
		}
		m.EveryMin = clampInt(m.EveryMin, 1, 1440)
		days := make([]string, 0, len(m.Days))
		for _, d := range weekdays {
			if slices.Contains(m.Days, d.Value) {
				days = append(days, d.Value)
			}
		}
		m.Days = days
		if m.ShowSec <= 0 {
			m.ShowSec = 30
		}
		m.ShowSec = clampInt(m.ShowSec, 3, 3600)
		list = append(list, m)
	}
	o.Messages = list
	o.Separator = RuneLimit(strings.NewReplacer("\r", "", "\n", "").Replace(o.Separator), 8)
	if o.Separator == "" {
		o.Separator = " · "
	}
}

// scheduleSlot is the minute ("2006-01-02T15:04") message m is due in at
// now, or "" when it is not due.
func scheduleSlot(m ScheduledMessage, now time.Time) string {
	minute := now.Hour()*60 + now.Minute()
	switch m.Kind {
	case ScheduleEvery:
		from, ok1 := parseClock(m.From)
		to, ok2 := parseClock(m.To)
		if !ok1 || !ok2 || m.EveryMin <= 0 || !inTimeWindow(now, from, to, m.Days) {
			return ""
		}
		since := minute - from
		if since < 0 {
			since += 24 * 60
		}
		if since%m.EveryMin != 0 {
			return ""
		}
	default:
		at, ok := parseClock(m.At)
		if !ok || minute != at || len(m.Days) > 0 && !slices.Contains(m.Days, weekdayKey[now.Weekday()]) {
			return ""
		}
	}
	return now.Format("2006-01-02T15:04")
}

// shownMessage is a scheduled message on the line.
type shownMessage struct {
	id    string
	text  string
	until time.Time
}

var (
	scheduleMu    sync.Mutex
	scheduleNow   = time.Now
	scheduleLast  = map[string]string{}    // slot each message last went out in
	scheduleSent  = map[string]time.Time{} // when it last went out
	scheduleShown []shownMessage           // newest first
	scheduleHold  = SetHold                // replaced by tests
)

// scheduleTick sends every message that is due.
func scheduleTick(o ScheduleOptions, now time.Time) {
	var due []ScheduledMessage
	scheduleMu.Lock()
	for _, m := range o.Messages {
		if !m.Enabled || strings.TrimSpace(m.Text) == "" {
			continue
		}
		slot := scheduleSlot(m, now)
		if slot == "" || scheduleLast[m.ID] == slot {
			continue
		}
		scheduleLast[m.ID] = slot
		due = append(due, m)
	}
	scheduleMu.Unlock()
	for _, m := range due {
		showScheduled(m, now)
	}
}

// showScheduled puts a message up now.
func showScheduled(m ScheduledMessage, now time.Time) {
	text := expandScheduleText(m.Text, now)
	if text == "" {
		return
	}
	d := time.Duration(m.ShowSec) * time.Second
	scheduleMu.Lock()
	scheduleSent[m.ID] = now
	if !m.Takeover {
		kept := scheduleShown[:0]
		for _, s := range scheduleShown {
			if s.id != m.ID && now.Before(s.until) {
				kept = append(kept, s)
			}
		}
		scheduleShown = append([]shownMessage{{id: m.ID, text: text, until: now.Add(d)}}, kept...)
	}
	scheduleMu.Unlock()
	if m.Takeover && !DNDActive() {
		scheduleHold(text, d)
	}
	FireTrigger(TriggerScheduleSent, map[string]string{"message": m.ID, "name": text})
	NotifyChange()
}

func expandScheduleText(text string, now time.Time) string {
	text = strings.ReplaceAll(text, "{time}", now.Format("15:04"))
	return strings.TrimSpace(expandRuleText(text, RuleEvent{}))
}

var errNoScheduled = errors.New("that scheduled message no longer exists")

// ShowScheduledNow sends a scheduled message right away, whatever its time.
func ShowScheduledNow(id string) error {
	for _, m := range GetConfig().ScheduleOptions.Messages {
		if m.ID == id {
			if strings.TrimSpace(m.Text) == "" {
				return errors.New("that message is empty")
			}
			showScheduled(m, scheduleNow())
			return nil
		}
	}
	return errNoScheduled
}

// ClearScheduled takes every scheduled message off the line.
func ClearScheduled() {
	scheduleMu.Lock()
	scheduleShown = nil
	scheduleMu.Unlock()
	NotifyChange()
}

// scheduleText is the line: the messages still showing, newest first.
func scheduleText(o ScheduleOptions, now time.Time) string {
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	parts := make([]string, 0, len(scheduleShown))
	for _, s := range scheduleShown {
		if now.Before(s.until) {
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, o.Separator)
}

func scheduleLine(cfg Config) string {
	return scheduleText(cfg.ScheduleOptions, scheduleNow())
}

// ScheduleStatus is one scheduled message as the panel shows it.
type ScheduleStatus struct {
	ID      string `json:"id"`
	LastMS  int64  `json:"last"`    // when it last went out, 0 for not yet
	Showing bool   `json:"showing"` // on the line right now
}

// CurrentSchedule reports when each message last went out.
func CurrentSchedule(o ScheduleOptions) []ScheduleStatus {
	now := scheduleNow()
	scheduleMu.Lock()
	defer scheduleMu.Unlock()
	out := make([]ScheduleStatus, 0, len(o.Messages))
	for _, m := range o.Messages {
		st := ScheduleStatus{ID: m.ID}
		if t, ok := scheduleSent[m.ID]; ok {
			st.LastMS = t.UnixMilli()
		}
		for _, s := range scheduleShown {
			if s.id == m.ID && now.Before(s.until) {
				st.Showing = true
			}
		}
		out = append(out, st)
	}
	return out
}
