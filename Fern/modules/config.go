package modules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Module identifiers used to order the lines of the chatbox text.
const (
	ModuleAFK     = "afk"
	ModuleLoading = "loading"
	ModuleTime    = "time"
	ModuleText    = "text"
	ModuleMemory  = "memory"
	ModuleCPU     = "cpu"
	ModuleMedia   = "media"
	ModuleWindow  = "window"
	ModuleLogs    = "logs"
	ModuleLyrics  = "lyrics"
)

// DefaultOrder is the order used before the user picks one.
var DefaultOrder = []string{
	ModuleAFK, ModuleLoading, ModuleTime, ModuleText,
	ModuleMemory, ModuleCPU, ModuleMedia, ModuleLyrics, ModuleWindow, ModuleLogs,
}

var knownModules = func() map[string]bool {
	known := make(map[string]bool, len(DefaultOrder))
	for _, id := range DefaultOrder {
		known[id] = true
	}
	return known
}()

// DisplayOptions controls which modules are shown and in what order.
type DisplayOptions struct {
	Show         bool     `json:"show"`
	Loading      bool     `json:"loading"`
	Time         bool     `json:"time"`
	Afk          bool     `json:"afk"`
	Media        bool     `json:"media"`
	Text         bool     `json:"text"`
	Memory       bool     `json:"memory"`
	ActiveWindow bool     `json:"active_window"`
	CPU          bool     `json:"cpu"`
	VRChatLogs   bool     `json:"vr_chat_logs"`
	Lyrics       bool     `json:"lyrics"`
	Order        []string `json:"order"`
	Separator    string   `json:"separator"`
	MaxChars     int      `json:"max_chars"`
	MaxLines     int      `json:"max_lines"`
}

// WindowOptions controls how the active window title is shortened.
type WindowOptions struct {
	Simplified  bool         `json:"simplified"`  // apps without a rule show their name, not the title
	Label       string       `json:"label"`       // optional prefix
	MaxChars    int          `json:"max_chars"`   // 0 means no limit
	OnlyListed  bool         `json:"only_listed"` // apps without a rule are treated as hidden
	HiddenText  string       `json:"hidden_text"` // shown for hidden apps; empty drops the line
	Rules       []WindowRule `json:"rules"`
	HideTitles  []string     `json:"hide_titles"` // words, or /regex/, that hide a window by its title
	ShowMode    bool         `json:"show_mode"`   // prefix the line with VRText or DesktopText
	VRText      string       `json:"vr_text"`
	DeskText    string       `json:"desktop_text"`
	Connector   string       `json:"connector"`    // between the mode and the app
	CountOculus bool         `json:"count_oculus"` // treat the Oculus runtime as VR too
}

// WindowRule changes how one app appears on the window line.
type WindowRule struct {
	App  string `json:"app"`  // executable name, e.g. chrome.exe
	Name string `json:"name"` // custom display name; empty uses the detected name
	Show string `json:"show"` // name, title, both or hide
}

// Ways a WindowRule can show its app.
const (
	WindowShowName  = "name"
	WindowShowTitle = "title"
	WindowShowBoth  = "both"
	WindowShowHide  = "hide"
)

// TextOptions controls the rotating custom messages.
type TextOptions struct {
	Cycle   bool   `json:"cycle"`
	Speed   int    `json:"speed"`
	Shuffle bool   `json:"shuffle"`
	Label   string `json:"label"` // optional prefix before the message
}

// LoadingOptions controls the loading module.
type LoadingOptions struct {
	Fast     bool   `json:"fast"`
	RateMS   int    `json:"rate_ms"`
	Text     string `json:"text"` // shown before the growing dots; empty means dots only
	ShowDots bool   `json:"show_dots"`
	Dots     int    `json:"dots"` // longest run of dots before looping
}

