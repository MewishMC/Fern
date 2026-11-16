package modules

import (
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func TestParseKeyCombo(t *testing.T) {
	cases := map[string]string{
		"ctrl+shift+m":       "Ctrl+Shift+M",
		"Shift + Ctrl + F13": "Ctrl+Shift+F13",
		"a":                  "A",
		"mediaplaypause":     "MediaPlayPause",
		"Shift":              "Shift",
		"win+alt+numpad5":    "Alt+Win+Numpad5",
		"Ctrl++":             "Ctrl+Equals",
		"alt+pageup":         "Alt+PageUp",
	}
	for in, want := range cases {
		c, err := ParseKeyCombo(in)
		if err != nil || c.Canonical != want {
			t.Errorf("%q: got %q, %v; want %q", in, c.Canonical, err, want)
		}
	}
	c, _ := ParseKeyCombo("Ctrl+Shift+M")
	if !reflect.DeepEqual(c.Keys, []uint16{vkControl, vkShift, 'M'}) {
		t.Errorf("keys %v", c.Keys)
	}
	for _, bad := range []string{"", "Ctrl+Ctrl+A", "A+B", "F25", "Hyper+A", "Ctrl+Nope"} {
		if _, err := ParseKeyCombo(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestKeyInputLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(keyInput{}) != 40 {
		t.Fatalf("INPUT is %d bytes, Windows wants 40", unsafe.Sizeof(keyInput{}))
	}
}

type keyLog struct {
	mu     sync.Mutex
	events []string
}

func captureKeys(t *testing.T) *keyLog {
	t.Helper()
	log := &keyLog{}
	old := keySendFn
	keySendFn = func(in []keyInput) error {
		log.mu.Lock()
		defer log.mu.Unlock()
		for _, k := range in {
			dir := "down"
			if k.flags&keyEventKeyUp != 0 {
				dir = "up"
			}
			log.events = append(log.events, dir+":"+string(rune(k.vk)))
		}
		return nil
	}
	t.Cleanup(func() {
		keySendFn = old
		keyHeldMu.Lock()
		keyHeld = map[uint16]int{}
		keyHeldMu.Unlock()
	})
	return log
}

func (k *keyLog) take() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := k.events
	k.events = nil
	return out
}

func TestKeysActionModes(t *testing.T) {
	keys := captureKeys(t)
	var timers []time.Duration
	var pending []func()
	old := keysAfterFunc
	keysAfterFunc = func(d time.Duration, f func()) *time.Timer {
		timers = append(timers, d)
		pending = append(pending, f)
		return nil
	}
	t.Cleanup(func() { keysAfterFunc = old })

	undo, err := runAction(t, ActionKeys, RuleParams{"keys": "ctrl+m", "mode": KeysTap})
	if err != nil || undo != nil {
		t.Fatalf("tap: %v", err)
	}
	if got := keys.take(); !reflect.DeepEqual(got, []string{"down:\x11", "down:M", "up:M", "up:\x11"}) {
		t.Fatalf("tap sent %q", got)
	}

	undo, err = runAction(t, ActionKeys, RuleParams{"keys": "B", "mode": KeysHold, "seconds": 0.25})
	if err != nil || undo != nil || len(timers) != 1 || timers[0] != 250*time.Millisecond {
		t.Fatalf("hold: %v %v", err, timers)
	}
	pending[0]()
	if got := keys.take(); !reflect.DeepEqual(got, []string{"down:B", "up:B"}) {
		t.Fatalf("hold sent %q", got)
	}

	// Until the rule ends: undo lets go, and the safety timer after it does nothing.
	undo, err = runAction(t, ActionKeys, RuleParams{"keys": "Shift", "mode": KeysDown})
	if err != nil || undo == nil || timers[1] != maxKeyDown {
		t.Fatalf("down: %v %v", err, timers)
	}
	undo()
	pending[1]()
	if got := keys.take(); !reflect.DeepEqual(got, []string{"down:\x10", "up:\x10"}) {
		t.Fatalf("down sent %q", got)
	}

	if _, err := runAction(t, ActionKeys, RuleParams{"keys": "Ctrl+Bogus"}); err == nil {
		t.Fatal("a bad combo should fail")
	}
}

func TestHoldKeysSharesKeys(t *testing.T) {
	keys := captureKeys(t)
	r1, _ := holdKeys([]uint16{vkShift, 'A'})
	r2, _ := holdKeys([]uint16{vkShift, 'B'})
	r1()
	r1() // twice is fine
	if got := keys.take(); !reflect.DeepEqual(got, []string{"down:\x10", "down:A", "down:B", "up:A"}) {
		t.Fatalf("got %q", got)
	}
	r2()
	if got := keys.take(); !reflect.DeepEqual(got, []string{"up:B", "up:\x10"}) {
		t.Fatalf("got %q", got)
	}
}
