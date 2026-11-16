package main

// The panel side of custom lines and the clipboard shortcut.

import (
	"time"

	"Fern/UI"
	"Fern/modules"
)

// clipKeyID is the UI.Hotkey ID of the clipboard shortcut.
const clipKeyID = "clipboard"

type customLinesState struct {
	Lines  []modules.CustomLineStatus `json:"lines"`
	Notice noticeState                `json:"notice"`
}

type clipboardState struct {
	HotkeyErr string      `json:"hotkeyErr"`
	Notice    noticeState `json:"notice"`
}

var customNotice, clipNotice noticeState

func currentCustomLines() customLinesState {
	return customLinesState{Lines: orEmpty(modules.CustomLineStatuses()), Notice: readNotice(&customNotice)}
}

func currentClipboard() clipboardState {
	return clipboardState{HotkeyErr: UI.HotkeyErrors()[clipKeyID], Notice: readNotice(&clipNotice)}
}

// handleCustomLineCommand is the custom line pages' buttons. Module is the
// line's ID.
func handleCustomLineCommand(cmd command) {
	var err error
	switch cmd.Action {
	case "refresh":
		err = modules.RefreshCustomLine(cmd.Module)
	case "set":
		err = modules.SetCustomLineText(cmd.Module, cmd.Text, time.Duration(cmd.Hold)*time.Second)
	case "clear":
		err = modules.SetCustomLineText(cmd.Module, "", 0)
	default:
		return
	}
	if err != nil {
		setNotice(&customNotice, err.Error(), true)
	}
}

// handleClipboardCommand is the clipboard card's Send now button.
func handleClipboardCommand(cmd command) {
	if cmd.Action == "send" {
		sendClipboard()
	}
}

func sendClipboard() {
	text, err := modules.SendClipboard()
	if err != nil {
		setNotice(&clipNotice, err.Error(), true)
		return
	}
	setNotice(&clipNotice, "Sent: "+text, false)
}

// clipboardHotkeys is the clipboard shortcut, when one is set.
func clipboardHotkeys() []UI.Hotkey {
	if mods, key, _, err := modules.ParseHotkey(modules.GetConfig().Clipboard.Hotkey); err == nil {
		return []UI.Hotkey{{ID: clipKeyID, Mods: mods, Key: key}}
	}
	return nil
}

// setupClipboard handles the clipboard shortcut and passes every other
// shortcut on to whatever handled it before.
func setupClipboard() {
	hotkey := UI.OnExtraHotkey
	UI.OnExtraHotkey = func(id string) {
		if id == clipKeyID {
			sendClipboard()
			return
		}
		if hotkey != nil {
			hotkey(id)
		}
	}
	go watchClipboardHotkey(modules.GetConfig().Clipboard.Hotkey)
}

// watchClipboardHotkey registers the shortcut again when it changes.
func watchClipboardHotkey(last string) {
	for range time.Tick(500 * time.Millisecond) {
		if key := modules.GetConfig().Clipboard.Hotkey; key != last {
			last = key
			UI.Apply(behaviour(modules.GetConfig().AppOptions))
		}
	}
}
