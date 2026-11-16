package modules

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Test triggers and actions, registered once for the whole package.
var (
	testRuleOnce  sync.Once
	testStateMu   sync.Mutex
	testState     = map[string]bool{} // test.state's answer by its "key" param
	testActMu     sync.Mutex
	testActCalls  []string // "run:<tag>" and "undo:<tag>"
	testActFailed = errors.New("boom")
)

func setTestState(key string, on bool) {
	testStateMu.Lock()
	testState[key] = on
	testStateMu.Unlock()
}

func takeTestCalls() []string {
	testActMu.Lock()
	defer testActMu.Unlock()
	out := testActCalls
	testActCalls = nil
	return out
}

func registerTestRules(t *testing.T) {
	t.Helper()
	testRuleOnce.Do(func() {
		mustRegister(RegisterRuleTrigger(RuleTrigger{
			ID: "test.state", Label: "Test state", Category: "Test",
			Fields: []RuleField{{Key: "key", Label: "Key", Type: FieldText}},
			Check: func(p RuleParams) bool {
				testStateMu.Lock()
				defer testStateMu.Unlock()
				return testState[p.String("key")]
			},
		}))
		mustRegister(RegisterRuleTrigger(RuleTrigger{
			ID: "test.event", Label: "Test event", Category: "Test",
			Fields: []RuleField{{Key: "name", Label: "Name", Type: FieldText}},
			Match:  func(p RuleParams, ev RuleEvent) bool { return MatchText(p.String("name"), ev.Data["name"]) },
		}))
		mustRegister(RegisterRuleAction(RuleAction{
			ID: "test.act", Label: "Test action", Category: "Test",
			Fields: []RuleField{{Key: "tag", Label: "Tag", Type: FieldText}, {Key: "fail", Label: "Fail", Type: FieldBool}},
			Run: func(_ RuleContext, p RuleParams) (func(), error) {
				tag := p.String("tag")
				testActMu.Lock()
				testActCalls = append(testActCalls, "run:"+tag)
				testActMu.Unlock()
				if p.Bool("fail") {
					return nil, testActFailed
				}
				return func() {
					testActMu.Lock()
					testActCalls = append(testActCalls, "undo:"+tag)
					testActMu.Unlock()
				}, nil
			},
		}))
	})
	takeTestCalls()
	testStateMu.Lock()
	testState = map[string]bool{}
	testStateMu.Unlock()
}

func act(tag string) RuleStep {
	return RuleStep{Type: "test.act", Params: RuleParams{"tag": tag}}
}

func stateRule(id, key string, undo bool, actions ...RuleStep) Rule {
	return Rule{ID: id, Enabled: true, Undo: undo, Trigger: RuleStep{Type: "test.state", Params: RuleParams{"key": key}}, Actions: actions}
}

// testEngine runs rules from a list the test controls, on a fake clock.
type testEngine struct {
	*rulesEngine
	mu   sync.Mutex
	opts RulesOptions
	at   time.Time
}

func newTestEngine(rules ...Rule) *testEngine {
	te := &testEngine{opts: RulesOptions{Enabled: true, List: rules}, at: time.Date(2026, 1, 5, 12, 0, 0, 0, time.Local)}
	te.rulesEngine = newRulesEngine(func() RulesOptions {
		te.mu.Lock()
		defer te.mu.Unlock()
		return te.opts.clone()
	})
	te.rulesEngine.now = func() time.Time {
		te.mu.Lock()
		defer te.mu.Unlock()
		return te.at
	}
	return te
}

func (te *testEngine) set(fn func(o *RulesOptions)) {
	te.mu.Lock()
	fn(&te.opts)
	te.mu.Unlock()
}

func (te *testEngine) advance(d time.Duration) {
	te.mu.Lock()
	te.at = te.at.Add(d)
	te.mu.Unlock()
}

func wantCalls(t *testing.T, step string, want ...string) {
	t.Helper()
	got := takeTestCalls()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: calls %v, want %v", step, got, want)
	}
}

func TestStateRuleFiresOnceAndUndoes(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine(stateRule("a", "world", true, act("1"), act("2")))
	te.evaluate()
	wantCalls(t, "not holding")
	setTestState("world", true)
	te.evaluate()
	wantCalls(t, "starts", "run:1", "run:2")
	te.evaluate()
	wantCalls(t, "still holds")
	if s := te.statuses()["a"]; !s.Active || s.Fires != 1 || s.LastFired == 0 || !s.Undoable {
		t.Errorf("status %+v", s)
	}
	setTestState("world", false)
	te.evaluate()
	wantCalls(t, "stops", "undo:2", "undo:1")
	if s := te.statuses()["a"]; s.Active || s.Undoable {
		t.Errorf("status after stop %+v", s)
	}
}

