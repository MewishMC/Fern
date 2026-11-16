package modules

import (
	"fmt"
	"strconv"
	"strings"
)

// Modifier flags as RegisterHotKey takes them.
const (
	HotkeyAlt   = 0x1
	HotkeyCtrl  = 0x2
	HotkeyShift = 0x4
	HotkeyWin   = 0x8
)

var hotkeyNamedKeys = map[string]uint32{
	"SPACE": 0x20, "PAGEUP": 0x21, "PAGEDOWN": 0x22, "END": 0x23, "HOME": 0x24,
	"INSERT": 0x2D, "DELETE": 0x2E, "PAUSE": 0x13,
}

// ParseHotkey reads a shortcut like "Alt+V" or "ctrl+shift+f9" into the
// modifiers and virtual-key code RegisterHotKey wants, and returns it written
// the canonical way ("Ctrl+Shift+F9"). Letters and digits need a modifier, so
// a stray hotkey cannot swallow plain typing.
func ParseHotkey(s string) (mods, vk uint32, canonical string, err error) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), "+")
	if len(parts) == 0 || parts[0] == "" {
		return 0, 0, "", fmt.Errorf("empty shortcut")
	}
	keyName := strings.ToUpper(parts[len(parts)-1])
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToUpper(p) {
		case "CTRL", "CONTROL":
			mods |= HotkeyCtrl
		case "ALT":
			mods |= HotkeyAlt
		case "SHIFT":
			mods |= HotkeyShift
		case "WIN", "META", "SUPER":
			mods |= HotkeyWin
		default:
			return 0, 0, "", fmt.Errorf("unknown modifier %q", p)
		}
	}
	display := keyName
	needsMod := true
	switch {
	case len(keyName) == 1 && keyName[0] >= 'A' && keyName[0] <= 'Z',
		len(keyName) == 1 && keyName[0] >= '0' && keyName[0] <= '9':
		vk = uint32(keyName[0])
	case len(keyName) >= 2 && keyName[0] == 'F':
		n, convErr := strconv.Atoi(keyName[1:])
		if convErr != nil || n < 1 || n > 24 {
			return 0, 0, "", fmt.Errorf("unknown key %q", keyName)
		}
		vk, needsMod = uint32(0x70+n-1), false
	case strings.HasPrefix(keyName, "NUMPAD") && len(keyName) == 7 && keyName[6] >= '0' && keyName[6] <= '9':
		vk = uint32(0x60 + keyName[6] - '0')
		display = "Numpad" + keyName[6:]
	default:
		code, ok := hotkeyNamedKeys[keyName]
		if !ok {
			return 0, 0, "", fmt.Errorf("unknown key %q", parts[len(parts)-1])
		}
		vk, needsMod = code, keyName == "SPACE"
		display = strings.ToUpper(keyName[:1]) + strings.ToLower(keyName[1:])
		display = strings.NewReplacer("Pageup", "PageUp", "Pagedown", "PageDown").Replace(display)
	}
	if needsMod && mods == 0 {
		return 0, 0, "", fmt.Errorf("%s needs Ctrl, Alt, Shift or Win with it", display)
	}
	var names []string
	for _, m := range []struct {
		flag uint32
		name string
	}{{HotkeyCtrl, "Ctrl"}, {HotkeyAlt, "Alt"}, {HotkeyShift, "Shift"}, {HotkeyWin, "Win"}} {
		if mods&m.flag != 0 {
			names = append(names, m.name)
		}
	}
	return mods, vk, strings.Join(append(names, display), "+"), nil
}
