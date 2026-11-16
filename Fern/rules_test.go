package main

import (
	"encoding/json"
	"strings"
	"testing"

	"Fern/UI"
)

func TestRulesStateCarriesTheRegistry(t *testing.T) {
	data, err := json.Marshal(currentRules())
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Triggers []struct {
			ID     string `json:"id"`
			State  bool   `json:"state"`
			Fields []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"triggers"`
		Actions []struct {
			ID string `json:"id"`
		} `json:"actions"`
		Sources map[string][]struct {
			Value string `json:"value"`
		} `json:"sources"`
		Status map[string]any `json:"status"`
		Limits map[string]int `json:"limits"`
		DND    map[string]any `json:"dnd"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, tr := range st.Triggers {
		ids[tr.ID] = tr.State
		if tr.ID == "time" && (len(tr.Fields) != 3 || tr.Fields[0].Type != "time") {
			t.Errorf("time fields %+v", tr.Fields)
		}
	}
	if !ids["world"] || ids["player_joined"] {
		t.Errorf("trigger kinds %v", ids)
	}
	if len(st.Actions) < 7 || st.Sources["lines"] == nil || st.Status == nil || st.Limits["rules"] != 100 || st.DND == nil {
		t.Errorf("state %s", data)
	}
	if strings.Contains(string(data), `"Check"`) || strings.Contains(string(data), `"Run"`) {
		t.Error("functions leaked into the state")
	}
}

func TestRuleCommandsReportErrors(t *testing.T) {
	before := readNotice(&ruleNotice).Seq
	handleRuleCommand(command{Type: "rule", Action: "test", Module: "no such rule"})
	if n := readNotice(&ruleNotice); n.Seq != before+1 || !n.Bad {
		t.Errorf("test: notice %+v", n)
	}
	handleRuleCommand(command{Type: "rule", Action: "save", Config: json.RawMessage(`{"id": 5}`)})
	if n := readNotice(&ruleNotice); n.Seq != before+2 || !n.Bad {
		t.Errorf("save: notice %+v", n)
	}
	handleRuleCommand(command{Type: "rule", Action: "bogus"})
	if n := readNotice(&ruleNotice); n.Seq != before+2 {
		t.Errorf("unknown action changed the notice: %+v", n)
	}
}

func TestRuleShortcutsPassOthersOn(t *testing.T) {
	oldKey, oldTray, oldToggles := UI.OnExtraHotkey, UI.OnTrayItem, UI.TrayToggles
	t.Cleanup(func() { UI.OnExtraHotkey, UI.OnTrayItem, UI.TrayToggles = oldKey, oldTray, oldToggles })
	var got []string
	UI.OnExtraHotkey = func(id string) { got = append(got, "key:"+id) }
	UI.OnTrayItem = func(id string) { got = append(got, "tray:"+id) }
	setupRules()
	UI.OnExtraHotkey(profileKeyPrefix + "Club")
	UI.OnTrayItem(profileKeyPrefix + "Chill")
	if strings.Join(got, ",") != "key:profile:Club,tray:profile:Chill" {
		t.Errorf("passed on %v", got)
	}
	items := UI.TrayToggles()
	if len(items) != 1 || items[0].ID != dndKeyID || items[0].Label != "Do not disturb" {
		t.Errorf("tray toggles %+v", items)
	}
}
