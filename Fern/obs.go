package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"Fern/UI"
	"Fern/modules"
)

// obsState is the OBS page: the connection and outputs, plus the address of
// the chatbox overlay for a browser source.
type obsState struct {
	modules.OBSStatus
	Overlay string `json:"overlay"`
}

// overlayBase is "http://127.0.0.1:<port>", set once the server listens.
var (
	overlayMu   sync.Mutex
	overlayBase string
)

func setOverlayBase(l net.Listener) {
	if addr, ok := l.Addr().(*net.TCPAddr); ok {
		overlayMu.Lock()
		overlayBase = fmt.Sprintf("http://127.0.0.1:%d", addr.Port)
		overlayMu.Unlock()
	}
}

func currentOBS() obsState {
	overlayMu.Lock()
	base := overlayBase
	overlayMu.Unlock()
	st := obsState{OBSStatus: modules.CurrentOBS()}
	if base != "" {
		st.Overlay = base + "/overlay"
	}
	return st
}

// handleOBSCommand is {type:"obs", action, module:<start|stop|toggle|pause|resume>,
// text:<scene or item id>, enabled}.
func handleOBSCommand(cmd command) {
	if err := modules.OBSCommand(cmd.Action, cmd.Module, cmd.Text, cmd.Enabled); err != nil {
		fmt.Println("obs:", err)
	}
}

// overlaySource is the text the overlay shows: the chatbox as Fern sends it,
// or nothing while the display is off. Swapped in tests.
var overlaySource = func() string {
	if !modules.GetConfig().DisplayOptions.Show {
		return ""
	}
	return modules.ComposeChatbox().Text
}

// overlayText shares one render between every open overlay.
var overlayCache struct {
	sync.Mutex
	at   time.Time
	text string
}

func overlayText() string {
	overlayCache.Lock()
	defer overlayCache.Unlock()
	if time.Since(overlayCache.at) > 200*time.Millisecond {
		overlayCache.text = overlaySource()
		overlayCache.at = time.Now()
	}
	return overlayCache.text
}

// The overlay is read-only, but it still only answers loopback requests, so
// a website cannot read the chatbox through DNS rebinding.
func handleOverlay(w http.ResponseWriter, r *http.Request) {
	if !overlayAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(UI.OverlayPage))
}

func handleOverlayText(w http.ResponseWriter, r *http.Request) {
	if !overlayAllowed(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"text": overlayText()})
}

// handleOverlayStream sends the chatbox text as server-sent events whenever
// it changes, with a comment now and then to keep the connection open.
func handleOverlayStream(w http.ResponseWriter, r *http.Request) {
	if !overlayAllowed(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")

	last := "\x00"
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	idle := 0
	for {
		text := overlayText()
		if text != last {
			last = text
			data, _ := json.Marshal(map[string]string{"text": text})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
			idle = 0
		} else if idle++; idle >= 60 {
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
			idle = 0
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func overlayAllowed(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !localRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}
