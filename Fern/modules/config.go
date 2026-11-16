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
	ModuleBattery = "battery"
	ModuleVRPerf  = "vrperf"
	ModuleHeart   = "heartrate"
	ModuleLyrics  = "lyrics"
	ModuleWeather = "weather"

	ModuleDiscord  = "discord"
	ModuleTwitch   = "twitch"
	ModuleSoundpad = "soundpad"
	ModuleVoicemod = "voicemod"
	ModuleNetwork  = "network"
	ModuleGPU      = "gpu"
	ModuleWorld    = "world"
)

// DefaultOrder is the order used before the user picks one.
var DefaultOrder = []string{
	ModuleAFK, ModuleLoading, ModuleTime, ModuleWeather, ModuleText,
	ModuleMemory, ModuleCPU, ModuleMedia, ModuleLyrics, ModuleWindow, ModuleLogs,
	ModuleBattery, ModuleVRPerf,
	ModuleHeart,
	ModuleDiscord, ModuleTwitch, ModuleSoundpad, ModuleVoicemod,
	ModuleNetwork, ModuleGPU, ModuleWorld,
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
	Battery      bool     `json:"battery"`
	VRPerf       bool     `json:"vr_perf"`
	HeartRate    bool     `json:"heart_rate"`
	Lyrics       bool     `json:"lyrics"`
	Weather      bool     `json:"weather"`
	Discord      bool     `json:"discord"`
	Twitch       bool     `json:"twitch"`
	Soundpad     bool     `json:"soundpad"`
	Voicemod     bool     `json:"voicemod"`
	Network      bool     `json:"network"`
	GPU          bool     `json:"gpu"`
	World        bool     `json:"world"`
	Order        []string `json:"order"`
	Separator    string   `json:"separator"`
	MaxChars     int      `json:"max_chars"`
	MaxLines     int      `json:"max_lines"`
	// DropOrder is which line goes first when the chatbox is too long.
	DropOrder []string `json:"drop_order"`
	// Visibility limits a line to VR or desktop: "vr" or "desktop" per id.
	// Missing ids show always.
	Visibility map[string]string `json:"visibility"`
	LinePrefix string            `json:"line_prefix"` // put before every line
	LineSuffix string            `json:"line_suffix"` // put after every line
}

// Values of DisplayOptions.Visibility.
const (
	ShowAlways  = "always"
	ShowVR      = "vr"
	ShowDesktop = "desktop"
)

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
	// Weighted makes the shuffle favour messages that have gone unseen the
	// longest. It only applies while Shuffle is on.
	Weighted bool `json:"weighted"`
	// Group limits the rotation to the messages in one group; empty means
	// every hearted message. Groups lists the groups, empty ones included.
	Group  string   `json:"group"`
	Groups []string `json:"groups"`
	// Icons go in front of the messages, one per message change.
	Icons       []string `json:"icons"`
	IconsRandom bool     `json:"icons_random"`
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
	// AFKMessage is the text shown with the timer. {time} and {since} place
	// the idle time and the time AFK started; without them the timer is
	// added at the end.
	AFKMessage string `json:"afk_message"`
	// PlainMessage is the text shown when the timer is off. Empty means the
	// timer text with its placeholders taken out.
	PlainMessage string `json:"afk_message_plain"`
	Icon         string `json:"icon"` // optional prefix, like 💤
	ShowTimer    bool   `json:"show_timer"`
	TimerStyle   string `json:"timer_style"` // full, short, minutes, compact, words
	TimerFont    string `json:"timer_font"`  // a TextStyles id for the timer, or ""

	// Activity sources (see afkSources). MovementOnly overrides them.
	UseMovement bool   `json:"use_movement"`
	UseVoice    bool   `json:"use_voice"`
	UseOSC      bool   `json:"use_osc_other"`
	UseWindow   bool   `json:"use_window"`
	InputMode   string `json:"input_mode"` // keyboard and mouse: always, desktop, off

	// Manual is the "I'm AFK" switch: on until it is turned off, whatever
	// detection says. ManualSince is when it was turned on (unix ms).
	Manual      bool  `json:"manual"`
	ManualSince int64 `json:"manual_since"`
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
	Label      string `json:"label"`
	ShowName   bool   `json:"show_name"`
	CustomName string `json:"custom_name"` // replaces the detected model when set
	ShowCores  bool   `json:"show_cores"`
	ShowUsage  bool   `json:"show_usage"`
	Decimals   int    `json:"decimals"`
}