// AfkOptions controls the afk timer shown in the chatbox.
type AfkOptions struct {
	Bypass     bool `json:"bypass"`
	AutoDetect bool `json:"auto_detect"`
	// MovementOnly counts only movement as activity, ignoring voice, input
	// and window switches. The JSON key predates the name.
	MovementOnly bool    `json:"poopyafk"`
	AFKTime      float64 `json:"afk_time"`
	AFKMessage   string  `json:"afk_message"`
	ShowTimer    bool    `json:"show_timer"`
	TimerStyle   string  `json:"timer_style"` // full, short, minutes
}

// MediaOptions controls how the now playing line is rendered.
type MediaOptions struct {
	ShowProgress   bool   `json:"show_progress"`
	ProgressStyle  string `json:"progress_style"` // bar, time, both, none
	ShowWhenPaused bool   `json:"show_when_paused"`
	AppFilter      string `json:"app_filter"` // empty means any media app
	Icon           string `json:"icon"`
	PausedIcon     string `json:"paused_icon"`
	ShowArtist     bool   `json:"show_artist"`
	ShowAlbum      bool   `json:"show_album"`
	BarWidth       int    `json:"bar_width"`     // 0 hides the bar
	BarFilled      string `json:"bar_filled"`    // played part, e.g. ▰
	BarEmpty       string `json:"bar_empty"`     // unplayed part, e.g. ▱
	BarHead        string `json:"bar_head"`      // optional marker at the position, e.g. ●
	BarLeft        string `json:"bar_left"`      // optional cap before the bar, e.g. [
	BarRight       string `json:"bar_right"`     // optional cap after the bar, e.g. ]
	MaxChars       int    `json:"max_chars"`     // limit for the track part
	PreferredApp   string `json:"preferred_app"` // session to read; empty follows the current one
	// State words in front of the track, e.g. "Listening to"; empty for none.
	PlayingText     string `json:"playing_text"`
	PausedText      string `json:"paused_text"`
	StoppedText     string `json:"stopped_text"`
	StoppedIcon     string `json:"stopped_icon"`
	ShowWhenStopped bool   `json:"show_when_stopped"`
	Separator       string `json:"separator"` // between the title and the artist
	Uppercase       bool   `json:"uppercase"`
	SuperscriptTime bool   `json:"superscript_time"` // ¹˸²⁴ᐟ³˸¹⁸ instead of 1:24/3:18
	// OnlyOnChange shows the line for ChangeSeconds after the track changes or
	// playback is paused or resumed, then hides it until the next change.
	OnlyOnChange  bool `json:"only_on_change"`
	ChangeSeconds int  `json:"change_seconds"`
	TidyTitles    bool `json:"tidy_titles"` // drop "(Official Video)", "[HD]" and the like
	// BarPresets are the user's saved progress bar styles.
	BarPresets []BarPreset `json:"bar_presets"`
}

// BarPreset is a named set of progress bar characters. The keys match the
// MediaOptions fields so the panel can copy one over the other.
type BarPreset struct {
	Name   string `json:"name"`
	Filled string `json:"bar_filled"`
	Empty  string `json:"bar_empty"`
	Head   string `json:"bar_head"`
	Left   string `json:"bar_left"`
	Right  string `json:"bar_right"`
}

// TimeOptions controls the clock line.
type TimeOptions struct {
	Use24h  bool   `json:"use_24h"`
	Seconds bool   `json:"seconds"`
	NoAmPm  bool   `json:"no_am_pm"` // 3:04 instead of 3:04 PM
	Label   string `json:"label"`
	// Zone is an IANA time zone like Europe/Berlin; empty uses the PC's.
	Zone         string        `json:"zone"`
	ShowZone     bool          `json:"show_zone"` // add the abbreviation, like CET
	Zone2        string        `json:"zone2"`     // optional second clock
	Label2       string        `json:"label2"`
	Counters     []TimeCounter `json:"counters"`
	CounterUnits int           `json:"counter_units"` // units per counter, 1-4
	CounterLine  bool          `json:"counter_line"`  // counters on a line of their own
}

// MemoryOptions controls the memory line.
type MemoryOptions struct {
	Label       string `json:"label"`
	ShowUsed    bool   `json:"show_used"`
	ShowTotal   bool   `json:"show_total"`
	ShowPercent bool   `json:"show_percent"`
	Decimals    int    `json:"decimals"`
}

