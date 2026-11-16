package main

// The timer, counters and scheduled messages from the panel's side: their
// actions, the state the pages draw from, and the global shortcuts.

import (
	"fmt"
	"strings"
	"time"

	"Fern/UI"
	"Fern/modules"
)

// UI.Hotkey IDs of the timer shortcuts; a counter's is counterKeyPrefix + its ID.
const (
	timerToggleKey   = "timer:toggle"
	timerResetKey    = "timer:reset"
	counterKeyPrefix = "counter:"
)

type tallyState struct {
	Timer    modules.TimerStatus      `json:"timer"`
	Counters []modules.CounterStatus  `json:"counters"`
	Schedule []modules.ScheduleStatus `json:"schedule"`
	// HotkeyErrs holds why Windows refused a shortcut: "toggle", "reset",
	// or a counter's ID.
	HotkeyErrs map[string]string `json:"hotkeyErrs"`
	Notice     noticeState       `json:"notice"`
}

var tallyNotice noticeState

func currentTally() tallyState {
	cfg := modules.GetConfig()
	errs := map[string]string{}
	for id, msg := range UI.HotkeyErrors() {
		switch {
		case id == timerToggleKey:
			errs["toggle"] = msg
		case id == timerResetKey:
			errs["reset"] = msg
		case strings.HasPrefix(id, counterKeyPrefix):
			errs[strings.TrimPrefix(id, counterKeyPrefix)] = msg
		}
	}
	return tallyState{
		Timer:      modules.CurrentTimer(),
		Counters:   orEmpty(modules.CurrentCounters(cfg.CounterOptions)),
		Schedule:   orEmpty(modules.CurrentSchedule(cfg.ScheduleOptions)),
		HotkeyErrs: errs,
		Notice:     readNotice(&tallyNotice),
	}
}

// handleTimerCommand is {type:"timer", action: start|pause|toggle|reset|skip}.
func handleTimerCommand(cmd command) {
	modules.TimerControl(cmd.Action)
}

// handleCounterCommand is {type:"counter", action: add|sub|reset|resetAll, module: id}.
func handleCounterCommand(cmd command) {
	var err error
	switch cmd.Action {
	case "add":
		err = modules.CounterAdd(cmd.Module, 1)
	case "sub":
		err = modules.CounterAdd(cmd.Module, -1)
	case "reset":
		err = modules.CounterReset(cmd.Module, false)
	case "resetAll":
		if err = modules.CounterReset(cmd.Module, true); err == nil {
			setNotice(&tallyNotice, "Reset the session and the total", false)
		}
	default:
		return
	}
	if err != nil {
		setNotice(&tallyNotice, err.Error(), true)
	}
}

// handleScheduleCommand is {type:"schedule", action: test (module: id) | clear}.
func handleScheduleCommand(cmd command) {
	switch cmd.Action {
	case "test":
		if err := modules.ShowScheduledNow(cmd.Module); err != nil {
			setNotice(&tallyNotice, err.Error(), true)
		} else {
			setNotice(&tallyNotice, "Sent it now", false)
		}
	case "clear":
		modules.ClearScheduled()
	}
}

// tallyHotkeys are the timer and counter shortcuts that are set.
func tallyHotkeys() []UI.Hotkey {
	cfg := modules.GetConfig()
	var keys []UI.Hotkey
	add := func(id, key string) {
		if mods, vk, _, err := modules.ParseHotkey(key); err == nil && key != "" {
			keys = append(keys, UI.Hotkey{ID: id, Mods: mods, Key: vk})
		}
	}
	add(timerToggleKey, cfg.TimerOptions.ToggleHotkey)
	add(timerResetKey, cfg.TimerOptions.ResetHotkey)
	for _, c := range cfg.CounterOptions.Counters {
		add(counterKeyPrefix+c.ID, c.Hotkey)
	}
	return keys
}

// tallyHotkey runs a timer or counter shortcut, and reports whether id was one.
func tallyHotkey(id string) bool {
	switch {
	case id == timerToggleKey:
		modules.TimerControl(modules.TimerActToggle)
	case id == timerResetKey:
		modules.TimerControl(modules.TimerActReset)
	case strings.HasPrefix(id, counterKeyPrefix):
		_ = modules.CounterAdd(strings.TrimPrefix(id, counterKeyPrefix), 1)
	default:
		return false
	}
	return true
}

// setupTally adds the timer and counter shortcuts next to whatever already
// handles shortcuts.
func setupTally() {
	prev := UI.OnExtraHotkey
	UI.OnExtraHotkey = func(id string) {
		if tallyHotkey(id) {
			return
		}
		if prev != nil {
			prev(id)
		}
	}
	go watchTallyHotkeys(hotkeySig(tallyHotkeys()))
}

func hotkeySig(keys []UI.Hotkey) string {
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%d+%d;", k.ID, k.Mods, k.Key)
	}
	return b.String()
}

// watchTallyHotkeys registers the shortcuts again when they change.
func watchTallyHotkeys(last string) {
	for range time.Tick(500 * time.Millisecond) {
		if sig := hotkeySig(tallyHotkeys()); sig != last {
			last = sig
			UI.Apply(behaviour(modules.GetConfig().AppOptions))
		}
	}
}