// GPUOptions controls the graphics card line.
type GPUOptions struct {
	Label         string `json:"label"`
	Adapter       string `json:"adapter"` // adapter name; empty picks the one with the most VRAM
	ShowName      bool   `json:"show_name"`
	CustomName    string `json:"custom_name"` // replaces the detected name when set
	ShowUsage     bool   `json:"show_usage"`
	ShowVRAM      bool   `json:"show_vram"`
	ShowVRAMTotal bool   `json:"show_vram_total"`
	ShowTemp      bool   `json:"show_temp"` // NVIDIA only, through the driver's nvml.dll
	Fahrenheit    bool   `json:"fahrenheit"`
}

// NetworkOptions controls the network traffic line.
type NetworkOptions struct {
	Label         string `json:"label"`
	Adapter       string `json:"adapter"` // adapter alias; empty follows the busiest one
	ShowDown      bool   `json:"show_down"`
	ShowUp        bool   `json:"show_up"`
	ShowPeakDown  bool   `json:"show_peak_down"`
	ShowPeakUp    bool   `json:"show_peak_up"`
	ShowTotalDown bool   `json:"show_total_down"`
	ShowTotalUp   bool   `json:"show_total_up"`
	ShowUtil      bool   `json:"show_util"`
	UtilVsPeak    bool   `json:"util_vs_peak"` // against the fastest rate seen, not the link speed
	Bytes         bool   `json:"bytes"`        // MB/s instead of Mbps
	SmallLabels   bool   `json:"small_labels"` // raised units and labels, fewer characters
}

// LogsOptions controls the VRChat log line.
type LogsOptions struct {
	Label          string `json:"label"`
	ShowJoin       bool   `json:"show_join"`
	ShowLeave      bool   `json:"show_leave"`
	ShowScreenshot bool   `json:"show_screenshot"`
	ShowLoaded     bool   `json:"show_loaded"`     // "Loaded <world> in 12s"
	MarkSeenAgain  bool   `json:"mark_seen_again"` // "<name> joined again" for players met earlier
	HideAfter      int    `json:"hide_after"`      // seconds an event stays; 0 keeps it until the next
	MaxChars       int    `json:"max_chars"`
}

// WorldOptions controls the VRChat world and instance line.
type WorldOptions struct {
	Label      string `json:"label"`
	ShowName   bool   `json:"show_name"`
	ShowCount  bool   `json:"show_count"`
	ShowPeak   bool   `json:"show_peak"`
	ShowType   bool   `json:"show_type"`
	ShowRegion bool   `json:"show_region"`
	ShowTime   bool   `json:"show_time"`  // time in this instance
	ShowStats  bool   `json:"show_stats"` // worlds visited and players met this session
	MaxChars   int    `json:"max_chars"`  // limit for the world name; 0 means none
}

// AppOptions controls the window, tray and global shortcut.
type AppOptions struct {
	Tray           bool   `json:"tray"`             // icon in the notification area
	StartMinimized bool   `json:"start_minimized"`  // to the tray when it is on, else the taskbar
	CloseToTray    bool   `json:"close_to_tray"`    // the X button hides the window
	MinimizeToTray bool   `json:"minimize_to_tray"` // minimising hides the window
	MicHotkey      string `json:"mic_hotkey"`       // global shortcut that toggles the VRChat mic; empty is off
}

// BatteryOptions controls the SteamVR tracker battery line.
type BatteryOptions struct {
	Label           string          `json:"label"`
	Template        string          `json:"template"` // per device; see batteryEntry
	Separator       string          `json:"separator"`
	ShowHeadset     bool            `json:"show_headset"`
	ShowControllers bool            `json:"show_controllers"`
	ShowTrackers    bool            `json:"show_trackers"`
	HeadsetIcon     string          `json:"headset_icon"`
	ControllerIcon  string          `json:"controller_icon"`
	TrackerIcon     string          `json:"tracker_icon"`
	ChargingMark    string          `json:"charging_mark"` // {charging}; added at the end when the template has none
	LowThreshold    int             `json:"low_threshold"` // percent at or below which a device is low
	LowTag          string          `json:"low_tag"`       // {low}
	OnlyLow         bool            `json:"only_low"`      // leave out devices that are not low
	OnlyLowest      bool            `json:"only_lowest"`   // show just the emptiest device
	Sort            string          `json:"sort"`          // detected, name, low, high, type
	MaxDevices      int             `json:"max_devices"`   // 0 means all
	Devices         []BatteryDevice `json:"devices"`       // remembered by serial
}