func TestStateRuleWithoutUndoKeepsChanges(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine(stateRule("a", "k", false, act("1")))
	setTestState("k", true)
	te.evaluate()
	setTestState("k", false)
	te.evaluate()
	wantCalls(t, "no undo", "run:1")
}

func TestConditionsGateAndNot(t *testing.T) {
	registerTestRules(t)
	r := stateRule("a", "trig", true, act("1"))
	r.Conditions = []RuleStep{
		{Type: "test.state", Params: RuleParams{"key": "cond"}},
		{Type: "test.state", Not: true, Params: RuleParams{"key": "blocker"}},
	}
	te := newTestEngine(r)
	setTestState("trig", true)
	te.evaluate()
	wantCalls(t, "condition false")
	setTestState("cond", true)
	te.evaluate()
	wantCalls(t, "condition true", "run:1")
	setTestState("blocker", true)
	te.evaluate()
	wantCalls(t, "negated condition fails", "undo:1")
}

func TestEventRuleMatchesAndCooldown(t *testing.T) {
	registerTestRules(t)
	r := Rule{ID: "e", Enabled: true, Cooldown: 60, Trigger: RuleStep{Type: "test.event", Params: RuleParams{"name": "/^fri/"}}, Actions: []RuleStep{act("hi")}}
	te := newTestEngine(r)
	te.handleEvent(RuleEvent{Type: "test.event", Data: map[string]string{"name": "Stark"}})
	wantCalls(t, "no match")
	te.handleEvent(RuleEvent{Type: "test.event", Data: map[string]string{"name": "Frieren"}})
	wantCalls(t, "match", "run:hi")
	te.advance(30 * time.Second)
	te.handleEvent(RuleEvent{Type: "test.event", Data: map[string]string{"name": "Frieren"}})
	wantCalls(t, "cooldown")
	te.advance(31 * time.Second)
	te.handleEvent(RuleEvent{Type: "test.event", Data: map[string]string{"name": "Frieren"}})
	wantCalls(t, "after cooldown", "run:hi")
	te.handleEvent(RuleEvent{Type: "other", Data: map[string]string{"name": "Frieren"}})
	wantCalls(t, "other event type")
}

func TestEventRuleNeedsConditions(t *testing.T) {
	registerTestRules(t)
	r := Rule{ID: "e", Enabled: true, Trigger: RuleStep{Type: "test.event"}, Conditions: []RuleStep{{Type: "test.state", Params: RuleParams{"key": "vr"}}}, Actions: []RuleStep{act("x")}}
	te := newTestEngine(r)
	te.handleEvent(RuleEvent{Type: "test.event"})
	wantCalls(t, "condition off")
	setTestState("vr", true)
	te.handleEvent(RuleEvent{Type: "test.event"})
	wantCalls(t, "condition on", "run:x")
	// An event trigger used as a condition never holds.
	te.set(func(o *RulesOptions) { o.List[0].Conditions = []RuleStep{{Type: "test.event"}} })
	te.handleEvent(RuleEvent{Type: "test.event"})
	wantCalls(t, "event as condition")
}

func TestDisablingAnActiveRuleUndoesIt(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine(stateRule("a", "k", true, act("1")), stateRule("b", "k", true, act("2")))
	setTestState("k", true)
	te.evaluate()
	wantCalls(t, "both start", "run:1", "run:2")
	te.set(func(o *RulesOptions) { o.List[0].Enabled = false })
	te.evaluate()
	wantCalls(t, "a off", "undo:1")
	te.set(func(o *RulesOptions) { o.List = o.List[:0] })
	te.evaluate()
	wantCalls(t, "b removed", "undo:2")
	if len(te.statuses()) != 0 {
		t.Errorf("statuses of removed rules kept: %v", te.statuses())
	}
	te.set(func(o *RulesOptions) { o.List = []Rule{stateRule("c", "k", true, act("3"))}; o.Enabled = false })
	te.evaluate()
	wantCalls(t, "master off")
	te.set(func(o *RulesOptions) { o.Enabled = true })
	te.evaluate()
	wantCalls(t, "master on", "run:3")
	te.set(func(o *RulesOptions) { o.Enabled = false })
	te.evaluate()
	wantCalls(t, "master off again", "undo:3")
}

