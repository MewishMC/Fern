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

	"Fern/UI"
	"Fern/modules"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// assetStamp is a short hash of the bundled stylesheets, appended to their URLs
// by the page. WebView2 happily reuses a cached stylesheet for a whole day
// without asking the server, so a rebuild used to keep drawing the old layout
// until the cache expired. A new URL per stylesheet change cannot be stale.
var assetStamp = func() string {
	hash := sha1.New()
	if assets := UI.Assets(); assets != nil {
		for _, name := range []string{"theme.css", "fonts/noto-emoji.css"} {
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
	Chars     int                   `json:"chars"`
	Lines     int                   `json:"lines"`
	MaxChars  int                   `json:"maxChars"`
	MaxLines  int                   `json:"maxLines"`
	Modules   map[string]bool       `json:"modules"`
	Order     []string              `json:"order"`
	Config    modules.Config        `json:"config"`
	Media     modules.MediaInfo     `json:"media"`
	Sources   []modules.MediaSource `json:"mediaSources"`
	Afk       afkState              `json:"afk"`
	Perf      perfState             `json:"perf"`
	Logs      logsState             `json:"logs"`
	History   []modules.LogEvent    `json:"history"`
	Hold      holdState             `json:"hold"`
	Stats     statsState            `json:"stats"`
	VRChat    bool                  `json:"vrchat"`
	Players   int                   `json:"players"`
	TextIndex int                   `json:"textIndex"`
	OSC       string                `json:"osc"`
	Version   string                `json:"version"`
}

type afkState struct {
	Active  bool    `json:"active"`
	IdleMS  int64   `json:"idleMs"`
	LimitMS int64   `json:"limitMs"`
	Talking bool    `json:"talking"`
	Source  float64 `json:"since"`
}

type perfState struct {
	CPU        float64 `json:"cpu"`
	MemUsed    float64 `json:"memUsed"`
	MemTotal   float64 `json:"memTotal"`
	MemPercent float64 `json:"memPercent"`
}

type logsState struct {
	Join     []modules.LogEvent `json:"join"`
	Leave    []modules.LogEvent `json:"leave"`
	Activity []modules.LogEvent `json:"activity"`
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
	mux.HandleFunc("/ws", handleSocket)
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
			modules.SetConfig(next)
		}
	case "send":
		hold := time.Duration(cmd.Hold) * time.Second
		modules.SetHold(cmd.Text, hold)
	case "clearHold":
		modules.ClearHold()
	case "media":
		go func() { _ = modules.MediaControl(cmd.Action) }()
	case "skipText":
		modules.SkipMessage()
	case "reset":
		modules.ResetConfig()
	case "save":
		_ = modules.SaveNow()
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
	preview := modules.PreviewText()
	chars := len([]rune(preview))
	lines := 0
	if preview != "" {
		lines = len(splitLines(preview))
	}
	holdText, holdUntil, holdPinned := modules.HoldState()

	active, idle := modules.AfkInfo()
	cfgAfk := cfg.AfkOptions
	used, total, percent := modules.MemoryStats()
	join := modules.JoinLogs()
	leave := modules.LeaveLogs()
	activity := modules.ActivityFeed()
	if join == nil {
		join = []modules.LogEvent{}
	}
	if leave == nil {
		leave = []modules.LogEvent{}
	}
	if activity == nil {
		activity = []modules.LogEvent{}
	}
	history := modules.History()
	if history == nil {
		history = []modules.LogEvent{}
	}

	d := cfg.DisplayOptions
	return uiState{
		Preview:  preview,
		Chars:    chars,
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
		},
		Order:  d.Order,
		Config: cfg,
		Media:  modules.CurrentMedia(),
		Sources: func() []modules.MediaSource {
			list := modules.MediaSources()
			if list == nil {
				return []modules.MediaSource{}
			}
			return list
		}(),
		Afk: afkState{
			Active:  active,
			IdleMS:  idle.Milliseconds(),
			LimitMS: int64(cfgAfk.AFKTime * 1000),
			Talking: modules.Talking(),
		},
		Perf: perfState{
			CPU:        modules.CpuUsage(),
			MemUsed:    used,
			MemTotal:   total,
			MemPercent: percent,
		},
		Logs: logsState{
			Join:     join,
			Leave:    leave,
			Activity: activity,
		},
		History:   history,
		Hold:      holdState{Text: holdText, Until: holdUntil, Pinned: holdPinned},
		Stats:     statsState{UptimeSec: int64(time.Since(startedAt).Seconds()), Sent: atomic.LoadInt64(&sentCount), LastSentMS: atomic.LoadInt64(&lastSentAt), RateMS: modules.EffectiveRateMS(cfg)},
		VRChat:    modules.VRChatActive(),
		Players:   modules.PlayerCount(),
		TextIndex: modules.MessageIndex(),
		OSC:       oscTarget(),
		Version:   appVersion,
	}
}

func splitLines(s string) []string {
	out := []string{""}
	for _, r := range s {
		if r == '\n' {
			out = append(out, "")
			continue
		}
		out[len(out)-1] += string(r)
	}
	return out
}
