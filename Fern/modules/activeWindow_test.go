package modules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowDisplayRules(t *testing.T) {
	chrome := foreground{Title: "Fern - GitHub - Google Chrome", Exe: "chrome.exe"}
	game := foreground{Title: "Some Game", Exe: "game.exe"}
	base := WindowOptions{Simplified: true}

	cases := []struct {
		name string
		fg   foreground
		o    WindowOptions
		want string
	}{
		{"simplified uses the app name", chrome, base, "Chrome"},
		{"full title", chrome, WindowOptions{}, "Fern - GitHub - Google Chrome"},
		{"custom name", chrome, WindowOptions{Simplified: true, Rules: []WindowRule{{App: "chrome.exe", Name: "Browsing", Show: WindowShowName}}}, "Browsing"},
		{"rule shows the title", chrome, WindowOptions{Simplified: true, Rules: []WindowRule{{App: "chrome.exe", Show: WindowShowTitle}}}, "Fern - GitHub - Google Chrome"},
		{"name and title drops the repeated app name", foreground{Title: "Notes.txt - Notepad", Exe: "notepad.exe"},
			WindowOptions{Rules: []WindowRule{{App: "notepad.exe", Show: WindowShowBoth}}}, "Notepad — Notes.txt"},
		{"hidden drops the line", chrome, WindowOptions{Rules: []WindowRule{{App: "chrome.exe", Show: WindowShowHide}}}, ""},
		{"hidden text", chrome, WindowOptions{HiddenText: "busy", Rules: []WindowRule{{App: "chrome.exe", Show: WindowShowHide}}}, "busy"},
		{"only listed hides others", game, WindowOptions{Simplified: true, OnlyListed: true, Rules: []WindowRule{{App: "chrome.exe", Show: WindowShowName}}}, ""},
		{"only listed keeps listed", chrome, WindowOptions{Simplified: true, OnlyListed: true, Rules: []WindowRule{{App: "chrome.exe", Show: WindowShowName}}}, "Chrome"},
		{"unknown exe falls back to its name", foreground{Title: "x", Exe: "mygame.exe"}, base, "Mygame"},
		{"no exe matches the title", foreground{Title: "Admin - Discord"}, base, "Discord"},
		{"nothing focused", foreground{}, base, ""},
	}
	for _, c := range cases {
		if got := windowDisplay(c.fg, c.o); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWindowTitleFilters(t *testing.T) {
	o := WindowOptions{Simplified: true, HiddenText: "private", HideTitles: []string{"incognito", `/^secret \d+/`}}
	cases := []struct {
		title string
		want  string
	}{
		{"New Incognito Tab - Google Chrome", "private"},
		{"secret 42 - Notepad", "private"},
		{"not secret 42 - Notepad", "Notepad"},
		{"Fern - Notepad", "Notepad"},
	}
	for _, c := range cases {
		if got := windowDisplay(foreground{Title: c.title, Exe: "notepad.exe"}, o); got != c.want {
			t.Errorf("%q: got %q, want %q", c.title, got, c.want)
		}
	}
}

func TestWindowMode(t *testing.T) {
	o := WindowOptions{ShowMode: true, VRText: "In VR", DeskText: "On desktop", Connector: "·"}
	if got := withMode(o, true, "Chrome"); got != "In VR · Chrome" {
		t.Errorf("vr: got %q", got)
	}
	if got := withMode(o, false, ""); got != "On desktop" {
		t.Errorf("hidden app: got %q", got)
	}
	o.Connector = ""
	if got := withMode(o, false, "Chrome"); got != "On desktop Chrome" {
		t.Errorf("no connector: got %q", got)
	}
	if !vrRuntimeRunning(map[string]bool{"vrserver.exe": true}, false) {
		t.Error("SteamVR should count as VR")
	}
	if vrRuntimeRunning(map[string]bool{"ovrserver_x64.exe": true}, false) || !vrRuntimeRunning(map[string]bool{"ovrserver_x64.exe": true}, true) {
		t.Error("Oculus should count only when asked")
	}
	if len(runningProcesses()) == 0 {
		t.Error("process list is empty")
	}
}

func TestSanitizeWindowRules(t *testing.T) {
	c := DefaultConfig()
	c.WindowOptions.Rules = []WindowRule{
		{App: " Chrome.EXE ", Show: "bogus"},
		{App: "chrome.exe", Show: WindowShowHide}, // duplicate
		{App: "", Show: WindowShowHide},
	}
	c.WindowOptions.HideTitles = []string{" ok ", "", "/[unclosed/"}
	c.sanitize()
	if h := c.WindowOptions.HideTitles; len(h) != 1 || h[0] != "ok" {
		t.Errorf("hide titles: got %q", h)
	}
	r := c.WindowOptions.Rules
	if len(r) != 1 || r[0].App != "chrome.exe" || r[0].Show != WindowShowName {
		t.Errorf("got %+v", r)
	}
}

func TestFileDescription(t *testing.T) {
	notepad := filepath.Join(os.Getenv("WINDIR"), "System32", "notepad.exe")
	if _, err := os.Stat(notepad); err != nil {
		t.Skip("notepad.exe not found")
	}
	if got := fileDescription(notepad); got == "" {
		t.Error("notepad.exe has no description")
	}
	if got := fileDescription(filepath.Join(t.TempDir(), "missing.exe")); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}
