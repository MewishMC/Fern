package modules

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleICS = "BEGIN:VCALENDAR\r\n" +
	"VERSION:2.0\r\n" +
	"BEGIN:VTIMEZONE\r\nTZID:W. Europe Standard Time\r\nEND:VTIMEZONE\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:standup\r\n" +
	"SUMMARY:Stand\r\n  up\\, daily\r\n" + // folding drops exactly one space
	"DTSTART;TZID=\"Europe/Berlin\":20261102T093000\r\n" +
	"DTEND;TZID=Europe/Berlin:20261102T094500\r\n" +
	"RRULE:FREQ=WEEKLY;BYDAY=MO,WE,FR;COUNT=6\r\n" +
	"EXDATE;TZID=Europe/Berlin:20261104T093000\r\n" +
	"BEGIN:VALARM\r\nSUMMARY:alarm text\r\nTRIGGER:-PT10M\r\nEND:VALARM\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:standup\r\n" +
	"RECURRENCE-ID;TZID=Europe/Berlin:20261106T093000\r\n" +
	"SUMMARY:Stand up (moved)\r\n" +
	"DTSTART;TZID=Europe/Berlin:20261106T110000\r\n" +
	"DURATION:PT15M\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:bday\r\n" +
	"SUMMARY:Birthday\r\n" +
	"DTSTART;VALUE=DATE:20261110\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:call\r\n" +
	"SUMMARY:Call\r\n" +
	"LOCATION:Room 4\r\n" +
	"DTSTART:20261110T150000Z\r\n" +
	"DTEND:20261110T160000Z\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:gone\r\n" +
	"SUMMARY:Canceled\r\n" +
	"STATUS:CANCELLED\r\n" +
	"DTSTART:20261110T120000Z\r\n" +
	"END:VEVENT\r\n" +
	"BEGIN:VEVENT\r\n" +
	"UID:gym\r\n" +
	"SUMMARY:Gym\r\n" +
	"DTSTART;TZID=W. Europe Standard Time:20261101T180000\r\n" +
	"DTEND;TZID=W. Europe Standard Time:20261101T190000\r\n" +
	"RRULE:FREQ=DAILY;INTERVAL=2;UNTIL=20261107T235959Z\r\n" +
	"END:VEVENT\r\n" +
	"END:VCALENDAR\r\n"

func TestParseICS(t *testing.T) {
	events := parseICS([]byte(sampleICS), time.UTC)
	if len(events) != 6 {
		t.Fatalf("got %d events, want 6", len(events))
	}
	e := events[0]
	if e.Summary != "Stand up, daily" {
		t.Errorf("summary %q: folding, escapes or the alarm went wrong", e.Summary)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if !e.Start.Equal(time.Date(2026, 11, 2, 9, 30, 0, 0, berlin)) || e.End.Sub(e.Start) != 15*time.Minute {
		t.Errorf("start %v end %v", e.Start, e.End)
	}
	if e.Rule == nil || e.Rule.Freq != "WEEKLY" || e.Rule.Count != 6 || len(e.Rule.ByDay) != 3 {
		t.Errorf("rule %+v", e.Rule)
	}
	if !events[2].AllDay || !events[2].End.Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("all-day %+v", events[2])
	}
	if events[1].End.Sub(events[1].Start) != 15*time.Minute {
		t.Errorf("duration not applied: %v", events[1].End.Sub(events[1].Start))
	}
	if events[5].Start.Location().String() != "Europe/Berlin" {
		t.Errorf("windows zone read as %v", events[5].Start.Location())
	}
}

func instTitles(list []calInstance, loc *time.Location) []string {
	var out []string
	for _, e := range list {
		out = append(out, e.Start.In(loc).Format("Jan 2 15:04")+" "+e.Title)
	}
	return out
}

