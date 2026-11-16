package main

import (
	"sync/atomic"
	"syscall"
	"time"

	"Fern/UI"
	"Fern/modules"
)

// UI.Hotkey IDs of the Voicemod microphone shortcuts.
const (
	vmMuteKeyID  = "voicemod.mute"
	vmBleepKeyID = "voicemod.bleep"
)

var procGetAsyncKeyState = syscall.NewLazyDLL("user32.dll").NewProc("GetAsyncKeyState")

// keyDown reports whether a virtual key is held right now.
var keyDown = func(vk uint32) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

// voicemodHotkeys are the mute and bleep shortcuts, when set.
func voicemodHotkeys() []UI.Hotkey {
	o := modules.GetConfig().VoicemodOptions
	var keys []UI.Hotkey
	for _, k := range [][2]string{{vmMuteKeyID, o.MuteHotkey}, {vmBleepKeyID, o.BleepHotkey}} {
		if mods, key, _, err := modules.ParseHotkey(k[1]); err == nil {
			keys = append(keys, UI.Hotkey{ID: k[0], Mods: mods, Key: key})
		}
	}
	return keys
}

// voicemodKeyErrors says why Windows refused a shortcut, by its config key.
func voicemodKeyErrors() map[string]string {
	errs := UI.HotkeyErrors()
	out := map[string]string{}
	if e := errs[vmMuteKeyID]; e != "" {
		out["mute"] = e
	}
	if e := errs[vmBleepKeyID]; e != "" {
		out["bleep"] = e
	}
	return out
}

// setupVoicemodKeys handles the Voicemod shortcuts, passing every other one
// on to whatever already handles it.
func setupVoicemodKeys() {
	prev := UI.OnExtraHotkey
	UI.OnExtraHotkey = func(id string) {
		switch id {
		case vmMuteKeyID:
			modules.VoicemodCommand("mute", "")
		case vmBleepKeyID:
			_, vk, _, err := modules.ParseHotkey(modules.GetConfig().VoicemodOptions.BleepHotkey)
			if err == nil {
				holdBleep(vk)
			}
		default:
			if prev != nil {
				prev(id)
			}
		}
	}
	go watchVoicemodKeys(modules.GetConfig().VoicemodOptions)
}

var bleepHeld atomic.Bool

// holdBleep bleeps while the shortcut's key stays down. Windows only reports
// the press, so the release is found by polling the key. The bleep also ends
// by itself after the longest bleep set on the Voicemod page.
func holdBleep(vk uint32) {
	if !bleepHeld.CompareAndSwap(false, true) {
		return
	}
	defer bleepHeld.Store(false)
	modules.VoicemodCommand("bleepOn", "")
	limit := time.Duration(modules.GetConfig().VoicemodOptions.BleepMaxSec)*time.Second + time.Second
	start := time.Now()
	for keyDown(vk) && time.Since(start) < limit {
		time.Sleep(30 * time.Millisecond)
	}
	modules.VoicemodCommand("bleepOff", "")
}

// watchVoicemodKeys registers the shortcuts again when they change.
func watchVoicemodKeys(last modules.VoicemodOptions) {
	for range time.Tick(500 * time.Millisecond) {
		o := modules.GetConfig().VoicemodOptions
		if o.MuteHotkey != last.MuteHotkey || o.BleepHotkey != last.BleepHotkey {
			last = o
			UI.Apply(behaviour(modules.GetConfig().AppOptions))
		}
	}
}
