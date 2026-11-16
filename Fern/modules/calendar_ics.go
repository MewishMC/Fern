package modules

// A small iCalendar (RFC 5545) reader for the calendar line: events with
// their start, end, title and place, all-day dates, time zones, and the
// common repeat rules (daily, weekly on chosen days, monthly on the same day,
// yearly), with exceptions and moved instances. Anything rarer is read as a
// single event rather than guessed at.

import (
	"bufio"
	"bytes"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// icsEvent is one VEVENT.
type icsEvent struct {
	UID      string
	Summary  string
	Location string
	Start    time.Time
	End      time.Time
	AllDay   bool
	Rule     *icsRule
	ExDates  map[int64]bool // unix seconds of skipped instance starts
	RecurID  time.Time      // set on an instance moved or changed from the series
	Canceled bool

	dur    time.Duration // from DURATION, used when there is no DTEND
	hasDur bool
}

// icsRule is the supported part of an RRULE.
type icsRule struct {
	Freq     string // DAILY, WEEKLY, MONTHLY or YEARLY
	Interval int
	Count    int // 0 means no limit
	Until    time.Time
	ByDay    []time.Weekday // WEEKLY only
}

// calInstance is one occurrence of an event.
type calInstance struct {
	Title    string
	Location string
	Start    time.Time
	End      time.Time
	AllDay   bool
}

// icsMaxSteps bounds how many repeats of one event are walked, so a broken or
// very old daily event cannot stall the worker.
const icsMaxSteps = 100000

// parseICS reads the events of a calendar file. loc is the zone for floating
// times and all-day dates, normally time.Local.
func parseICS(data []byte, loc *time.Location) []icsEvent {
	var (
		events []icsEvent
		cur    *icsEvent
		stack  []string
	)
	for _, line := range unfoldICS(data) {
		name, params, value := splitICSLine(line)
		switch name {
		case "BEGIN":
			comp := strings.ToUpper(strings.TrimSpace(value))
			stack = append(stack, comp)
			if comp == "VEVENT" {
				cur = &icsEvent{}
			}
			continue
		case "END":
			comp := strings.ToUpper(strings.TrimSpace(value))
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if comp == "VEVENT" && cur != nil {
				if !cur.Start.IsZero() {
					finishICSEvent(cur)
					events = append(events, *cur)
				}
				cur = nil
			}
			continue
		}
		// Only the event's own properties: an alarm inside it has a SUMMARY too.
		if cur == nil || len(stack) == 0 || stack[len(stack)-1] != "VEVENT" {
			continue
		}
		switch name {
		case "UID":
			cur.UID = value
		case "SUMMARY":
			cur.Summary = icsText(value)
		case "LOCATION":
			cur.Location = icsText(value)
		case "DTSTART":
			cur.Start, cur.AllDay = parseICSTime(value, params, loc)
		case "DTEND":
			cur.End, _ = parseICSTime(value, params, loc)
		case "DURATION":
			cur.dur, cur.hasDur = parseICSDuration(value)
		case "RRULE":
			cur.Rule = parseICSRule(value, params, loc)
		case "EXDATE":
			for _, v := range strings.Split(value, ",") {
				if t, _ := parseICSTime(v, params, loc); !t.IsZero() {
					if cur.ExDates == nil {
						cur.ExDates = map[int64]bool{}
					}
					cur.ExDates[t.Unix()] = true
				}
			}
		case "RECURRENCE-ID":
			cur.RecurID, _ = parseICSTime(value, params, loc)
		case "STATUS":
			cur.Canceled = strings.EqualFold(strings.TrimSpace(value), "CANCELLED")
		}
	}
	return events
}

// finishICSEvent fills in a missing or nonsensical end.
func finishICSEvent(e *icsEvent) {
	if e.End.IsZero() && e.hasDur && e.dur >= 0 {
		e.End = e.Start.Add(e.dur)
	}
	if e.End.IsZero() || e.End.Before(e.Start) {
		if e.AllDay {
			e.End = e.Start.AddDate(0, 0, 1)
		} else {
			e.End = e.Start
		}
	}
}

// unfoldICS splits the file into logical lines, joining folded ones.
func unfoldICS(data []byte) []string {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if l == "" {
			continue
		}
		if (l[0] == ' ' || l[0] == '\t') && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// splitICSLine splits "NAME;P1=a;P2="b:c":value". Names and parameter keys
// come back upper case.
func splitICSLine(line string) (name string, params map[string]string, value string) {
	quoted := false
	colon := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon < 0 {
		return "", nil, ""
	}
	head, value := line[:colon], line[colon+1:]
	parts := splitICSParams(head)
	name = strings.ToUpper(strings.TrimSpace(parts[0]))
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		if params == nil {
			params = map[string]string{}
		}
		params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	return name, params, value
}

func splitICSParams(head string) []string {
	var out []string
	quoted, start := false, 0
	for i := 0; i < len(head); i++ {
		switch head[i] {
		case '"':
			quoted = !quoted
		case ';':
			if !quoted {
				out = append(out, head[start:i])
				start = i + 1
			}
		}
	}
	return append(out, head[start:])
}

// icsText undoes TEXT escaping and keeps the result on one line.
func icsText(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case 'n', 'N':
				b.WriteByte(' ')
			default:
				b.WriteByte(v[i])
			}
			continue
		}
		b.WriteByte(c)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// parseICSTime reads a DATE or DATE-TIME. A DATE is an all-day value at
// midnight in loc; a time ending in Z is UTC; one with TZID is in that zone;
// any other is floating, read in loc.
func parseICSTime(v string, params map[string]string, loc *time.Location) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) == 8 || strings.EqualFold(params["VALUE"], "DATE") {
		t, err := time.ParseInLocation("20060102", v[:min(len(v), 8)], loc)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	}
	if strings.HasSuffix(v, "Z") || strings.HasSuffix(v, "z") {
		t, err := time.Parse("20060102T150405", v[:len(v)-1])
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), false
	}
	zone := loc
	if id := params["TZID"]; id != "" {
		zone = icsZone(id, loc)
	}
	t, err := time.ParseInLocation("20060102T150405", v, zone)
	if err != nil {
		// Some exporters leave the seconds out.
		if t, err = time.ParseInLocation("20060102T1504", v, zone); err != nil {
			return time.Time{}, false
		}
	}
	return t, false
}

