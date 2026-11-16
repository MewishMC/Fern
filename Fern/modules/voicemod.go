package modules

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Voicemod's Control API is a local websocket. A client registers with its
// client key, then sends {"action", "id", "payload"} requests; replies and
// events carry "actionType" and an "actionObject". Voicemod has used several
// ports over the years, so each is tried in turn.
var voicemodPorts = []int{59129, 20000, 39273, 42152, 43782, 46667, 35679, 37170, 38501, 33952, 30546}

// VoicemodItem is a voice or a sound in the panel's switcher.
type VoicemodItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Board   string `json:"board,omitempty"`
	Enabled bool   `json:"enabled"`
}

// VoicemodInfo is what the Voicemod page shows.
type VoicemodInfo struct {
	Link      LinkStatus     `json:"link"`
	VoiceID   string         `json:"voiceId"`
	Voice     string         `json:"voice"` // "" for no effect
	Voices    []VoicemodItem `json:"voices"`
	Sounds    []VoicemodItem `json:"sounds"`
	LastSound string         `json:"lastSound"`
	LastAt    int64          `json:"lastAt"`
	// MuteKnown is false until Voicemod has reported the microphone state.
	MuteKnown bool `json:"muteKnown"`
	Muted     bool `json:"muted"`
	Bleeping  bool `json:"bleeping"`
}

// vmMessage is the useful part of anything Voicemod sends.
type vmMessage struct {
	Action    string
	ID        string
	Status    int // 0 when the message has no status
	Voices    []VoicemodItem
	HasVoices bool
	VoiceID   string // current voice, when the message names one
	Sounds    []VoicemodItem
	HasSounds bool
	Muted     *bool // the microphone mute state, when the message gives it
}

