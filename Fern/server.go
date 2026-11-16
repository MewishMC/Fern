package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"Fern/UI"
	"Fern/modules"

	"github.com/gorilla/websocket"
)

// upgrader keeps gorilla's default origin check: only the page Fern serves
// may open the socket, so other websites in a browser cannot drive the panel.
var upgrader = websocket.Upgrader{}

// assetStamp is a short hash of the bundled stylesheets, appended to their URLs
// by the page. WebView2 happily reuses a cached stylesheet for a whole day
// without asking the server, so a rebuild used to keep drawing the old layout
// until the cache expired. A new URL per stylesheet change cannot be stale.
var assetStamp = func() string {
	hash := sha1.New()
	if assets := UI.Assets(); assets != nil {
		for _, name := range []string{"theme.css", "fonts/noto-emoji.css", "font.js"} {
			if data, err := fs.ReadFile(assets, name); err == nil {
				_, _ = hash.Write(data)
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:6])
}()

// uiState is the single payload pushed to the interface.
type uiState struct {
	Preview   string                `json:"preview"`
	Fit       modules.Chatbox       `json:"fit"`
	Chars     int                   `json:"chars"`
	Lines     int                   `json:"lines"`
	MaxChars  int                   `json:"maxChars"`
	MaxLines  int                   `json:"maxLines"`
	Modules   map[string]bool       `json:"modules"`
	Samples   map[string]string     `json:"samples"`
	Order     []string              `json:"order"`
	Config    modules.Config        `json:"config"`
	Media     modules.MediaInfo     `json:"media"`
	Sources   []modules.MediaSource `json:"mediaSources"`
	Apps      []modules.WindowApp   `json:"windowApps"`
	InVR      bool                  `json:"inVR"`
	Afk       afkState              `json:"afk"`
	Perf      perfState             `json:"perf"`
	Logs      logsState             `json:"logs"`
	History   []modules.LogEvent    `json:"history"`
	Hold      holdState             `json:"hold"`
	Stats     statsState            `json:"stats"`
	VRChat    modules.VRChatStatus  `json:"vrchat"`
	Players   int                   `json:"players"`
	TextIndex int                   `json:"textIndex"`
	OSC       string                `json:"osc"`
	Version   string                `json:"version"`
	Update    updateState           `json:"update"`
	SteamVR   modules.SteamVRStatus `json:"steamvr"`
	HeartRate heartState            `json:"heartRate"`
	Assist    assistState           `json:"assist"`
	Lyrics    modules.LyricsStatus  `json:"lyrics"`
	Weather   modules.WeatherStatus `json:"weather"`

	Social  modules.SocialInfo `json:"social"`
	Network networkState       `json:"network"`
	GPUs    []modules.GPUInfo  `json:"gpus"`
	App     appState           `json:"app"`
	OSCIn   oscInState         `json:"oscIn"`

	Profiles profilesState `json:"profiles"`
	Backups  backupsState  `json:"backups"`
	Rules    rulesState    `json:"rules"`

	Calendar modules.CalendarStatus `json:"calendar"`
	Prices   modules.PricesStatus   `json:"prices"`
	Steam    modules.SteamStatus    `json:"steam"`
}

type appState struct {
	HotkeyErr string       `json:"hotkeyErr"`
	SteamVR   steamVRState `json:"steamvr"`
}

type networkState struct {
	Adapters []modules.NetAdapter `json:"adapters"`
	Busiest  string               `json:"busiest"`
}

// assistState is the Assist page: AI tools, text to speech and speech to
// text. Keys appear only as masked hints.
type assistState struct {
	AI   modules.AIStatus  `json:"ai"`
	TTS  modules.TTSStatus `json:"tts"`
	STT  modules.STTStatus `json:"stt"`
	Keys map[string]string `json:"keys"`
}

type heartState = modules.HeartRateStatus

type afkState struct {
	Active  bool  `json:"active"`
	IdleMS  int64 `json:"idleMs"`
	LimitMS int64 `json:"limitMs"`
	Talking bool  `json:"talking"`
	Manual  bool  `json:"manual"`
	// Style is the AFK preset the settings match, or "" for a custom look.
	Style   string                   `json:"style"`
	Styles  []modules.AfkSample      `json:"styles"`
	Sources []modules.AfkSourceState `json:"sources"`
}

type perfState struct {
	CPU        float64 `json:"cpu"`
	MemUsed    float64 `json:"memUsed"`
	MemTotal   float64 `json:"memTotal"`
	MemPercent float64 `json:"memPercent"`
}

type logsState struct {
	Join     []modules.LogEvent   `json:"join"`
	Leave    []modules.LogEvent   `json:"leave"`
	Activity []modules.LogEvent   `json:"activity"`
	World    *modules.WorldInfo   `json:"world"` // null when not in an instance
	Session  modules.SessionStats `json:"session"`
}

type holdState struct {
	Text   string `json:"text"`
	Until  int64  `json:"until"`
	Pinned bool   `json:"pinned"`
}

type statsState struct {
	UptimeSec  int64 `json:"uptimeSec"`
	Sent       int64 `json:"sent"`
	LastSentMS int64 `json:"lastSentMs"`
	RateMS     int64 `json:"rateMs"`
}

type command struct {
	Type    string          `json:"type"`
	Module  string          `json:"module"`
	Order   []string        `json:"order"`
	Config  json.RawMessage `json:"config"`
	Text    string          `json:"text"`
	Hold    int             `json:"hold"`
	Action  string          `json:"action"`
	Enabled bool            `json:"enabled"`
}

type clientConn struct {
	conn *websocket.Conn
	out  chan []byte
}

var (
	clientsMu sync.Mutex
	clients   = map[*clientConn]bool{}
)

// openListener picks a loopback port, reusing Fern's usual range first.
func openListener() (int, net.Listener, error) {
	for port := 1932; port < 1999; port++ {
		if listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			return port, listener, nil
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, err
	}
	return listener.Addr().(*net.TCPAddr).Port, listener, nil
}

// StartServer serves the interface and the state socket.
func StartServer(listener net.Listener) {
	// WebView2 wants a font type for the bundled icon font, and Go ships no
	// .woff/.woff2 entry of its own.
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".woff2", "font/woff2")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(strings.ReplaceAll(UI.MainPage, "{{ASSET_STAMP}}", assetStamp)))
	})
	mux.HandleFunc("/css-editor", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(strings.ReplaceAll(UI.CSSEditorPage, "{{ASSET_STAMP}}", assetStamp)))
	})
	mux.HandleFunc("/media/cover", func(w http.ResponseWriter, r *http.Request) {
		cover := modules.CurrentCover()
		if len(cover.Data) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", cover.ContentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(cover.Data)
	})
	mux.HandleFunc("/fonts/system", handleSystemFonts)
	mux.HandleFunc("/fonts/custom", handleCustomFont)
	mux.HandleFunc("/textstyle.js", serveTextStyleJS)
	mux.HandleFunc("/ws", handleSocket)
	mux.HandleFunc("/backup/export", handleBackupExport)
	mux.HandleFunc("/profiles/appearance", handleProfileAppearance)
	if assets := UI.Assets(); assets != nil {
		mux.Handle("/assets/", http.StripPrefix("/assets/", freshAssets(http.FileServer(http.FS(assets)))))
	}

	go broadcastLoop()

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Println("server stopped:", err)
	}
}

