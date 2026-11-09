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

func GetWindowText(hwnd uintptr) (string, error) {
	buf := make([]uint16, 512)
	ret, _, err := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if ret == 0 {
		if err != nil && err.Error() != "The operation completed successfully." {
			return "", err
		}
		return "", nil
	}
	return syscall.UTF16ToString(buf[:ret]), nil
}

func Getforgroundwindow() (uintptr, error) {
	ret, _, err := getForegroundWindow.Call()
	if ret == 0 {
		if err != nil && err.Error() != "The operation completed successfully." {
			return 0, err
		}
		return 0, nil
	}
	return ret, nil
}

// foregroundTitle returns the raw title of the active window.
func foregroundTitle() string {
	hwnd, _ := Getforgroundwindow()
	title, _ := GetWindowText(hwnd)
	return strings.TrimSpace(title)
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