func TestBurstLimitPausesARule(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine(Rule{ID: "e", Enabled: true, Trigger: RuleStep{Type: "test.event"}, Actions: []RuleStep{act("x")}})
	for i := 0; i < ruleBurst+5; i++ {
		te.handleEvent(RuleEvent{Type: "test.event"})
	}
	if n := len(takeTestCalls()); n != ruleBurst {
		t.Errorf("fired %d times, want %d", n, ruleBurst)
	}
	if s := te.statuses()["e"]; !s.Paused || s.Error == "" {
		t.Errorf("status %+v, want paused", s)
	}
	te.advance(61 * time.Second)
	te.handleEvent(RuleEvent{Type: "test.event"})
	wantCalls(t, "after a minute", "run:x")
	if s := te.statuses()["e"]; s.Paused {
		t.Errorf("still paused: %+v", s)
	}
}

func TestActionErrorsAreReportedAndOthersRun(t *testing.T) {
	registerTestRules(t)
	bad := act("bad")
	bad.Params["fail"] = true
	te := newTestEngine(Rule{ID: "e", Enabled: true, Trigger: RuleStep{Type: "test.event"},
		Actions: []RuleStep{bad, {Type: "missing.action"}, act("ok")}})
	te.handleEvent(RuleEvent{Type: "test.event"})
	wantCalls(t, "run", "run:bad", "run:ok")
	s := te.statuses()["e"]
	if !strings.Contains(s.Error, "boom") || !strings.Contains(s.Error, "missing.action") {
		t.Errorf("error %q", s.Error)
	}
}

func TestTestRuleIgnoresTriggerAndRecordsNoUndo(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine(stateRule("a", "never", true, act("1")), Rule{ID: "empty", Enabled: true})
	if err := te.test("a"); err != nil {
		t.Fatal(err)
	}
	wantCalls(t, "test", "run:1")
	if s := te.statuses()["a"]; s.Fires != 1 || s.Undoable {
		t.Errorf("status %+v", s)
	}
	if err := te.test("nope"); err == nil {
		t.Error("testing a missing rule should fail")
	}
	if err := te.test("empty"); err == nil {
		t.Error("testing a rule with no actions should fail")
	}
}

func TestRunAnotherRule(t *testing.T) {
	registerTestRules(t)
	run := func(id string) RuleStep { return RuleStep{Type: ActionRunRule, Params: RuleParams{"rule": id}} }
	te := newTestEngine(
		Rule{ID: "main", Enabled: true, Undo: true, Trigger: RuleStep{Type: "test.state", Params: RuleParams{"key": "k"}}, Actions: []RuleStep{run("helper")}},
		Rule{ID: "helper", Enabled: false, Actions: []RuleStep{act("h1"), act("h2")}},
		Rule{ID: "self", Enabled: true, Trigger: RuleStep{Type: "test.event"}, Actions: []RuleStep{run("self")}},
		Rule{ID: "loop1", Actions: []RuleStep{run("loop2")}},
		Rule{ID: "loop2", Actions: []RuleStep{run("loop1")}},
	)
	setTestState("k", true)
	te.evaluate()
	wantCalls(t, "runs helper", "run:h1", "run:h2")
	setTestState("k", false)
	te.evaluate()
	wantCalls(t, "undoes helper", "undo:h2", "undo:h1")

	te.handleEvent(RuleEvent{Type: "test.event"})
	if s := te.statuses()["self"]; !strings.Contains(s.Error, "itself") {
		t.Errorf("self error %q", s.Error)
	}
	if err := te.test("loop1"); err == nil || !strings.Contains(err.Error(), "deeply") {
		t.Errorf("loop error %v", err)
	}
}

func TestPanickingTriggerAndActionAreContained(t *testing.T) {
	registerTestRules(t)
	te := newTestEngine()
	rt := &RuleTrigger{ID: "x", Check: func(RuleParams) bool { panic("no") }}
	if safeCheck(rt, nil) {
		t.Error("a panicking check should not hold")
	}
	ra := &RuleAction{ID: "y", Run: func(RuleContext, RuleParams) (func(), error) { panic("no") }}
	if _, err := safeRun(ra, RuleContext{eng: te.rulesEngine}, nil); err == nil {
		t.Error("a panicking action should report an error")
	}
}

func TestRegisterRejectsBadDefinitions(t *testing.T) {
	registerTestRules(t)
	cases := []error{
		RegisterRuleTrigger(RuleTrigger{ID: "Bad ID", Label: "x"}),
		RegisterRuleTrigger(RuleTrigger{ID: "test.nolabel"}),
		RegisterRuleTrigger(RuleTrigger{ID: "test.state", Label: "again"}),
		RegisterRuleTrigger(RuleTrigger{ID: "test.badfield", Label: "x", Fields: []RuleField{{Key: "a", Type: "color"}}}),
		RegisterRuleTrigger(RuleTrigger{ID: "test.dupfield", Label: "x", Fields: []RuleField{{Key: "a", Type: FieldText}, {Key: "a", Type: FieldText}}}),
		RegisterRuleAction(RuleAction{ID: "test.norun", Label: "x"}),
		RegisterRuleAction(RuleAction{ID: "test.act", Label: "again", Run: func(RuleContext, RuleParams) (func(), error) { return nil, nil }}),
	}
	for i, err := range cases {
		if err == nil {
			t.Errorf("case %d registered", i)
		}
	}
}