// CPUOptions controls the CPU line.
type CPUOptions struct {
	Label     string `json:"label"`
	ShowName  bool   `json:"show_name"`
	ShowCores bool   `json:"show_cores"`
	ShowUsage bool   `json:"show_usage"`
	Decimals  int    `json:"decimals"`
}

// LogsOptions controls the VRChat log line.
type LogsOptions struct {
	Label     string `json:"label"`
	ShowJoin  bool   `json:"show_join"`
	ShowLeave bool   `json:"show_leave"`
	MaxChars  int    `json:"max_chars"`
}

// LyricsOptions controls the synced lyrics line.
type LyricsOptions struct {
	Label        string `json:"label"`
	OffsetMS     int    `json:"offset_ms"`     // positive shows lines later
	HoldSeconds  int    `json:"hold_seconds"`  // longest a line stays up
	GapSeconds   int    `json:"gap_seconds"`   // pauses this long show GapMarker; 0 never
	GapMarker    string `json:"gap_marker"`    // shown in breaks and instrumentals
	ReplaceTrack bool   `json:"replace_track"` // show on the media line instead of the track
	Folder       string `json:"folder"`        // .lrc files here win over LRCLIB
	Online       bool   `json:"online"`        // look lyrics up on lrclib.net
	MaxChars     int    `json:"max_chars"`
}

// OSCOptions is where chatbox messages are sent.
type OSCOptions struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Config is the persisted application configuration.
type Config struct {
	DisplayOptions DisplayOptions `json:"display_options"`
	TextMessages   []string       `json:"TextMessages"`
	WindowOptions  WindowOptions  `json:"WindowOptions"`
	TextOptions    TextOptions    `json:"TextOptions"`
	LoadingOptions LoadingOptions `json:"LoadingOptions"`
	AfkOptions     AfkOptions     `json:"AfkOptions"`
	MediaOptions   MediaOptions   `json:"MediaOptions"`
	TimeOptions    TimeOptions    `json:"TimeOptions"`
	MemoryOptions  MemoryOptions  `json:"MemoryOptions"`
	CPUOptions     CPUOptions     `json:"CPUOptions"`
	LogsOptions    LogsOptions    `json:"LogsOptions"`
	LyricsOptions  LyricsOptions  `json:"LyricsOptions"`
	OSCOptions     OSCOptions     `json:"OSC"`
	TimeoutMS      int64          `json:"timeout_ms"`
}

var (
	cfgMu    sync.RWMutex
	cfg      Config
	cfgPath  string // set by Load; see configPath
	saveMu   sync.Mutex
	saveAt   *time.Timer
	changeCh = make(chan struct{}, 1)
)

