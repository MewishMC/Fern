package modules

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Pressing keys for rules: "when this parameter is on, hold Ctrl+M", "when a
// friend joins, tap F13". Keys go through SendInput, so they reach whatever
// app has focus, like a real keyboard.

// Virtual-key codes used outside the name table.
const (
	vkShift      = 0x10
	vkControl    = 0x11
	vkMenu       = 0x12 // Alt
	vkLWin       = 0x5B
	vkVolumeMute = 0xAD
	vkVolumeDown = 0xAE
	vkVolumeUp   = 0xAF
)

// keyNames maps the names a key combo may use to virtual-key codes. Letters,
// digits, F1-F24 and NumpadN are handled in code.
var keyNames = map[string]uint16{
	"SPACE": 0x20, "ENTER": 0x0D, "RETURN": 0x0D, "TAB": 0x09, "ESC": 0x1B, "ESCAPE": 0x1B,
	"BACKSPACE": 0x08, "INSERT": 0x2D, "DELETE": 0x2E, "DEL": 0x2E, "HOME": 0x24, "END": 0x23,
	"PAGEUP": 0x21, "PAGEDOWN": 0x22, "UP": 0x26, "DOWN": 0x28, "LEFT": 0x25, "RIGHT": 0x27,
	"PRINTSCREEN": 0x2C, "PAUSE": 0x13, "CAPSLOCK": 0x14, "SCROLLLOCK": 0x91, "NUMLOCK": 0x90,
	"VOLUMEUP": vkVolumeUp, "VOLUMEDOWN": vkVolumeDown, "VOLUMEMUTE": vkVolumeMute, "MUTE": vkVolumeMute,
	"MEDIANEXT": 0xB0, "MEDIAPREV": 0xB1, "MEDIAPREVIOUS": 0xB1, "MEDIASTOP": 0xB2, "MEDIAPLAYPAUSE": 0xB3,
	"NUMPADADD": 0x6B, "NUMPADSUBTRACT": 0x6D, "NUMPADMULTIPLY": 0x6A, "NUMPADDIVIDE": 0x6F, "NUMPADDECIMAL": 0x6E,
	"MINUS": 0xBD, "EQUALS": 0xBB, "PLUS": 0xBB, "COMMA": 0xBC, "PERIOD": 0xBE, "SLASH": 0xBF,
	"SEMICOLON": 0xBA, "QUOTE": 0xDE, "BACKSLASH": 0xDC, "LBRACKET": 0xDB, "RBRACKET": 0xDD, "BACKTICK": 0xC0,
}

// keyDisplay is how canonical combos write the named keys.
var keyDisplay = map[uint16]string{
	0x20: "Space", 0x0D: "Enter", 0x09: "Tab", 0x1B: "Esc", 0x08: "Backspace", 0x2D: "Insert", 0x2E: "Delete",
	0x24: "Home", 0x23: "End", 0x21: "PageUp", 0x22: "PageDown", 0x26: "Up", 0x28: "Down", 0x25: "Left",
	0x27: "Right", 0x2C: "PrintScreen", 0x13: "Pause", 0x14: "CapsLock", 0x91: "ScrollLock", 0x90: "NumLock",
	vkVolumeUp: "VolumeUp", vkVolumeDown: "VolumeDown", vkVolumeMute: "VolumeMute",
	0xB0: "MediaNext", 0xB1: "MediaPrev", 0xB2: "MediaStop", 0xB3: "MediaPlayPause",
	0x6B: "NumpadAdd", 0x6D: "NumpadSubtract", 0x6A: "NumpadMultiply", 0x6F: "NumpadDivide", 0x6E: "NumpadDecimal",
	0xBD: "Minus", 0xBB: "Equals", 0xBC: "Comma", 0xBE: "Period", 0xBF: "Slash", 0xBA: "Semicolon",
	0xDE: "Quote", 0xDC: "Backslash", 0xDB: "LBracket", 0xDD: "RBracket", 0xC0: "Backtick",
}

// extendedKeys need KEYEVENTF_EXTENDEDKEY, or they arrive as their numpad
// twins.
var extendedKeys = map[uint16]bool{
	0x2D: true, 0x2E: true, 0x24: true, 0x23: true, 0x21: true, 0x22: true, 0x26: true, 0x28: true,
	0x25: true, 0x27: true, 0x6F: true, 0x90: true, 0x2C: true, vkLWin: true,
}

