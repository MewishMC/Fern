package UI

import (
	"testing"
	"unsafe"
)

func TestNotifyIconDataLayout(t *testing.T) {
	var d notifyIconData
	if got := unsafe.Sizeof(d); got != 976 {
		t.Errorf("sizeof NOTIFYICONDATAW = %d, want 976", got)
	}
	for name, c := range map[string][2]uintptr{
		"hIcon":        {unsafe.Offsetof(d.Icon), 32},
		"szTip":        {unsafe.Offsetof(d.Tip), 40},
		"uVersion":     {unsafe.Offsetof(d.Version), 816},
		"guidItem":     {unsafe.Offsetof(d.GUIDItem), 952},
		"hBalloonIcon": {unsafe.Offsetof(d.BalloonIcon), 968},
	} {
		if c[0] != c[1] {
			t.Errorf("%s at %d, want %d", name, c[0], c[1])
		}
	}
}

func TestMenuLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Home":        "Home",
		"Rock & Roll": "Rock && Roll",
		"a\tb":        "a b",
	} {
		if got := menuLabel(in); got != want {
			t.Errorf("menuLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
