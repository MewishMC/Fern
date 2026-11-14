package modules

import (
	"encoding/json"
	"fmt"
	"os"
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
)

// DefaultOrder is the order used before the user picks one.
var DefaultOrder = []string{
	ModuleAFK, ModuleLoading, ModuleTime, ModuleText,
	ModuleMemory, ModuleCPU, ModuleMedia, ModuleWindow, ModuleLogs,
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
	Order        []string `json:"order"`
	Separator    string   `json:"separator"`
	MaxChars     int      `json:"max_chars"`
	MaxLines     int      `json:"max_lines"`
}

// WindowOptions controls how the active window title is shortened.
type WindowOptions struct {
	Simplified bool   `json:"simplified"`
	Label      string `json:"label"`     // optional prefix
	MaxChars   int    `json:"max_chars"` // 0 means no limit
}

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
	Bypass     bool    `json:"bypass"`
	AutoDetect bool    `json:"auto_detect"`
	Poopyafk   bool    `json:"poopyafk"`
	AFKTime    float64 `json:"afk_time"`
	AFKMessage string  `json:"afk_message"`
	ShowTimer  bool    `json:"show_timer"`
	TimerStyle string  `json:"timer_style"` // full, short, minutes
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
	MaxChars       int    `json:"max_chars"`     // limit for the track part
	PreferredApp   string `json:"preferred_app"` // session to read; empty follows the current one
}

// TimeOptions controls the clock line.
type TimeOptions struct {
	Use24h  bool   `json:"use_24h"`
	Seconds bool   `json:"seconds"`
	NoAmPm  bool   `json:"no_am_pm"` // 3:04 instead of 3:04 PM
	Label   string `json:"label"`
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
	OSCOptions     OSCOptions     `json:"OSC"`
	TimeoutMS      int64          `json:"timeout_ms"`
}

var (
	cfgMu    sync.RWMutex
	cfg      Config
	cfgPath  = "./config.json"
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
			MaxChars:       90,
			PreferredApp:   "",
		},
		TimeOptions: TimeOptions{
			Use24h: false,
			Label:  "",
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

// Load reads the config file, filling anything missing with defaults.
func Load() {
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
	c.DisplayOptions.MaxChars = clampInt(c.DisplayOptions.MaxChars, 32, 144)
	c.DisplayOptions.MaxLines = clampInt(c.DisplayOptions.MaxLines, 1, 9)
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
	c.MediaOptions.MaxChars = clampInt(c.MediaOptions.MaxChars, 4, 144)
	c.MediaOptions.PreferredApp = RuneLimit(strings.TrimSpace(c.MediaOptions.PreferredApp), 128)

	c.MemoryOptions.Label = RuneLimit(strings.TrimSpace(c.MemoryOptions.Label), 16)
	c.MemoryOptions.Decimals = clampInt(c.MemoryOptions.Decimals, 0, 3)
	c.CPUOptions.Label = RuneLimit(strings.TrimSpace(c.CPUOptions.Label), 16)
	c.CPUOptions.Decimals = clampInt(c.CPUOptions.Decimals, 0, 2)
	c.WindowOptions.Label = RuneLimit(strings.TrimSpace(c.WindowOptions.Label), 16)
	c.WindowOptions.MaxChars = clampInt(c.WindowOptions.MaxChars, 0, 144)
	c.LogsOptions.Label = RuneLimit(strings.TrimSpace(c.LogsOptions.Label), 16)
	c.LogsOptions.MaxChars = clampInt(c.LogsOptions.MaxChars, 0, 144)
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
