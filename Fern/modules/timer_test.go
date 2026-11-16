package modules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

var timerT0 = time.Date(2026, 11, 16, 10, 0, 0, 0, time.Local)

func timerOpts(mut func(*TimerOptions)) TimerOptions {
	o := defaultTimerOptions()
	if mut != nil {
		mut(&o)
	}
	o.sanitize()
	return o
}

func TestTimerSanitizeDefaultsAndBounds(t *testing.T) {
	o := TimerOptions{Mode: "bogus", DurationSec: -4, Until: "25:00", Template: "  ", TimeStyle: "x",
		Rounds: 99, WorkMin: 9999, ToggleHotkey: "ctrl+shift+t", ResetHotkey: "Ctrl+Shift+T",
		ParamToggle: " /avatar/parameters/TimerGo ", Label: "a\nb"}
	o.sanitize()
	d := defaultTimerOptions()
	if o.Mode != TimerCountdown || o.DurationSec != d.DurationSec || o.Until != d.Until || o.Template != d.Template {
		t.Errorf("defaults not restored: %+v", o)
	}
	if o.TimeStyle != "clock" || o.Rounds != 12 || o.WorkMin != 600 || o.BreakMin != d.BreakMin {
		t.Errorf("bounds: %+v", o)
	}
	if o.ToggleHotkey != "Ctrl+Shift+T" || o.ResetHotkey != "" {
		t.Errorf("hotkeys %q %q: the reset key may not repeat the toggle key", o.ToggleHotkey, o.ResetHotkey)
	}
	if o.ParamToggle != "TimerGo" || o.Label != "a b" {
		t.Errorf("param %q label %q", o.ParamToggle, o.Label)
	}
	big := TimerOptions{DurationSec: 1 << 30}
	big.sanitize()
	if big.DurationSec != maxTimerSec {
		t.Errorf("duration %d", big.DurationSec)
	}
}

func TestDefaultConfigTimerSections(t *testing.T) {
	c := DefaultConfig()
	before := c.TimerOptions
	c.sanitize()
	if c.TimerOptions != before {
		t.Errorf("sanitize changed the default timer: %+v", c.TimerOptions)
	}
	if c.CounterOptions.Counters == nil || c.ScheduleOptions.Messages == nil {
		t.Error("lists should be empty, not null")
	}
	for _, id := range []string{ModuleTimer, ModuleCounter, ModuleSchedule} {
		if !knownModules[id] || LineLabel(id) == id {
			t.Errorf("%s is not a known line with a label", id)
		}
	}
}

func TestCountdownRunsPausesAndFinishes(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) { o.DurationSec = 90 })
	var r timerRun
	now := timerT0
	if got := timerText(o, r, now); got != "" {
		t.Errorf("idle line should hide, got %q", got)
	}
	r.start(o, now)
	now = now.Add(30 * time.Second)
	if got := timerText(o, r, now); got != "⏱ 1:00" {
		t.Errorf("after 30s %q", got)
	}
	r.pause(now)
	now = now.Add(time.Hour)
	if got := timerText(o, r, now); got != "⏱ 1:00 ⏸" {
		t.Errorf("paused %q", got)
	}
	if ev := r.advance(o, now); ev != nil || r.state(o, now) != TimerStatePaused {
		t.Errorf("a paused countdown must not finish: %v %s", ev, r.state(o, now))
	}
	r.toggle(o, now)
	now = now.Add(59500 * time.Millisecond)
	if got := timerText(o, r, now); got != "⏱ 0:01" {
		t.Errorf("half a second left shows %q", got)
	}
	now = now.Add(time.Second)
	ev := r.advance(o, now)
	if len(ev) != 1 || ev[0].kind != "finished" {
		t.Fatalf("events %v", ev)
	}
	if got := timerText(o, r, now); got != "⏰ Time's up!" {
		t.Errorf("finished %q", got)
	}
	if got := timerText(o, r, now.Add(31*time.Second)); got != "" {
		t.Errorf("finished text should go after 30s, got %q", got)
	}
}

func TestCountdownShowsWhenIdleAndPlaceholders(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) {
		o.HideIdle = false
		o.DurationSec = 3725
		o.Label = "Tea"
		o.Template = "{label}: {time} left, {elapsed} done, ends {end}{paused}"
		o.TimeStyle = "short"
	})
	var r timerRun
	if got := timerText(o, r, timerT0); got != "Tea: 1h 2m left, 0s done, ends" {
		t.Errorf("idle %q", got)
	}
	r.start(o, timerT0)
	got := timerText(o, r, timerT0.Add(65*time.Second))
	if got != "Tea: 1h 1m left, 1m 5s done, ends 11:02" {
		t.Errorf("running %q", got)
	}
}

