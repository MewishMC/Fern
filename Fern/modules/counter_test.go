package modules

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// useCounters makes defs the live counters, with counts kept in a
// temporary file, and puts everything back afterwards.
func useCounters(t *testing.T, defs ...CounterDef) (path string, now *time.Time) {
	t.Helper()
	c := DefaultConfig()
	c.CounterOptions.Counters = defs
	c.sanitize()
	useConfig(t, c)
	storeTallyOptions(c)
	path = filepath.Join(t.TempDir(), "counters.json")
	clock := timerT0
	counterMu.Lock()
	oldFile, oldNow := counterFile, counterNow
	counterFile, counterNow = path, func() time.Time { return clock }
	counterCounts, counterLoaded, counterPrev, counterQuiet = map[string]*counterCount{}, false, map[string]float64{}, time.Time{}
	counterMu.Unlock()
	t.Cleanup(func() {
		counterMu.Lock()
		if counterSave != nil {
			counterSave.Stop()
		}
		counterFile, counterNow = oldFile, oldNow
		counterCounts, counterLoaded, counterPrev = map[string]*counterCount{}, false, map[string]float64{}
		counterMu.Unlock()
		tallyCache.Store(nil)
	})
	return path, &clock
}

func TestCounterSanitize(t *testing.T) {
	o := CounterOptions{Separator: "", Counters: []CounterDef{
		{ID: "a", Name: " Head\npats ", Param: "/avatar/parameters/HeadPat", Mode: "x", Cooldown: -1, Threshold: 900, Hotkey: "nope"},
		{ID: "a", Template: "{name}", ShowSec: 99999},
		{ID: "BAD ID!"},
	}}
	for i := 0; i < 12; i++ {
		o.Counters = append(o.Counters, CounterDef{})
	}
	o.sanitize()
	if len(o.Counters) != MaxCounters || o.Separator != " | " {
		t.Fatalf("len %d sep %q", len(o.Counters), o.Separator)
	}
	a := o.Counters[0]
	if a.ID != "a" || a.Name != "Head pats" || a.Param != "HeadPat" || a.Mode != CounterRising ||
		a.Cooldown != 0 || a.Threshold != 255 || a.Hotkey != "" || a.Template != DefaultCounterTemplate || a.ShowSec != 15 {
		t.Errorf("first %+v", a)
	}
	ids := map[string]bool{}
	for _, c := range o.Counters {
		if c.ID == "" || ids[c.ID] {
			t.Errorf("IDs must be unique and set: %q", c.ID)
		}
		ids[c.ID] = true
	}
	if o.Counters[1].ShowSec != 3600 || o.Counters[2].ID == "BAD ID!" {
		t.Errorf("second %+v third %+v", o.Counters[1], o.Counters[2])
	}
}

