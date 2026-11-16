package modules

// The worker behind the timer, counter and schedule lines, and their rule
// triggers, actions and option sources.

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Rule trigger IDs of the timer, counters and scheduled messages.
const (
	TriggerTimerFinished  = "timer.finished"
	TriggerTimerPhase     = "timer.phase"
	TriggerTimerRunning   = "timer.running"
	TriggerCounterChanged = "counter.changed"
	TriggerScheduleSent   = "schedule.sent"
)

// Rule action IDs.
const (
	ActionTimer        = "timer.control"
	ActionCounter      = "counter.adjust"
	ActionScheduleShow = "schedule.show"
)

// Option sources.
const (
	SourceCounters  = "counters"
	SourceSchedules = "schedules"
)

// tallySnapshot is the part of the config the OSC handlers need. They run
// on the listener goroutine for every parameter VRChat sends, so they read
// this copy instead of cloning the whole config each time.
type tallySnapshot struct {
	timer    TimerOptions
	counters []CounterDef
	at       time.Time
}

var tallyCache atomic.Pointer[tallySnapshot]

func storeTallyOptions(cfg Config) *tallySnapshot {
	s := &tallySnapshot{timer: cfg.TimerOptions, counters: cfg.CounterOptions.Counters, at: time.Now()}
	tallyCache.Store(s)
	return s
}

// tallyOptions is the cached snapshot, refreshed when it is over a second old.
func tallyOptions() *tallySnapshot {
	if s := tallyCache.Load(); s != nil && time.Since(s.at) < time.Second {
		return s
	}
	return storeTallyOptions(GetConfig())
}

var tallyOnce sync.Once

// startTallyWorker listens for the parameters the timer and counters use and
// moves the timer and schedule on twice a second.
func startTallyWorker() {
	tallyOnce.Do(func() {
		SubscribeOSCPrefix(AvatarParamPrefix, func(msg *osc.Message) {
			onTimerParam(msg)
			onCounterParam(msg)
		})
		OnAvatarChange(counterAvatarChanged)
		go func() {
			for range time.Tick(500 * time.Millisecond) {
				cfg := GetConfig()
				storeTallyOptions(cfg)
				timerTick(cfg.TimerOptions, cfg.OSCOptions)
				scheduleTick(cfg.ScheduleOptions, scheduleNow())
			}
		}()
	})
}

func init() {
	registerTallyRules()
}

