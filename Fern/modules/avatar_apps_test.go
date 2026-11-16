package modules

import (
	"reflect"
	"strings"
	"testing"
)

func TestExeNameAndArgs(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Program Files\OBS\obs64.exe`: "obs64.exe",
		`"D:/Apps/VRCFT.exe"`:            "vrcft.exe",
		"Spotify":                        "spotify.exe",
		"":                               "",
	} {
		if got := exeName(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	got := splitArgs(`--scene "Just Chatting" -v  ""`)
	if !reflect.DeepEqual(got, []string{"--scene", "Just Chatting", "-v", ""}) {
		t.Errorf("args %q", got)
	}
}

func fakeProcesses(t *testing.T, procs ...procInfo) (closed *[]uint32, killed *[]uint32) {
	t.Helper()
	var c, k []uint32
	oldList, oldClose, oldKill := procListFn, closeWindowsFn, terminateFn
	procListFn = func() ([]procInfo, error) { return procs, nil }
	closeWindowsFn = func(pids []uint32) int {
		n := 0
		for _, p := range pids {
			if p != 13 { // 13 has no window
				c = append(c, p)
				n++
			}
		}
		return n
	}
	terminateFn = func(pid uint32) error { k = append(k, pid); return nil }
	t.Cleanup(func() { procListFn, closeWindowsFn, terminateFn = oldList, oldClose, oldKill })
	return &c, &k
}

func TestCloseApp(t *testing.T) {
	closed, killed := fakeProcesses(t, procInfo{10, "obs64.exe"}, procInfo{11, "obs64.exe"}, procInfo{12, "chrome.exe"}, procInfo{13, "tray.exe"})
	if n, err := CloseApp("OBS64", false); err != nil || n != 2 || !reflect.DeepEqual(*closed, []uint32{10, 11}) {
		t.Fatalf("close: %d %v %v", n, err, *closed)
	}
	if n, err := CloseApp(`C:\x\chrome.exe`, true); err != nil || n != 1 || !reflect.DeepEqual(*killed, []uint32{12}) {
		t.Fatalf("kill: %d %v %v", n, err, *killed)
	}
	if _, err := CloseApp("tray.exe", false); err == nil {
		t.Fatal("an app without a window can't be asked to close")
	}
	if n, err := CloseApp("notrunning.exe", true); err != nil || n != 0 {
		t.Fatalf("not running: %d %v", n, err)
	}
	for _, app := range []string{"explorer.exe", "Fern", "svchost"} {
		if _, err := CloseApp(app, true); err == nil {
			t.Errorf("%s should be protected", app)
		}
	}
	if _, err := runAction(t, ActionAppClose, RuleParams{"app": "lsass.exe", "mode": "kill"}); err == nil {
		t.Error("the action should refuse protected apps")
	}
}

func TestOpenAppAction(t *testing.T) {
	closed, _ := fakeProcesses(t, procInfo{20, "running.exe"}, procInfo{77, "game.exe"})
	var started []string
	var opened []string
	oldStart, oldShell := startAppFn, shellOpenFn
	startAppFn = func(path string, args []string) (int, error) {
		started = append(started, path+" "+strings.Join(args, "|"))
		return 77, nil
	}
	shellOpenFn = func(path, args string) error { opened = append(opened, path); return nil }
	t.Cleanup(func() { startAppFn, shellOpenFn = oldStart, oldShell })

	undo, err := runAction(t, ActionAppOpen, RuleParams{"path": `C:\Games\game.exe`, "args": `-a "b c"`, "once": false})
	if err != nil || undo == nil || !reflect.DeepEqual(started, []string{`C:\Games\game.exe -a|b c`}) {
		t.Fatalf("open: %v %q", err, started)
	}
	undo()
	if !reflect.DeepEqual(*closed, []uint32{77}) {
		t.Fatalf("undo closed %v", *closed)
	}
	// Already running: nothing to do.
	started = nil
	if _, err := runAction(t, ActionAppOpen, RuleParams{"path": `D:\x\Running.exe`}); err != nil || len(started) != 0 {
		t.Fatalf("once: %v %q", err, started)
	}
	// Shortcuts and files go through the shell.
	undo, err = runAction(t, ActionAppOpen, RuleParams{"path": `C:\Users\me\Desktop\Tool.lnk`})
	if err != nil || undo != nil || !reflect.DeepEqual(opened, []string{`C:\Users\me\Desktop\Tool.lnk`}) {
		t.Fatalf("shell: %v %q", err, opened)
	}
	if _, err := runAction(t, ActionAppOpen, RuleParams{"path": "  "}); err == nil {
		t.Fatal("an empty path should fail")
	}
}
