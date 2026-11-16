package modules

// The timer line: a countdown (for a length, or until a time of day), a
// stopwatch, or a Pomodoro timer that moves between work and break phases.
//
// The settings live in TimerOptions; the run itself (started, paused, how
// far along) is kept in memory only, so restarting Fern stops the timer.
// Every change of the run goes through timerRun, which takes the time as an
// argument and does no I/O, so the state machine can be tested on its own.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Timer modes.
const (
	TimerCountdown = "countdown"
	TimerStopwatch = "stopwatch"
	TimerPomodoro  = "pomodoro"
)

// Kinds of countdown.
const (
	CountdownFor   = "duration" // a set length
	CountdownUntil = "until"    // until a time of day
)

// Pomodoro phases.
const (
	PhaseWork  = "work"
	PhaseBreak = "break"
	PhaseLong  = "long"
)

// Timer states, as the panel sees them.
const (
	TimerStateIdle     = "idle"
	TimerStateRunning  = "running"
	TimerStatePaused   = "paused"
	TimerStateFinished = "finished"
)

// maxTimerSec is the longest countdown, 99:59:59.
const maxTimerSec = 99*3600 + 59*60 + 59

// TimerOptions controls the timer line.
type TimerOptions struct {
	Mode        string `json:"mode"`         // countdown, stopwatch or pomodoro
	Countdown   string `json:"countdown"`    // duration or until
	DurationSec int    `json:"duration_sec"` // countdown length
	Until       string `json:"until"`        // "HH:MM", the next time it comes round
	Label       string `json:"label"`        // {label} of a countdown or stopwatch
	// Template is the line. {time} is what is left (the time so far for the
	// stopwatch), {elapsed}, {remaining}, {label}, {round}, {rounds},
	// {end} (the clock time it ends) and {paused}.
	Template     string `json:"template"`
	FinishedText string `json:"finished_text"` // shown when a countdown ends; empty hides the line
	FinishedSec  int    `json:"finished_sec"`  // how long the finished text stays
	PausedMark   string `json:"paused_mark"`   // {paused}, added at the end when the template has none
	HideIdle     bool   `json:"hide_idle"`     // no line while the timer is not started
	TimeStyle    string `json:"time_style"`    // clock (4:05) or short (4m 5s)

	WorkMin      int    `json:"work_min"`
	BreakMin     int    `json:"break_min"`
	LongBreakMin int    `json:"long_break_min"`
	Rounds       int    `json:"rounds"` // work rounds before a long break
	WorkLabel    string `json:"work_label"`
	BreakLabel   string `json:"break_label"`
	LongLabel    string `json:"long_label"`
	AutoAdvance  bool   `json:"auto_advance"` // start the next phase by itself

	ToggleHotkey string `json:"toggle_hotkey"` // start or pause, from any app
	ResetHotkey  string `json:"reset_hotkey"`

	// Avatar parameters. ParamToggle and ParamReset are read: turning them
	// on starts or pauses, and resets. ParamRunning (bool) and ParamProgress
	// (float 0..1, how far through the countdown or phase) are sent.
	ParamToggle   string `json:"param_toggle"`
	ParamReset    string `json:"param_reset"`
	ParamRunning  string `json:"param_running"`
	ParamProgress string `json:"param_progress"`
}

func defaultTimerOptions() TimerOptions {
	return TimerOptions{
		Mode:         TimerCountdown,
		Countdown:    CountdownFor,
		DurationSec:  300,
		Until:        "18:00",
		Template:     "⏱ {label} {time}",
		FinishedText: "⏰ Time's up!",
		FinishedSec:  30,
		PausedMark:   "⏸",
		HideIdle:     true,
		TimeStyle:    "clock",
		WorkMin:      25,
		BreakMin:     5,
		LongBreakMin: 15,
		Rounds:       4,
		WorkLabel:    "Focus",
		BreakLabel:   "Break",
		LongLabel:    "Long break",
		AutoAdvance:  true,
	}
}

// canonicalHotkey is s written the way ParseHotkey does, or "" if s is not a
// shortcut.
func canonicalHotkey(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if _, _, canonical, err := ParseHotkey(s); err == nil {
		return canonical
	}
	return ""
}