func TestExpandICS(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	events := parseICS([]byte(sampleICS), berlin)
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, berlin)
	got := instTitles(expandICS(events, from, from.AddDate(0, 0, 30)), berlin)
	want := []string{
		"Nov 1 18:00 Gym",
		"Nov 2 09:30 Stand up, daily",
		"Nov 3 18:00 Gym",
		// Nov 4 skipped by EXDATE; Nov 6 moved to 11:00.
		"Nov 5 18:00 Gym",
		"Nov 6 11:00 Stand up (moved)",
		"Nov 7 18:00 Gym",
		"Nov 9 09:30 Stand up, daily",
		"Nov 10 00:00 Birthday",
		"Nov 10 16:00 Call",
		"Nov 11 09:30 Stand up, daily",
		// COUNT=6 counts Nov 2, 4, 6, 9, 11, 13.
		"Nov 13 09:30 Stand up, daily",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("instances:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Only instances still on after from are kept.
	later := expandICS(events, time.Date(2026, 11, 10, 15, 30, 0, 0, time.UTC), from.AddDate(0, 0, 30))
	if len(later) == 0 || later[0].Title != "Birthday" || later[1].Title != "Call" {
		t.Errorf("after from: %v", instTitles(later, berlin))
	}
}

func TestICSRulesAcrossDST(t *testing.T) {
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nSUMMARY:Weekly\nDTSTART;TZID=America/New_York:20261025T100000\nRRULE:FREQ=WEEKLY\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nSUMMARY:Monthly\nDTSTART:20260131T100000Z\nRRULE:FREQ=MONTHLY;COUNT=3\nEND:VEVENT\n" +
		"BEGIN:VEVENT\nSUMMARY:Odd\nDTSTART:20260101T100000Z\nRRULE:FREQ=MONTHLY;BYDAY=2MO\nEND:VEVENT\nEND:VCALENDAR\n"
	events := parseICS([]byte(ics), time.UTC)
	ny, _ := time.LoadLocation("America/New_York")
	list := expandICS(events, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC))
	var weekly, monthly, odd []string
	for _, e := range list {
		switch e.Title {
		case "Weekly":
			weekly = append(weekly, e.Start.In(ny).Format("Jan 2 15:04"))
		case "Monthly":
			monthly = append(monthly, e.Start.Format("Jan 2"))
		case "Odd":
			odd = append(odd, e.Start.Format("Jan 2"))
		}
	}
	if strings.Join(weekly, ",") != "Oct 25 10:00,Nov 1 10:00,Nov 8 10:00" {
		t.Errorf("weekly kept its wall time across DST: %v", weekly)
	}
	// Months without a 31st are skipped but still within COUNT's walk.
	if strings.Join(monthly, ",") != "Jan 31,Mar 31,May 31" {
		t.Errorf("monthly: %v", monthly)
	}
	if strings.Join(odd, ",") != "Jan 1" {
		t.Errorf("unsupported rule should leave the first instance only: %v", odd)
	}
}

func TestParseICSDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT1H30M": 90 * time.Minute, "P1D": 24 * time.Hour, "P1W": 7 * 24 * time.Hour,
		"-PT15M": -15 * time.Minute, "P1DT2H": 26 * time.Hour,
	} {
		if got, ok := parseICSDuration(in); !ok || got != want {
			t.Errorf("%s = %v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "1H", "PTXM", "PT1D"} {
		if _, ok := parseICSDuration(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCalendarPickAndFormat(t *testing.T) {
	now := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC)
	o := defaultCalendarOptions()
	list := []calInstance{
		{Title: "Holiday", Start: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC), AllDay: true},
		{Title: "Lunch", Start: now.Add(-time.Hour), End: now.Add(-time.Minute)},
		{Title: "Meeting", Start: now.Add(-10 * time.Minute), End: now.Add(20 * time.Minute)},
		{Title: "Dentist", Start: now.Add(2*time.Hour + 5*time.Minute), End: now.Add(3 * time.Hour), Location: "Main St"},
	}
	e, running, ok := calendarPick(o, list, now)
	if !ok || e.Title != "Meeting" || !running {
		t.Fatalf("pick = %q %v %v", e.Title, running, ok)
	}
	if got := formatCalendar(o, e, running, now, true); got != "Now: Meeting, 20m left" {
		t.Errorf("now line %q", got)
	}
	o.ShowCurrent = false
	e, running, _ = calendarPick(o, list, now)
	if e.Title != "Dentist" || running {
		t.Fatalf("without current: %q", e.Title)
	}
	if got := formatCalendar(o, e, running, now, true); got != "Next: Dentist in 2h 5m" {
		t.Errorf("next line %q", got)
	}
	o.Template = "{title} {when} @ {location} ({time}-{end})"
	if got := formatCalendar(o, e, running, now, false); got != "Dentist today 4:05 PM @ Main St (4:05 PM-5:00 PM)" {
		t.Errorf("placeholders %q", got)
	}
	o.WithinHours = 1
	if _, _, ok := calendarPick(o, list, now); ok {
		t.Error("an event beyond the window was shown")
	}
	// The running all-day event shows only when nothing else is due.
	o.ShowCurrent = true
	e, _, _ = calendarPick(o, list[:2], now)
	if e.Title != "Holiday" {
		t.Errorf("all-day fallback: %q", e.Title)
	}
	o.AllDay = false
	if _, _, ok := calendarPick(o, list[:2], now); ok {
		t.Error("all-day event shown with all-day events off")
	}
	o.AllDay, o.Skip = true, "/^holi/"
	if _, _, ok := calendarPick(o, list[:2], now); ok {
		t.Error("skipped title shown")
	}
}

func TestCalendarSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "1m", 25 * time.Minute: "25m", 59*time.Minute + time.Second: "1h",
		2*time.Hour + 5*time.Minute: "2h 5m", 27 * time.Hour: "1d 3h", 48 * time.Hour: "2d",
	} {
		if got := calendarSpan(d); got != want {
			t.Errorf("%v = %q, want %q", d, got, want)
		}
	}
}