// BatteryDevice is a device Fern has seen, so a name or hide choice sticks.
type BatteryDevice struct {
	Serial string `json:"serial"`
	Name   string `json:"name"`   // the user's name; empty uses the detected one
	Kind   string `json:"kind"`   // headset, left, right, controller or tracker, as last seen
	Model  string `json:"model"`  // as SteamVR reports it
	Hidden bool   `json:"hidden"` // never shown on the line
}

// Ways the battery line can be sorted.
const (
	BatterySortDetected = "detected"
	BatterySortName     = "name"
	BatterySortLow      = "low"
	BatterySortHigh     = "high"
	BatterySortType     = "type"
)

// VRPerfOptions controls the SteamVR performance line.
type VRPerfOptions struct {
	Label            string `json:"label"`
	ShowFPS          bool   `json:"show_fps"`
	ShowHz           bool   `json:"show_hz"`
	ShowReprojection bool   `json:"show_reprojection"`
	ShowDropped      bool   `json:"show_dropped"`
	ShowGPU          bool   `json:"show_gpu"`
	ShowCPU          bool   `json:"show_cpu"`
	ShowBudget       bool   `json:"show_budget"`
	ShowSmoothing    bool   `json:"show_smoothing"`
	SmallUnits       bool   `json:"small_units"` // superscript labels, e.g. 90ᶠᵖˢ
	Separator        string `json:"separator"`
	OnlyWhenBad      bool   `json:"only_when_bad"`
	BadReprojection  int    `json:"bad_reprojection"`    // percent of frames reprojected; 0 ignores it
	BadDroppedPerMin int    `json:"bad_dropped_per_min"` // dropped frames in the last minute; 0 ignores it
	BadFPSPercent    int    `json:"bad_fps_percent"`     // fps below this share of the refresh rate; 0 ignores it
	ClearSeconds     int    `json:"clear_seconds"`       // how long it must run well before it counts as fine again
}

// HeartOptions controls the heart rate line and the avatar parameters it
// can drive. The credentials are stored as typed; the panel masks them and
// Fern never logs them or puts them in a status message.
type HeartOptions struct {
	Source       string `json:"source"`        // pulsoid, hyperate or custom
	PulsoidToken string `json:"pulsoid_token"` // from pulsoid.net/ui/keys
	HypeRateKey  string `json:"hyperate_key"`  // HypeRate websocket API key
	HypeRateID   string `json:"hyperate_id"`   // the session/device ID shown in the HypeRate app
	CustomURL    string `json:"custom_url"`    // ws(s):// for a socket, http(s):// to poll
	CustomPath   string `json:"custom_path"`   // JSON field, e.g. data.heart_rate; empty guesses
	PollMS       int    `json:"poll_ms"`       // how often an HTTP source is read

	Label      string `json:"label"`
	ShowIcon   bool   `json:"show_icon"`
	Icon       string `json:"icon"`       // normal zone
	ZoneIcons  bool   `json:"zone_icons"` // swap the icon below/above the thresholds
	IconLow    string `json:"icon_low"`
	IconHigh   string `json:"icon_high"`
	ShowSuffix bool   `json:"show_suffix"`
	Suffix     string `json:"suffix"`

	ShowTrend        bool    `json:"show_trend"`
	TrendSec         int     `json:"trend_sec"`         // length of each compared window
	TrendSensitivity float64 `json:"trend_sensitivity"` // bpm change that counts as a trend

	SmoothSec int `json:"smooth_sec"` // moving average window; 0 shows the raw value
	Offset    int `json:"offset"`     // added to every reading

	ShowZoneText bool   `json:"show_zone_text"`
	LowBPM       int    `json:"low_bpm"`
	LowText      string `json:"low_text"`
	HighBPM      int    `json:"high_bpm"`
	HighText     string `json:"high_text"`

	OfflineSec  int    `json:"offline_sec"`  // no reading for this long counts as offline
	OfflineText string `json:"offline_text"` // shown while offline; empty hides the line

	ShowStats bool `json:"show_stats"` // session min / avg / max

	SendOSC        bool   `json:"send_osc"`
	ParamInt       string `json:"param_int"`       // int 0-255; empty skips it
	ParamFloat     string `json:"param_float"`     // float, see FloatRange; empty skips it
	FloatRange     string `json:"float_range"`     // signed (-1..1) or unsigned (0..1), both over 0-255 bpm
	ParamBeat      string `json:"param_beat"`      // bool once per beat; empty skips it
	BeatMode       string `json:"beat_mode"`       // toggle (flip each beat) or pulse (true briefly)
	ParamConnected string `json:"param_connected"` // bool, true while readings arrive; empty skips it
}