// freshAssets keeps WebView2 from holding on to a stylesheet from an older
// build. The files are embedded in the binary, so re-reading them costs nothing,
// while a cached copy made every rebuild look broken until the cache expired.
func freshAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func handleSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &clientConn{conn: conn, out: make(chan []byte, 4)}

	clientsMu.Lock()
	clients[client] = true
	clientsMu.Unlock()

	go client.writeLoop()

	if payload, err := json.Marshal(buildState()); err == nil {
		select {
		case client.out <- payload:
		default:
		}
	}

	client.readLoop()
}

func (c *clientConn) writeLoop() {
	defer func() {
		_ = c.conn.Close()
		clientsMu.Lock()
		delete(clients, c)
		clientsMu.Unlock()
	}()
	for payload := range c.out {
		_ = c.conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return
		}
	}
}

func (c *clientConn) readLoop() {
	defer func() {
		_ = c.conn.Close()
	}()

	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var cmd command
		if err := json.Unmarshal(data, &cmd); err != nil {
			continue
		}
		handleCommand(cmd)
	}
}

func handleCommand(cmd command) {
	switch cmd.Type {
	case "toggle":
		modules.ToggleModule(cmd.Module)
	case "master":
		modules.UpdateConfig(func(c *modules.Config) { c.DisplayOptions.Show = cmd.Enabled })
	case "order":
		modules.SetOrder(cmd.Order)
	case "config":
		var next modules.Config
		if err := json.Unmarshal(cmd.Config, &next); err == nil {
			// The panel's copy of the message history can be a send behind;
			// Fern's own copy is the one that counts.
			cur := modules.GetConfig()
			next.ChatOptions.History = cur.ChatOptions.History
			// Rules and the do not disturb switch change through their own
			// actions, and from rules and shortcuts, so a config push never
			// carries them back.
			next.Rules = cur.Rules
			next.DND.On = cur.DND.On
			modules.SetConfig(next)
		}
	case "send":
		hold := time.Duration(cmd.Hold) * time.Second
		modules.SetHold(cmd.Text, hold)
		modules.SpeakSent(cmd.Text)
		modules.AddChatHistory(cmd.Text)
	case "clearChatHistory":
		modules.ClearChatHistory()
	case "live":
		modules.SetLive(cmd.Text, time.Duration(cmd.Hold)*time.Second)
	case "typing":
		go sendTyping(cmd.Enabled)
	case "clearHold":
		modules.ClearHold()
	case "media":
		go func() { _ = modules.MediaControl(cmd.Action) }()
	case "skipText":
		modules.SkipMessage()
	case "afkStyle":
		modules.ApplyAfkPreset(cmd.Text)
	case "reset":
		modules.ResetConfig()
	case "save":
		_ = modules.SaveNow()
	case "update":
		go startUpdate()
	case "steamvr":
		go steamVRAction(cmd.Action)
	case "showWindow":
		UI.ShowWindow()
	case "secret":
		// The value is never echoed or logged; the panel only learns it is set.
		_ = modules.SetPanelSecret(cmd.Module, cmd.Text)
	case "discord":
		switch cmd.Action {
		case "authorize":
			modules.DiscordAuthorize()
		case "signout":
			modules.DiscordSignOut()
		}
	case "twitch":
		switch cmd.Action {
		case "signin":
			modules.TwitchStartSignIn()
		case "cancel":
			modules.TwitchCancelSignIn()
		case "signout":
			modules.TwitchSignOut()
		case "refresh":
			modules.TwitchRefreshNow()
		}
	case "voicemod":
		modules.VoicemodCommand(cmd.Action, cmd.Text)
	case "heartReset":
		modules.ResetHeartStats()
	case "profile":
		handleProfileCommand(cmd)
	case "backup":
		handleBackupCommand(cmd)
	case "rule":
		handleRuleCommand(cmd)
	case "dnd":
		handleDNDCommand(cmd)
	case "calendar", "prices", "steam":
		handleWebLinesCommand(cmd)
	case "assist", "assistCancel", "assistKey", "tts", "stt":
		handleAssistCommand(cmd)
	}
}