func TestCounterEdge(t *testing.T) {
	rising := CounterDef{Mode: CounterRising, Threshold: 0.5}
	change := CounterDef{Mode: CounterChange}
	cases := []struct {
		def     CounterDef
		prev    float64
		hadPrev bool
		v       float64
		want    bool
	}{
		{rising, 0, true, 1, true},
		{rising, 1, true, 1, false},
		{rising, 1, true, 0, false},
		{rising, 0.4, true, 0.6, true},
		{rising, 0, false, 1, true}, // nothing known yet: off before
		{change, 3, true, 4, true},
		{change, 3, true, 3, false},
		{change, 0, false, 4, false},
	}
	for i, c := range cases {
		if got := counterEdge(c.def, c.prev, c.hadPrev, c.v); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestCounterCountsParamWithCooldownAndPersists(t *testing.T) {
	path, now := useCounters(t,
		CounterDef{ID: "pats", Name: "Headpats", Param: "HeadPat", Cooldown: 2, Show: true},
		CounterDef{ID: "boops", Name: "Boops", Param: "Boop", Show: true, Template: "boops {session}"},
	)
	send := func(name string, v any) { onCounterParam(osc.NewMessage(AvatarParamPrefix+name, v)) }
	send("HeadPat", true)
	send("HeadPat", false)
	*now = now.Add(time.Second)
	send("HeadPat", true) // inside the cooldown
	send("HeadPat", false)
	*now = now.Add(3 * time.Second)
	send("HeadPat", true)
	send("Unrelated", true)
	if got := counterLine(GetConfig()); got != "Headpats: 2 (2) | boops 0" {
		t.Errorf("line %q", got)
	}

	// An avatar change: the values VRChat re-sends are not counted.
	counterAvatarChanged("avtr_x")
	send("Boop", true)
	*now = now.Add(3 * time.Second)
	send("Boop", false)
	send("Boop", true)
	if got := counterLine(GetConfig()); got != "Headpats: 2 (2) | boops 1" {
		t.Errorf("after avatar change %q", got)
	}

	if err := SaveCounters(); err != nil {
		t.Fatal(err)
	}
	var f counterFileData
	data, _ := os.ReadFile(path)
	if err := json.Unmarshal(data, &f); err != nil || f.Totals["pats"] != 2 || f.Totals["boops"] != 1 {
		t.Fatalf("saved %s %v", data, err)
	}

	// A restart: the session starts over, the total stays.
	counterMu.Lock()
	counterCounts, counterLoaded = map[string]*counterCount{}, false
	counterMu.Unlock()
	if err := CounterAdd("pats", 1); err != nil {
		t.Fatal(err)
	}
	st := CurrentCounters(GetConfig().CounterOptions)
	if st[0].Session != 1 || st[0].Total != 3 || st[1].Total != 1 || st[0].Text != "Headpats: 1 (3)" {
		t.Errorf("after restart %+v", st)
	}
}

func TestCounterManualAndReset(t *testing.T) {
	useCounters(t, CounterDef{ID: "c", Name: "Hugs", Show: true})
	_ = CounterAdd("c", 5)
	_ = CounterAdd("c", -7) // never below zero
	st := CurrentCounters(GetConfig().CounterOptions)[0]
	if st.Session != 0 || st.Total != 0 {
		t.Errorf("clamped %+v", st)
	}
	_ = CounterAdd("c", 4)
	_ = CounterReset("c", false)
	st = CurrentCounters(GetConfig().CounterOptions)[0]
	if st.Session != 0 || st.Total != 4 {
		t.Errorf("session reset %+v", st)
	}
	_ = CounterReset("c", true)
	if st = CurrentCounters(GetConfig().CounterOptions)[0]; st.Total != 0 {
		t.Errorf("full reset %+v", st)
	}
	if err := CounterAdd("missing", 1); err == nil {
		t.Error("unknown counter accepted")
	}
}

func TestCounterOnlyAfterChange(t *testing.T) {
	o := CounterOptions{Separator: " | ", Counters: []CounterDef{
		{ID: "a", Name: "A", Show: true, OnlyAfterChange: true, ShowSec: 10, Template: "{name} {session}"},
		{ID: "b", Name: "B", Show: false, Template: "{name}"},
	}}
	counts := map[string]counterCount{"a": {Session: 3, Changed: timerT0}}
	if got := counterLineText(o, counts, timerT0.Add(5*time.Second)); got != "A 3" {
		t.Errorf("just changed %q", got)
	}
	if got := counterLineText(o, counts, timerT0.Add(10*time.Second)); got != "" {
		t.Errorf("after the show time %q", got)
	}
	if got := counterLineText(o, map[string]counterCount{}, timerT0); got != "" {
		t.Errorf("never changed %q", got)
	}
}

func TestCounterRuleTrigger(t *testing.T) {
	var trig RuleTrigger
	for _, tr := range RuleTriggers() {
		if tr.ID == TriggerCounterChanged {
			trig = tr
		}
	}
	if trig.Match == nil {
		t.Fatal("counter trigger not registered")
	}
	p := SanitizeRuleParams(trig.Fields, RuleParams{"counter": "pats", "every": 10.0})
	ev := func(id, session, delta string) RuleEvent {
		return RuleEvent{Type: TriggerCounterChanged, Data: map[string]string{"counter": id, "session": session, "delta": delta}}
	}
	if !trig.Match(p, ev("pats", "20", "1")) || trig.Match(p, ev("pats", "21", "1")) ||
		trig.Match(p, ev("boops", "20", "1")) || trig.Match(p, ev("pats", "20", "-1")) {
		t.Error("every 10th headpat matched wrongly")
	}
}