func TestRegistryListsAndSchemaJSON(t *testing.T) {
	registerTestRules(t)
	var world *RuleTrigger
	triggers := RuleTriggers()
	for i := range triggers {
		if triggers[i].ID == TriggerWorld {
			world = &triggers[i]
		}
	}
	if world == nil || !world.State || triggers[0].Category != "VRChat" {
		t.Fatalf("world trigger %+v, first %+v", world, triggers[0])
	}
	data, err := json.Marshal(triggers)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Check") || !strings.Contains(string(data), `"state":true`) {
		t.Errorf("schema JSON %s", data[:200])
	}
	last := triggers[len(triggers)-1]
	if last.Category != "Test" {
		t.Errorf("unknown categories should sort last, got %s", last.Category)
	}
	actions := RuleActions()
	found := map[string]bool{}
	for _, a := range actions {
		found[a.ID] = true
	}
	for _, id := range []string{ActionProfile, ActionLine, ActionChat, ActionAvatarParam, ActionGroup, ActionDND, ActionRunRule} {
		if !found[id] {
			t.Errorf("action %s missing", id)
		}
	}
	sources := RuleSources()
	if len(sources[SourceLines]) != len(DefaultOrder) {
		t.Errorf("lines source %v", sources[SourceLines])
	}
}

func TestSanitizeRuleParams(t *testing.T) {
	fields := []RuleField{
		{Key: "n", Type: FieldNumber, Min: 0, Max: 10, Step: 1, Default: 3.0},
		{Key: "s", Type: FieldSelect, Options: []RuleOption{{"a", "A"}, {"b", "B"}}},
		{Key: "src", Type: FieldSelect, Source: "profiles"},
		{Key: "m", Type: FieldMulti, Options: weekdays},
		{Key: "t", Type: FieldTime, Default: "22:00"},
		{Key: "b", Type: FieldBool, Default: true},
		{Key: "x", Type: FieldText, MaxLen: 5},
	}
	got := SanitizeRuleParams(fields, RuleParams{
		"n": 12.7, "s": "zzz", "src": "Any Name", "m": []any{"mon", "bogus", "mon", "fri"},
		"t": "25:00", "x": "hello\nworld", "extra": "dropped",
	})
	want := RuleParams{"n": 10.0, "s": "a", "src": "Any Name", "m": []string{"mon", "fri"}, "t": "22:00", "b": true, "x": "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
	got = SanitizeRuleParams(fields, nil)
	if got["n"] != 3.0 || got["s"] != "a" || got["t"] != "22:00" || !got["b"].(bool) || len(got["m"].([]string)) != 0 {
		t.Errorf("defaults %#v", got)
	}
	if got := SanitizeRuleParams(fields, RuleParams{"n": 4.4}); got["n"] != 4.0 {
		t.Errorf("step rounding %v", got["n"])
	}
}

func TestSanitizeRules(t *testing.T) {
	registerTestRules(t)
	c := DefaultConfig()
	if !c.Rules.Enabled || c.Rules.List == nil {
		t.Fatalf("default rules %+v", c.Rules)
	}
	long := strings.Repeat("x", 100)
	var data = `{"Rules":{"enabled":true,"list":[
		{"id":"same","name":"` + long + `","enabled":true,"cooldown":999999,
		 "trigger":{"type":"world","not":true,"params":{"world":"Cat","junk":1}},
		 "conditions":[{"type":"vr_mode","not":true,"params":{"mode":"weird"}}],
		 "actions":[{"type":"chat","not":true,"params":{"text":"hi","seconds":5000}}]},
		{"id":"same","trigger":{"type":"from.plugin","params":{"keep":"me","n":2,"l":["a"]}},"actions":null},
		{"id":"bad id!","trigger":{"type":"started"}}
	]}}`
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		t.Fatal(err)
	}
	c.sanitize()
	list := c.Rules.List
	if len(list) != 3 {
		t.Fatalf("rules %d", len(list))
	}
	r := list[0]
	if r.ID != "same" || len([]rune(r.Name)) != 60 || r.Cooldown != MaxRuleCooldown || r.Trigger.Not {
		t.Errorf("rule 0 %+v", r)
	}
	if !reflect.DeepEqual(r.Trigger.Params, RuleParams{"world": "Cat"}) {
		t.Errorf("trigger params %v", r.Trigger.Params)
	}
	if !r.Conditions[0].Not || r.Conditions[0].Params.String("mode") != "vr" {
		t.Errorf("condition %+v", r.Conditions[0])
	}
	if a := r.Actions[0]; a.Not || a.Params.Number("seconds") != 600 || a.Params.String("text") != "hi" {
		t.Errorf("action %+v", a)
	}
	if list[1].ID == "same" || list[1].ID == "" || list[2].ID == "bad id!" {
		t.Errorf("ids %q %q", list[1].ID, list[2].ID)
	}
	if p := list[1].Trigger.Params; p.String("keep") != "me" || p.Number("n") != 2 || !reflect.DeepEqual(p.List("l"), []string{"a"}) {
		t.Errorf("unknown trigger params %v", p)
	}
	if list[1].Actions == nil || list[1].Conditions == nil {
		t.Error("nil lists should become empty")
	}

	many := make([]Rule, MaxRules+5)
	c.Rules.List = many
	c.sanitize()
	if len(c.Rules.List) != MaxRules {
		t.Errorf("kept %d rules", len(c.Rules.List))
	}
	// Cloning must not share params.
	c.Rules.List = []Rule{{ID: "a", Trigger: RuleStep{Type: "time", Params: RuleParams{"days": []string{"mon"}}}}}
	cp := c.clone()
	cp.Rules.List[0].Trigger.Params["days"].([]string)[0] = "tue"
	if c.Rules.List[0].Trigger.Params.List("days")[0] != "mon" {
		t.Error("clone shares rule params")
	}
}