// DefaultConfig returns a fully populated configuration.
func DefaultConfig() Config {
	return Config{
		DisplayOptions: DisplayOptions{
			Show:         true,
			Loading:      true,
			Time:         true,
			Afk:          true,
			Media:        true,
			Text:         true,
			Memory:       false,
			ActiveWindow: false,
			CPU:          false,
			VRChatLogs:   false,
			Order:        append([]string{}, DefaultOrder...),
			Separator:    "\n",
			MaxChars:     144,
			MaxLines:     9,
		},
		TextMessages: []string{"♡ hi there", "♡ have a good day", "♡ be kind"},
		WindowOptions: WindowOptions{
			Simplified: true,
			Label:      "",
			MaxChars:   60,
			HideTitles: []string{"incognito", "inprivate", "private browsing"},
			VRText:     "In VR",
			DeskText:   "On desktop",
			Connector:  "·",
		},
		TextOptions: TextOptions{
			Cycle:   true,
			Speed:   2500,
			Shuffle: false,
			Label:   "",
		},
		LoadingOptions: LoadingOptions{
			Fast:     true,
			RateMS:   1000,
			ShowDots: true,
			Dots:     6,
		},
		AfkOptions: AfkOptions{
			AutoDetect: true,
			AFKTime:    10,
			AFKMessage: "AFK",
			ShowTimer:  true,
			TimerStyle: "full",
		},
		MediaOptions: MediaOptions{
			ShowProgress:   true,
			ProgressStyle:  "both",
			ShowWhenPaused: false,
			AppFilter:      "",
			Icon:           "♪",
			PausedIcon:     "❚❚",
			ShowArtist:     true,
			ShowAlbum:      false,
			BarWidth:       8,
			BarFilled:      "▰",
			BarEmpty:       "▱",
			MaxChars:       90,
			PreferredApp:   "",
			StoppedIcon:    "⏹",
			Separator:      " — ",
			ChangeSeconds:  25,
			TidyTitles:     true,
		},
		TimeOptions: TimeOptions{
			Use24h:       false,
			Label:        "",
			CounterUnits: 2,
		},
		MemoryOptions: MemoryOptions{
			Label:       "ᵐᵉᵐ",
			ShowUsed:    true,
			ShowTotal:   true,
			ShowPercent: true,
			Decimals:    1,
		},
		CPUOptions: CPUOptions{
			Label:     "ᶜᵖᵘ",
			ShowName:  true,
			ShowCores: false,
			ShowUsage: true,
			Decimals:  0,
		},
		LogsOptions: LogsOptions{
			Label:     "",
			ShowJoin:  true,
			ShowLeave: true,
			MaxChars:  80,
		},
		LyricsOptions: LyricsOptions{
			HoldSeconds: 6,
			GapSeconds:  8,
			GapMarker:   "♪ ♪ ♪",
			Online:      true,
			MaxChars:    90,
		},
		OSCOptions: OSCOptions{
			Host: "127.0.0.1",
			Port: 9000,
		},
		TimeoutMS: 2500,
	}
}

func (c Config) clone() Config {
	out := c
	out.DisplayOptions.Order = append([]string{}, c.DisplayOptions.Order...)
	out.TextMessages = append([]string{}, c.TextMessages...)
	out.WindowOptions.Rules = append([]WindowRule{}, c.WindowOptions.Rules...)
	out.WindowOptions.HideTitles = append([]string{}, c.WindowOptions.HideTitles...)
	out.MediaOptions.BarPresets = append([]BarPreset{}, c.MediaOptions.BarPresets...)
	out.TimeOptions.Counters = append([]TimeCounter{}, c.TimeOptions.Counters...)
	return out
}

// GetConfig returns a safe copy of the current configuration.
func GetConfig() Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.clone()
}

// Changes receives a signal whenever the configuration changes, so the main
// loop can re-send the chatbox immediately instead of waiting for the timeout.
func Changes() <-chan struct{} { return changeCh }

// NotifyChange wakes up the sender loop.
func NotifyChange() {
	select {
	case changeCh <- struct{}{}:
	default:
	}
}

// configPath returns where Fern keeps its settings: %LOCALAPPDATA%\Fern\config.json,
// so they survive replacing or moving the executable. It falls back to the
// folder Fern runs from if no per-user folder can be created.
func configPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	if base != "" {
		dir := filepath.Join(base, "Fern")
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return filepath.Join(dir, "config.json")
		}
	}
	return "config.json"
}

// legacyConfigPaths are where versions up to 2.1 kept config.json: next to the
// executable, or the folder Fern was started from.
func legacyConfigPaths() []string {
	var paths []string
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "config.json"))
	}
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, "config.json"))
	}
	return paths
}

// migrateConfig copies the first existing legacy config to path when path does
// not exist yet, then renames the old file to config.json.old so deleting the
// new one later resets the settings instead of importing the old ones again.
func migrateConfig(path string, legacy []string) {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return
	}
	for _, old := range legacy {
		if same, _ := filepath.Abs(old); same == path {
			continue
		}
		data, err := os.ReadFile(old)
		if err != nil {
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Println("config: could not copy", old, "to", path+":", err)
			return
		}
		if err := os.Rename(old, old+".old"); err != nil {
			fmt.Println("config: could not rename", old+":", err)
		}
		fmt.Println("config: moved settings from", old, "to", path)
		return
	}
}