// windowsZones maps the Windows zone names Outlook and Exchange write to IANA
// names. The rest fall back to the PC's zone.
var windowsZones = map[string]string{
	"Dateline Standard Time":          "Etc/GMT+12",
	"UTC-11":                          "Etc/GMT+11",
	"Hawaiian Standard Time":          "Pacific/Honolulu",
	"Alaskan Standard Time":           "America/Anchorage",
	"Pacific Standard Time":           "America/Los_Angeles",
	"Pacific Standard Time (Mexico)":  "America/Tijuana",
	"US Mountain Standard Time":       "America/Phoenix",
	"Mountain Standard Time":          "America/Denver",
	"Central Standard Time":           "America/Chicago",
	"Central Standard Time (Mexico)":  "America/Mexico_City",
	"Canada Central Standard Time":    "America/Regina",
	"Eastern Standard Time":           "America/New_York",
	"US Eastern Standard Time":        "America/Indianapolis",
	"Atlantic Standard Time":          "America/Halifax",
	"Newfoundland Standard Time":      "America/St_Johns",
	"E. South America Standard Time":  "America/Sao_Paulo",
	"Argentina Standard Time":         "America/Buenos_Aires",
	"SA Pacific Standard Time":        "America/Bogota",
	"UTC":                             "UTC",
	"Coordinated Universal Time":      "UTC",
	"GMT Standard Time":               "Europe/London",
	"Greenwich Standard Time":         "Atlantic/Reykjavik",
	"W. Europe Standard Time":         "Europe/Berlin",
	"Central Europe Standard Time":    "Europe/Budapest",
	"Romance Standard Time":           "Europe/Paris",
	"Central European Standard Time":  "Europe/Warsaw",
	"E. Europe Standard Time":         "Europe/Chisinau",
	"FLE Standard Time":               "Europe/Kiev",
	"GTB Standard Time":               "Europe/Bucharest",
	"Turkey Standard Time":            "Europe/Istanbul",
	"Israel Standard Time":            "Asia/Jerusalem",
	"South Africa Standard Time":      "Africa/Johannesburg",
	"Russian Standard Time":           "Europe/Moscow",
	"Arabian Standard Time":           "Asia/Dubai",
	"India Standard Time":             "Asia/Calcutta",
	"SE Asia Standard Time":           "Asia/Bangkok",
	"China Standard Time":             "Asia/Shanghai",
	"Singapore Standard Time":         "Asia/Singapore",
	"Taipei Standard Time":            "Asia/Taipei",
	"Tokyo Standard Time":             "Asia/Tokyo",
	"Korea Standard Time":             "Asia/Seoul",
	"AUS Eastern Standard Time":       "Australia/Sydney",
	"E. Australia Standard Time":      "Australia/Brisbane",
	"Cen. Australia Standard Time":    "Australia/Adelaide",
	"W. Australia Standard Time":      "Australia/Perth",
	"New Zealand Standard Time":       "Pacific/Auckland",
	"Mid-Atlantic Standard Time":      "Etc/GMT+2",
	"Azores Standard Time":            "Atlantic/Azores",
	"Morocco Standard Time":           "Africa/Casablanca",
	"W. Central Africa Standard Time": "Africa/Lagos",
	"Egypt Standard Time":             "Africa/Cairo",
}