func TestMatchText(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"", "anything", true},
		{"cat", "The Black Cat", true},
		{"dog", "The Black Cat", false},
		{"/^the .* cat$/", "The Black Cat", true},
		{"/^cat/", "The Black Cat", false},
		{"/[/", "[", false},
		{"wrld_123", "wrld_123", true},
	}
	for _, c := range cases {
		if got := MatchText(c.pattern, c.s); got != c.want {
			t.Errorf("MatchText(%q, %q) = %v", c.pattern, c.s, got)
		}
	}
}

func TestInTimeWindow(t *testing.T) {
	mon := func(h, m int) time.Time { return time.Date(2026, 1, 5, h, m, 0, 0, time.Local) } // a Monday
	all := allWeekdays()
	cases := []struct {
		name     string
		at       time.Time
		from, to string
		days     []string
		want     bool
	}{
		{"inside", mon(10, 0), "09:00", "17:00", all, true},
		{"end is exclusive", mon(17, 0), "09:00", "17:00", all, false},
		{"before", mon(8, 59), "09:00", "17:00", all, false},
		{"wrong day", mon(10, 0), "09:00", "17:00", []string{"tue"}, false},
		{"overnight evening", mon(23, 0), "22:00", "06:00", []string{"mon"}, true},
		{"overnight morning counts for the day before", mon(5, 0), "22:00", "06:00", []string{"sun"}, true},
		{"overnight morning, not the day before", mon(5, 0), "22:00", "06:00", []string{"mon"}, false},
		{"all day", mon(3, 0), "00:00", "00:00", []string{"mon"}, true},
		{"no days is every day", mon(10, 0), "09:00", "17:00", nil, true},
	}
	for _, c := range cases {
		from, _ := parseClock(c.from)
		to, _ := parseClock(c.to)
		if got := inTimeWindow(c.at, from, to, c.days); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	for _, bad := range []string{"", "9:00", "24:00", "12:60", "ab:cd"} {
		if validClock(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTimeTriggerUsesClock(t *testing.T) {
	old := ruleNowFn
	t.Cleanup(func() { ruleNowFn = old })
	ruleNowFn = func() time.Time { return time.Date(2026, 1, 5, 23, 30, 0, 0, time.Local) }
	trig := lookupTrigger(TriggerTime)
	p := SanitizeRuleParams(trig.Fields, nil)
	if !trig.Check(p) {
		t.Errorf("default 22:00-06:00 every day should hold at 23:30 (%v)", p)
	}
	p["days"] = []string{}
	if trig.Check(p) {
		t.Error("no days ticked should never hold")
	}
}

func TestCompareParam(t *testing.T) {
	cases := []struct {
		op      string
		v, want float64
		ok      bool
	}{
		{"true", 1, 0, true}, {"true", 0, 0, false}, {"false", 0, 0, true},
		{"eq", 0.5, 0.5, true}, {"eq", 0.50001, 0.5, true}, {"ne", 3, 2, true},
		{"gt", 0.6, 0.5, true}, {"gt", 0.5, 0.5, false}, {"lt", 2, 3, true}, {"bogus", 1, 1, false},
	}
	for _, c := range cases {
		if got := compareParam(c.op, c.v, c.want); got != c.ok {
			t.Errorf("%s %v %v = %v", c.op, c.v, c.want, got)
		}
	}
}

func TestBuiltinStateTriggers(t *testing.T) {
	oldWorld, oldAvatar, oldVR, oldFg, oldAfk := ruleWorld, ruleAvatar, ruleVRFn, ruleFgFn, ruleAfkFn
	t.Cleanup(func() {
		ruleWorld, ruleAvatar, ruleVRFn, ruleFgFn, ruleAfkFn = oldWorld, oldAvatar, oldVR, oldFg, oldAfk
	})
	check := func(id string, p RuleParams) bool {
		trig := lookupTrigger(id)
		return trig.Check(SanitizeRuleParams(trig.Fields, p))
	}

	ruleWorld = func() (WorldInfo, bool) { return WorldInfo{Name: "The Black Cat", ID: "wrld_abc", Loading: true}, true }
	if check(TriggerWorld, RuleParams{"world": "cat"}) {
		t.Error("a world still loading should not hold")
	}
	ruleWorld = func() (WorldInfo, bool) { return WorldInfo{Name: "The Black Cat", ID: "wrld_abc"}, true }
	if !check(TriggerWorld, RuleParams{"world": "cat"}) || !check(TriggerWorld, RuleParams{"world": "wrld_abc"}) || check(TriggerWorld, RuleParams{"world": "dog"}) {
		t.Error("world matching")
	}
	ruleWorld = func() (WorldInfo, bool) { return WorldInfo{}, false }
	if check(TriggerWorld, nil) {
		t.Error("not in a world")
	}

	ruleAvatar = func() (string, string) { return "avtr_1", "Fern" }
	if !check(TriggerAvatar, RuleParams{"avatar": "fern"}) || !check(TriggerAvatar, RuleParams{"avatar": "avtr_1"}) || check(TriggerAvatar, RuleParams{"avatar": "stark"}) {
		t.Error("avatar matching")
	}

	ruleVRFn = func() bool { return true }
	if !check(TriggerVRMode, RuleParams{"mode": "vr"}) || check(TriggerVRMode, RuleParams{"mode": "desktop"}) {
		t.Error("VR mode")
	}

	ruleFgFn = func() foreground { return foreground{Exe: "spotify.exe", Title: "Song - Artist"} }
	if !check(TriggerApp, RuleParams{"app": "spotify"}) || !check(TriggerApp, RuleParams{"app": "artist"}) || check(TriggerApp, RuleParams{"app": ""}) {
		t.Error("focused app")
	}

	ruleAfkFn = func() bool { return true }
	if !check(TriggerAfk, nil) {
		t.Error("afk")
	}
}

func TestAvatarParamTrigger(t *testing.T) {
	resetAvatarCache(t)
	trig := lookupTrigger(TriggerAvatarParam)
	p := func(name, op string, v float64) RuleParams {
		return SanitizeRuleParams(trig.Fields, RuleParams{"name": name, "op": op, "value": v})
	}
	if trig.Check(p("MuteSelf", "true", 0)) {
		t.Error("unknown parameter should not hold")
	}
	receiveTestOSC(t, "/avatar/parameters/MuteSelf", true)
	receiveTestOSC(t, "/avatar/parameters/Volume", float32(0.75))
	if !trig.Check(p("MuteSelf", "true", 0)) || trig.Check(p("MuteSelf", "false", 0)) {
		t.Error("bool test")
	}
	if !trig.Check(p("/avatar/parameters/Volume", "gt", 0.5)) || trig.Check(p("Volume", "lt", 0.5)) || !trig.Check(p("Volume", "eq", 0.75)) {
		t.Error("number tests")
	}
}

func TestMatchPlayerAndProfileEvents(t *testing.T) {
	joined := lookupTrigger(TriggerPlayerJoined)
	p := SanitizeRuleParams(joined.Fields, RuleParams{"name": "fri"})
	if !joined.Match(p, RuleEvent{Data: map[string]string{"name": "Frieren"}}) || joined.Match(p, RuleEvent{Data: map[string]string{"name": "Stark"}}) {
		t.Error("player name match")
	}
	me := RuleEvent{Data: map[string]string{"name": "Me", "self": "1"}}
	if joined.Match(SanitizeRuleParams(joined.Fields, nil), me) {
		t.Error("own join should be skipped by default")
	}
	if !joined.Match(SanitizeRuleParams(joined.Fields, RuleParams{"self": true}), me) {
		t.Error("own join with Count yourself")
	}
	prof := lookupTrigger(TriggerProfileSwitched)
	if !prof.Match(RuleParams{"profile": "club  night"}, RuleEvent{Data: map[string]string{"profile": "Club Night"}}) ||
		prof.Match(RuleParams{"profile": "Chill"}, RuleEvent{Data: map[string]string{"profile": "Club"}}) ||
		!prof.Match(RuleParams{"profile": ""}, RuleEvent{Data: map[string]string{"profile": "Club"}}) {
		t.Error("profile match")
	}
}

func TestNoteAvatarSwitchAndAfkEnd(t *testing.T) {
	logMu.Lock()
	oldSelf, oldAvatar := selfName, selfAvatar
	selfName = "Me"
	parseWorldLine("2026.01.05 12:00:00 Log - [Behaviour] Switching Someone to avatar Theirs", time.Now())
	parseWorldLine("2026.01.05 12:00:00 Log - [Behaviour] Switching Me to avatar Fern Mage", time.Now())
	got := selfAvatar
	selfName, selfAvatar = oldSelf, oldAvatar
	logMu.Unlock()
	if got != "Fern Mage" {
		t.Errorf("avatar %q", got)
	}

	for len(ruleEvents) > 0 {
		<-ruleEvents
	}
	afkWatch.Lock()
	afkWatch.known, afkWatch.on = false, false
	afkWatch.Unlock()
	noteAfk(true) // only learns the state
	noteAfk(true)
	noteAfk(false)
	noteAfk(false)
	if n := len(ruleEvents); n != 1 {
		t.Fatalf("%d events, want 1", n)
	}
	if ev := <-ruleEvents; ev.Type != TriggerAfkEnd {
		t.Errorf("event %v", ev)
	}
}

func TestExpandRuleText(t *testing.T) {
	old := ruleWorld
	t.Cleanup(func() { ruleWorld = old })
	ruleWorld = func() (WorldInfo, bool) { return WorldInfo{Name: "Cat"}, true }
	got := expandRuleText("Hi {name}, welcome to {world}", RuleEvent{Data: map[string]string{"name": "Stark"}})
	if got != "Hi Stark, welcome to Cat" {
		t.Errorf("got %q", got)
	}
}

// receiveTestOSC delivers one message as if VRChat had sent it.
func receiveTestOSC(t *testing.T, addr string, value any) {
	t.Helper()
	deliverOSC(osc.NewMessage(addr, value))
}

func runAction(t *testing.T, id string, p RuleParams) (func(), error) {
	t.Helper()
	a := lookupAction(id)
	return a.Run(RuleContext{RuleID: "t"}, SanitizeRuleParams(a.Fields, p))
}

func TestLineAndGroupActions(t *testing.T) {
	c := DefaultConfig()
	c.DisplayOptions.Memory = false
	c.DisplayOptions.Text = false
	c.TextOptions.Groups = []string{"Night", "Busy"}
	useTestConfig(t, c)

	undo, err := runAction(t, ActionLine, RuleParams{"line": ModuleMemory, "state": "on"})
	if err != nil || !GetConfig().DisplayOptions.Memory || undo == nil {
		t.Fatalf("line on: %v", err)
	}
	undo()
	if GetConfig().DisplayOptions.Memory {
		t.Error("undo should turn it back off")
	}
	if undo, _ := runAction(t, ActionLine, RuleParams{"line": ModuleMemory, "state": "off"}); undo != nil {
		t.Error("no change should have no undo")
	}
	if _, err := runAction(t, ActionLine, RuleParams{"line": "bogus"}); err == nil {
		t.Error("unknown line accepted")
	}

	undo, err = runAction(t, ActionGroup, RuleParams{"group": "night", "show": true})
	if err != nil {
		t.Fatal(err)
	}
	if cur := GetConfig(); cur.TextOptions.Group != "night" && cur.TextOptions.Group != "Night" || !cur.DisplayOptions.Text {
		t.Errorf("group %q text %v", cur.TextOptions.Group, cur.DisplayOptions.Text)
	}
	undo()
	if cur := GetConfig(); cur.TextOptions.Group != "" || cur.DisplayOptions.Text {
		t.Errorf("after undo group %q text %v", cur.TextOptions.Group, cur.DisplayOptions.Text)
	}
	if _, err := runAction(t, ActionGroup, RuleParams{"group": "Missing"}); err == nil {
		t.Error("missing group accepted")
	}

	// Undo leaves a group someone picked since alone.
	undo, _ = runAction(t, ActionGroup, RuleParams{"group": "Night"})
	UpdateConfig(func(c *Config) { c.TextOptions.Group = "Busy" })
	undo()
	if g := GetConfig().TextOptions.Group; g != "Busy" {
		t.Errorf("undo replaced a later choice: %q", g)
	}
}

func TestDNDAndChatActions(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	ClearHold()
	t.Cleanup(ClearHold)
	undo, err := runAction(t, ActionDND, RuleParams{"state": "on"})
	if err != nil || !DNDActive() {
		t.Fatal("dnd on", err)
	}
	undo()
	if DNDActive() {
		t.Error("dnd undo")
	}
	if got := ToggleDND(); !got || !DNDActive() {
		t.Error("toggle on")
	}
	if !SetDND(false) || SetDND(false) {
		t.Error("SetDND should report changes only")
	}

	old := ruleWorld
	t.Cleanup(func() { ruleWorld = old })
	ruleWorld = func() (WorldInfo, bool) { return WorldInfo{}, false }
	a := lookupAction(ActionChat)
	undo, err = a.Run(RuleContext{Event: RuleEvent{Data: map[string]string{"name": "Stark"}}}, SanitizeRuleParams(a.Fields, RuleParams{"text": "Hi {name}", "seconds": 30.0}))
	if err != nil {
		t.Fatal(err)
	}
	if text, until, pinned := HoldState(); text != "Hi Stark" || pinned || until == 0 {
		t.Errorf("hold %q %v %v", text, until, pinned)
	}
	undo()
	if text, _, _ := HoldState(); text != "" {
		t.Errorf("undo left %q", text)
	}
	if _, err := runAction(t, ActionChat, RuleParams{"text": "  "}); err == nil {
		t.Error("empty message accepted")
	}
}

func TestAvatarParamAction(t *testing.T) {
	resetAvatarCache(t)
	got := captureAvatarSend(t)
	useTestConfig(t, DefaultConfig())
	undo, err := runAction(t, ActionAvatarParam, RuleParams{"name": "Glasses", "kind": "bool", "on": true})
	if err != nil || undo != nil {
		t.Fatalf("first send: %v, undo %v", err, undo != nil)
	}
	receiveTestOSC(t, "/avatar/parameters/Hue", float32(0.25))
	undo, err = runAction(t, ActionAvatarParam, RuleParams{"name": "Hue", "kind": "float", "value": 0.9})
	if err != nil || undo == nil {
		t.Fatalf("float send: %v", err)
	}
	undo()
	if _, err := runAction(t, ActionAvatarParam, RuleParams{"name": ""}); err == nil {
		t.Error("empty name accepted")
	}
	sent := *got
	if len(sent) != 3 || sent[0].Address != "/avatar/parameters/Glasses" || sent[0].Arguments[0] != true ||
		sent[2].Address != "/avatar/parameters/Hue" || sent[2].Arguments[0] != float32(0.25) {
		t.Errorf("sent %v", sent)
	}
}

func TestProfileActionAndUndo(t *testing.T) {
	useTempProfiles(t)
	if err := CreateProfile("Home"); err != nil {
		t.Fatal(err)
	}
	if err := CreateProfile("Club"); err != nil {
		t.Fatal(err)
	}
	if err := SwitchProfile("Home"); err != nil {
		t.Fatal(err)
	}
	// Rules are per PC: a profile switch keeps them.
	UpdateConfig(func(c *Config) { c.Rules.List = []Rule{{ID: "keep", Name: "kept"}} })
	undo, err := runAction(t, ActionProfile, RuleParams{"profile": "club"})
	if err != nil || ActiveProfile() != "Club" || undo == nil {
		t.Fatalf("switch: %v, active %q", err, ActiveProfile())
	}
	if l := GetConfig().Rules.List; len(l) != 1 || l[0].ID != "keep" {
		t.Errorf("rules after switch %v", l)
	}
	undo()
	if ActiveProfile() != "Home" {
		t.Errorf("undo left %q", ActiveProfile())
	}
	if undo, _ := runAction(t, ActionProfile, RuleParams{"profile": "Home"}); undo != nil {
		t.Error("switching to the active profile should have no undo")
	}
	if _, err := runAction(t, ActionProfile, RuleParams{"profile": "Nope"}); err == nil {
		t.Error("missing profile accepted")
	}
}

func TestFireTriggerNeverBlocks(t *testing.T) {
	done := make(chan struct{})
	go func() {
		for i := 0; i < ruleEventBuffer*2; i++ {
			FireTrigger("test.flood", nil)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("FireTrigger blocked")
	}
	for len(ruleEvents) > 0 {
		<-ruleEvents
	}
}
