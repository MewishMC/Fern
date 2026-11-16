package modules

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// LogEvent is one timestamped line shown in the UI.
type LogEvent struct {
	Time  int64  `json:"time"`
	Text  string `json:"text"`
	Kind  string `json:"kind,omitempty"`  // EventJoin, EventLeave, ... for VRChat events
	Again bool   `json:"again,omitempty"` // a join by someone already met this session
}

var (
	logMu        sync.Mutex
	logPath      string
	logOffset    int64
	logRemainder string

	joined   = map[string]LogEvent{}
	left     = map[string]LogEvent{}
	activity []LogEvent // newest last
	history  []LogEvent // newest last
	order    []string   // insertion order of joined names
)

const (
	maxLogEntries   = 30
	maxActivitySize = 60
	maxHistorySize  = 40
)

// logDirectory is VRChat's log folder. FERN_VRCHAT_LOGS points it elsewhere,
// for testing with a prepared log or a non-standard VRChat install.
func logDirectory() string {
	if dir := os.Getenv("FERN_VRCHAT_LOGS"); dir != "" {
		return dir
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "AppData", "LocalLow", "VRChat", "VRChat")
}

func latestLogFile() (string, error) {
	dir := logDirectory()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var newest string
	var newestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".txt") {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = filepath.Join(dir, entry.Name())
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no log files")
	}
	return newest, nil
}

func startLogWorker() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			scanLog()
		}
	}()
}

// scanLog reads only what was appended to the newest VRChat log since the last
// scan, so large logs stay cheap.
func scanLog() {
	path, err := latestLogFile()
	if err != nil {
		return
	}

	logMu.Lock()
	defer logMu.Unlock()

	if path != logPath {
		// A new log means VRChat restarted: nobody from the old one is around.
		// The session numbers carry on.
		if logPath != "" {
			leaveInstance()
		}
		logPath = path
		logOffset = 0
		logRemainder = ""
	}

	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	if _, err := file.Seek(logOffset, io.SeekStart); err != nil {
		return
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		return
	}
	logOffset += int64(len(data))

	chunk := logRemainder + string(data)
	lines := strings.Split(chunk, "\n")
	logRemainder = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		parseLogLine(strings.TrimRight(line, "\r"))
	}
}

// parseLogLine updates the players, world and activity from one log line.
// The caller holds logMu.
func parseLogLine(line string) {
	at := logTime(line)
	if parseWorldLine(line, at) {
		return
	}
	switch {
	// "OnPlayerJoined:" and "OnPlayerLeftRoom" are other messages that share
	// the prefix; only "OnPlayerJoined <name>" is a join.
	case strings.Contains(line, "] OnPlayerJoined ") && strings.Contains(line, "[Behaviour]"):
		name := playerName(line, "OnPlayerJoined ")
		if name == "" {
			return
		}
		if _, ok := joined[name]; ok {
			return
		}
		ev := LogEvent{Time: at.UnixMilli(), Text: name, Kind: EventJoin}
		joined[name] = ev
		delete(left, name)
		order = append(order, name)
		ev.Again = notePlayerJoined(name, at)
		joined[name] = ev
		act := addActivityAt(at, EventJoin, name+" joined")
		act.Again = ev.Again
		if liveLogLine(at) {
			FireTrigger(TriggerPlayerJoined, playerEvent(name))
		}
	case strings.Contains(line, "] OnPlayerLeft ") && strings.Contains(line, "[Behaviour]"):
		name := playerName(line, "OnPlayerLeft ")
		if name == "" {
			return
		}
		left[name] = LogEvent{Time: at.UnixMilli(), Text: name, Kind: EventLeave}
		delete(joined, name)
		for i, n := range order {
			if n == name {
				order = append(order[:i], order[i+1:]...)
				break
			}
		}
		addActivityAt(at, EventLeave, name+" left")
		if liveLogLine(at) {
			FireTrigger(TriggerPlayerLeft, playerEvent(name))
		}
	}
}

// addActivityAt appends an event to the feed and returns it for small edits.
func addActivityAt(at time.Time, kind, text string) *LogEvent {
	activity = append(activity, LogEvent{Time: at.UnixMilli(), Text: text, Kind: kind})
	if len(activity) > maxActivitySize {
		activity = activity[len(activity)-maxActivitySize:]
	}
	return &activity[len(activity)-1]
}