// tallyParam cleans an avatar parameter name and drops a pasted
// /avatar/parameters/.
func tallyParam(s string) string {
	return RuneLimit(paramName(strings.TrimPrefix(strings.TrimSpace(s), AvatarParamPrefix)), 128)
}

func (o *TimerOptions) sanitize() {
	d := defaultTimerOptions()
	switch o.Mode {
	case TimerCountdown, TimerStopwatch, TimerPomodoro:
	default:
		o.Mode = d.Mode
	}
	if o.Countdown != CountdownUntil {
		o.Countdown = CountdownFor
	}
	if o.DurationSec <= 0 {
		o.DurationSec = d.DurationSec
	}
	o.DurationSec = clampInt(o.DurationSec, 1, maxTimerSec)
	if _, ok := parseClock(o.Until); !ok {
		o.Until = d.Until
	}
	o.Label = oneLine(o.Label, 24)
	o.Template = oneLine(o.Template, 100)
	if o.Template == "" {
		o.Template = d.Template
	}
	o.FinishedText = oneLine(o.FinishedText, 100)
	o.FinishedSec = clampInt(o.FinishedSec, 0, 3600)
	o.PausedMark = oneLine(o.PausedMark, 8)
	if o.TimeStyle != "short" {
		o.TimeStyle = "clock"
	}
	o.WorkMin = clampInt(orDefault(o.WorkMin, d.WorkMin), 1, 600)
	o.BreakMin = clampInt(orDefault(o.BreakMin, d.BreakMin), 1, 600)
	o.LongBreakMin = clampInt(orDefault(o.LongBreakMin, d.LongBreakMin), 1, 600)
	o.Rounds = clampInt(orDefault(o.Rounds, d.Rounds), 1, 12)
	o.WorkLabel = oneLine(o.WorkLabel, 24)
	o.BreakLabel = oneLine(o.BreakLabel, 24)
	o.LongLabel = oneLine(o.LongLabel, 24)
	o.ToggleHotkey = canonicalHotkey(o.ToggleHotkey)
	o.ResetHotkey = canonicalHotkey(o.ResetHotkey)
	if o.ResetHotkey == o.ToggleHotkey {
		o.ResetHotkey = ""
	}
	o.ParamToggle = tallyParam(o.ParamToggle)
	o.ParamReset = tallyParam(o.ParamReset)
	o.ParamRunning = tallyParam(o.ParamRunning)
	o.ParamProgress = tallyParam(o.ParamProgress)
}

// orDefault is v, or def when v is zero or less (a missing value).
func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// timerRun is one run of the timer.
type timerRun struct {
	mode    string
	active  bool // started and not reset or finished
	running bool
	since   time.Time     // start of the current running stretch
	done    time.Duration // time run before since
	length  time.Duration // countdown or phase length; 0 for the stopwatch
	phase   string        // Pomodoro phase
	round   int           // Pomodoro work round, from 1
	label   string        // countdown or stopwatch label when started

	finishedAt time.Time // when a countdown last ran out
}

// timerEvent is something the rules can react to.
type timerEvent struct {
	kind  string // "finished" or "phase"
	phase string
	label string
	round int
}

func (r *timerRun) elapsed(now time.Time) time.Duration {
	d := r.done
	if r.running {
		d += now.Sub(r.since)
	}
	if d < 0 {
		d = 0
	}
	return d
}

func (r *timerRun) remaining(now time.Time) time.Duration {
	if r.length <= 0 {
		return 0
	}
	left := r.length - r.elapsed(now)
	if left < 0 {
		left = 0
	}
	return left
}

// untilLength is how long it is from now to the next hh:mm.
func untilLength(hhmm string, now time.Time) time.Duration {
	minutes, ok := parseClock(hhmm)
	if !ok {
		return 0
	}
	target := time.Date(now.Year(), now.Month(), now.Day(), minutes/60, minutes%60, 0, 0, now.Location())
	if !target.After(now) {
		target = target.AddDate(0, 0, 1)
	}
	return target.Sub(now)
}