func TestSanitizeCalendar(t *testing.T) {
	o := CalendarOptions{Files: []string{` "C:\a.ics" `, `c:\A.ics`, "", "b", "c", "d", "e", "f"}, WithinHours: 999, IntervalMin: 1, Template: " a \n b "}
	sanitizeCalendar(&o)
	if len(o.Files) != MaxCalendarFiles || o.Files[0] != `C:\a.ics` || o.Files[1] != "b" {
		t.Errorf("files %q", o.Files)
	}
	if o.WithinHours != 168 || o.IntervalMin != 5 || o.Template != "a b" || o.NowTemplate == "" || o.MaxTitle != 40 {
		t.Errorf("sanitized %+v", o)
	}
	d := DefaultConfig()
	d.sanitize()
	if d.CalendarOptions.Template != "Next: {title} in {in}" || d.CalendarOptions.WithinHours != 12 {
		t.Errorf("defaults changed by sanitize: %+v", d.CalendarOptions)
	}
}

func TestCalendarLinks(t *testing.T) {
	if got := calendarHTTPURL("webcal://example.com/x.ics"); got != "https://example.com/x.ics" {
		t.Errorf("webcal: %q", got)
	}
	if got := calendarLinkName("https://calendar.example.com/private-abc123/basic.ics"); got != "calendar.example.com/…" {
		t.Errorf("name shows more than the host: %q", got)
	}
	for _, bad := range []string{"", "ftp://x/y", "not a link", "C:\\cal.ics"} {
		if _, err := normalizeCalendarLink(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestFetchCalendarHidesTheLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok.ics" {
			_, _ = w.Write([]byte(sampleICS))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	data, err := fetchCalendar(srv.URL + "/ok.ics")
	if err != nil || !strings.Contains(string(data), "VCALENDAR") {
		t.Fatalf("fetch: %v", err)
	}
	_, err = fetchCalendar(srv.URL + "/secret-key-123.ics")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Errorf("404 error %v", err)
	}
	srv.Close()
	_, err = fetchCalendar("http://127.0.0.1:1/secret-key-123.ics")
	if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("connect error leaks the link: %v", err)
	}
}

func TestCalendarTickReadsFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cal.ics")
	now := time.Now()
	start := now.Add(30 * time.Minute).UTC().Format("20060102T150405Z")
	ics := "BEGIN:VCALENDAR\nBEGIN:VEVENT\nSUMMARY:Raid night\nDTSTART:" + start + "\nDURATION:PT1H\nEND:VEVENT\nEND:VCALENDAR\n"
	if err := os.WriteFile(path, []byte(ics), 0o600); err != nil {
		t.Fatal(err)
	}
	secretsMu.Lock()
	oldCache, oldLoaded := secretsCache, secretsLoaded
	secretsCache, secretsLoaded = map[string]string{}, true
	secretsMu.Unlock()
	t.Cleanup(func() {
		secretsMu.Lock()
		secretsCache, secretsLoaded = oldCache, oldLoaded
		secretsMu.Unlock()
		calMu.Lock()
		calSources, calInst, calExpanded = map[string]*calSource{}, nil, time.Time{}
		calMu.Unlock()
	})
	c := DefaultConfig()
	c.DisplayOptions.Calendar = true
	c.CalendarOptions.Files = []string{path, filepath.Join(dir, "missing.ics")}
	useConfig(t, c)
	calendarTick(now)
	if got := calendarText(c, now); got != "Next: Raid night in 30m" {
		t.Errorf("line %q", got)
	}
	st := CurrentCalendarStatus()
	if st.State != "ok" || len(st.Sources) != 2 || st.Sources[0].Events != 1 || st.Sources[1].Error == "" {
		t.Errorf("status %+v", st)
	}
	if len(st.Upcoming) != 1 || st.Upcoming[0].Title != "Raid night" {
		t.Errorf("upcoming %+v", st.Upcoming)
	}
}
