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
	Time int64  `json:"time"`
	Text string `json:"text"`
}

var (
	logMu        sync.Mutex
	logPath      string
	logOffset    int64
	logRemainder string
	logUpdated   time.Time

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

func logDirectory() string {
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
	logUpdated = time.Now()

	chunk := logRemainder + string(data)
	lines := strings.Split(chunk, "\n")
	logRemainder = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		parseLogLine(line)
	}
}

func parseLogLine(line string) {
	switch {
	case strings.Contains(line, "OnPlayerJoined"):
		name := playerName(line, "OnPlayerJoined")
		if name == "" {
			return
		}
		ev := LogEvent{Time: time.Now().UnixMilli(), Text: name}
		if _, ok := joined[name]; ok {
			return
		}
		joined[name] = ev
		delete(left, name)
		order = append(order, name)
		addActivity(fmt.Sprintf("%s joined", name))
	case strings.Contains(line, "OnPlayerLeft"):
		name := playerName(line, "OnPlayerLeft")
		if name == "" {
			return
		}
		ev := LogEvent{Time: time.Now().UnixMilli(), Text: name}
		left[name] = ev
		delete(joined, name)
		for i, n := range order {
			if n == name {
				order = append(order[:i], order[i+1:]...)
				break
			}
		}
		addActivity(fmt.Sprintf("%s left", name))
	}
}

func addActivity(text string) {
	activity = append(activity, LogEvent{Time: time.Now().UnixMilli(), Text: text})
	if len(activity) > maxActivitySize {
		activity = activity[len(activity)-maxActivitySize:]
	}
}

// playerName extracts the display name in front of the usr_ id.
func playerName(line, marker string) string {
	idx := strings.Index(line, marker)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(line[idx+len(marker):])
	if un := strings.Index(rest, "(usr_"); un >= 0 {
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

// ActivityFeed returns recent join/leave events, newest last.
func ActivityFeed() []LogEvent {
	logMu.Lock()
	defer logMu.Unlock()
	return append([]LogEvent{}, activity...)
}

// logsLine renders the latest join/leave that is still enabled.
func logsLine(cfg Config) string {
	o := cfg.LogsOptions
	logMu.Lock()
	line := ""
	for i := len(activity) - 1; i >= 0; i-- {
		text := activity[i].Text
		if strings.HasSuffix(text, " joined") && !o.ShowJoin {
			continue
		}
		if strings.HasSuffix(text, " left") && !o.ShowLeave {
			continue
		}
		line = text
		break
	}
	logMu.Unlock()
	if line == "" {
		return ""
	}
	if limit := o.MaxChars; limit > 0 {
		line = RuneLimit(line, limit)
	}
	return withLabel(o.Label, line)
}

// PlayerCount is how many players are currently known to be in the instance.
func PlayerCount() int {
	logMu.Lock()
	defer logMu.Unlock()
	return len(joined)
}

// VRChatActive reports whether VRChat has written to its log recently.
func VRChatActive() bool {
	logMu.Lock()
	defer logMu.Unlock()
	return !logUpdated.IsZero() && time.Since(logUpdated) < 90*time.Second
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