func phaseLength(o TimerOptions, phase string) time.Duration {
	switch phase {
	case PhaseBreak:
		return time.Duration(o.BreakMin) * time.Minute
	case PhaseLong:
		return time.Duration(o.LongBreakMin) * time.Minute
	}
	return time.Duration(o.WorkMin) * time.Minute
}

// start begins a new run, or resumes a paused one.
func (r *timerRun) start(o TimerOptions, now time.Time) {
	if r.active && r.mode != o.Mode {
		r.reset()
	}
	if r.active {
		if !r.running {
			r.running = true
			r.since = now
		}
		return
	}
	*r = timerRun{mode: o.Mode, active: true, running: true, since: now, label: o.Label}
	switch o.Mode {
	case TimerCountdown:
		r.length = time.Duration(o.DurationSec) * time.Second
		if o.Countdown == CountdownUntil {
			r.length = untilLength(o.Until, now)
		}
	case TimerPomodoro:
		r.phase, r.round = PhaseWork, 1
		r.length = phaseLength(o, PhaseWork)
	}
}

func (r *timerRun) pause(now time.Time) {
	if r.running {
		r.done = r.elapsed(now)
		r.running = false
	}
}

// toggle starts or resumes a stopped timer and pauses a running one.
func (r *timerRun) toggle(o TimerOptions, now time.Time) {
	if r.running && r.mode == o.Mode {
		r.pause(now)
		return
	}
	r.start(o, now)
}

func (r *timerRun) reset() { *r = timerRun{} }

// skip ends the current countdown or Pomodoro phase now.
func (r *timerRun) skip(o TimerOptions, now time.Time) []timerEvent {
	if !r.active || r.length <= 0 {
		return nil
	}
	wasRunning := r.running
	r.done, r.since = r.length, now
	r.running = true
	events := r.advance(o, now)
	if r.active && r.phase != "" && !wasRunning {
		r.pause(now)
	}
	return events
}

// advance moves a countdown or Pomodoro on once its time is up.
func (r *timerRun) advance(o TimerOptions, now time.Time) []timerEvent {
	var events []timerEvent
	for i := 0; i < 8 && r.active && r.running && r.length > 0; i++ {
		over := r.elapsed(now) - r.length
		if over < 0 {
			break
		}
		if r.mode != TimerPomodoro {
			label := r.label
			r.reset()
			r.finishedAt = now
			events = append(events, timerEvent{kind: "finished", label: label})
			break
		}
		switch {
		case r.phase == PhaseWork && r.round >= o.Rounds:
			r.phase = PhaseLong
		case r.phase == PhaseWork:
			r.phase = PhaseBreak
		case r.phase == PhaseLong:
			r.phase, r.round = PhaseWork, 1
		default:
			r.phase = PhaseWork
			r.round++
		}
		r.length = phaseLength(o, r.phase)
		r.since, r.done = now, 0
		if o.AutoAdvance {
			// Keep the overshoot so a late tick does not stretch the phase.
			r.done = over
		} else {
			r.running = false
		}
		events = append(events, timerEvent{kind: "phase", phase: r.phase, label: phaseLabel(o, r.phase), round: r.round})
	}
	return events
}

func phaseLabel(o TimerOptions, phase string) string {
	switch phase {
	case PhaseWork:
		return o.WorkLabel
	case PhaseBreak:
		return o.BreakLabel
	case PhaseLong:
		return o.LongLabel
	}
	return ""
}

// state is idle, running, paused or finished (showing the finished text).
func (r *timerRun) state(o TimerOptions, now time.Time) string {
	switch {
	case r.active && r.running:
		return TimerStateRunning
	case r.active:
		return TimerStatePaused
	case !r.finishedAt.IsZero() && o.FinishedText != "" && now.Sub(r.finishedAt) < time.Duration(o.FinishedSec)*time.Second:
		return TimerStateFinished
	}
	return TimerStateIdle
}

