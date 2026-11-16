package modules

// The calendar line: the next event from one or more iCalendar links or local
// .ics files. Links often carry a private key in the address (Google's
// "secret address", Outlook's published calendars), so they are kept in the
// encrypted secrets store and only shown back as their host.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ModuleCalendar is the calendar line's id.
const ModuleCalendar = "calendar"

// SecretCalendarURLs holds the calendar links, one per line.
const SecretCalendarURLs = "calendar_urls"

// Limits on calendar sources.
const (
	MaxCalendarLinks = 5
	MaxCalendarFiles = 5
	calendarMaxBytes = 5 << 20
)

// CalendarOptions controls the calendar line.
type CalendarOptions struct {
	Files       []string `json:"files"`        // local .ics files
	Template    string   `json:"template"`     // the next event, like "Next: {title} in {in}"
	NowTemplate string   `json:"now_template"` // an event in progress
	ShowCurrent bool     `json:"show_current"` // show events in progress
	AllDay      bool     `json:"all_day"`      // include all-day events
	WithinHours int      `json:"within_hours"` // hide the line when nothing starts this soon
	IntervalMin int      `json:"interval_min"` // minutes between downloads of the links
	Skip        string   `json:"skip"`         // titles to leave out: words or /regex/
	MaxTitle    int      `json:"max_title"`    // longest title, in characters
	Label       string   `json:"label"`
}

func defaultCalendarOptions() CalendarOptions {
	return CalendarOptions{
		Files:       []string{},
		Template:    "Next: {title} in {in}",
		NowTemplate: "Now: {title}, {left} left",
		ShowCurrent: true,
		AllDay:      true,
		WithinHours: 12,
		IntervalMin: 15,
		MaxTitle:    40,
	}
}

func sanitizeCalendar(o *CalendarOptions) {
	files := make([]string, 0, len(o.Files))
	seen := map[string]bool{}
	for _, f := range o.Files {
		f = strings.Trim(strings.TrimSpace(f), `"`)
		f = RuneLimit(f, 260)
		if f == "" || seen[strings.ToLower(f)] || len(files) >= MaxCalendarFiles {
			continue
		}
		seen[strings.ToLower(f)] = true
		files = append(files, f)
	}
	o.Files = files
	o.Template = oneLineTemplate(o.Template, 120)
	if o.Template == "" {
		o.Template = "Next: {title} in {in}"
	}
	o.NowTemplate = oneLineTemplate(o.NowTemplate, 120)
	if o.NowTemplate == "" {
		o.NowTemplate = "Now: {title}, {left} left"
	}
	o.WithinHours = clampInt(o.WithinHours, 1, 168)
	o.IntervalMin = clampInt(o.IntervalMin, 5, 240)
	o.Skip = RuneLimit(strings.TrimSpace(o.Skip), 200)
	if o.MaxTitle == 0 {
		o.MaxTitle = 40
	}
	o.MaxTitle = clampInt(o.MaxTitle, 8, 144)
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
}

// oneLineTemplate keeps a template on one line and within n characters.
func oneLineTemplate(s string, n int) string {
	return RuneLimit(strings.Join(strings.Fields(s), " "), n)
}

// CalendarSource is one link or file, for the panel. A link is shown only by
// its host.
type CalendarSource struct {
	Kind      string `json:"kind"` // "link" or "file"
	Name      string `json:"name"`
	Index     int    `json:"index"` // position among links or files
	Events    int    `json:"events"`
	Error     string `json:"error"`
	UpdatedMS int64  `json:"updatedMs"`
}

// CalendarEvent is an upcoming event, for the panel.
type CalendarEvent struct {
	Title   string `json:"title"`
	StartMS int64  `json:"startMs"`
	EndMS   int64  `json:"endMs"`
	AllDay  bool   `json:"allDay"`
}

// PanelNotice is a one-off message for the panel; Seq changes with each one.
type PanelNotice struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
	Bad  bool   `json:"bad"`
}

