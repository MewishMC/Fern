package modules

import "testing"

func ruleIDs() []string {
	var out []string
	for _, r := range GetConfig().Rules.List {
		out = append(out, r.ID)
	}
	return out
}

func TestEditRules(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	a, err := SaveRule(Rule{Name: "A", Enabled: true, Trigger: RuleStep{Type: TriggerStarted}})
	if err != nil || a == "" {
		t.Fatal(err)
	}
	b, _ := SaveRule(Rule{ID: "fixed", Name: "B"})
	if b != "fixed" {
		t.Errorf("id %q", b)
	}
	if _, err := SaveRule(Rule{ID: a, Name: "A2", Trigger: RuleStep{Type: TriggerStarted}}); err != nil {
		t.Fatal(err)
	}
	if l := GetConfig().Rules.List; len(l) != 2 || l[0].Name != "A2" || l[0].Enabled {
		t.Errorf("after replace %+v", l)
	}
	if err := MoveRule(b, -1); err != nil {
		t.Fatal(err)
	}
	if ids := ruleIDs(); ids[0] != b {
		t.Errorf("order %v", ids)
	}
	_ = MoveRule(b, -1) // already first: no change, no error
	c, err := DuplicateRule(b)
	if err != nil {
		t.Fatal(err)
	}
	if ids := ruleIDs(); len(ids) != 3 || ids[1] != c {
		t.Errorf("duplicate order %v", ids)
	}
	if r := GetConfig().Rules.List[1]; r.Enabled || r.Name != "B copy" {
		t.Errorf("copy %+v", r)
	}
	if err := SetRuleEnabled(c, true); err != nil || !GetConfig().Rules.List[1].Enabled {
		t.Error("enable", err)
	}
	if err := DeleteRule(c); err != nil || len(ruleIDs()) != 2 {
		t.Error("delete", err)
	}
	if err := DeleteRule("nope"); err == nil {
		t.Error("deleting a missing rule should fail")
	}
	SetRulesEnabled(false)
	if GetConfig().Rules.Enabled {
		t.Error("master switch")
	}
}

func TestUseGroupInWorld(t *testing.T) {
	c := DefaultConfig()
	c.TextOptions.Groups = []string{"Chill", "Club"}
	useTestConfig(t, c)
	w := WorldInfo{ID: "wrld_1", Name: "The Black Cat"}
	if _, _, err := UseGroupInWorld("chill", WorldInfo{}); err == nil {
		t.Error("no world accepted")
	}
	if _, _, err := UseGroupInWorld("Missing", w); err == nil {
		t.Error("missing group accepted")
	}
	name, created, err := UseGroupInWorld("chill", w)
	if err != nil || !created || name != "Chill in The Black Cat" {
		t.Fatalf("%q %v %v", name, created, err)
	}
	r := GetConfig().Rules.List[0]
	if !r.Enabled || !r.Undo || r.Trigger.Type != TriggerWorld || r.Trigger.Params.String("world") != "wrld_1" ||
		len(r.Actions) != 1 || r.Actions[0].Params.String("group") != "Chill" || !r.Actions[0].Params.Bool("show") {
		t.Errorf("rule %+v", r)
	}
	// The same world again points the existing rule at the new group.
	name, created, err = UseGroupInWorld("Club", w)
	if err != nil || created || name != "Club in The Black Cat" {
		t.Fatalf("%q %v %v", name, created, err)
	}
	if l := GetConfig().Rules.List; len(l) != 1 || l[0].Actions[0].Params.String("group") != "Club" {
		t.Errorf("rules %+v", l)
	}
	if _, created, _ := UseGroupInWorld("", WorldInfo{ID: "wrld_2"}); !created {
		t.Error("all messages in a second world")
	}
}