// formatTimer writes d as 4:05 / 1:04:05, or 4m 5s / 1h 4m.
func formatTimer(d time.Duration, style string) string {
	secs := int((d + time.Second - 1) / time.Second) // a countdown shows 0:00 only at the end
	if d < 0 {
		secs = 0
	}
	h, m, s := secs/3600, secs/60%60, secs%60
	if style == "short" {
		switch {
		case h > 0:
			return fmt.Sprintf("%dh %dm", h, m)
		case m > 0 && s > 0:
			return fmt.Sprintf("%dm %ds", m, s)
		case m > 0:
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%ds", s)
	}
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// collapseSpaces drops the double spaces an empty placeholder leaves.
func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// timerText renders the line for run r.
func timerText(o TimerOptions, r timerRun, now time.Time) string {
	state := r.state(o, now)
	if state == TimerStateFinished {
		return collapseSpaces(strings.ReplaceAll(o.FinishedText, "{label}", r.labelOr(o)))
	}
	if state == TimerStateIdle && o.HideIdle {
		return ""
	}
	if state == TimerStateIdle {
		// What a fresh start would show.
		r = timerRun{}
		r.start(o, now)
		r.pause(now)
	}
	elapsed, remaining := r.elapsed(now), r.remaining(now)
	shown := remaining
	if r.mode == TimerStopwatch {
		shown = elapsed
	}
	end := ""
	if r.length > 0 && state == TimerStateRunning {
		end = now.Add(remaining).Format("15:04")
	}
	paused := ""
	if state == TimerStatePaused {
		paused = o.PausedMark
	}
	rounds := ""
	round := ""
	if r.mode == TimerPomodoro {
		round, rounds = fmt.Sprint(r.round), fmt.Sprint(o.Rounds)
	}
	text := strings.NewReplacer(
		"{time}", formatTimer(shown, o.TimeStyle),
		"{elapsed}", formatTimer(elapsed, o.TimeStyle),
		"{remaining}", formatTimer(remaining, o.TimeStyle),
		"{label}", r.labelOr(o),
		"{round}", round,
		"{rounds}", rounds,
		"{end}", end,
		"{paused}", paused,
	).Replace(o.Template)
	if paused != "" && !strings.Contains(o.Template, "{paused}") {
		text += " " + paused
	}
	return collapseSpaces(text)
}

// labelOr is the label of the run: the phase for Pomodoro, otherwise the
// label it started with (or the current one when it has not started).
func (r timerRun) labelOr(o TimerOptions) string {
	if r.phase != "" {
		return phaseLabel(o, r.phase)
	}
	if r.active || !r.finishedAt.IsZero() {
		return r.label
	}
	return o.Label
}

// progress is how far through the countdown or phase the run is, 0..1. The
// stopwatch has none.
func (r *timerRun) progress(now time.Time) float64 {
	if !r.active || r.length <= 0 {
		return 0
	}
	p := float64(r.elapsed(now)) / float64(r.length)
	if p > 1 {
		p = 1
	}
	return p
}

var (
	timerMu  sync.Mutex
	timerNow = time.Now
	timer    timerRun

	// What was last sent to the avatar, so only changes are sent.
	timerSentRunning  *bool
	timerSentProgress = -1.0
	timerParamPrev    = map[string]bool{} // last value of the control parameters
)

// TimerStatus is the timer as the panel shows it.
type TimerStatus struct {
	Mode        string  `json:"mode"`
	State       string  `json:"state"`
	ElapsedMS   int64   `json:"elapsedMs"`
	RemainingMS int64   `json:"remainingMs"`
	LengthMS    int64   `json:"lengthMs"`
	Progress    float64 `json:"progress"`
	Phase       string  `json:"phase"`
	Label       string  `json:"label"`
	Round       int     `json:"round"`
	Rounds      int     `json:"rounds"`
	Text        string  `json:"text"`
}

// CurrentTimer reports the timer for the panel.
func CurrentTimer() TimerStatus {
	o := GetConfig().TimerOptions
	timerMu.Lock()
	defer timerMu.Unlock()
	now := timerNow()
	r := timer
	st := TimerStatus{
		Mode:        o.Mode,
		State:       r.state(o, now),
		ElapsedMS:   r.elapsed(now).Milliseconds(),
		RemainingMS: r.remaining(now).Milliseconds(),
		LengthMS:    r.length.Milliseconds(),
		Progress:    r.progress(now),
		Phase:       r.phase,
		Label:       r.labelOr(o),
		Round:       r.round,
		Rounds:      o.Rounds,
		Text:        timerText(o, r, now),
	}
	if r.active {
		st.Mode = r.mode
	}
	return st
}

func timerLine(cfg Config) string {
	timerMu.Lock()
	defer timerMu.Unlock()
	return timerText(cfg.TimerOptions, timer, timerNow())
}

// Timer actions, as the panel, shortcuts and rules name them.
const (
	TimerActStart  = "start"
	TimerActPause  = "pause"
	TimerActToggle = "toggle"
	TimerActReset  = "reset"
	TimerActSkip   = "skip"
)

// TimerControl starts, pauses, toggles, resets or skips the timer. It
// reports whether the action was known.
func TimerControl(action string) bool {
	o := GetConfig().TimerOptions
	timerMu.Lock()
	now := timerNow()
	var events []timerEvent
	switch action {
	case TimerActStart:
		timer.start(o, now)
	case TimerActPause:
		timer.pause(now)
	case TimerActToggle:
		timer.toggle(o, now)
	case TimerActReset:
		timer.reset()
	case TimerActSkip:
		events = timer.skip(o, now)
	default:
		timerMu.Unlock()
		return false
	}
	timerMu.Unlock()
	afterTimerChange(o, events)
	return true
}

// TimerRunning reports whether the timer is counting.
func TimerRunning() bool {
	timerMu.Lock()
	defer timerMu.Unlock()
	return timer.active && timer.running
}

// timerTick moves the run on and sends the avatar parameters.
func timerTick(o TimerOptions, osc OSCOptions) {
	timerMu.Lock()
	events := timer.advance(o, timerNow())
	timerMu.Unlock()
	if len(events) > 0 {
		afterTimerChange(o, events)
		return
	}
	sendTimerParams(o, osc)
}

func afterTimerChange(o TimerOptions, events []timerEvent) {
	for _, ev := range events {
		data := map[string]string{"label": ev.label, "name": ev.label}
		if ev.kind == "phase" {
			data["phase"] = ev.phase
			data["round"] = fmt.Sprint(ev.round)
			FireTrigger(TriggerTimerPhase, data)
		} else {
			FireTrigger(TriggerTimerFinished, data)
		}
	}
	sendTimerParams(o, GetConfig().OSCOptions)
	NudgeRules()
	NotifyChange()
}

// sendTimerParams sends ParamRunning and ParamProgress when they changed.
func sendTimerParams(o TimerOptions, opts OSCOptions) {
	if o.ParamRunning == "" && o.ParamProgress == "" {
		return
	}
	timerMu.Lock()
	now := timerNow()
	running := timer.active && timer.running
	progress := float64(int(timer.progress(now)*100+0.5)) / 100
	var sendRunning, sendProgress bool
	if o.ParamRunning != "" && (timerSentRunning == nil || *timerSentRunning != running) {
		timerSentRunning = &running
		sendRunning = true
	}
	if o.ParamProgress != "" && progress != timerSentProgress {
		timerSentProgress = progress
		sendProgress = true
	}
	timerMu.Unlock()
	if sendRunning {
		_ = sendAvatarParamTo(opts, o.ParamRunning, running)
	}
	if sendProgress {
		_ = sendAvatarParamTo(opts, o.ParamProgress, progress)
	}
}

// onTimerParam reads the start / pause and reset parameters: each acts when
// it turns on.
func onTimerParam(msg *osc.Message) {
	name := strings.TrimPrefix(msg.Address, AvatarParamPrefix)
	o := tallyOptions().timer
	if name == "" || (name != o.ParamToggle && name != o.ParamReset) || len(msg.Arguments) == 0 {
		return
	}
	v, ok := oscNumber(msg.Arguments[0])
	if !ok {
		return
	}
	on := v > 0.5
	timerMu.Lock()
	was := timerParamPrev[name]
	timerParamPrev[name] = on
	timerMu.Unlock()
	if !on || was {
		return
	}
	action := TimerActToggle
	if name == o.ParamReset {
		action = TimerActReset
	}
	TimerControl(action)
}