// Heart rate sources.
const (
	HeartPulsoid  = "pulsoid"
	HeartHypeRate = "hyperate"
	HeartCustom   = "custom"
)

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

// WeatherOptions controls the weather line, from Open-Meteo.
type WeatherOptions struct {
	City          string `json:"city"` // a place name, or "lat, lon"
	Label         string `json:"label"`
	ShowCondition bool   `json:"show_condition"`
	ShowEmoji     bool   `json:"show_emoji"`
	ShowTemp      bool   `json:"show_temp"`
	ShowFeels     bool   `json:"show_feels_like"`
	ShowHumidity  bool   `json:"show_humidity"`
	ShowWind      bool   `json:"show_wind"`
	TempUnit      string `json:"temp_unit"`    // c or f
	WindUnit      string `json:"wind_unit"`    // kmh, mph or ms
	Decimals      int    `json:"decimals"`     // 0 or 1
	IntervalMin   int    `json:"interval_min"` // minutes between refreshes, 10-60
	InTime        bool   `json:"in_time"`      // show on the time line instead
}

// DiscordOptions controls Rich Presence and the voice channel line. Both talk
// to the Discord client through the user's own Discord application, whose
// client secret lives in secrets.dat, not here.
type DiscordOptions struct {
	ClientID    string `json:"client_id"`    // the Discord application ID
	RedirectURI string `json:"redirect_uri"` // one of the app's OAuth2 redirects
	// Rich Presence
	Presence     bool   `json:"presence"`
	OnlyInVRChat bool   `json:"only_in_vrchat"`
	Details      string `json:"details"`
	State        string `json:"state"`
	LargeImage   string `json:"large_image"` // art asset key or https image URL
	LargeText    string `json:"large_text"`
	ShowElapsed  bool   `json:"show_elapsed"`
	JoinButton   bool   `json:"join_button"` // public instances only
	JoinLabel    string `json:"join_label"`
	ButtonLabel  string `json:"button_label"`
	ButtonURL    string `json:"button_url"`
	// Voice line
	Template    string `json:"template"`
	QuietText   string `json:"quiet_text"`
	NotInVCText string `json:"not_in_vc_text"` // empty hides the line outside a call
	MaxSpeakers int    `json:"max_speakers"`
	HideSelf    bool   `json:"hide_self"`
	CountOnly   bool   `json:"count_only"`
	DebounceMS  int    `json:"debounce_ms"`
	ShowMute    bool   `json:"show_mute"`
	MuteText    string `json:"mute_text"`
	DeafText    string `json:"deaf_text"`
}

// TwitchOptions controls the stream status line.
type TwitchOptions struct {
	ClientID      string `json:"client_id"`
	Channel       string `json:"channel"` // empty means the signed-in account
	IntervalSec   int    `json:"interval_sec"`
	ShowLive      bool   `json:"show_live"`
	LiveText      string `json:"live_text"`
	OfflineText   string `json:"offline_text"` // empty hides the line while offline
	ShowViewers   bool   `json:"show_viewers"`
	ViewerLabel   string `json:"viewer_label"`
	ShowGame      bool   `json:"show_game"`
	GamePrefix    string `json:"game_prefix"`
	ShowTitle     bool   `json:"show_title"`
	ShowFollowers bool   `json:"show_followers"`
	FollowerLabel string `json:"follower_label"`
	Compact       bool   `json:"compact"` // 1.2k instead of 1234
	Separator     string `json:"separator"`
	Template      string `json:"template"` // replaces the toggles when set
}

// SoundpadOptions controls the last-played Soundpad sound line.
type SoundpadOptions struct {
	Icon        string `json:"icon"`
	DurationSec int    `json:"duration_sec"`
}

// VoicemodOptions controls the Voicemod connection and its line.
type VoicemodOptions struct {
	Enabled        bool   `json:"enabled"` // connect, for the panel soundboard
	AnnounceSounds bool   `json:"announce_sounds"`
	SoundSec       int    `json:"sound_sec"`
	AnnounceVoice  bool   `json:"announce_voice"`
	SoundIcon      string `json:"sound_icon"`
	VoiceIcon      string `json:"voice_icon"`
}

