package main

import (
	"encoding/json"
	"strings"
	"testing"

	"Fern/modules"
)

func TestAvatarCommands(t *testing.T) {
	const id = "avtr_panel_test"
	defer func() { _ = modules.ForgetAvatar(id) }()
	before := readNotice(&avatarNotice).Seq

	handleAvatarCommand(command{Type: "avatar", Action: "params", Module: id, Config: json.RawMessage(`["Hat","Hue","Hat"]`)})
	handleAvatarCommand(command{Type: "avatar", Action: "flags", Module: id, Config: json.RawMessage(`{"autoSave":true,"restore":true}`)})
	handleAvatarCommand(command{Type: "avatar", Action: "rename", Module: id, Text: "Panel test"})
	e, ok := modules.AvatarInfo(id)
	if !ok || len(e.Params) != 2 || !e.AutoSave || !e.Restore || e.Name != "Panel test" {
		t.Fatalf("entry %+v", e)
	}
	if n := readNotice(&avatarNotice); n.Seq != before {
		t.Errorf("quiet edits should not toast: %+v", n)
	}

	handleAvatarCommand(command{Type: "avatar", Action: "save", Module: id})
	if n := readNotice(&avatarNotice); n.Seq != before+1 || !n.Bad {
		t.Errorf("saving an avatar you don't wear should fail: %+v", n)
	}
	handleAvatarCommand(command{Type: "avatar", Action: "profile", Module: id, Text: "No such profile"})
	if n := readNotice(&avatarNotice); n.Seq != before+2 || !n.Bad {
		t.Errorf("unknown profile: %+v", n)
	}
	handleAvatarCommand(command{Type: "avatar", Action: "bogus"})
	if n := readNotice(&avatarNotice); n.Seq != before+2 {
		t.Errorf("unknown action changed the notice: %+v", n)
	}

	data, err := json.Marshal(currentAvatar())
	if err != nil || !strings.Contains(string(data), `"id":"avtr_panel_test"`) || !strings.Contains(string(data), `"limits":{"params":64}`) {
		t.Fatalf("state %s %v", data, err)
	}
}

func TestAvatarParamRuleCommand(t *testing.T) {
	before := len(modules.GetConfig().Rules.List)
	handleAvatarCommand(command{Type: "avatar", Action: "paramRule",
		Config: json.RawMessage(`{"param":"MediaNext","action":{"type":"keys.press","params":{"keys":"medianext","mode":"tap"}}}`)})
	n := readNotice(&avatarNotice)
	list := modules.GetConfig().Rules.List
	if n.Bad || len(list) != before+1 {
		t.Fatalf("notice %+v, rules %d", n, len(list))
	}
	r := list[len(list)-1]
	defer func() { _ = modules.DeleteRule(r.ID) }()
	if r.Trigger.Type != modules.TriggerAvatarParam || r.Actions[0].Params.String("keys") != "MediaNext" {
		t.Errorf("rule %+v", r)
	}
}