var (
	icsZoneMu    sync.Mutex
	icsZoneCache = map[string]*time.Location{}
)

// icsZone resolves a TZID: an IANA name, one with a vendor prefix like
// "/mozilla.org/20050126_1/Europe/Berlin", or a Windows name.
func icsZone(id string, fallback *time.Location) *time.Location {
	id = strings.TrimSpace(id)
	icsZoneMu.Lock()
	defer icsZoneMu.Unlock()
	if l, ok := icsZoneCache[id]; ok {
		if l == nil {
			return fallback
		}
		return l
	}
	try := []string{id}
	if iana, ok := windowsZones[id]; ok {
		try = append([]string{iana}, try...)
	}
	if parts := strings.Split(strings.Trim(id, "/"), "/"); len(parts) >= 2 {
		try = append(try, strings.Join(parts[len(parts)-2:], "/"))
		if len(parts) >= 3 {
			try = append(try, strings.Join(parts[len(parts)-3:], "/"))
		}
	}
	for _, name := range try {
		if name == "" || strings.EqualFold(name, "local") {
			continue
		}
		if l, err := time.LoadLocation(name); err == nil {
			icsZoneCache[id] = l
			return l
		}
	}
	icsZoneCache[id] = nil
	return fallback
}

// parseICSDuration reads durations like P1D, PT1H30M, P2W or -PT15M.
func parseICSDuration(v string) (time.Duration, bool) {
	v = strings.ToUpper(strings.TrimSpace(v))
	neg := false
	switch {
	case strings.HasPrefix(v, "-"):
		neg, v = true, v[1:]
	case strings.HasPrefix(v, "+"):
		v = v[1:]
	}
	if !strings.HasPrefix(v, "P") {
		return 0, false
	}
	v = v[1:]
	var d time.Duration
	inTime := false
	numStart := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == 'T' {
			inTime, numStart = true, i+1
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		n, err := strconv.Atoi(v[numStart:i])
		if err != nil {
			return 0, false
		}
		unit := time.Duration(0)
		switch {
		case c == 'W' && !inTime:
			unit = 7 * 24 * time.Hour
		case c == 'D' && !inTime:
			unit = 24 * time.Hour
		case c == 'H' && inTime:
			unit = time.Hour
		case c == 'M' && inTime:
			unit = time.Minute
		case c == 'S' && inTime:
			unit = time.Second
		default:
			return 0, false
		}
		d += time.Duration(n) * unit
		numStart = i + 1
	}
	if numStart != len(v) {
		return 0, false
	}
	if neg {
		d = -d
	}
	return d, true
}

var icsWeekdays = map[string]time.Weekday{
	"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday,
	"TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday,
}

// parseICSRule reads the supported part of an RRULE. It returns nil for a
// rule this reader cannot follow, which leaves just the first instance.
func parseICSRule(v string, params map[string]string, loc *time.Location) *icsRule {
	r := &icsRule{Interval: 1}
	for _, part := range strings.Split(v, ";") {
		k, val, _ := strings.Cut(part, "=")
		k, val = strings.ToUpper(strings.TrimSpace(k)), strings.TrimSpace(val)
		switch k {
		case "FREQ":
			r.Freq = strings.ToUpper(val)
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return nil
			}
			r.Interval = min(n, 1000)
		case "COUNT":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return nil
			}
			r.Count = n
		case "UNTIL":
			r.Until, _ = parseICSTime(val, nil, loc)
			if r.Until.IsZero() {
				return nil
			}
			if len(val) == 8 {
				// A date includes that whole day.
				r.Until = r.Until.AddDate(0, 0, 1).Add(-time.Second)
			}
		case "BYDAY":
			for _, d := range strings.Split(val, ",") {
				wd, ok := icsWeekdays[strings.ToUpper(strings.TrimSpace(d))]
				if !ok {
					return nil // "1MO", "-1FR" and so on
				}
				r.ByDay = append(r.ByDay, wd)
			}
		case "WKST", "":
		default:
			// BYMONTHDAY, BYSETPOS, BYMONTH...: not followed.
			return nil
		}
	}
	switch r.Freq {
	case "DAILY", "WEEKLY":
	case "MONTHLY", "YEARLY":
		if len(r.ByDay) > 0 {
			return nil
		}
	default:
		return nil
	}
	if r.Freq != "WEEKLY" && len(r.ByDay) > 0 {
		return nil
	}
	return r
}