// playerName extracts the display name in front of the usr_ id, which newer
// VRChat builds add in brackets.
func playerName(line, marker string) string {
	rest, ok := after(line, marker)
	if !ok {
		return ""
	}
	if un := strings.LastIndex(rest, " (usr_"); un >= 0 && strings.HasSuffix(rest, ")") {
		rest = rest[:un]
	}
	return strings.TrimSpace(rest)
}

// JoinLogs returns the most recent joins, newest first.
func JoinLogs() []LogEvent {
	logMu.Lock()
	defer logMu.Unlock()
	out := make([]LogEvent, 0, len(joined))
	for i := len(order) - 1; i >= 0 && len(out) < maxLogEntries; i-- {
		if ev, ok := joined[order[i]]; ok {
			out = append(out, ev)
		}
	}
	return out
}

// LeaveLogs returns the most recent leaves, newest first.
func LeaveLogs() []LogEvent {
	logMu.Lock()
	defer logMu.Unlock()
	all := make([]LogEvent, 0, len(left))
	for _, ev := range left {
		all = append(all, ev)
	}
	slices.SortFunc(all, func(a, b LogEvent) int { return cmp.Compare(b.Time, a.Time) })
	if len(all) > maxLogEntries {
		all = all[:maxLogEntries]
	}
	return all
}

// ActivityFeed returns recent VRChat events, newest last.
func ActivityFeed() []LogEvent {
	logMu.Lock()
	defer logMu.Unlock()
	return append([]LogEvent{}, activity...)
}

// logsLine renders the latest VRChat event that is still enabled.
func logsLine(cfg Config) string {
	logMu.Lock()
	line := latestLogEvent(cfg.LogsOptions, activity, nowFunc())
	logMu.Unlock()
	return line
}

func latestLogEvent(o LogsOptions, events []LogEvent, now time.Time) string {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		switch ev.Kind {
		case EventJoin:
			if !o.ShowJoin {
				continue
			}
		case EventLeave:
			if !o.ShowLeave {
				continue
			}
		case EventScreenshot:
			if !o.ShowScreenshot {
				continue
			}
		case EventLoaded:
			if !o.ShowLoaded {
				continue
			}
		default:
			continue
		}
		// Only the newest enabled event counts: once it is too old the line
		// goes quiet instead of falling back to an even older one.
		if o.HideAfter > 0 && now.Sub(time.UnixMilli(ev.Time)) > time.Duration(o.HideAfter)*time.Second {
			return ""
		}
		text := ev.Text
		if ev.Kind == EventJoin && ev.Again && o.MarkSeenAgain {
			text += " again"
		}
		if limit := o.MaxChars; limit > 0 {
			text = RuneLimit(text, limit)
		}
		return withLabel(o.Label, text)
	}
	return ""
}

// PlayerCount is how many players are currently known to be in the instance.
func PlayerCount() int {
	logMu.Lock()
	defer logMu.Unlock()
	return len(joined)
}

var lastHistoryAt int64

// normalizeHistory ignores the animated parts (spinner frame, clock, progress
// bar) so the history lists meaningful chatbox changes instead of every frame.
func normalizeHistory(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case unicode.IsDigit(r), r == ':', r == '/', r == '▰', r == '▱', r == '.':
			continue
		case r >= 0x2800 && r <= 0x28FF: // legacy braille frames
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// AddHistory records a chatbox message that was actually sent.
func AddHistory(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	norm := normalizeHistory(text)
	logMu.Lock()
	defer logMu.Unlock()
	now := time.Now().UnixMilli()
	if len(history) > 0 && normalizeHistory(history[len(history)-1].Text) == norm {
		return
	}
	if now-lastHistoryAt < 800 {
		return
	}
	lastHistoryAt = now
	history = append(history, LogEvent{Time: now, Text: text})
	if len(history) > maxHistorySize {
		history = history[len(history)-maxHistorySize:]
	}
}

// History returns sent chatbox messages, newest last.
func History() []LogEvent {
	logMu.Lock()
	defer logMu.Unlock()
	return append([]LogEvent{}, history...)
}
