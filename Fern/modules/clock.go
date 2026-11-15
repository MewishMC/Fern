package modules

import (
	"fmt"
	"strings"
	"sync"
	"time"

	// Windows has no zoneinfo database of its own, so the IANA zones ship in
	// the binary. This adds about 400 KB to Fern.exe.
	_ "time/tzdata"
)

// TimeCounter is a "time since" or "time until" entry, like "together 1y 2m".
type TimeCounter struct {
	Label string `json:"label"`
	Date  string `json:"date"` // 2006-01-02, or 2006-01-02T15:04 with a time
}

// Layouts a counter date may use.
const (
	counterDay     = "2006-01-02"
	counterMinute  = "2006-01-02T15:04"
	maxTimeCounter = 10
)

var (
	zoneMu    sync.Mutex
	zoneCache = map[string]*time.Location{}
)

// zone returns the location for an IANA name, or the PC's own zone for an
// empty or unknown name.
func zone(name string) *time.Location {
	if name == "" {
		return time.Local
	}
	zoneMu.Lock()
	defer zoneMu.Unlock()
	if loc, ok := zoneCache[name]; ok {
		return loc
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.Local
	}
	zoneCache[name] = loc
	return loc
}

// validZone reports whether name is a zone Fern can load. Empty means the
// PC's zone and is valid.
func validZone(name string) bool {
	if name == "" {
		return true
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func clockText(cfg Config) string {
	return formatClockLine(cfg, time.Now())
}

// formatClockLine renders the time line at now: the clock, an optional second
// clock, the counters, and the weather when it is merged into this line.
func formatClockLine(cfg Config, now time.Time) string {
	o := cfg.TimeOptions
	parts := []string{withLabel(o.Label, formatClock(o, now.In(zone(o.Zone)), o.ShowZone))}
	if o.Zone2 != "" {
		// The second clock always says which zone it is unless it has a label.
		parts = append(parts, withLabel(o.Label2, formatClock(o, now.In(zone(o.Zone2)), o.Label2 == "")))
	}
	line := strings.Join(parts, " · ")
	if counters := counterText(o, now); counters != "" {
		if o.CounterLine {
			line += cfg.DisplayOptions.Separator + counters
		} else {
			line += " · " + counters
		}
	}
	return line
}

// formatClock writes t in the configured 12/24 hour style.
func formatClock(o TimeOptions, t time.Time, showZone bool) string {
	layout := "3:04 PM"
	switch {
	case o.Use24h:
		layout = "15:04"
	case o.NoAmPm:
		layout = "3:04"
	}
	if o.Seconds {
		switch {
		case o.Use24h:
			layout = "15:04:05"
		case o.NoAmPm:
			layout = "3:04:05"
		default:
			layout = "3:04:05 PM"
		}
	}
	if showZone {
		layout += " MST"
	}
	return t.Format(layout)
}

// parseCounterDate reads a counter date in loc.
func parseCounterDate(s string, loc *time.Location) (time.Time, bool) {
	for _, layout := range []string{counterMinute, counterDay} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// counterText renders every counter with a valid date, joined by " · ".
func counterText(o TimeOptions, now time.Time) string {
	loc := zone(o.Zone)
	var out []string
	for _, c := range o.Counters {
		at, ok := parseCounterDate(c.Date, loc)
		if !ok {
			continue
		}
		out = append(out, withLabel(c.Label, formatSpan(at, now.In(loc), o.CounterUnits)))
	}
	return strings.Join(out, " · ")
}

// formatSpan writes the calendar time between a and b, either way round, as
// its largest units: "1y 2m", "3d 4h", "12min". m is months; min is minutes.
func formatSpan(a, b time.Time, units int) string {
	if units < 1 {
		units = 1
	}
	y, mo, d, h, mi := calendarDiff(a, b)
	values := []int{y, mo, d, h, mi}
	names := []string{"y", "m", "d", "h", "min"}
	start := 0
	for start < len(values)-1 && values[start] == 0 {
		start++
	}
	var out []string
	for i := start; i < len(values) && i < start+units; i++ {
		if values[i] > 0 || (len(out) == 0 && i == len(values)-1) {
			out = append(out, fmt.Sprintf("%d%s", values[i], names[i]))
		}
	}
	return strings.Join(out, " ")
}

// calendarDiff counts whole years, months, days, hours and minutes from the
// earlier of a and b to the later, the way people count anniversaries: a
// month from Jan 31 is the end of February, so Jan 31 to Mar 1 is one month
// and a day. Wall-clock times are compared, so a DST change doesn't show up
// as a stray hour.
func calendarDiff(a, b time.Time) (years, months, days, hours, minutes int) {
	if b.Before(a) {
		a, b = b, a
	}
	b = b.In(a.Location())
	wall := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	}
	a, b = wall(a), wall(b)
	total := (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
	mark := addMonths(a, total)
	if mark.After(b) {
		total--
		mark = addMonths(a, total)
	}
	rest := b.Sub(mark)
	days = int(rest / (24 * time.Hour))
	hours = int(rest/time.Hour) % 24
	minutes = int(rest/time.Minute) % 60
	return total / 12, total % 12, days, hours, minutes
}

// addMonths moves t on by n months, keeping the day of the month where the
// target month has it and using its last day otherwise.
func addMonths(t time.Time, n int) time.Time {
	first := time.Date(t.Year(), t.Month()+time.Month(n), 1, t.Hour(), t.Minute(), 0, 0, t.Location())
	last := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if day > last {
		day = last
	}
	return first.AddDate(0, 0, day-1)
}

// sanitizeTime keeps the time options usable. Counter rows are kept while
// their date is still being typed; rendering skips dates that don't parse.
func sanitizeTime(o *TimeOptions) {
	o.Zone = strings.TrimSpace(o.Zone)
	if !validZone(o.Zone) {
		o.Zone = ""
	}
	o.Zone2 = strings.TrimSpace(o.Zone2)
	if !validZone(o.Zone2) {
		o.Zone2 = ""
	}
	o.Label2 = RuneLimit(strings.TrimSpace(o.Label2), 16)
	o.CounterUnits = clampInt(o.CounterUnits, 1, 4)
	if len(o.Counters) > maxTimeCounter {
		o.Counters = o.Counters[:maxTimeCounter]
	}
	counters := make([]TimeCounter, 0, len(o.Counters))
	for _, c := range o.Counters {
		c.Label = RuneLimit(strings.Join(strings.Fields(c.Label), " "), 24)
		c.Date = RuneLimit(strings.TrimSpace(c.Date), 16)
		counters = append(counters, c)
	}
	o.Counters = counters
}