// eachStart calls fn with every instance start of e in order, until fn
// returns false. Skipped dates (EXDATE) still count toward COUNT, as the
// standard says.
func (e icsEvent) eachStart(fn func(start time.Time) bool) {
	emit := func(t time.Time) bool {
		if e.ExDates[t.Unix()] {
			return true
		}
		return fn(t)
	}
	r := e.Rule
	if r == nil {
		emit(e.Start)
		return
	}
	n := 0
	ok := func(t time.Time) bool {
		if !r.Until.IsZero() && t.After(r.Until) {
			return false
		}
		if r.Count > 0 && n >= r.Count {
			return false
		}
		n++
		return true
	}
	loc := e.Start.Location()
	y, mo, d := e.Start.Date()
	hh, mm, ss := e.Start.Clock()
	at := func(y int, mo time.Month, d int) time.Time {
		return time.Date(y, mo, d, hh, mm, ss, 0, loc)
	}
	switch r.Freq {
	case "DAILY":
		for k := 0; k < icsMaxSteps; k++ {
			t := at(y, mo, d+k*r.Interval)
			if !ok(t) || !emit(t) {
				return
			}
		}
	case "WEEKLY":
		days := r.ByDay
		if len(days) == 0 {
			days = []time.Weekday{e.Start.Weekday()}
		}
		// Weeks start on Monday (WKST's default).
		offs := make([]int, 0, len(days))
		for _, wd := range days {
			offs = append(offs, (int(wd)+6)%7)
		}
		sort.Ints(offs)
		weekStart := d - (int(e.Start.Weekday())+6)%7
		for k := 0; k < icsMaxSteps/7; k++ {
			for i, off := range offs {
				if i > 0 && off == offs[i-1] {
					continue
				}
				t := at(y, mo, weekStart+k*7*r.Interval+off)
				if t.Before(e.Start) {
					continue
				}
				if !ok(t) || !emit(t) {
					return
				}
			}
		}
	case "MONTHLY", "YEARLY":
		for k := 0; k < icsMaxSteps/10; k++ {
			ty, tm := y, mo
			if r.Freq == "MONTHLY" {
				tm += time.Month(k * r.Interval)
			} else {
				ty += k * r.Interval
			}
			t := at(ty, tm, d)
			if t.Day() != d {
				continue // the 31st, or Feb 29, in a month without it
			}
			if !ok(t) || !emit(t) {
				return
			}
		}
	}
}

// expandICS lists the instances of events that are still on or start before
// to, sorted by start. Moved instances replace the ones they came from;
// canceled ones are left out.
func expandICS(events []icsEvent, from, to time.Time) []calInstance {
	moved := map[string]map[int64]bool{}
	for _, e := range events {
		if !e.RecurID.IsZero() && e.UID != "" {
			if moved[e.UID] == nil {
				moved[e.UID] = map[int64]bool{}
			}
			moved[e.UID][e.RecurID.Unix()] = true
		}
	}
	var out []calInstance
	for _, e := range events {
		if e.Canceled {
			continue
		}
		length := e.End.Sub(e.Start)
		skip := map[int64]bool(nil)
		if e.RecurID.IsZero() {
			skip = moved[e.UID]
		}
		e.eachStart(func(start time.Time) bool {
			if !start.Before(to) {
				return false
			}
			end := start.Add(length)
			if e.AllDay {
				// Keep all-day events on their dates across clock changes.
				days := int(length.Hours()/24 + 0.5)
				end = start.AddDate(0, 0, max(days, 1))
			}
			if end.After(from) && !skip[start.Unix()] {
				out = append(out, calInstance{Title: e.Summary, Location: e.Location, Start: start, End: end, AllDay: e.AllDay})
			}
			return len(out) < 5000
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Title < out[j].Title
	})
	return out
}