// Load reads the config file, filling anything missing with defaults.
func Load() {
	if cfgPath == "" {
		cfgPath = configPath()
		migrateConfig(cfgPath, legacyConfigPaths())
	}
	fmt.Println("config:", cfgPath)
	loaded := DefaultConfig()

	data, err := os.ReadFile(cfgPath)
	if err == nil {
		// Unmarshal on top of the defaults so new fields keep sane values.
		if err := json.Unmarshal(data, &loaded); err != nil {
			fmt.Println("config: could not parse, using defaults:", err)
			loaded = DefaultConfig()
		}
	} else if !os.IsNotExist(err) {
		fmt.Println("config: could not read, using defaults:", err)
	}

	if loaded.TextMessages == nil {
		loaded.TextMessages = DefaultConfig().TextMessages
	}

	loaded.sanitize()

	cfgMu.Lock()
	cfg = loaded
	cfgMu.Unlock()

	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		_ = SaveNow()
	}
	NotifyChange()
}

// SetConfig replaces the whole configuration after validating it.
func SetConfig(in Config) {
	in.sanitize()
	cfgMu.Lock()
	cfg = in.clone()
	cfgMu.Unlock()
	scheduleSave()
	NotifyChange()
}

// UpdateConfig mutates the configuration under lock.
func UpdateConfig(fn func(*Config)) {
	cfgMu.Lock()
	next := cfg.clone()
	fn(&next)
	next.sanitize()
	cfg = next
	cfgMu.Unlock()
	scheduleSave()
	NotifyChange()
}

// ResetConfig restores the defaults.
func ResetConfig() { SetConfig(DefaultConfig()) }

// moduleFlag returns the switch that enables a module, or nil for an unknown id.
func moduleFlag(d *DisplayOptions, id string) *bool {
	switch id {
	case ModuleAFK:
		return &d.Afk
	case ModuleLoading:
		return &d.Loading
	case ModuleTime:
		return &d.Time
	case ModuleText:
		return &d.Text
	case ModuleMemory:
		return &d.Memory
	case ModuleCPU:
		return &d.CPU
	case ModuleMedia:
		return &d.Media
	case ModuleWindow:
		return &d.ActiveWindow
	case ModuleLogs:
		return &d.VRChatLogs
	case ModuleLyrics:
		return &d.Lyrics
	}
	return nil
}

// ToggleModule flips one module on or off.
func ToggleModule(id string) {
	if !knownModules[id] {
		return
	}
	UpdateConfig(func(c *Config) {
		flag := moduleFlag(&c.DisplayOptions, id)
		*flag = !*flag
	})
}

// SetOrder stores a new module order.
func SetOrder(order []string) {
	UpdateConfig(func(c *Config) { c.DisplayOptions.Order = order })
}

// ModuleEnabled reports whether a module should be rendered.
func ModuleEnabled(id string, d DisplayOptions) bool {
	flag := moduleFlag(&d, id)
	return flag != nil && *flag
}