func registerTallyRules() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTimerFinished, Label: "Timer finished", Category: "Time",
		Description: "A countdown on the timer line runs out",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTimerPhase, Label: "Pomodoro phase", Category: "Time",
		Description: "The Pomodoro timer moves on to work or a break",
		Fields: []RuleField{
			{Key: "phase", Label: "Phase", Type: FieldSelect, Options: []RuleOption{
				{"", "Any phase"}, {PhaseWork, "Work"}, {PhaseBreak, "Break"}, {PhaseLong, "Long break"},
			}},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			want := p.String("phase")
			return want == "" || want == ev.Data["phase"]
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTimerRunning, Label: "Timer running", Category: "Time",
		Description: "While the timer is counting (not paused or stopped)",
		Fields:      []RuleField{},
		Check:       func(RuleParams) bool { return TimerRunning() },
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerCounterChanged, Label: "Counter went up", Category: "Chatbox",
		Description: "A counter counts, from its parameter or by hand. {name} is the counter.",
		Fields: []RuleField{
			{Key: "counter", Label: "Counter", Type: FieldSelect, Source: SourceCounters, Placeholder: "Any counter"},
			{Key: "every", Label: "Every", Type: FieldNumber, Min: 0, Max: 10000, Step: 1, Hint: "Only when the session count is a multiple of this, like every 10th headpat. 0 is every time."},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			if ev.Data["delta"] != "" && strings.HasPrefix(ev.Data["delta"], "-") {
				return false
			}
			if want := p.String("counter"); want != "" && want != ev.Data["counter"] {
				return false
			}
			every := int64(p.Number("every"))
			if every <= 0 {
				return true
			}
			var n int64
			for _, c := range ev.Data["session"] {
				if c < '0' || c > '9' {
					return false
				}
				n = n*10 + int64(c-'0')
			}
			return n > 0 && n%every == 0
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerScheduleSent, Label: "Scheduled message sent", Category: "Chatbox",
		Description: "A scheduled or repeating message goes out",
		Fields: []RuleField{
			{Key: "message", Label: "Message", Type: FieldSelect, Source: SourceSchedules, Placeholder: "Any message"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			want := p.String("message")
			return want == "" || want == ev.Data["message"]
		},
	}))

	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionTimer, Label: "Control the timer", Category: "Time",
		Description: "Start, pause, reset or skip the timer line. Undo pauses a timer it started, or resumes one it paused.",
		Fields: []RuleField{
			{Key: "do", Label: "Do", Type: FieldSelect, Options: []RuleOption{
				{TimerActStart, "Start or resume"}, {TimerActPause, "Pause"}, {TimerActToggle, "Start or pause"},
				{TimerActReset, "Reset"}, {TimerActSkip, "Skip to the end of the phase"},
			}},
			{Key: "mode", Label: "Switch to", Type: FieldSelect, Options: []RuleOption{
				{"", "Keep the mode"}, {TimerCountdown, "Countdown"}, {TimerStopwatch, "Stopwatch"}, {TimerPomodoro, "Pomodoro"},
			}, ShowIf: &RuleShowIf{Key: "do", Values: []string{TimerActStart, TimerActToggle}}, Hint: "Changing the mode starts the timer afresh"},
			{Key: "minutes", Label: "Minutes", Type: FieldNumber, Min: 0, Max: 5999, Step: 1, ShowIf: &RuleShowIf{Key: "mode", Values: []string{TimerCountdown}}, Hint: "Countdown length. 0 keeps the one set on the Timer page."},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			do := p.String("do")
			if do == TimerActStart || do == TimerActToggle {
				if mode := p.String("mode"); mode != "" {
					minutes := int(math.Round(p.Number("minutes")))
					UpdateConfig(func(c *Config) {
						c.TimerOptions.Mode = mode
						if mode == TimerCountdown && minutes > 0 {
							c.TimerOptions.Countdown = CountdownFor
							c.TimerOptions.DurationSec = minutes * 60
						}
					})
					TimerControl(TimerActReset)
					do = TimerActStart
				}
			}
			before := TimerRunning()
			if !TimerControl(do) {
				return nil, errors.New("pick what to do")
			}
			after := TimerRunning()
			switch {
			case !before && after:
				return func() {
					if TimerRunning() {
						TimerControl(TimerActPause)
					}
				}, nil
			case before && !after && do == TimerActPause:
				return func() {
					if st := CurrentTimer(); st.State == TimerStatePaused {
						TimerControl(TimerActStart)
					}
				}, nil
			}
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionCounter, Label: "Change a counter", Category: "Chatbox",
		Description: "Add to, take from or reset a counter",
		Fields: []RuleField{
			{Key: "counter", Label: "Counter", Type: FieldSelect, Source: SourceCounters, Placeholder: "Pick a counter"},
			{Key: "op", Label: "Do", Type: FieldSelect, Options: []RuleOption{
				{"add", "Add"}, {"sub", "Take away"}, {"reset", "Reset this session"}, {"reset_all", "Reset session and total"},
			}},
			{Key: "amount", Label: "Amount", Type: FieldNumber, Min: 1, Max: 1000, Step: 1, Default: 1.0, ShowIf: &RuleShowIf{Key: "op", Values: []string{"add", "sub"}}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			id := p.String("counter")
			if id == "" {
				return nil, errors.New("pick a counter")
			}
			n := int64(math.Max(1, math.Round(p.Number("amount"))))
			switch p.String("op") {
			case "sub":
				// Run holds the rules lock and CounterAdd fires a trigger,
				// which only queues, so this is safe.
				return nil, CounterAdd(id, -n)
			case "reset":
				return nil, CounterReset(id, false)
			case "reset_all":
				return nil, CounterReset(id, true)
			}
			return nil, CounterAdd(id, n)
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionScheduleShow, Label: "Show a scheduled message", Category: "Chatbox",
		Description: "Sends one of your scheduled messages now, whatever its time",
		Fields: []RuleField{
			{Key: "message", Label: "Message", Type: FieldSelect, Source: SourceSchedules, Placeholder: "Pick a message"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			id := p.String("message")
			if id == "" {
				return nil, errors.New("pick a message")
			}
			return nil, ShowScheduledNow(id)
		},
	}))

	RegisterRuleSource(SourceCounters, func() []RuleOption {
		list := tallyOptions().counters
		out := make([]RuleOption, 0, len(list))
		for _, c := range list {
			label := c.Name
			if label == "" {
				label = "Counter " + c.ID
			}
			out = append(out, RuleOption{Value: c.ID, Label: label})
		}
		return out
	})
	RegisterRuleSource(SourceSchedules, func() []RuleOption {
		list := GetConfig().ScheduleOptions.Messages
		out := make([]RuleOption, 0, len(list))
		for _, m := range list {
			label := RuneLimit(m.Text, 40)
			if label == "" {
				label = "(empty message)"
			}
			out = append(out, RuleOption{Value: m.ID, Label: label})
		}
		return out
	})
}
