package modules

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")

var (
	getWindowTextW      = user32.NewProc("GetWindowTextW")
	getForegroundWindow = user32.NewProc("GetForegroundWindow")

	winMu    sync.RWMutex
	winCache string
	winAt    time.Time
)

// windowText returns a window's title, or "" when it has none.
func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// foregroundTitle returns the raw title of the active window.
func foregroundTitle() string {
	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	return strings.TrimSpace(windowText(hwnd))
}

var simplifiedApps = []struct {
	match string
	name  string
}{
	{"firefox", "Firefox"},
	{"c:\\", "Explorer"},
	{"google chrome", "Chrome"},
	{"chromium", "Chromium"},
	{"discord", "Discord"},
	{"opera", "Opera"},
	{"brave", "Brave"},
	{"microsoft edge", "Edge"},
	{"visual studio code", "VS Code"},
	{"steam", "Steam"},
	{"vrchat", "VRChat"},
	{"spotify", "Spotify"},
	{"notepad", "Notepad"},
	{"obs", "OBS"},
}

// windowLine renders the active window line with its optional label and limit.
func windowLine(cfg Config) string {
	title := strings.TrimSpace(GetActiveWindow())
	if title == "" {
		return ""
	}
	if limit := cfg.WindowOptions.MaxChars; limit > 0 {
		title = RuneLimit(title, limit)
	}
	return withLabel(cfg.WindowOptions.Label, title)
}

// GetActiveWindow returns the active window title, optionally simplified.
// Results are cached for a moment so several modules can read it cheaply.
func GetActiveWindow() string {
	winMu.RLock()
	title, at := winCache, winAt
	winMu.RUnlock()

	if time.Since(at) > 700*time.Millisecond {
		title = foregroundTitle()
		winMu.Lock()
		winCache = title
		winAt = time.Now()
		winMu.Unlock()
	}

	cfg := GetConfig()
	if !cfg.WindowOptions.Simplified {
		return title
	}
	lower := strings.ToLower(title)
	for _, app := range simplifiedApps {
		if strings.Contains(lower, app.match) {
			return app.name
		}
	}
	return title
}