func handleAssistCommand(cmd command) {
	switch cmd.Type {
	case "assist":
		modules.StartAssist(cmd.Action, cmd.Text)
	case "assistCancel":
		modules.CancelAssist()
	case "assistKey":
		if err := modules.SetAssistKey(cmd.Module, cmd.Text); err != nil {
			modules.ReportAssistError(err)
		}
	case "tts":
		switch cmd.Action {
		case "speak":
			modules.SpeakPreview(cmd.Text)
		case "stop":
			modules.StopSpeaking()
		case "refresh":
			go modules.RefreshSpeechDevices()
		}
	case "stt":
		switch cmd.Action {
		case "hold":
			modules.StartListening(true)
		case "start":
			modules.StartListening(false)
		case "release":
			modules.ReleaseListening()
		case "stop":
			modules.StopListening()
		case "cancel":
			modules.CancelListening()
		}
	}
}

// statePushInterval keeps the page in step with the chatbox. The dots advance
// once per update, so pushing the UI slower than the send rate made the preview
// skip frames (". ." straight to "......") while the chatbox grew one dot at a
// time. Never slower than 400ms, so slow send rates still leave the UI snappy.
func statePushInterval() time.Duration {
	interval := 400 * time.Millisecond
	if rate := modules.EffectiveRateMS(modules.GetConfig()); rate > 0 {
		if faster := time.Duration(rate) * time.Millisecond; faster < interval {
			interval = faster
		}
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	return interval
}

func broadcastLoop() {
	var last string
	for {
		time.Sleep(statePushInterval())
		clientsMu.Lock()
		empty := len(clients) == 0
		clientsMu.Unlock()
		if empty {
			continue
		}

		payload, err := json.Marshal(buildState())
		if err != nil {
			continue
		}
		encoded := string(payload)
		if encoded == last {
			continue
		}
		last = encoded
		broadcast(payload)
	}
}

func broadcast(payload []byte) {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	for c := range clients {
		select {
		case c.out <- payload:
		default:
			// Slow client: drop this frame instead of blocking the loop.
		}
	}
}

func buildState() uiState {
	cfg := modules.GetConfig()
	box := modules.ComposeChatbox()
	preview := box.Text
	lines := 0
	if preview != "" {
		lines = strings.Count(preview, "\n") + 1
	}
	holdText, holdUntil, holdPinned := modules.HoldState()

	active, idle := modules.AfkInfo()
	cfgAfk := cfg.AfkOptions
	used, total, percent := modules.MemoryStats()
	d := cfg.DisplayOptions
	return uiState{
		Preview:  preview,
		Fit:      box,
		Samples:  modules.ModuleSamples(cfg),
		Chars:    utf8.RuneCountInString(preview),
		Lines:    lines,
		MaxChars: d.MaxChars,
		MaxLines: d.MaxLines,
		Modules: map[string]bool{
			modules.ModuleAFK:     d.Afk,
			modules.ModuleLoading: d.Loading,
			modules.ModuleTime:    d.Time,
			modules.ModuleText:    d.Text,
			modules.ModuleMemory:  d.Memory,
			modules.ModuleCPU:     d.CPU,
			modules.ModuleMedia:   d.Media,
			modules.ModuleWindow:  d.ActiveWindow,
			modules.ModuleLogs:    d.VRChatLogs,
			modules.ModuleBattery: d.Battery,
			modules.ModuleVRPerf:  d.VRPerf,
			modules.ModuleHeart:   d.HeartRate,
			modules.ModuleLyrics:  d.Lyrics,
			modules.ModuleWeather: d.Weather,

			modules.ModuleDiscord:  d.Discord,
			modules.ModuleTwitch:   d.Twitch,
			modules.ModuleSoundpad: d.Soundpad,
			modules.ModuleVoicemod: d.Voicemod,
			modules.ModuleNetwork:  d.Network,
			modules.ModuleGPU:      d.GPU,
			modules.ModuleWorld:    d.World,
			modules.ModuleCalendar: d.Calendar,
			modules.ModulePrices:   d.Prices,
			modules.ModuleSteam:    d.Steam,
		},
		Order:   d.Order,
		Config:  cfg.Public(),
		Media:   modules.CurrentMedia(),
		Sources: orEmpty(modules.MediaSources()),
		Apps:    orEmpty(modules.RecentWindowApps()),
		InVR:    modules.InVR(),
		Afk: afkState{
			Active:  active,
			IdleMS:  idle.Milliseconds(),
			LimitMS: int64(cfgAfk.AFKTime * 1000),
			Talking: modules.Talking(),
			Manual:  cfgAfk.Manual,
			Style:   modules.AfkPresetMatch(cfgAfk),
			Styles:  modules.AfkPresetSamples(cfg),
			Sources: modules.AfkSourcesNow(),
		},
		Perf: perfState{
			CPU:        modules.CpuUsage(),
			MemUsed:    used,
			MemTotal:   total,
			MemPercent: percent,
		},
		Logs: logsState{
			Join:     orEmpty(modules.JoinLogs()),
			Leave:    orEmpty(modules.LeaveLogs()),
			Activity: orEmpty(modules.ActivityFeed()),
			World:    currentWorld(),
			Session:  modules.Session(),
		},
		History:   orEmpty(modules.History()),
		Hold:      holdState{Text: holdText, Until: holdUntil, Pinned: holdPinned},
		Stats:     statsState{UptimeSec: int64(time.Since(startedAt).Seconds()), Sent: atomic.LoadInt64(&sentCount), LastSentMS: atomic.LoadInt64(&lastSentAt), RateMS: modules.EffectiveRateMS(cfg)},
		VRChat:    modules.CurrentVRChatStatus(),
		Players:   modules.PlayerCount(),
		TextIndex: modules.MessageIndex(),
		OSC:       oscTarget(),
		Version:   appVersion,
		Update:    currentUpdate(),
		SteamVR:   modules.SteamVRState(),
		HeartRate: modules.CurrentHeartRate(),
		Assist: assistState{
			AI:   modules.CurrentAI(),
			TTS:  modules.CurrentTTS(),
			STT:  modules.CurrentSTT(),
			Keys: modules.KeyHints(cfg),
		},
		Lyrics:  modules.CurrentLyricsStatus(),
		Weather: modules.CurrentWeatherStatus(),
		Social:  modules.CurrentSocial(cfg),
		Network: currentNetwork(),
		GPUs:    orEmpty(modules.GPUs()),
		App:     appState{HotkeyErr: UI.HotkeyError(), SteamVR: currentSteamVR()},
		OSCIn:   currentOSCIn(),

		Profiles: currentProfiles(),
		Backups:  currentBackups(),
		Rules:    currentRules(),

		Calendar: modules.CurrentCalendarStatus(),
		Prices:   modules.CurrentPricesStatus(),
		Steam:    modules.CurrentSteamStatus(),
	}
}

func currentWorld() *modules.WorldInfo {
	if w, ok := modules.CurrentWorld(); ok {
		return &w
	}
	return nil
}

func currentNetwork() networkState {
	list, busiest := modules.NetAdapters()
	return networkState{Adapters: orEmpty(list), Busiest: busiest}
}

// orEmpty keeps nil slices from reaching the page as JSON null.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}
