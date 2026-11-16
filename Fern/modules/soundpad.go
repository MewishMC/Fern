package modules

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Soundpad exposes a remote control named pipe. A request is a command string
// such as GetPlayStatus(); the reply is plain text (PLAYING, PAUSED, SEEKING,
// STOPPED), an XML document for GetSoundlist(), or R-<code> for actions.
// Soundpad has no "current sound" request, so Fern watches each sound's play
// count in the sound list and treats the one that went up as the one played.
const soundpadPipe = `\\.\pipe\sp_remote_control`

// SoundpadInfo is what the Soundpad page shows.
type SoundpadInfo struct {
	Link   LinkStatus `json:"link"`
	Status string     `json:"status"` // PLAYING, PAUSED, STOPPED, ...
	Last   string     `json:"last"`   // last sound played
	LastAt int64      `json:"lastAt"` // unix ms
}

type soundpadSound struct {
	Index        int
	Title        string
	URL          string
	PlayCount    int
	LastPlayedOn string
}

var (
	soundpadMu     sync.Mutex
	soundpadLink   = LinkStatus{State: linkOff}
	soundpadStatus string
	soundpadLast   string
	soundpadLastAt time.Time
)

// CurrentSoundpad returns the Soundpad snapshot for the panel.
func CurrentSoundpad() SoundpadInfo {
	soundpadMu.Lock()
	defer soundpadMu.Unlock()
	info := SoundpadInfo{Link: soundpadLink, Status: soundpadStatus, Last: soundpadLast}
	if !soundpadLastAt.IsZero() {
		info.LastAt = soundpadLastAt.UnixMilli()
	}
	return info
}

func setSoundpadLink(state, detail string) {
	soundpadMu.Lock()
	soundpadLink = LinkStatus{State: state, Detail: detail}
	if state != linkConnected {
		soundpadStatus = ""
	}
	soundpadMu.Unlock()
}