// OSCOptions is where chatbox messages are sent.
type OSCOptions struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// Extra are more places that get the same chatbox, e.g. a second VRChat
	// or an OSC router. Always ExtraOutputs entries.
	Extra []OSCTarget `json:"extra"`
	// OSCQuery listens for VRChat on a free port and advertises it over
	// OSCQuery, leaving 9001 to other apps. Off, or if it fails, Fern
	// listens on 9001.
	OSCQuery bool `json:"oscquery"`
	// Forward passes received OSC on to other apps; see oscrouter.go.
	Forward []OSCForward `json:"forward"`
}

// OSCTarget is one more OSC destination.
type OSCTarget struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

// ExtraOutputs is how many OSC destinations can be added to the main one.
const ExtraOutputs = 2

func defaultExtraOutput(i int) OSCTarget {
	return OSCTarget{Host: "127.0.0.1", Port: 9002 + i}
}

// ChatOptions controls messages typed on the Live page and how sends sound.
type ChatOptions struct {
	NotifySend bool `json:"notify_send"` // play VRChat's sound for "Send now" messages
	NotifyAuto bool `json:"notify_auto"` // play it when the automatic chatbox changes
	// LiveTyping sends the message box as you type, at most every LiveRateMS.
	LiveTyping bool `json:"live_typing"`
	LiveRateMS int  `json:"live_rate_ms"`
	// TypingIndicator shows VRChat's typing bubble while composing.
	TypingIndicator bool `json:"typing_indicator"`
	// History holds the last messages sent from the Live page, newest first,
	// for resending and autocomplete. Empty and not kept while KeepHistory is off.
	History      []string `json:"history"`
	KeepHistory  bool     `json:"keep_history"`
	Autocomplete bool     `json:"autocomplete"` // suggest whole earlier messages while typing
	// EmojiRecent and EmojiFavorites are the emoji picker's lists, newest
	// first; see cleanEmojiList.
	EmojiRecent    []string `json:"emoji_recent"`
	EmojiFavorites []string `json:"emoji_favorites"`
}

