package modules

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The engine runs every rule on one goroutine: events from FireTrigger are
// queued and handled in order, and state triggers are re-checked once a
// second and whenever something nudges the engine. Actions run while the
// engine's lock is held, so they never overlap.

// Rule firing limits.
const (
	ruleBurst       = 30 // fires per rule per minute before it is paused
	ruleMaxDepth    = 3  // "run another rule" nesting
	ruleEventBuffer = 256
)

// RuleStatus is what the panel shows about a rule.
type RuleStatus struct {
	LastFired int64  `json:"lastFired"` // unix ms, 0 for never
	Fires     int    `json:"fires"`     // since Fern started
	Active    bool   `json:"active"`    // a state trigger and its conditions hold
	Undoable  bool   `json:"undoable"`  // there is something to put back
	Error     string `json:"error,omitempty"`
	Paused    bool   `json:"paused"` // fired too often in the last minute
}

type ruleRuntime struct {
	RuleStatus
	known  bool // Active has been worked out at least once
	undo   []func()
	recent []time.Time
}

type rulesEngine struct {
	mu      sync.Mutex
	rt      map[string]*ruleRuntime
	options func() RulesOptions
	now     func() time.Time
}

func newRulesEngine(options func() RulesOptions) *rulesEngine {
	return &rulesEngine{rt: map[string]*ruleRuntime{}, options: options, now: time.Now}
}

var (
	rulesEng   = newRulesEngine(func() RulesOptions { return GetConfig().Rules })
	ruleEvents = make(chan RuleEvent, ruleEventBuffer)
	ruleNudge  = make(chan struct{}, 1)
	rulesOnce  sync.Once
)

// FireTrigger reports an event for the event trigger id, e.g.
// FireTrigger("player_joined", map[string]string{"name": "Frieren"}). It
// never blocks: the event is queued for the rules goroutine, and dropped
// with a log line if the queue is full. Safe from any goroutine, with any
// lock held.
func FireTrigger(id string, data map[string]string) {
	select {
	case ruleEvents <- RuleEvent{Type: id, Data: data}:
	default:
		fmt.Println("rules: event queue full, dropped", id)
	}
}

// NudgeRules asks the engine to re-check state triggers soon, for code that
// knows a state trigger's answer may have changed. It never blocks.
func NudgeRules() {
	select {
	case ruleNudge <- struct{}{}:
	default:
	}
}

// TestRule runs a rule's actions now, ignoring its trigger, conditions and
// cooldown. It returns the first error.
func TestRule(id string) error {
	return rulesEng.test(id)
}

// RuleStatuses returns the status of every rule that has one, by rule ID.
func RuleStatuses() map[string]RuleStatus {
	return rulesEng.statuses()
}

func startRulesWorker() {
	rulesOnce.Do(func() {
		watchRuleSources()
		go func() {
			// Give the log reader and the OSC listener a moment, so a rule
			// for "Fern started" in a world sees the world.
			time.Sleep(3 * time.Second)
			FireTrigger(TriggerStarted, nil)
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case ev := <-ruleEvents:
					rulesEng.handleEvent(ev)
				case <-ruleNudge:
					rulesEng.evaluate()
				case <-tick.C:
					rulesEng.evaluate()
				}
			}
		}()
	})
}

func (e *rulesEngine) runtime(id string) *ruleRuntime {
	r := e.rt[id]
	if r == nil {
		r = &ruleRuntime{}
		e.rt[id] = r
	}
	return r
}

func (e *rulesEngine) statuses() map[string]RuleStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[string]RuleStatus, len(e.rt))
	for id, r := range e.rt {
		s := r.RuleStatus
		s.Undoable = len(r.undo) > 0
		out[id] = s
	}
	return out
}

// checkStep reports whether a trigger used as a condition holds. An event
// trigger or an unknown one never holds.
func checkStep(s RuleStep) bool {
	t := lookupTrigger(s.Type)
	if t == nil || t.Check == nil {
		return false
	}
	ok := safeCheck(t, s.Params)
	if s.Not {
		return !ok
	}
	return ok
}

func safeCheck(t *RuleTrigger, p RuleParams) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("rules: trigger", t.ID, "panicked:", r)
			ok = false
		}
	}()
	return t.Check(p)
}

func conditionsHold(r Rule) bool {
	for _, c := range r.Conditions {
		if !checkStep(c) {
			return false
		}
	}
	return true
}

// handleEvent fires the enabled rules whose event trigger matches ev, then
// re-checks the state triggers, since an event often changes those too.
func (e *rulesEngine) handleEvent(ev RuleEvent) {
	o := e.options()
	e.mu.Lock()
	if o.Enabled {
		for _, r := range o.List {
			if !r.Enabled || r.Trigger.Type != ev.Type {
				continue
			}
			t := lookupTrigger(r.Trigger.Type)
			if t == nil || t.Check != nil {
				continue
			}
			if t.Match != nil && !safeMatch(t, r.Trigger.Params, ev) {
				continue
			}
			if !conditionsHold(r) {
				continue
			}
			e.fire(r, ev, false)
		}
	}
	e.mu.Unlock()
	e.evaluateWith(o)
}

func safeMatch(t *RuleTrigger, p RuleParams, ev RuleEvent) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("rules: trigger", t.ID, "panicked:", r)
			ok = false
		}
	}()
	return t.Match(p, ev)
}

func (e *rulesEngine) evaluate() { e.evaluateWith(e.options()) }