var modifierNames = map[string]uint16{
	"CTRL": vkControl, "CONTROL": vkControl, "SHIFT": vkShift, "ALT": vkMenu, "WIN": vkLWin, "META": vkLWin, "SUPER": vkLWin,
}

var modifierDisplay = map[uint16]string{vkControl: "Ctrl", vkShift: "Shift", vkMenu: "Alt", vkLWin: "Win"}

// KeyCombo is a parsed key combination: modifiers first, then the key.
type KeyCombo struct {
	Keys      []uint16
	Canonical string // e.g. "Ctrl+Shift+M"
}

// ParseKeyCombo reads "Ctrl+Shift+M", "F13", "MediaPlayPause" or just
// "Shift". Unlike a global shortcut, a plain letter is fine here.
func ParseKeyCombo(s string) (KeyCombo, error) {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), " ", ""), "+")
	// "Ctrl++" means Ctrl and the plus key.
	if strings.HasSuffix(strings.ReplaceAll(s, " ", ""), "++") && len(parts) >= 2 {
		parts = append(parts[:len(parts)-2], "PLUS")
	}
	if len(parts) == 0 || parts[0] == "" {
		return KeyCombo{}, errors.New("name a key, e.g. Ctrl+Shift+M or F13")
	}
	var c KeyCombo
	var names []string
	seen := map[uint16]bool{}
	for i, p := range parts {
		up := strings.ToUpper(p)
		last := i == len(parts)-1
		if vk, ok := modifierNames[up]; ok {
			if seen[vk] {
				return KeyCombo{}, fmt.Errorf("%s is in there twice", modifierDisplay[vk])
			}
			seen[vk] = true
			c.Keys = append(c.Keys, vk)
			names = append(names, modifierDisplay[vk])
			continue
		}
		if !last {
			return KeyCombo{}, fmt.Errorf("%q isn't Ctrl, Shift, Alt or Win; only the last key can be another key", p)
		}
		vk, name, err := keyByName(up)
		if err != nil {
			return KeyCombo{}, err
		}
		c.Keys = append(c.Keys, vk)
		names = append(names, name)
	}
	// Modifiers in a fixed order read better and match other shortcuts.
	order := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Win": 3}
	mods := names
	key := ""
	if _, isMod := order[names[len(names)-1]]; !isMod {
		mods, key = names[:len(names)-1], names[len(names)-1]
	}
	sorted := append([]string{}, mods...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && order[sorted[j]] < order[sorted[j-1]]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	if key != "" {
		sorted = append(sorted, key)
	}
	c.Canonical = strings.Join(sorted, "+")
	return c, nil
}

func keyByName(up string) (uint16, string, error) {
	switch {
	case len(up) == 1 && (up[0] >= 'A' && up[0] <= 'Z' || up[0] >= '0' && up[0] <= '9'):
		return uint16(up[0]), up, nil
	case len(up) >= 2 && up[0] == 'F' && up[1] >= '0' && up[1] <= '9':
		n, err := strconv.Atoi(up[1:])
		if err != nil || n < 1 || n > 24 {
			return 0, "", fmt.Errorf("unknown key %q", up)
		}
		return uint16(0x70 + n - 1), "F" + strconv.Itoa(n), nil
	case strings.HasPrefix(up, "NUMPAD") && len(up) == 7 && up[6] >= '0' && up[6] <= '9':
		return uint16(0x60 + up[6] - '0'), "Numpad" + up[6:], nil
	}
	if vk, ok := keyNames[up]; ok {
		return vk, keyDisplay[vk], nil
	}
	return 0, "", fmt.Errorf("unknown key %q", up)
}

// keyInput is Windows' INPUT with a KEYBDINPUT, laid out for 64-bit.
type keyInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	_     uint32
	extra uintptr
	_     [8]byte
}

const (
	inputKeyboard   = 1
	keyEventExtend  = 0x1
	keyEventKeyUp   = 0x2
	mapVKToScanCode = 0
)

var (
	sendInputProc       = user32.NewProc("SendInput")
	mapVirtualKeyWProc  = user32.NewProc("MapVirtualKeyW")
	keySendFn           = sendKeyInputs // swapped by tests
	keyHeldMu           sync.Mutex
	keyHeld             = map[uint16]int{} // how many holds press each key
	errKeysNotDelivered = errors.New("Windows didn't take the key presses (an app running as administrator may be focused)")
)

