package main

import (
	"encoding/json"
	"strings"
	"testing"

	"Fern/UI"
)

func TestTallyStateShape(t *testing.T) {
	data, err := json.Marshal(currentTally())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"timer":{`, `"counters":[`, `"schedule":[`, `"hotkeyErrs":{`, `"notice":{`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("state lacks %s: %s", key, data)
		}
	}
}

func TestTallyCommandsReportErrors(t *testing.T) {
	before := readNotice(&tallyNotice).Seq
	handleCounterCommand(command{Type: "counter", Action: "add", Module: "no such counter"})
	if n := readNotice(&tallyNotice); n.Seq != before+1 || !n.Bad {
		t.Errorf("counter: notice %+v", n)
	}
	handleScheduleCommand(command{Type: "schedule", Action: "test", Module: "no such message"})
	if n := readNotice(&tallyNotice); n.Seq != before+2 || !n.Bad {
		t.Errorf("schedule: notice %+v", n)
	}
	handleCounterCommand(command{Type: "counter", Action: "bogus"})
	if n := readNotice(&tallyNotice); n.Seq != before+2 {
		t.Errorf("unknown action changed the notice: %+v", n)
	}
}

func TestTallyShortcutsPassOthersOn(t *testing.T) {
	old := UI.OnExtraHotkey
	t.Cleanup(func() { UI.OnExtraHotkey = old })
	var got []string
	UI.OnExtraHotkey = func(id string) { got = append(got, id) }
	setupTally()
	UI.OnExtraHotkey(dndKeyID)
	UI.OnExtraHotkey(timerResetKey)
	UI.OnExtraHotkey(counterKeyPrefix + "missing")
	if strings.Join(got, ",") != dndKeyID {
		t.Errorf("passed on %v", got)
	}
	if hotkeySig([]UI.Hotkey{{ID: "a", Mods: 2, Key: 65}}) != "a=2+65;" {
		t.Error("signature")
	}
}
