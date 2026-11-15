package modules

import (
	"path/filepath"
	"sort"
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
	winCache foreground
	winAt    time.Time

	// recentApps remembers the apps focused this session so the panel can
	// offer them when adding a rule, instead of making people type exe names.
	recentMu   sync.Mutex
	recentApps = map[string]*WindowApp{}
)

// foreground is what is focused right now.
type foreground struct {
	Title string // raw window title
	Exe   string // lower-case executable name, e.g. chrome.exe; "" if unknown
	Path  string // full executable path, used for the friendly name
}

// WindowApp is an app seen in the foreground, for the panel's app list.
type WindowApp struct {
	App      string `json:"app"`   // lower-case executable name
	Name     string `json:"name"`  // detected display name
	Title    string `json:"title"` // last window title
	LastSeen int64  `json:"lastSeen"`
}

// windowText returns a window's title, or "" when it has none.
func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := getWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// readForeground looks up the active window's title and executable.
func readForeground() foreground {
	hwnd, _, _ := getForegroundWindow.Call()
	if hwnd == 0 {
		return foreground{}
	}
	fg := foreground{Title: strings.TrimSpace(windowText(hwnd))}
	if path, err := getProcessPath(hwnd); err == nil {
		fg.Path = path
		fg.Exe = strings.ToLower(filepath.Base(path))
	}
	return fg
}

// knownApps names common executables whose own description is long or odd.
var knownApps = map[string]string{
	"chrome.exe":          "Chrome",
	"firefox.exe":         "Firefox",
	"msedge.exe":          "Edge",
	"brave.exe":           "Brave",
	"opera.exe":           "Opera",
	"vivaldi.exe":         "Vivaldi",
	"explorer.exe":        "Explorer",
	"discord.exe":         "Discord",
	"code.exe":            "VS Code",
	"steam.exe":           "Steam",
	"steamwebhelper.exe":  "Steam",
	"vrchat.exe":          "VRChat",
	"spotify.exe":         "Spotify",
	"notepad.exe":         "Notepad",
	"obs64.exe":           "OBS",
	"vrmonitor.exe":       "SteamVR",
	"vrserver.exe":        "SteamVR",
	"windowsterminal.exe": "Terminal",
}

// simplifiedApps catches apps by their title when the executable can't be
// read, which happens for windows running as administrator.
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

var (
	descMu    sync.Mutex
	descCache = map[string]string{}
)

// appName picks a display name for a foreground app: a known name, the
// executable's own description ("Visual Studio Code"), a name matched from
// the title, or the executable name without .exe.
func appName(fg foreground) string {
	if name, ok := knownApps[fg.Exe]; ok {
		return name
	}
	if fg.Path != "" {
		descMu.Lock()
		desc, ok := descCache[fg.Path]
		descMu.Unlock()
		if !ok {
			desc = fileDescription(fg.Path)
			descMu.Lock()
			descCache[fg.Path] = desc
			descMu.Unlock()
		}
		if desc != "" {
			return desc
		}
	}
	lower := strings.ToLower(fg.Title)
	for _, app := range simplifiedApps {
		if strings.Contains(lower, app.match) {
			return app.name
		}
	}
	if base := strings.TrimSuffix(fg.Exe, ".exe"); base != "" {
		return strings.ToUpper(base[:1]) + base[1:]
	}
	return fg.Title
}

// windowDisplay applies the window options to a foreground app and returns
// what the chatbox should show, or "" to leave the line out.
func windowDisplay(fg foreground, o WindowOptions) string {
	if fg.Title == "" && fg.Exe == "" {
		return ""
	}
	show := WindowShowTitle
	if o.Simplified {
		show = WindowShowName
	}
	name := ""
	ruled := false
	for _, r := range o.Rules {
		if fg.Exe != "" && r.App == fg.Exe {
			show, name, ruled = r.Show, r.Name, true
			break
		}
	}
	if (o.OnlyListed && !ruled) || show == WindowShowHide {
		return o.HiddenText
	}
	detected := appName(fg)
	if name == "" {
		name = detected
	}
	switch show {
	case WindowShowName:
		return name
	case WindowShowBoth:
		// Most apps end their title with their own name; don't repeat it.
		title := strings.TrimSpace(fg.Title)
		if strings.HasSuffix(strings.ToLower(title), strings.ToLower(detected)) {
			title = title[:len(title)-len(detected)]
		}
		title = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(title), "-—|·"))
		if title == "" || strings.EqualFold(title, name) {
			return name
		}
		return name + " — " + title
	default:
		if fg.Title == "" {
			return name
		}
		return fg.Title
	}
}

// windowLine renders the active window line with its optional label and limit.
func windowLine(cfg Config) string {
	text := strings.TrimSpace(windowDisplay(currentForeground(), cfg.WindowOptions))
	if text == "" {
		return ""
	}
	if limit := cfg.WindowOptions.MaxChars; limit > 0 {
		text = RuneLimit(text, limit)
	}
	return withLabel(cfg.WindowOptions.Label, text)
}

// currentForeground returns the focused app, cached for a moment so several
// callers can read it cheaply.
func currentForeground() foreground {
	winMu.RLock()
	fg, at := winCache, winAt
	winMu.RUnlock()
	if time.Since(at) <= 700*time.Millisecond {
		return fg
	}
	fg = readForeground()
	winMu.Lock()
	winCache, winAt = fg, time.Now()
	winMu.Unlock()
	rememberApp(fg)
	return fg
}

func rememberApp(fg foreground) {
	if fg.Exe == "" {
		return
	}
	name := appName(fg)
	recentMu.Lock()
	defer recentMu.Unlock()
	app := recentApps[fg.Exe]
	if app == nil {
		if len(recentApps) >= 50 {
			return
		}
		app = &WindowApp{App: fg.Exe, Name: name}
		recentApps[fg.Exe] = app
	}
	app.Title = RuneLimit(fg.Title, 80)
	app.LastSeen = time.Now().Unix()
}

// RecentWindowApps lists the apps focused this session, most recent first.
func RecentWindowApps() []WindowApp {
	currentForeground()
	recentMu.Lock()
	out := make([]WindowApp, 0, len(recentApps))
	for _, app := range recentApps {
		out = append(out, *app)
	}
	recentMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// GetActiveWindow returns what the window line would show, before the label
// and length limit.
func GetActiveWindow() string {
	return windowDisplay(currentForeground(), GetConfig().WindowOptions)
}