// Config is the persisted application configuration.
type Config struct {
	DisplayOptions DisplayOptions `json:"display_options"`
	TextMessages   []TextMessage  `json:"TextMessages"`
	WindowOptions  WindowOptions  `json:"WindowOptions"`
	TextOptions    TextOptions    `json:"TextOptions"`
	LoadingOptions LoadingOptions `json:"LoadingOptions"`
	AfkOptions     AfkOptions     `json:"AfkOptions"`
	MediaOptions   MediaOptions   `json:"MediaOptions"`
	TimeOptions    TimeOptions    `json:"TimeOptions"`
	MemoryOptions  MemoryOptions  `json:"MemoryOptions"`
	CPUOptions     CPUOptions     `json:"CPUOptions"`
	LogsOptions    LogsOptions    `json:"LogsOptions"`
	BatteryOptions BatteryOptions `json:"BatteryOptions"`
	VRPerfOptions  VRPerfOptions  `json:"VRPerfOptions"`
	HeartRate      HeartOptions   `json:"HeartRateOptions"`
	LyricsOptions  LyricsOptions  `json:"LyricsOptions"`
	WeatherOptions WeatherOptions `json:"WeatherOptions"`
	OSCOptions     OSCOptions     `json:"OSC"`
	ChatOptions    ChatOptions    `json:"ChatOptions"`
	NetworkOptions NetworkOptions `json:"NetworkOptions"`
	GPUOptions     GPUOptions     `json:"GPUOptions"`
	WorldOptions   WorldOptions   `json:"WorldOptions"`
	AppOptions     AppOptions     `json:"AppOptions"`
	TimeoutMS      int64          `json:"timeout_ms"`
	AssistOptions  AssistOptions  `json:"AssistOptions"`
	TTSOptions     TTSOptions     `json:"TTSOptions"`
	STTOptions     STTOptions     `json:"STTOptions"`

	// App integrations
	DiscordOptions  DiscordOptions  `json:"DiscordOptions"`
	TwitchOptions   TwitchOptions   `json:"TwitchOptions"`
	SoundpadOptions SoundpadOptions `json:"SoundpadOptions"`
	VoicemodOptions VoicemodOptions `json:"VoicemodOptions"`

	// Rules and do not disturb; see rules.go and dnd.go.
	Rules RulesOptions `json:"Rules"`
	DND   DNDOptions   `json:"DND"`
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
			Battery:      false,
			VRPerf:       false,
			Order:        append([]string{}, DefaultOrder...),
			Separator:    "\n",
			MaxChars:     144,
			MaxLines:     9,
			DropOrder:    append([]string{}, DefaultDropOrder...),
			Visibility:   map[string]string{},
		},
		TextMessages: Messages("♡ hi there", "♡ have a good day", "♡ be kind"),
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
			Icon:       "💤",
			ShowTimer:  true,
			TimerStyle: "full",

			UseMovement: true,
			UseVoice:    true,
			UseOSC:      true,
			UseWindow:   true,
			InputMode:   AfkInputAlways,
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
			Label:          "",
			ShowJoin:       true,
			ShowLeave:      true,
			ShowScreenshot: true,
			ShowLoaded:     true,
			MarkSeenAgain:  true,
			MaxChars:       80,
		},
		WorldOptions: WorldOptions{
			ShowName:   true,
			ShowCount:  true,
			ShowType:   true,
			ShowRegion: true,
			MaxChars:   40,
		},
		GPUOptions: GPUOptions{
			Label:         "ᵍᵖᵘ",
			ShowName:      true,
			ShowUsage:     true,
			ShowVRAM:      true,
			ShowVRAMTotal: true,
		},
		NetworkOptions: NetworkOptions{
			ShowDown:    true,
			ShowUp:      true,
			ShowUtil:    true,
			SmallLabels: true,
		},
		BatteryOptions: BatteryOptions{
			Template:        "{icon} {name} {batt}%{charging} {low}",
			Separator:       " | ",
			ShowHeadset:     true,
			ShowControllers: true,
			ShowTrackers:    true,
			HeadsetIcon:     "🥽",
			ControllerIcon:  "🎮",
			TrackerIcon:     "📍",
			ChargingMark:    "⚡",
			LowThreshold:    20,
			LowTag:          "LOW",
			Sort:            BatterySortDetected,
		},
		VRPerfOptions: VRPerfOptions{
			ShowFPS:          true,
			ShowReprojection: true,
			SmallUnits:       true,
			Separator:        " ¦ ",
			BadReprojection:  10,
			BadDroppedPerMin: 5,
			BadFPSPercent:    90,
			ClearSeconds:     5,
		},
		HeartRate: HeartOptions{
			Source:           HeartPulsoid,
			PollMS:           1000,
			ShowIcon:         true,
			Icon:             "❤️",
			ZoneIcons:        true,
			IconLow:          "💙",
			IconHigh:         "💖",
			ShowSuffix:       true,
			Suffix:           "bpm",
			ShowTrend:        true,
			TrendSec:         10,
			TrendSensitivity: 3,
			SmoothSec:        4,
			LowBPM:           60,
			LowText:          "sleepy",
			HighBPM:          100,
			HighText:         "hot",
			OfflineSec:       30,
			ParamInt:         "HR",
			ParamFloat:       "HeartRateFloat",
			FloatRange:       "signed",
			ParamBeat:        "HeartBeatToggle",
			BeatMode:         "toggle",
			ParamConnected:   "isHRConnected",
		},
		LyricsOptions: LyricsOptions{
			HoldSeconds: 6,
			GapSeconds:  8,
			GapMarker:   "♪ ♪ ♪",
			Online:      true,
			MaxChars:    90,
		},
		WeatherOptions: WeatherOptions{
			ShowCondition: true,
			ShowEmoji:     true,
			ShowTemp:      true,
			TempUnit:      "c",
			WindUnit:      "kmh",
			IntervalMin:   15,
		},
		DiscordOptions: DiscordOptions{
			RedirectURI:  "http://localhost",
			OnlyInVRChat: true,
			Details:      "In {world}",
			State:        "{type} • {count} players",
			LargeText:    "VRChat",
			ShowElapsed:  true,
			JoinLabel:    "Join World",
			Template:     "🔊 {channel} ({count}) | 🎙️ {speaking}",
			QuietText:    "Quiet...",
			MaxSpeakers:  2,
			DebounceMS:   500,
			ShowMute:     true,
			MuteText:     "ᵐᵘᵗᵉᵈ",
			DeafText:     "ᵈᵉᵃᶠᵉⁿ",
		},
		TwitchOptions: TwitchOptions{
			IntervalSec:   60,
			ShowLive:      true,
			LiveText:      "🔴 LIVE",
			ShowViewers:   true,
			ViewerLabel:   "viewers",
			ShowGame:      true,
			GamePrefix:    "playing",
			FollowerLabel: "followers",
			Compact:       true,
			Separator:     " | ",
		},
		SoundpadOptions: SoundpadOptions{
			Icon:        "🎶",
			DurationSec: 8,
		},
		VoicemodOptions: VoicemodOptions{
			AnnounceSounds: true,
			SoundSec:       8,
			AnnounceVoice:  true,
			SoundIcon:      "🎶",
			VoiceIcon:      "🎙️",
		},
		OSCOptions: OSCOptions{
			Host:  "127.0.0.1",
			Port:  9000,
			Extra: []OSCTarget{defaultExtraOutput(0), defaultExtraOutput(1)},

			OSCQuery: true,
			Forward:  []OSCForward{},
		},
		ChatOptions: ChatOptions{
			NotifySend:      true,
			LiveRateMS:      1500,
			TypingIndicator: true,
			History:         []string{},
			KeepHistory:     true,
			Autocomplete:    true,
			EmojiRecent:     []string{},
			EmojiFavorites:  []string{},
		},
		TimeoutMS:     2500,
		AssistOptions: defaultAssistOptions(),
		TTSOptions:    defaultTTSOptions(),
		STTOptions:    defaultSTTOptions(),
		Rules:         defaultRulesOptions(),
		DND:           defaultDNDOptions(),
	}
}