// evaluateWith re-checks every rule with a state trigger: it fires when the
// trigger and conditions start to hold and undoes when they stop. Rules that
// were removed or switched off are undone too.
func (e *rulesEngine) evaluateWith(o RulesOptions) {
	e.mu.Lock()
	defer e.mu.Unlock()
	live := make(map[string]bool, len(o.List))
	for _, r := range o.List {
		t := lookupTrigger(r.Trigger.Type)
		if !o.Enabled || !r.Enabled || t == nil || t.Check == nil {
			continue
		}
		live[r.ID] = true
		rt := e.runtime(r.ID)
		holds := safeCheck(t, r.Trigger.Params) && conditionsHold(r)
		was := rt.Active
		rt.Active, rt.known = holds, true
		switch {
		case holds && !was:
			e.fire(r, RuleEvent{}, false)
		case !holds && was && r.Undo:
			e.runUndo(rt)
		case !holds && was:
			rt.undo = nil
		}
	}
	for id, rt := range e.rt {
		if live[id] || !rt.Active && len(rt.undo) == 0 {
			continue
		}
		// Gone, off, or no longer a state rule: put back what it changed.
		rt.Active, rt.known = false, false
		e.runUndo(rt)
	}
	for id := range e.rt {
		if !ruleExists(o, id) {
			delete(e.rt, id)
		}
	}
}

func ruleExists(o RulesOptions, id string) bool {
	for _, r := range o.List {
		if r.ID == id {
			return true
		}
	}
	return false
}

func (e *rulesEngine) runUndo(rt *ruleRuntime) {
	undo := rt.undo
	rt.undo = nil
	for i := len(undo) - 1; i >= 0; i-- {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("rules: undo panicked:", r)
				}
			}()
			undo[i]()
		}()
	}
}

// fire runs a rule's actions unless its cooldown or the burst limit holds it
// back. The caller holds e.mu.
func (e *rulesEngine) fire(r Rule, ev RuleEvent, test bool) {
	rt := e.runtime(r.ID)
	now := e.now()
	if !test {
		if r.Cooldown > 0 && rt.LastFired > 0 && now.Sub(time.UnixMilli(rt.LastFired)) < time.Duration(r.Cooldown)*time.Second {
			return
		}
		cut := 0
		for cut < len(rt.recent) && now.Sub(rt.recent[cut]) >= time.Minute {
			cut++
		}
		rt.recent = rt.recent[cut:]
		if len(rt.recent) >= ruleBurst {
			if !rt.Paused {
				fmt.Println("rules:", ruleLabel(r), "fired too often, paused for a minute")
			}
			rt.Paused = true
			rt.Error = "Fired too often, so it is paused for a minute"
			return
		}
		rt.Paused = false
		rt.recent = append(rt.recent, now)
	}
	rt.LastFired = now.UnixMilli()
	rt.Fires++
	ctx := RuleContext{RuleID: r.ID, RuleName: r.Name, Event: ev, Test: test, eng: e}
	undo, errs := e.runActions(ctx, r)
	if !test && r.Undo {
		rt.undo = append(rt.undo, undo...)
	}
	rt.Error = strings.Join(errs, "; ")
}

func ruleLabel(r Rule) string {
	if r.Name != "" {
		return r.Name
	}
	return r.ID
}

// runActions runs r's actions in order, carrying on after a failure, and
// returns their undo functions and errors.
func (e *rulesEngine) runActions(ctx RuleContext, r Rule) (undo []func(), errs []string) {
	for i, step := range r.Actions {
		a := lookupAction(step.Type)
		if a == nil {
			errs = append(errs, fmt.Sprintf("action %d: %q isn't available", i+1, step.Type))
			continue
		}
		u, err := safeRun(a, ctx, step.Params)
		if err != nil {
			errs = append(errs, a.Label+": "+err.Error())
		}
		if u != nil {
			undo = append(undo, u)
		}
	}
	return undo, errs
}

func safeRun(a *RuleAction, ctx RuleContext, p RuleParams) (undo func(), err error) {
	defer func() {
		if r := recover(); r != nil {
			undo, err = nil, fmt.Errorf("panicked: %v", r)
		}
	}()
	return a.Run(ctx, p.clone())
}

// runOther runs the actions of the rule with id for "run another rule". The
// caller holds e.mu, since it runs inside an action.
func (e *rulesEngine) runOther(ctx RuleContext, id string) (func(), error) {
	if ctx.depth >= ruleMaxDepth {
		return nil, errors.New("rules run each other too deeply")
	}
	if id == ctx.RuleID {
		return nil, errors.New("a rule can't run itself")
	}
	var target *Rule
	for _, r := range e.options().List {
		if r.ID == id {
			target = &r
			break
		}
	}
	if target == nil {
		return nil, errors.New("that rule no longer exists")
	}
	sub := ctx
	sub.RuleID, sub.RuleName, sub.depth = target.ID, target.Name, ctx.depth+1
	undo, errs := e.runActions(sub, *target)
	var err error
	if len(errs) > 0 {
		err = errors.New(strings.Join(errs, "; "))
	}
	if len(undo) == 0 {
		return nil, err
	}
	return func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}, err
}

func (e *rulesEngine) test(id string) error {
	o := e.options()
	for _, r := range o.List {
		if r.ID != id {
			continue
		}
		if len(r.Actions) == 0 {
			return errors.New("this rule has no actions yet")
		}
		e.mu.Lock()
		e.fire(r, RuleEvent{Type: "test"}, true)
		msg := e.runtime(id).Error
		e.mu.Unlock()
		if msg != "" {
			return errors.New(msg)
		}
		return nil
	}
	return errors.New("no such rule")
}