// CalendarStatus is what the panel shows about the calendar.
type CalendarStatus struct {
	State    string           `json:"state"` // off, nosource, loading, ok, error
	Sources  []CalendarSource `json:"sources"`
	Upcoming []CalendarEvent  `json:"upcoming"`
	Line     string           `json:"line"`
	Notice   PanelNotice      `json:"notice"`
}

type calSource struct {
	kind    string // link or file
	key     string // the address or path
	events  []icsEvent
	have    bool
	fetched time.Time
	retryAt time.Time
	fails   int
	err     string
	modTime time.Time
	size    int64
}

var (
	calMu       sync.RWMutex
	calSources  = map[string]*calSource{}
	calInst     []calInstance
	calExpanded time.Time
	calNotice   PanelNotice
	calWake     = make(chan struct{}, 1)
)

// calendarHorizon is how far ahead instances are listed, past the longest
// "within" setting so changing it needs no new pass.
const calendarHorizon = 169 * time.Hour

func startCalendarWorker() {
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			calendarTick(time.Now())
			select {
			case <-ticker.C:
			case <-calWake:
			}
		}
	}()
}

func wakeCalendar() {
	select {
	case calWake <- struct{}{}:
	default:
	}
}

// calendarLinks reads the stored links.
func calendarLinks() []string {
	var out []string
	for _, l := range strings.Split(GetSecret(SecretCalendarURLs), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// calendarTick downloads due links, re-reads changed files and refreshes the
// list of instances. It runs on the calendar worker only.
func calendarTick(now time.Time) {
	cfg := GetConfig()
	if !cfg.DisplayOptions.Calendar {
		return
	}
	o := cfg.CalendarOptions
	want := map[string]string{}
	for _, l := range calendarLinks() {
		want["link\x00"+l] = "link"
	}
	for _, f := range o.Files {
		want["file\x00"+f] = "file"
	}

	calMu.Lock()
	changed := false
	for id := range calSources {
		if _, ok := want[id]; !ok {
			delete(calSources, id)
			changed = true
		}
	}
	var due []*calSource
	for id, kind := range want {
		s := calSources[id]
		if s == nil {
			_, key, _ := strings.Cut(id, "\x00")
			s = &calSource{kind: kind, key: key}
			calSources[id] = s
		}
		if now.Before(s.retryAt) {
			continue
		}
		if kind == "file" || !s.have || now.Sub(s.fetched) >= time.Duration(o.IntervalMin)*time.Minute {
			due = append(due, s)
		}
	}
	calMu.Unlock()

	for _, s := range due {
		if loadCalendarSource(s, now) {
			changed = true
		}
	}

	calMu.Lock()
	defer calMu.Unlock()
	if !changed && now.Sub(calExpanded) < 5*time.Minute {
		return
	}
	var all []icsEvent
	for _, s := range calSources {
		all = append(all, s.events...)
	}
	calInst = expandICS(all, now.Add(-time.Minute), now.Add(calendarHorizon))
	calExpanded = now
}

// loadCalendarSource reads one source and reports whether its events changed.
// Files are only read again when they change on disk.
func loadCalendarSource(s *calSource, now time.Time) bool {
	var (
		data []byte
		err  error
	)
	if s.kind == "file" {
		info, serr := os.Stat(s.key)
		if serr == nil && s.have && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
			return false
		}
		switch {
		case serr != nil:
			err = errors.New("file not found")
		case info.IsDir():
			err = errors.New("that is a folder, not a file")
		case info.Size() > calendarMaxBytes:
			err = errors.New("file is larger than 5 MB")
		default:
			data, err = os.ReadFile(s.key)
			if err == nil {
				calMu.Lock()
				s.modTime, s.size = info.ModTime(), info.Size()
				calMu.Unlock()
			}
		}
	} else {
		data, err = fetchCalendar(s.key)
	}
	var events []icsEvent
	if err == nil {
		events = parseICS(data, time.Local)
		if len(events) == 0 && !strings.Contains(string(data[:min(len(data), 4096)]), "BEGIN:VCALENDAR") {
			err = errors.New("not an iCalendar file")
		}
	}

	calMu.Lock()
	defer calMu.Unlock()
	if err != nil {
		s.fails++
		wait := time.Minute << min(s.fails-1, 5)
		if s.kind == "file" {
			wait = 20 * time.Second
		}
		s.retryAt = now.Add(wait)
		s.err = err.Error()
		if s.kind == "file" && s.have {
			s.events, s.have = nil, false
			return true
		}
		return false
	}
	s.fails, s.err, s.retryAt = 0, "", time.Time{}
	s.events, s.have, s.fetched = events, true, now
	return true
}

// fetchCalendar downloads a calendar link. Errors never include the address,
// which may hold a private key.
func fetchCalendar(link string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, calendarHTTPURL(link), nil)
	if err != nil {
		return nil, errors.New("bad link")
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	resp, err := webClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			if ue.Timeout() {
				return nil, errors.New("timed out")
			}
			return nil, errors.New("couldn't connect")
		}
		return nil, errors.New("couldn't connect")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errRateLimited
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, errors.New("not found (the link may have been reset)")
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, errors.New("access denied")
	case resp.StatusCode != http.StatusOK:
		return nil, httpStatusError(resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, calendarMaxBytes+1))
	if err != nil {
		return nil, errors.New("download interrupted")
	}
	if len(data) > calendarMaxBytes {
		return nil, errors.New("calendar is larger than 5 MB")
	}
	return data, nil
}

