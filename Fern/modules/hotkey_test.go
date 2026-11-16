package modules

import "testing"

func TestParseHotkey(t *testing.T) {
	good := []struct {
		in        string
		mods, vk  uint32
		canonical string
	}{
		{"Alt+V", HotkeyAlt, 'V', "Alt+V"},
		{"shift + ctrl + m", HotkeyCtrl | HotkeyShift, 'M', "Ctrl+Shift+M"},
		{"F9", 0, 0x78, "F9"},
		{"Win+F24", HotkeyWin, 0x87, "Win+F24"},
		{"Ctrl+5", HotkeyCtrl, '5', "Ctrl+5"},
		{"Alt+numpad3", HotkeyAlt, 0x63, "Alt+Numpad3"},
		{"Ctrl+pagedown", HotkeyCtrl, 0x22, "Ctrl+PageDown"},
		{"Pause", 0, 0x13, "Pause"},
		{"Ctrl+Space", HotkeyCtrl, 0x20, "Ctrl+Space"},
	}
	for _, c := range good {
		mods, vk, canonical, err := ParseHotkey(c.in)
		if err != nil || mods != c.mods || vk != c.vk || canonical != c.canonical {
			t.Errorf("ParseHotkey(%q) = %x %x %q %v", c.in, mods, vk, canonical, err)
		}
	}
	for _, bad := range []string{"", "V", "Space", "Hyper+V", "Alt+F25", "Alt+Banana", "Ctrl+"} {
		if _, _, _, err := ParseHotkey(bad); err == nil {
			t.Errorf("ParseHotkey(%q) accepted", bad)
		}
	}
}
