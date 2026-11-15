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

func TestSanitizeWindowRules(t *testing.T) {
	c := DefaultConfig()
	c.WindowOptions.Rules = []WindowRule{
		{App: " Chrome.EXE ", Show: "bogus"},
		{App: "chrome.exe", Show: WindowShowHide}, // duplicate
		{App: "", Show: WindowShowHide},
	}
	c.sanitize()
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