// parseVoicemodMessage reads replies and events leniently: Voicemod builds
// differ in whether data sits in "actionObject" or "payload".
func parseVoicemodMessage(data []byte) (vmMessage, error) {
	var raw struct {
		Action       string          `json:"action"`
		ActionType   string          `json:"actionType"`
		ID           string          `json:"id"`
		ActionID     string          `json:"actionID"`
		Payload      json.RawMessage `json:"payload"`
		ActionObject json.RawMessage `json:"actionObject"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return vmMessage{}, err
	}
	m := vmMessage{Action: firstNonEmpty(raw.ActionType, raw.Action), ID: firstNonEmpty(raw.ActionID, raw.ID)}
	m.Muted = parseVoicemodMute(m.Action, raw.ActionObject, raw.Payload)
	type statusPart struct {
		Status *struct {
			Code int `json:"code"`
		} `json:"status"`
	}
	type voicePart struct {
		Voices *[]struct {
			ID           string `json:"id"`
			FriendlyName string `json:"friendlyName"`
			Enabled      *bool  `json:"enabled"`
			IsEnabled    *bool  `json:"isEnabled"`
		} `json:"voices"`
		CurrentVoice string `json:"currentVoice"`
		VoiceID      string `json:"voiceID"`
		Soundboards  *[]struct {
			Name   string `json:"name"`
			Sounds []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Enabled   *bool  `json:"enabled"`
				IsEnabled *bool  `json:"isEnabled"`
			} `json:"sounds"`
		} `json:"soundboards"`
	}
	enabled := func(a, b *bool) bool {
		if a != nil {
			return *a
		}
		if b != nil {
			return *b
		}
		return true
	}
	for _, part := range []json.RawMessage{raw.ActionObject, raw.Payload} {
		if len(part) == 0 || part[0] != '{' {
			continue
		}
		var st statusPart
		if json.Unmarshal(part, &st) == nil && st.Status != nil && m.Status == 0 {
			m.Status = st.Status.Code
		}
		var vp voicePart
		if json.Unmarshal(part, &vp) != nil {
			continue
		}
		if vp.Voices != nil && !m.HasVoices {
			m.HasVoices = true
			for _, v := range *vp.Voices {
				m.Voices = append(m.Voices, VoicemodItem{ID: v.ID, Name: firstNonEmpty(v.FriendlyName, v.ID), Enabled: enabled(v.Enabled, v.IsEnabled)})
			}
		}
		if m.VoiceID == "" {
			m.VoiceID = firstNonEmpty(vp.CurrentVoice, vp.VoiceID)
		}
		if vp.Soundboards != nil && !m.HasSounds {
			m.HasSounds = true
			for _, b := range *vp.Soundboards {
				for _, s := range b.Sounds {
					m.Sounds = append(m.Sounds, VoicemodItem{ID: s.ID, Name: firstNonEmpty(s.Name, s.ID), Board: b.Name, Enabled: enabled(s.Enabled, s.IsEnabled)})
				}
			}
		}
	}
	return m, nil
}

// noVoice reports whether a voice id means the voice changer is off.
func noVoice(id string) bool {
	switch strings.ToLower(id) {
	case "", "nofx", "clean":
		return true
	}
	return false
}

type vmCommand struct {
	action string
	id     string
}

const maxVoicemodSounds = 600

var (
	vmMu      sync.Mutex
	vmLink    = LinkStatus{State: linkOff}
	vmVoiceID string
	vmVoices  []VoicemodItem
	vmSounds  []VoicemodItem
	vmLast    string
	vmLastAt  time.Time
	vmMuteOK  bool // vmMuted is known
	vmMuted   bool
	vmCmdCh   = make(chan vmCommand, 8)
	vmKickCh  = make(chan struct{}, 1)
	vmURLs    = func() []string {
		urls := make([]string, 0, len(voicemodPorts))
		for _, p := range voicemodPorts {
			urls = append(urls, "ws://localhost:"+strconv.Itoa(p)+"/v1")
		}
		return urls
	}
)

func voicemodKick() { poke(vmKickCh) }

func setVoicemodLink(state, detail string) {
	vmMu.Lock()
	vmLink = LinkStatus{State: state, Detail: detail}
	vmMu.Unlock()
}

// VoicemodCommand queues a panel action: voice (load voice id), play (sound
// id), stop (all sounds) or reload (the lists).
func VoicemodCommand(action, id string) {
	select {
	case vmCmdCh <- vmCommand{action: action, id: id}:
	default:
	}
}

// CurrentVoicemod returns the Voicemod snapshot for the panel.
func CurrentVoicemod() VoicemodInfo {
	vmMu.Lock()
	defer vmMu.Unlock()
	info := VoicemodInfo{
		Link:      vmLink,
		VoiceID:   vmVoiceID,
		Voice:     voiceNameLocked(vmVoiceID),
		Voices:    append([]VoicemodItem{}, vmVoices...),
		Sounds:    append([]VoicemodItem{}, vmSounds...),
		LastSound: vmLast,
		MuteKnown: vmMuteOK,
		Muted:     vmMuted,
		Bleeping:  vmBleeping(),
	}
	if !vmLastAt.IsZero() {
		info.LastAt = vmLastAt.UnixMilli()
	}
	return info
}

func voiceNameLocked(id string) string {
	if noVoice(id) {
		return ""
	}
	for _, v := range vmVoices {
		if v.ID == id {
			return v.Name
		}
	}
	return id
}

// applyVoicemodMessage folds a message into the shared snapshot.
func applyVoicemodMessage(m vmMessage) {
	vmMu.Lock()
	if m.HasVoices {
		voices := m.Voices
		sort.SliceStable(voices, func(i, j int) bool { return strings.ToLower(voices[i].Name) < strings.ToLower(voices[j].Name) })
		vmVoices = voices
	}
	if m.HasSounds {
		sounds := m.Sounds
		if len(sounds) > maxVoicemodSounds {
			sounds = sounds[:maxVoicemodSounds]
		}
		vmSounds = sounds
	}
	if m.VoiceID != "" {
		vmVoiceID = m.VoiceID
	}
	changed := false
	if m.Muted != nil {
		changed = !vmMuteOK || vmMuted != *m.Muted
		vmMuteOK, vmMuted = true, *m.Muted
	}
	vmMu.Unlock()
	if changed {
		NudgeRules()
	}
	NotifyChange()
}

func clearVoicemod() {
	vmMu.Lock()
	vmVoiceID, vmVoices, vmSounds = "", nil, nil
	vmMuteOK, vmMuted = false, false
	vmMu.Unlock()
	endBleep()
}

func startVoicemodWorker() {
	go func() {
		for {
			cfg := GetConfig()
			if !cfg.VoicemodOptions.Enabled && !cfg.DisplayOptions.Voicemod {
				setVoicemodLink(linkOff, "")
				clearVoicemod()
				sleepOrKick(2*time.Second, vmKickCh)
				continue
			}
			key := GetSecret(SecretVoicemodKey)
			if key == "" {
				setVoicemodLink(linkWaiting, "Add your Voicemod client key")
				sleepOrKick(2*time.Second, vmKickCh)
				continue
			}
			conn := dialVoicemod(vmURLs())
			if conn == nil {
				setVoicemodLink(linkWaiting, "Voicemod isn't running")
				clearVoicemod()
				sleepOrKick(5*time.Second, vmKickCh)
				continue
			}
			err := voicemodSession(conn, key, func() bool {
				c := GetConfig()
				return c.VoicemodOptions.Enabled || c.DisplayOptions.Voicemod
			})
			_ = conn.Close()
			clearVoicemod()
			if err != nil {
				setVoicemodLink(linkError, err.Error())
				sleepOrKick(5*time.Second, vmKickCh)
			}
		}
	}()
}

func dialVoicemod(urls []string) *websocket.Conn {
	dialer := websocket.Dialer{HandshakeTimeout: 1500 * time.Millisecond}
	for _, u := range urls {
		conn, resp, err := dialer.Dial(u, http.Header{})
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			return conn
		}
	}
	return nil
}

// vmWriter serialises writes; gorilla allows one writer at a time.
type vmWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
	seq  int
}

func (w *vmWriter) send(action string, payload any) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	id := fmt.Sprintf("fern-%d-%d", time.Now().UnixNano()%1e9, w.seq)
	if payload == nil {
		payload = map[string]any{}
	}
	_ = w.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return id, w.conn.WriteJSON(map[string]any{"action": action, "id": id, "payload": payload})
}

// voicemodSession registers and then services the socket until it closes,
// the settings turn it off, or it is kicked. A nil error means it was ended on
// purpose; an error is shown on the page.
func voicemodSession(conn *websocket.Conn, key string, wanted func() bool) error {
	w := &vmWriter{conn: conn}
	setVoicemodLink(linkWaiting, "Registering with Voicemod")
	regID, err := w.send("registerClient", map[string]string{"clientKey": key})
	if err != nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		registered := false
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
			_, data, err := conn.ReadMessage()
			if err != nil {
				done <- nil
				return
			}
			m, err := parseVoicemodMessage(data)
			if err != nil {
				continue
			}
			if !registered && (m.Action == "registerClient" || (m.ID == regID && m.Status != 0)) {
				if m.Status != 0 && m.Status != 200 {
					done <- fmt.Errorf("Voicemod refused the client key (%d)", m.Status)
					return
				}
				registered = true
				setVoicemodLink(linkConnected, "")
				for _, a := range []string{"getVoices", "getCurrentVoice", "getAllSoundboard", "getMuteMicStatus"} {
					_, _ = w.send(a, nil)
				}
				if bleepLeftOn() {
					// The last session ended mid-bleep; make sure it stops.
					_, _ = w.send("setBeepSound", map[string]int{"badLanguage": 0})
				}
				continue
			}
			applyVoicemodMessage(m)
		}
	}()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	for {
		select {
		case err := <-done:
			if err == nil {
				setVoicemodLink(linkWaiting, "Voicemod closed the connection")
			}
			return err
		case <-vmKickCh:
			return nil
		case <-check.C:
			if !wanted() {
				return nil
			}
			if bleepOverdue() {
				voicemodMic(w, vmCommand{action: "bleepOff"})
			}
		case c := <-vmCmdCh:
			voicemodDo(w, c)
		}
	}
}

func voicemodDo(w *vmWriter, c vmCommand) {
	switch c.action {
	case "voice":
		id := c.id
		if id == "" {
			id = "nofx"
		}
		if _, err := w.send("loadVoice", map[string]string{"voiceID": id}); err == nil {
			vmMu.Lock()
			vmVoiceID = id
			vmMu.Unlock()
			NotifyChange()
		}
	case "play":
		if _, err := w.send("playMeme", map[string]any{"FileName": c.id, "IsKeyDown": true}); err == nil {
			vmMu.Lock()
			for _, s := range vmSounds {
				if s.ID == c.id {
					vmLast, vmLastAt = s.Name, time.Now()
				}
			}
			vmMu.Unlock()
			NotifyChange()
		}
	case "stop":
		_, _ = w.send("stopAllMemeSounds", nil)
	case "mute", "muteSet", "bleepOn", "bleepOff":
		voicemodMic(w, c)
	case "reload":
		for _, a := range []string{"getVoices", "getCurrentVoice", "getAllSoundboard", "getMuteMicStatus"} {
			_, _ = w.send(a, nil)
		}
	}
}

// voicemodLine shows the sound just played from Fern and the active voice.
func voicemodLine(cfg Config) string {
	o := cfg.VoicemodOptions
	vmMu.Lock()
	last, at := vmLast, vmLastAt
	voice := voiceNameLocked(vmVoiceID)
	connected := vmLink.State == linkConnected
	muted := vmMuteOK && vmMuted
	vmMu.Unlock()
	var parts []string
	if o.AnnounceMute && connected && muted {
		parts = append(parts, o.MuteText)
	}
	if o.AnnounceSounds && last != "" && time.Since(at) <= time.Duration(o.SoundSec)*time.Second {
		parts = append(parts, strings.TrimSpace(o.SoundIcon+" '"+last+"'"))
	}
	if o.AnnounceVoice && connected && voice != "" {
		parts = append(parts, strings.TrimSpace(o.VoiceIcon+" '"+voice+"'"))
	}
	return strings.Join(parts, " · ")
}