// parseSoundlist reads every <Sound> element, wherever categories nest them.
func parseSoundlist(data []byte) ([]soundpadSound, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var out []soundpadSound
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		el, ok := tok.(xml.StartElement)
		if !ok || el.Name.Local != "Sound" {
			continue
		}
		var s soundpadSound
		for _, a := range el.Attr {
			switch a.Name.Local {
			case "index":
				s.Index, _ = strconv.Atoi(a.Value)
			case "title":
				s.Title = a.Value
			case "url":
				s.URL = a.Value
			case "playCount":
				s.PlayCount, _ = strconv.Atoi(a.Value)
			case "lastPlayedOn":
				s.LastPlayedOn = a.Value
			}
		}
		if s.Index > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// name is the sound's title, or its file name without the extension.
func (s soundpadSound) name() string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	base := filepath.Base(strings.ReplaceAll(s.URL, "\\", "/"))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// playedSound picks the sound whose play count rose since prev. Without an
// earlier list to compare, it falls back to the most recently played one.
func playedSound(prev map[int]int, now []soundpadSound) (soundpadSound, bool) {
	var best soundpadSound
	found := false
	for _, s := range now {
		if prev != nil {
			if old, seen := prev[s.Index]; seen && s.PlayCount <= old {
				continue
			}
			if _, seen := prev[s.Index]; !seen && s.PlayCount == 0 {
				continue
			}
		}
		if !found || s.LastPlayedOn > best.LastPlayedOn {
			best, found = s, true
		}
	}
	return best, found
}

func playCounts(list []soundpadSound) map[int]int {
	m := make(map[int]int, len(list))
	for _, s := range list {
		m[s.Index] = s.PlayCount
	}
	return m
}

// soundpadConn is one open remote control pipe.
type soundpadConn struct {
	rw io.ReadWriteCloser
}

// request sends a command and reads the whole reply. Replies can span several
// reads (a large sound list), so reading continues while more is waiting.
func (c *soundpadConn) request(cmd string) (string, error) {
	if _, err := c.rw.Write([]byte(cmd)); err != nil {
		return "", err
	}
	var out bytes.Buffer
	buf := make([]byte, 64<<10)
	n, err := c.rw.Read(buf)
	if err != nil {
		return "", err
	}
	out.Write(buf[:n])
	if p, ok := c.rw.(interface{ Available() (int, error) }); ok {
		deadline := time.Now().Add(1500 * time.Millisecond)
		for out.Len() < 16<<20 {
			avail, err := p.Available()
			if err != nil {
				break
			}
			if avail == 0 {
				// A sound list arrives in pieces; wait for its closing tag.
				if !strings.HasPrefix(strings.TrimSpace(out.String()), "<") ||
					bytes.Contains(out.Bytes(), []byte("</Soundlist>")) || time.Now().After(deadline) {
					break
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			n, err := c.rw.Read(buf)
			if err != nil {
				return "", err
			}
			out.Write(buf[:n])
		}
	}
	return strings.TrimRight(out.String(), "\x00"), nil
}

func startSoundpadWorker() {
	go func() {
		var conn *soundpadConn
		var counts map[int]int
		var lastStatus string
		closeConn := func() {
			if conn != nil {
				_ = conn.rw.Close()
				conn = nil
			}
			counts = nil
			lastStatus = ""
		}
		for {
			if !GetConfig().DisplayOptions.Soundpad {
				closeConn()
				setSoundpadLink(linkOff, "")
				time.Sleep(2 * time.Second)
				continue
			}
			if conn == nil {
				p, err := openPipe(soundpadPipe)
				if err != nil {
					setSoundpadLink(linkWaiting, "Soundpad isn't running")
					time.Sleep(4 * time.Second)
					continue
				}
				p.messageMode()
				conn = &soundpadConn{rw: p}
			}
			status, err := conn.request("GetPlayStatus()")
			if err != nil {
				closeConn()
				setSoundpadLink(linkWaiting, "Soundpad closed the connection")
				time.Sleep(2 * time.Second)
				continue
			}
			status = strings.TrimSpace(status)
			soundpadMu.Lock()
			soundpadLink = LinkStatus{State: linkConnected}
			soundpadStatus = status
			soundpadMu.Unlock()

			// The list is fetched once to learn the counts, then only while
			// something plays, which is when a count can change.
			if counts == nil || status == "PLAYING" || lastStatus == "PLAYING" {
				if err := soundpadCheckList(conn, &counts, status == "PLAYING" && status != lastStatus); err != nil {
					closeConn()
					continue
				}
			}
			lastStatus = status
			time.Sleep(time.Second)
		}
	}()
}

// soundpadCheckList refreshes the play counts and records a newly played
// sound. started says playback just began.
func soundpadCheckList(conn *soundpadConn, counts *map[int]int, started bool) error {
	raw, err := conn.request("GetSoundlist()")
	if err != nil {
		return err
	}
	list, err := parseSoundlist([]byte(raw))
	if err != nil {
		return errors.New("unreadable sound list")
	}
	if *counts == nil {
		// Fern connected mid-sound: there is nothing to compare with yet, so
		// the most recently played sound is the best guess.
		if started {
			if s, ok := playedSound(nil, list); ok {
				noteSoundpadSound(s.name())
			}
		}
	} else if s, ok := playedSound(*counts, list); ok {
		noteSoundpadSound(s.name())
	}
	*counts = playCounts(list)
	return nil
}

func noteSoundpadSound(name string) {
	soundpadMu.Lock()
	soundpadLast = name
	soundpadLastAt = time.Now()
	soundpadMu.Unlock()
	NotifyChange()
}

// soundpadLine shows the last sound for the configured time.
func soundpadLine(cfg Config) string {
	o := cfg.SoundpadOptions
	soundpadMu.Lock()
	name, at := soundpadLast, soundpadLastAt
	soundpadMu.Unlock()
	if name == "" || time.Since(at) > time.Duration(o.DurationSec)*time.Second {
		return ""
	}
	return strings.TrimSpace(o.Icon + " '" + name + "'")
}
