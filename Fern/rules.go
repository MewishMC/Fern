package main

// Rules and do not disturb from the panel's side: the Rules page actions,
// the state it draws from, and the tray entry and shortcut for DND.

import (
	"encoding/json"
	"time"

	"Fern/UI"
	"Fern/modules"
)

// dndKeyID is the UI.Hotkey and UI.TrayItem ID of the do not disturb switch.
const dndKeyID = "dnd"

type rulesState struct {
	Enabled bool `json:"enabled"`
	// Triggers and Actions are the registry, which the page draws its
	// editor from; Sources are the current choices of the fields that use one.
	Triggers []modules.RuleTrigger           `json:"triggers"`
	Actions  []modules.RuleAction            `json:"actions"`
	Sources  map[string][]modules.RuleOption `json:"sources"`
	Status   map[string]modules.RuleStatus   `json:"status"`
	Notice   noticeState                     `json:"notice"`
	DND      dndState                        `json:"dnd"`
	Limits   map[string]int                  `json:"limits"`
}

type dndState struct {
	On        bool   `json:"on"`
	HotkeyErr string `json:"hotkeyErr"`
}

var ruleNotice noticeState

func currentRules() rulesState {
	cfg := modules.GetConfig()
	return rulesState{
		Enabled:  cfg.Rules.Enabled,
		Triggers: orEmpty(modules.RuleTriggers()),
		Actions:  orEmpty(modules.RuleActions()),
		Sources:  modules.RuleSources(),
		Status:   modules.RuleStatuses(),
		Notice:   readNotice(&ruleNotice),
		DND:      dndState{On: cfg.DND.On, HotkeyErr: UI.HotkeyErrors()[dndKeyID]},
		Limits: map[string]int{
			"rules":      modules.MaxRules,
			"conditions": modules.MaxRuleConditions,
			"actions":    modules.MaxRuleActions,
			"cooldown":   modules.MaxRuleCooldown,
		},
	}
}

// handleRuleCommand runs a Rules page action. Module is the rule's ID,
// Config a whole rule for "save", Text a group for "worldGroup".
func handleRuleCommand(cmd command) {
	var err error
	done := ""
	switch cmd.Action {
	case "save":
		var r modules.Rule
		if err = json.Unmarshal(cmd.Config, &r); err != nil {
			setNotice(&ruleNotice, "Couldn't read that rule", true)
			return
		}
		if _, err = modules.SaveRule(r); err == nil {
			done = "Rule saved"
		}
	case "delete":
		if err = modules.DeleteRule(cmd.Module); err == nil {
			done = "Rule deleted"
		}
	case "enable":
		err = modules.SetRuleEnabled(cmd.Module, cmd.Enabled)
	case "up", "down":
		delta := 1
		if cmd.Action == "up" {
			delta = -1
		}
		err = modules.MoveRule(cmd.Module, delta)
	case "duplicate":
		if _, err = modules.DuplicateRule(cmd.Module); err == nil {
			done = "Copied; the copy starts switched off"
		}
	case "test":
		if err = modules.TestRule(cmd.Module); err == nil {
			done = "Ran the rule's actions"
		}
	case "master":
		modules.SetRulesEnabled(cmd.Enabled)
		if cmd.Enabled {
			done = "Rules on"
		} else {
			done = "Rules off"
		}
	case "worldGroup":
		w, ok := modules.CurrentWorld()
		if !ok {
			setNotice(&ruleNotice, "Join a world first", true)
			return
		}
		var name string
		var created bool
		if name, created, err = modules.UseGroupInWorld(cmd.Text, w); err == nil {
			if created {
				done = "Added the rule " + name
			} else {
				done = "Updated the rule " + name
			}
		}
	default:
		return
	}
	if err != nil {
		setNotice(&ruleNotice, err.Error(), true)
	} else if done != "" {
		setNotice(&ruleNotice, done, false)
	}
}

// handleDNDCommand is the do not disturb switch on the Live page.
func handleDNDCommand(cmd command) {
	switch cmd.Action {
	case "toggle":
		modules.ToggleDND()
	default:
		modules.SetDND(cmd.Enabled)
	}
}

// ruleHotkeys is the do not disturb shortcut, when one is set.
func ruleHotkeys() []UI.Hotkey {
	if mods, key, _, err := modules.ParseHotkey(modules.GetConfig().DND.Hotkey); err == nil {
		return []UI.Hotkey{{ID: dndKeyID, Mods: mods, Key: key}}
	}
	return nil
}

// setupRules adds do not disturb to the tray menu and the shortcuts, next
// to whatever already handles them.
func setupRules() {
	hotkey, tray := UI.OnExtraHotkey, UI.OnTrayItem
	UI.OnExtraHotkey = func(id string) {
		if id == dndKeyID {
			modules.ToggleDND()
			return
		}
		if hotkey != nil {
			hotkey(id)
		}
	}
	UI.OnTrayItem = func(id string) {
		if id == dndKeyID {
			modules.ToggleDND()
			return
		}
		if tray != nil {
			tray(id)
		}
	}
	UI.TrayToggles = func() []UI.TrayItem {
		return []UI.TrayItem{{ID: dndKeyID, Label: "Do not disturb", Checked: modules.DNDActive()}}
	}
	go watchDNDHotkey(modules.GetConfig().DND.Hotkey)
}

// watchDNDHotkey registers the shortcut again when it changes.
func watchDNDHotkey(last string) {
	for range time.Tick(500 * time.Millisecond) {
		if key := modules.GetConfig().DND.Hotkey; key != last {
			last = key
			UI.Apply(behaviour(modules.GetConfig().AppOptions))
		}
	}
}