func (c Config) clone() Config {
	out := c
	out.DisplayOptions.Order = append([]string{}, c.DisplayOptions.Order...)
	out.TextMessages = append([]TextMessage{}, c.TextMessages...)
	out.TextOptions.Groups = append([]string{}, c.TextOptions.Groups...)
	out.TextOptions.Icons = append([]string{}, c.TextOptions.Icons...)
	out.DisplayOptions.DropOrder = append([]string{}, c.DisplayOptions.DropOrder...)
	out.DisplayOptions.Visibility = make(map[string]string, len(c.DisplayOptions.Visibility))
	for id, v := range c.DisplayOptions.Visibility {
		out.DisplayOptions.Visibility[id] = v
	}
	out.OSCOptions.Extra = append([]OSCTarget{}, c.OSCOptions.Extra...)
	out.OSCOptions.Forward = append([]OSCForward{}, c.OSCOptions.Forward...)
	out.ChatOptions.History = append([]string{}, c.ChatOptions.History...)
	out.ChatOptions.EmojiRecent = append([]string{}, c.ChatOptions.EmojiRecent...)
	out.ChatOptions.EmojiFavorites = append([]string{}, c.ChatOptions.EmojiFavorites...)
	out.WindowOptions.Rules = append([]WindowRule{}, c.WindowOptions.Rules...)
	out.WindowOptions.HideTitles = append([]string{}, c.WindowOptions.HideTitles...)
	out.BatteryOptions.Devices = append([]BatteryDevice{}, c.BatteryOptions.Devices...)
	out.MediaOptions.BarPresets = append([]BarPreset{}, c.MediaOptions.BarPresets...)
	out.TimeOptions.Counters = append([]TimeCounter{}, c.TimeOptions.Counters...)
	out.Rules = c.Rules.clone()
	out.DND = c.DND.clone()
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
	if dir := DataDir(); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	return "config.json"
}

// DataDir is Fern's per-user folder, %LOCALAPPDATA%\Fern, created on first
// use. It returns "" when no such folder can be made.
func DataDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, "Fern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
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
	in.keepSecrets(cfg)
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
	case ModuleBattery:
		return &d.Battery
	case ModuleVRPerf:
		return &d.VRPerf
	case ModuleHeart:
		return &d.HeartRate
	case ModuleLyrics:
		return &d.Lyrics
	case ModuleWeather:
		return &d.Weather
	case ModuleDiscord:
		return &d.Discord
	case ModuleTwitch:
		return &d.Twitch
	case ModuleSoundpad:
		return &d.Soundpad
	case ModuleVoicemod:
		return &d.Voicemod
	case ModuleNetwork:
		return &d.Network
	case ModuleGPU:
		return &d.GPU
	case ModuleWorld:
		return &d.World
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