// EffectiveRateMS is how often the chatbox is actually sent. The spinner needs
// faster updates than the normal refresh to look like it is spinning.
func EffectiveRateMS(cfg Config) int64 {
	rate := cfg.TimeoutMS
	if cfg.DisplayOptions.Show && cfg.DisplayOptions.Loading && cfg.LoadingOptions.Fast {
		fast := int64(cfg.LoadingOptions.RateMS)
		if fast > 0 && fast < rate {
			rate = fast
		}
	}
	return rate
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sanitize keeps stored values inside ranges the rest of the app can handle.
func (c *Config) sanitize() {
	c.TimeoutMS = int64(clampInt(int(c.TimeoutMS), 250, 15000))
	// VRChat accepts roughly one chatbox update per second and silently drops
	// the rest, so sending faster does not make the animation smoother: it makes
	// the dots skip. One dot per second is the pace VRChat can actually show.
	c.LoadingOptions.RateMS = clampInt(c.LoadingOptions.RateMS, 1000, 3000)
	// The loading text is one chatbox line: no newlines, no runaway length.
	trimmed := strings.Join(strings.Fields(c.LoadingOptions.Text), " ")
	c.LoadingOptions.Text = RuneLimit(strings.TrimRight(trimmed, "."), 40)
	c.TextOptions.Speed = clampInt(c.TextOptions.Speed, 400, 60000)
	// Always VRChat's own limits. The panel no longer offers these, so an old
	// config with a lower value would otherwise cut the chatbox short for good.
	c.DisplayOptions.MaxChars = 144
	c.DisplayOptions.MaxLines = 9
	switch c.DisplayOptions.Separator {
	case " | ", " · ", " — ", " / ", "  ":
	default:
		c.DisplayOptions.Separator = "\n"
	}

	// Rebuild the module order: keep known ids, drop duplicates, append new ones.
	seen := make(map[string]bool, len(knownModules))
	order := make([]string, 0, len(knownModules))
	for _, id := range c.DisplayOptions.Order {
		if !knownModules[id] || seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
	}
	for _, id := range DefaultOrder {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	c.DisplayOptions.Order = order

	if c.AfkOptions.AFKTime < 0 {
		c.AfkOptions.AFKTime = 0
	}
	if c.AfkOptions.AFKTime > 100000 {
		c.AfkOptions.AFKTime = 100000
	}
	if strings.TrimSpace(c.AfkOptions.AFKMessage) == "" {
		c.AfkOptions.AFKMessage = "AFK"
	}

	switch c.MediaOptions.ProgressStyle {
	case "bar", "time", "both", "none":
	default:
		c.MediaOptions.ProgressStyle = "both"
	}
	c.MediaOptions.Icon = RuneLimit(strings.TrimSpace(c.MediaOptions.Icon), 4)
	c.MediaOptions.PausedIcon = RuneLimit(strings.TrimSpace(c.MediaOptions.PausedIcon), 4)
	c.MediaOptions.BarWidth = clampInt(c.MediaOptions.BarWidth, 0, 20)
	// Spaces are allowed on purpose (a blank empty part is a valid style), so
	// these are only shortened, not trimmed.
	for _, s := range []*string{&c.MediaOptions.BarFilled, &c.MediaOptions.BarEmpty,
		&c.MediaOptions.BarHead, &c.MediaOptions.BarLeft, &c.MediaOptions.BarRight} {
		*s = RuneLimit(*s, 2)
	}
	c.MediaOptions.MaxChars = clampInt(c.MediaOptions.MaxChars, 4, 144)
	c.MediaOptions.PreferredApp = RuneLimit(strings.TrimSpace(c.MediaOptions.PreferredApp), 128)
	for _, s := range []*string{&c.MediaOptions.PlayingText, &c.MediaOptions.PausedText, &c.MediaOptions.StoppedText} {
		*s = RuneLimit(strings.Join(strings.Fields(*s), " "), 24)
	}
	c.MediaOptions.StoppedIcon = RuneLimit(strings.TrimSpace(c.MediaOptions.StoppedIcon), 4)
	// The separator keeps its spaces (" ᵇʸ " needs them) but stays on one line.
	c.MediaOptions.Separator = RuneLimit(strings.ReplaceAll(c.MediaOptions.Separator, "\n", " "), 8)
	if strings.TrimSpace(c.MediaOptions.Separator) == "" {
		c.MediaOptions.Separator = " — "
	}
	c.MediaOptions.ChangeSeconds = clampInt(c.MediaOptions.ChangeSeconds, 3, 600)
	c.MediaOptions.BarPresets = sanitizeBarPresets(c.MediaOptions.BarPresets)

	c.MemoryOptions.Label = RuneLimit(strings.TrimSpace(c.MemoryOptions.Label), 16)
	c.MemoryOptions.Decimals = clampInt(c.MemoryOptions.Decimals, 0, 3)
	c.CPUOptions.Label = RuneLimit(strings.TrimSpace(c.CPUOptions.Label), 16)
	c.CPUOptions.Decimals = clampInt(c.CPUOptions.Decimals, 0, 2)
	c.WindowOptions.Label = RuneLimit(strings.TrimSpace(c.WindowOptions.Label), 16)
	c.WindowOptions.MaxChars = clampInt(c.WindowOptions.MaxChars, 0, 144)
	c.WindowOptions.HiddenText = RuneLimit(strings.TrimSpace(c.WindowOptions.HiddenText), 40)
	rules := make([]WindowRule, 0, len(c.WindowOptions.Rules))
	seenApps := map[string]bool{}
	for _, r := range c.WindowOptions.Rules {
		r.App = strings.ToLower(strings.TrimSpace(r.App))
		if r.App == "" || seenApps[r.App] || len(rules) >= 100 {
			continue
		}
		seenApps[r.App] = true
		r.App = RuneLimit(r.App, 64)
		r.Name = RuneLimit(strings.TrimSpace(r.Name), 40)
		switch r.Show {
		case WindowShowName, WindowShowTitle, WindowShowBoth, WindowShowHide:
		default:
			r.Show = WindowShowName
		}
		rules = append(rules, r)
	}
	c.WindowOptions.Rules = rules
	hide := make([]string, 0, len(c.WindowOptions.HideTitles))
	for _, h := range c.WindowOptions.HideTitles {
		h = RuneLimit(strings.TrimSpace(h), 100)
		if h == "" || len(hide) >= 50 {
			continue
		}
		if _, err := titlePattern(h); err != nil {
			continue
		}
		hide = append(hide, h)
	}
	c.WindowOptions.HideTitles = hide
	c.WindowOptions.VRText = RuneLimit(strings.TrimSpace(c.WindowOptions.VRText), 24)
	c.WindowOptions.DeskText = RuneLimit(strings.TrimSpace(c.WindowOptions.DeskText), 24)
	c.WindowOptions.Connector = RuneLimit(strings.TrimSpace(c.WindowOptions.Connector), 16)
	c.LogsOptions.Label = RuneLimit(strings.TrimSpace(c.LogsOptions.Label), 16)
	c.LogsOptions.MaxChars = clampInt(c.LogsOptions.MaxChars, 0, 144)
	sanitizeLyrics(&c.LyricsOptions)
	sanitizeTime(&c.TimeOptions)
	c.TextOptions.Label = RuneLimit(strings.TrimSpace(c.TextOptions.Label), 16)
	if c.LoadingOptions.Dots == 0 {
		c.LoadingOptions.Dots = 6
	}
	c.LoadingOptions.Dots = clampInt(c.LoadingOptions.Dots, 1, 6)
	switch c.AfkOptions.TimerStyle {
	case "full", "short", "minutes":
	default:
		c.AfkOptions.TimerStyle = "full"
	}

	if c.OSCOptions.Host == "" {
		c.OSCOptions.Host = "127.0.0.1"
	}
	if c.OSCOptions.Port < 1 || c.OSCOptions.Port > 65535 {
		c.OSCOptions.Port = 9000
	}

	// Keep messages exactly as typed, blanks included. Editing a message briefly
	// empties it, and dropping blank entries here made rows vanish mid-edit and
	// shifted every index after them. Blank entries are skipped when rendering.
	if c.TextMessages != nil {
		if len(c.TextMessages) > 200 {
			c.TextMessages = c.TextMessages[:200]
		}
		limited := make([]string, 0, len(c.TextMessages))
		for _, m := range c.TextMessages {
			limited = append(limited, RuneLimit(m, 144))
		}
		c.TextMessages = limited
	}
}

func scheduleSave() {
	saveMu.Lock()
	defer saveMu.Unlock()
	if saveAt != nil {
		saveAt.Stop()
	}
	saveAt = time.AfterFunc(900*time.Millisecond, func() {
		if err := SaveNow(); err != nil {
			fmt.Println("config: save failed:", err)
		}
	})
}

// SaveNow writes the configuration to disk atomically.
func SaveNow() error {
	cfgMu.RLock()
	out := cfg.clone()
	cfgMu.RUnlock()

	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, cfgPath)
}