// calendarHTTPURL turns webcal:// links into https://.
func calendarHTTPURL(link string) string {
	if rest, ok := cutPrefixFold(link, "webcals://"); ok {
		return "https://" + rest
	}
	if rest, ok := cutPrefixFold(link, "webcal://"); ok {
		return "https://" + rest
	}
	return link
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// normalizeCalendarLink checks a pasted link and returns the form to store.
func normalizeCalendarLink(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("Paste a calendar link first")
	}
	u, err := url.Parse(calendarHTTPURL(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("That doesn't look like a calendar link (https:// or webcal://)")
	}
	if len(raw) > 2000 {
		return "", errors.New("That link is too long")
	}
	return raw, nil
}

// calendarLinkName is how a link is shown: its host only.
func calendarLinkName(link string) string {
	u, err := url.Parse(calendarHTTPURL(link))
	if err != nil || u.Host == "" {
		return "link"
	}
	return u.Hostname() + "/…"
}

func setCalendarNotice(text string, bad bool) {
	calMu.Lock()
	calNotice = PanelNotice{Seq: calNotice.Seq + 1, Text: text, Bad: bad}
	calMu.Unlock()
	NotifyChange()
}

// AddCalendarLink stores another calendar link.
func AddCalendarLink(raw string) error {
	link, err := normalizeCalendarLink(raw)
	if err == nil {
		links := calendarLinks()
		switch {
		case len(links) >= MaxCalendarLinks:
			err = fmt.Errorf("Up to %d calendar links", MaxCalendarLinks)
		default:
			for _, l := range links {
				if strings.EqualFold(calendarHTTPURL(l), calendarHTTPURL(link)) {
					err = errors.New("That calendar is already added")
				}
			}
		}
		if err == nil {
			err = SetSecret(SecretCalendarURLs, strings.Join(append(links, link), "\n"))
		}
	}
	if err != nil {
		setCalendarNotice(err.Error(), true)
		return err
	}
	setCalendarNotice("Calendar added", false)
	wakeCalendar()
	return nil
}

// RemoveCalendarLink forgets the link at index i.
func RemoveCalendarLink(i int) error {
	links := calendarLinks()
	if i < 0 || i >= len(links) {
		return errors.New("no such calendar")
	}
	links = append(links[:i], links[i+1:]...)
	if err := SetSecret(SecretCalendarURLs, strings.Join(links, "\n")); err != nil {
		setCalendarNotice("Couldn't save: "+err.Error(), true)
		return err
	}
	setCalendarNotice("Calendar removed", false)
	wakeCalendar()
	return nil
}

// RefreshCalendar downloads every link again now.
func RefreshCalendar() {
	calMu.Lock()
	for _, s := range calSources {
		s.fetched, s.retryAt, s.have = time.Time{}, time.Time{}, false
	}
	calMu.Unlock()
	wakeCalendar()
}

// calendarPick chooses what the line shows at now: an event in progress (when
// enabled), else the next one starting within the window, else an all-day
// event in progress.
func calendarPick(o CalendarOptions, list []calInstance, now time.Time) (calInstance, bool, bool) {
	within := now.Add(time.Duration(o.WithinHours) * time.Hour)
	var allDayNow *calInstance
	for i := range list {
		e := list[i]
		if !e.End.After(now) || (e.AllDay && !o.AllDay) {
			continue
		}
		if o.Skip != "" && MatchText(o.Skip, e.Title) {
			continue
		}
		if e.Start.After(within) {
			break
		}
		running := !e.Start.After(now)
		if running {
			if !o.ShowCurrent {
				continue
			}
			if e.AllDay {
				if allDayNow == nil {
					allDayNow = &list[i]
				}
				continue
			}
			return e, true, true
		}
		// The first upcoming one. A running timed event would have been
		// returned already, since the list is sorted by start.
		return e, false, true
	}
	if allDayNow != nil {
		return *allDayNow, true, true
	}
	return calInstance{}, false, false
}

// calendarSpan writes a length of time as "25m", "2h 5m" or "1d 3h", rounding
// minutes up so "in 1m" never shows for an event that is about to start.
func calendarSpan(d time.Duration) string {
	mins := int((d + time.Minute - 1) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	switch {
	case mins >= 24*60:
		days, hours := mins/(24*60), (mins%(24*60))/60
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd %dh", days, hours)
	case mins >= 60:
		if mins%60 == 0 {
			return fmt.Sprintf("%dh", mins/60)
		}
		return fmt.Sprintf("%dh %dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

func calendarClock(t time.Time, h24 bool) string {
	if h24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

// calendarWhen is "today 3:00 PM", "tomorrow", "Mon 9:00 AM" or "Nov 20".
func calendarWhen(e calInstance, now time.Time, h24 bool) string {
	start := e.Start.In(now.Location())
	y1, m1, d1 := now.Date()
	y2, m2, d2 := start.Date()
	days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)).Hours() / 24)
	day := start.Format("Jan 2")
	switch {
	case days <= 0:
		day = "today"
	case days == 1:
		day = "tomorrow"
	case days < 7:
		day = start.Format("Mon")
	}
	if e.AllDay {
		return day
	}
	return day + " " + calendarClock(start, h24)
}

// formatCalendar fills in the template for one event.
func formatCalendar(o CalendarOptions, e calInstance, running bool, now time.Time, h24 bool) string {
	tpl := o.Template
	if running {
		tpl = o.NowTemplate
	}
	in := calendarSpan(e.Start.Sub(now))
	if running {
		in = "now"
	}
	start, end := e.Start.In(now.Location()), e.End.In(now.Location())
	tm, endTm := calendarClock(start, h24), calendarClock(end, h24)
	if e.AllDay {
		tm, endTm = "all day", "all day"
	}
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	r := strings.NewReplacer(
		"{title}", ellipsize(title, o.MaxTitle),
		"{in}", in,
		"{left}", calendarSpan(e.End.Sub(now)),
		"{time}", tm,
		"{end}", endTm,
		"{date}", start.Format("Mon Jan 2"),
		"{when}", calendarWhen(e, now, h24),
		"{location}", e.Location,
	)
	return strings.Join(strings.Fields(r.Replace(tpl)), " ")
}

// calendarText is the line's text at now, or "" when nothing is due.
func calendarText(cfg Config, now time.Time) string {
	if !cfg.DisplayOptions.Calendar {
		return ""
	}
	calMu.RLock()
	list := calInst
	calMu.RUnlock()
	e, running, ok := calendarPick(cfg.CalendarOptions, list, now)
	if !ok {
		return ""
	}
	return formatCalendar(cfg.CalendarOptions, e, running, now, cfg.TimeOptions.Use24h)
}

func calendarLine(cfg Config) string {
	return withLabel(cfg.CalendarOptions.Label, calendarText(cfg, time.Now()))
}

// CurrentCalendarStatus reports the calendar for the panel.
func CurrentCalendarStatus() CalendarStatus {
	cfg := GetConfig()
	now := time.Now()
	links := calendarLinks()
	st := CalendarStatus{State: "off", Sources: []CalendarSource{}, Upcoming: []CalendarEvent{}}
	calMu.RLock()
	st.Notice = calNotice
	loading, failed, okCount := false, false, 0
	add := func(kind, key, name string, i int) {
		src := CalendarSource{Kind: kind, Name: name, Index: i}
		if s := calSources[kind+"\x00"+key]; s != nil {
			src.Events, src.Error = len(s.events), s.err
			if s.have {
				src.UpdatedMS = s.fetched.UnixMilli()
				okCount++
			} else if s.err != "" {
				failed = true
			} else {
				loading = true
			}
		} else {
			loading = true
		}
		st.Sources = append(st.Sources, src)
	}
	for i, l := range links {
		add("link", l, calendarLinkName(l), i)
	}
	for i, f := range cfg.CalendarOptions.Files {
		add("file", f, filepath.Base(f), i)
	}
	o := cfg.CalendarOptions
	for _, e := range calInst {
		if len(st.Upcoming) >= 5 {
			break
		}
		if !e.End.After(now) || (e.AllDay && !o.AllDay) || (o.Skip != "" && MatchText(o.Skip, e.Title)) {
			continue
		}
		st.Upcoming = append(st.Upcoming, CalendarEvent{Title: RuneLimit(e.Title, 80), StartMS: e.Start.UnixMilli(), EndMS: e.End.UnixMilli(), AllDay: e.AllDay})
	}
	calMu.RUnlock()
	switch {
	case !cfg.DisplayOptions.Calendar:
		st.State = "off"
	case len(st.Sources) == 0:
		st.State = "nosource"
	case okCount > 0:
		st.State = "ok"
	case failed && !loading:
		st.State = "error"
	default:
		st.State = "loading"
	}
	st.Line = calendarText(cfg, now)
	return st
}

// calendarMatches reports whether fn accepts any instance starting within
// the next five hours (or already running).
func calendarMatches(now time.Time, fn func(e calInstance) bool) bool {
	calMu.RLock()
	defer calMu.RUnlock()
	limit := now.Add(5 * time.Hour)
	for _, e := range calInst {
		if e.Start.After(limit) {
			break
		}
		if fn(e) {
			return true
		}
	}
	return false
}

// Rule trigger ids for the calendar.
const (
	TriggerCalendarNow  = "calendar.now"
	TriggerCalendarSoon = "calendar.soon"
)

func init() {
	lineLabels[ModuleCalendar] = "Calendar"
	titleField := RuleField{Key: "title", Label: "Title", Type: FieldText, Placeholder: "any event", Hint: "Contains, or /regex/. Empty matches every event.", MaxLen: 200}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerCalendarNow, Label: "Calendar event on", Category: "Calendar",
		Description: "While an event from the calendar line's calendars is happening",
		Fields: []RuleField{
			titleField,
			{Key: "all_day", Label: "Count all-day events", Type: FieldBool, Default: false},
		},
		Check: func(p RuleParams) bool {
			now := nowFunc()
			pat, allDay := p.String("title"), p.Bool("all_day")
			return calendarMatches(now, func(e calInstance) bool {
				return !e.Start.After(now) && e.End.After(now) && (allDay || !e.AllDay) && MatchText(pat, e.Title)
			})
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerCalendarSoon, Label: "Calendar event soon", Category: "Calendar",
		Description: "While an event starts within the chosen number of minutes",
		Fields: []RuleField{
			titleField,
			{Key: "minutes", Label: "Minutes before", Type: FieldNumber, Default: 10.0, Min: 1, Max: 240, Step: 1},
		},
		Check: func(p RuleParams) bool {
			now := nowFunc()
			pat, ahead := p.String("title"), time.Duration(p.Number("minutes"))*time.Minute
			return calendarMatches(now, func(e calInstance) bool {
				return !e.AllDay && e.Start.After(now) && !e.Start.After(now.Add(ahead)) && MatchText(pat, e.Title)
			})
		},
	}))
}