func TestCountdownUntilTime(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) { o.Countdown = CountdownUntil; o.Until = "10:30" })
	var r timerRun
	r.start(o, timerT0)
	if r.length != 30*time.Minute {
		t.Errorf("length %v", r.length)
	}
	o.Until = "09:00" // already past today: tomorrow
	r = timerRun{}
	r.start(o, timerT0)
	if r.length != 23*time.Hour {
		t.Errorf("tomorrow length %v", r.length)
	}
}

func TestStopwatch(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) { o.Mode = TimerStopwatch; o.Template = "{time}" })
	var r timerRun
	r.start(o, timerT0)
	now := timerT0.Add(2*time.Hour + 3*time.Second)
	if got := timerText(o, r, now); got != "2:00:03" {
		t.Errorf("stopwatch %q", got)
	}
	if ev := r.advance(o, now.Add(1000*time.Hour)); ev != nil {
		t.Errorf("a stopwatch never finishes: %v", ev)
	}
	if r.progress(now) != 0 {
		t.Error("a stopwatch has no progress")
	}
}

func TestPomodoroCycle(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) {
		o.Mode = TimerPomodoro
		o.WorkMin, o.BreakMin, o.LongBreakMin, o.Rounds = 10, 2, 5, 2
		o.Template = "{label} {round}/{rounds} {time}"
	})
	var r timerRun
	now := timerT0
	r.start(o, now)
	if got := timerText(o, r, now); got != "Focus 1/2 10:00" {
		t.Errorf("start %q", got)
	}
	var phases []string
	step := func(d time.Duration) {
		now = now.Add(d)
		for _, ev := range r.advance(o, now) {
			phases = append(phases, ev.phase)
		}
	}
	step(10*time.Minute + 3*time.Second) // late tick: the overshoot carries over
	if got := timerText(o, r, now); got != "Break 1/2 1:57" {
		t.Errorf("break %q", got)
	}
	step(117 * time.Second)
	step(10 * time.Minute)
	if r.phase != PhaseLong || r.round != 2 {
		t.Errorf("after two work rounds want a long break, got %s round %d", r.phase, r.round)
	}
	step(5 * time.Minute)
	if r.phase != PhaseWork || r.round != 1 {
		t.Errorf("after the long break a new set starts, got %s %d", r.phase, r.round)
	}
	if strings.Join(phases, ",") != "break,work,long,work" {
		t.Errorf("phases %v", phases)
	}
}

func TestPomodoroWaitsWithoutAutoAdvanceAndSkip(t *testing.T) {
	o := timerOpts(func(o *TimerOptions) { o.Mode = TimerPomodoro; o.AutoAdvance = false })
	var r timerRun
	r.start(o, timerT0)
	now := timerT0.Add(25 * time.Minute)
	r.advance(o, now)
	if r.phase != PhaseBreak || r.running || r.state(o, now) != TimerStatePaused {
		t.Fatalf("should wait at the break: %+v", r)
	}
	if got := r.remaining(now.Add(time.Hour)); got != 5*time.Minute {
		t.Errorf("waiting break left %v", got)
	}
	ev := r.skip(o, now)
	if len(ev) != 1 || r.phase != PhaseWork || r.round != 2 || r.running {
		t.Errorf("skip of a paused break: %v %+v", ev, r)
	}
	r.start(o, now)
	if r.elapsed(now.Add(time.Minute)) != time.Minute {
		t.Errorf("resumed elapsed %v", r.elapsed(now.Add(time.Minute)))
	}
}

func TestTimerModeChangeRestarts(t *testing.T) {
	o := timerOpts(nil)
	var r timerRun
	r.start(o, timerT0)
	o.Mode = TimerStopwatch
	r.toggle(o, timerT0.Add(time.Minute))
	if r.mode != TimerStopwatch || !r.running || r.elapsed(timerT0.Add(time.Minute)) != 0 {
		t.Errorf("toggling after a mode change should start the new mode: %+v", r)
	}
}