// knownIDs keeps the known module ids of list once each, then appends the
// ones it is missing in the order of fallback.
func knownIDs(list, fallback []string) []string {
	seen := make(map[string]bool, len(knownModules))
	out := make([]string, 0, len(knownModules))
	for _, id := range append(append([]string{}, list...), fallback...) {
		if knownModules[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range DefaultOrder {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// affixLine removes line breaks and shortens s to n runes. Spaces are kept,
// since a prefix like "» " needs its space.
func affixLine(s string, n int) string {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	return RuneLimit(s, n)
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
	c.DisplayOptions.DropOrder = knownIDs(c.DisplayOptions.DropOrder, DefaultDropOrder)
	visibility := make(map[string]string, len(c.DisplayOptions.Visibility))
	for id, v := range c.DisplayOptions.Visibility {
		if knownModules[id] && (v == ShowVR || v == ShowDesktop) {
			visibility[id] = v
		}
	}
	c.DisplayOptions.Visibility = visibility
	c.DisplayOptions.LinePrefix = affixLine(c.DisplayOptions.LinePrefix, 16)
	c.DisplayOptions.LineSuffix = affixLine(c.DisplayOptions.LineSuffix, 16)

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
	c.CPUOptions.CustomName = RuneLimit(strings.TrimSpace(c.CPUOptions.CustomName), 40)
	c.GPUOptions.Label = RuneLimit(strings.TrimSpace(c.GPUOptions.Label), 16)
	c.GPUOptions.CustomName = RuneLimit(strings.TrimSpace(c.GPUOptions.CustomName), 40)
	c.GPUOptions.Adapter = RuneLimit(strings.TrimSpace(c.GPUOptions.Adapter), 128)
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
	sanitizeWeather(&c.WeatherOptions)
	c.LogsOptions.HideAfter = clampInt(c.LogsOptions.HideAfter, 0, 3600)
	c.WorldOptions.Label = RuneLimit(strings.TrimSpace(c.WorldOptions.Label), 16)
	c.WorldOptions.MaxChars = clampInt(c.WorldOptions.MaxChars, 0, 144)
	if c.AppOptions.MicHotkey != "" {
		if _, _, canonical, err := ParseHotkey(c.AppOptions.MicHotkey); err == nil {
			c.AppOptions.MicHotkey = canonical
		} else {
			c.AppOptions.MicHotkey = ""
		}
	}
	c.TextOptions.Label = RuneLimit(strings.TrimSpace(c.TextOptions.Label), 16)
	c.NetworkOptions.Label = RuneLimit(strings.TrimSpace(c.NetworkOptions.Label), 16)
	c.NetworkOptions.Adapter = RuneLimit(strings.TrimSpace(c.NetworkOptions.Adapter), 256)
	if c.LoadingOptions.Dots == 0 {
		c.LoadingOptions.Dots = 6
	}
	c.LoadingOptions.Dots = clampInt(c.LoadingOptions.Dots, 1, 6)
	switch c.AfkOptions.TimerStyle {
	case "full", "short", "minutes", "compact", "words":
	default:
		c.AfkOptions.TimerStyle = "full"
	}
	c.AfkOptions.sanitize()

	c.sanitizeSteamVR()
	c.HeartRate.sanitize()
	c.sanitizeAssist()

	c.sanitizeSocial()

	// VRChat drops chatbox messages sent faster than about one a second.
	c.ChatOptions.LiveRateMS = clampInt(c.ChatOptions.LiveRateMS, 1000, 3000)
	c.ChatOptions.History = cleanChatHistory(c.ChatOptions.History, c.ChatOptions.KeepHistory)
	c.ChatOptions.EmojiRecent = cleanEmojiList(c.ChatOptions.EmojiRecent, EmojiRecentLimit)
	c.ChatOptions.EmojiFavorites = cleanEmojiList(c.ChatOptions.EmojiFavorites, EmojiFavoriteLimit)

	if c.OSCOptions.Host == "" {
		c.OSCOptions.Host = "127.0.0.1"
	}
	if c.OSCOptions.Port < 1 || c.OSCOptions.Port > 65535 {
		c.OSCOptions.Port = 9000
	}
	extra := make([]OSCTarget, ExtraOutputs)
	for i := range extra {
		extra[i] = defaultExtraOutput(i)
		if i < len(c.OSCOptions.Extra) {
			t := c.OSCOptions.Extra[i]
			extra[i].Enabled = t.Enabled
			if host := RuneLimit(strings.TrimSpace(t.Host), 253); host != "" {
				extra[i].Host = host
			}
			if t.Port >= 1 && t.Port <= 65535 {
				extra[i].Port = t.Port
			}
		}
	}
	c.OSCOptions.Extra = extra
	c.OSCOptions.sanitizeForward()

	sanitizeTexts(c)
	c.Rules.sanitize()
	c.DND.sanitize()
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
	if err := os.Rename(tmp, cfgPath); err != nil {
		return err
	}
	captureActiveProfile()
	return nil
}