func keyEvent(vk uint16, up bool) keyInput {
	in := keyInput{typ: inputKeyboard, vk: vk}
	if sc, _, _ := mapVirtualKeyWProc.Call(uintptr(vk), mapVKToScanCode); sc != 0 {
		in.scan = uint16(sc)
	}
	if extendedKeys[vk] {
		in.flags |= keyEventExtend
	}
	if up {
		in.flags |= keyEventKeyUp
	}
	return in
}

// keyEvents lists the presses (down) or releases (up, in reverse order) of
// keys.
func keyEvents(keys []uint16, up bool) []keyInput {
	out := make([]keyInput, 0, len(keys))
	if up {
		for i := len(keys) - 1; i >= 0; i-- {
			out = append(out, keyEvent(keys[i], true))
		}
		return out
	}
	for _, k := range keys {
		out = append(out, keyEvent(k, false))
	}
	return out
}

func sendKeyInputs(in []keyInput) error {
	if len(in) == 0 {
		return nil
	}
	n, _, _ := sendInputProc.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if int(n) != len(in) {
		return errKeysNotDelivered
	}
	return nil
}

// tapKeys presses and releases a combo.
func tapKeys(keys []uint16) error {
	return keySendFn(append(keyEvents(keys, false), keyEvents(keys, true)...))
}

// holdKeys presses a combo and keeps it down until release is called. Keys
// another hold has down stay down until that one lets go too. release is
// safe to call more than once.
func holdKeys(keys []uint16) (release func(), err error) {
	keyHeldMu.Lock()
	var down []uint16
	for _, k := range keys {
		if keyHeld[k] == 0 {
			down = append(down, k)
		}
		keyHeld[k]++
	}
	keyHeldMu.Unlock()
	err = keySendFn(keyEvents(down, false))
	var once sync.Once
	release = func() {
		once.Do(func() {
			keyHeldMu.Lock()
			var up []uint16
			for _, k := range keys {
				keyHeld[k]--
				if keyHeld[k] <= 0 {
					delete(keyHeld, k)
					up = append(up, k)
				}
			}
			keyHeldMu.Unlock()
			_ = keySendFn(keyEvents(up, true))
		})
	}
	if err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// ActionKeys is the rule action that presses keys.
const ActionKeys = "keys.press"

// Key press modes of the keys.press action.
const (
	KeysTap  = "tap"
	KeysHold = "hold" // for a number of seconds
	KeysDown = "down" // until the rule ends
)

// maxKeyDown is how long "until the rule ends" may hold keys, in case the
// rule has no undo or never ends.
const maxKeyDown = 10 * time.Minute

var keysAfterFunc = time.AfterFunc

func init() {
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionKeys, Label: "Press keys", Category: "You",
		Description: "Presses a key combo in the focused app, like the keyboard would",
		Fields: []RuleField{
			{Key: "keys", Label: "Keys", Type: FieldText, MaxLen: 60, Placeholder: "e.g. Ctrl+Shift+M or F13",
				Hint: "Ctrl, Alt, Shift and Win plus one key: letters, digits, F1–F24, NumpadN, Space, Enter, arrows, MediaPlayPause, VolumeUp…"},
			{Key: "mode", Label: "How", Type: FieldSelect, Options: []RuleOption{
				{KeysTap, "Tap"}, {KeysHold, "Hold for a while"}, {KeysDown, "Hold until the rule ends"},
			}},
			{Key: "seconds", Label: "Seconds", Type: FieldNumber, Min: 0.05, Max: 30, Step: 0.05, Default: 0.5,
				ShowIf: &RuleShowIf{Key: "mode", Values: []string{KeysHold}}},
		},
		Run: runKeysAction,
	}))
}

func runKeysAction(ctx RuleContext, p RuleParams) (func(), error) {
	combo, err := ParseKeyCombo(p.String("keys"))
	if err != nil {
		return nil, err
	}
	switch p.String("mode") {
	case KeysHold, KeysDown:
		release, err := holdKeys(combo.Keys)
		if err != nil {
			return nil, err
		}
		limit := maxKeyDown
		if p.String("mode") == KeysHold {
			limit = time.Duration(p.Number("seconds") * float64(time.Second))
		} else if ctx.Test {
			limit = time.Second // a test press should not stay down
		}
		keysAfterFunc(limit, release)
		if p.String("mode") == KeysDown {
			return release, nil
		}
		return nil, nil
	default:
		return nil, tapKeys(combo.Keys)
	}
}