func TestFormatTimer(t *testing.T) {
	for _, c := range []struct {
		d     time.Duration
		style string
		want  string
	}{
		{0, "clock", "0:00"}, {59 * time.Second, "clock", "0:59"}, {3600 * time.Second, "clock", "1:00:00"},
		{-time.Second, "clock", "0:00"}, {90 * time.Second, "short", "1m 30s"}, {120 * time.Second, "short", "2m"},
		{5 * time.Second, "short", "5s"}, {3*time.Hour + 5*time.Minute, "short", "3h 5m"},
	} {
		if got := formatTimer(c.d, c.style); got != c.want {
			t.Errorf("formatTimer(%v, %s) = %q, want %q", c.d, c.style, got, c.want)
		}
	}
}

func TestTimerControlParamsAndAvatarInput(t *testing.T) {
	c := DefaultConfig()
	c.TimerOptions.ParamRunning = "TimerOn"
	c.TimerOptions.ParamProgress = "TimerPct"
	c.TimerOptions.ParamToggle = "TimerGo"
	c.sanitize()
	useConfig(t, c)
	storeTallyOptions(c)
	oldNow, oldSend := timerNow, avatarSend
	now := timerT0
	timerNow = func() time.Time { return now }
	var sent []string
	avatarSend = func(_ OSCOptions, msg *osc.Message) error {
		sent = append(sent, fmt.Sprint(msg.Address, " ", msg.Arguments[0]))
		return nil
	}
	reset := func() {
		timerMu.Lock()
		timer, timerSentRunning, timerSentProgress = timerRun{}, nil, -1
		clear(timerParamPrev)
		timerMu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		timerNow, avatarSend = oldNow, oldSend
		reset()
	})

	if TimerControl("explode") {
		t.Error("unknown action accepted")
	}
	param := func(v bool) { onTimerParam(osc.NewMessage(AvatarParamPrefix+"TimerGo", v)) }
	param(true)
	if !TimerRunning() {
		t.Fatal("turning the toggle parameter on should start the timer")
	}
	param(true) // still on: not a new press
	if !TimerRunning() {
		t.Fatal("a repeated true must not toggle again")
	}
	now = now.Add(150 * time.Second)
	timerTick(c.TimerOptions, c.OSCOptions)
	want := "/avatar/parameters/TimerOn true,/avatar/parameters/TimerPct 0,/avatar/parameters/TimerPct 0.5"
	if strings.Join(sent, ",") != want {
		t.Errorf("sent %v", sent)
	}
	if st := CurrentTimer(); st.State != TimerStateRunning || st.RemainingMS != 150000 || st.Text != "⏱ 2:30" {
		t.Errorf("status %+v", st)
	}
	param(false)
	param(true)
	if TimerRunning() {
		t.Error("a second press should pause")
	}
	TimerControl(TimerActReset)
	if st := CurrentTimer(); st.State != TimerStateIdle || st.Text != "" {
		t.Errorf("after reset %+v", st)
	}
}

func TestTimerTickStopsARunOfAnotherMode(t *testing.T) {
	t.Cleanup(func() {
		timerMu.Lock()
		timer = timerRun{}
		timerMu.Unlock()
	})
	o := timerOpts(nil)
	timerMu.Lock()
	timer = timerRun{}
	timer.start(o, time.Now())
	timerMu.Unlock()
	timerTick(o, OSCOptions{})
	if !TimerRunning() {
		t.Fatal("a tick of the same mode must keep the run")
	}
	o.Mode = TimerPomodoro
	timerTick(o, OSCOptions{})
	if TimerRunning() {
		t.Error("switching the mode in the settings should stop the old run")
	}
}

func TestTimerRuleAction(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	t.Cleanup(func() {
		timerMu.Lock()
		timer = timerRun{}
		timerMu.Unlock()
	})
	var act RuleAction
	for _, a := range RuleActions() {
		if a.ID == ActionTimer {
			act = a
		}
	}
	if act.Run == nil {
		t.Fatal("timer action not registered")
	}
	p := SanitizeRuleParams(act.Fields, RuleParams{"do": "start", "mode": "countdown", "minutes": 2.0})
	undo, err := act.Run(RuleContext{}, p)
	if err != nil || undo == nil {
		t.Fatalf("run: %v, undo %v", err, undo != nil)
	}
	o := GetConfig().TimerOptions
	if o.Mode != TimerCountdown || o.DurationSec != 120 || !TimerRunning() {
		t.Fatalf("config %+v running %v", o, TimerRunning())
	}
	if st := CurrentTimer(); st.LengthMS != 120000 {
		t.Errorf("length %d", st.LengthMS)
	}
	undo()
	if TimerRunning() || CurrentTimer().State != TimerStatePaused {
		t.Error("undo should pause the timer it started")
	}
}
