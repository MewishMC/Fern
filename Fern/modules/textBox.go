package modules

import (
	"strings"
	"sync"
	"time"
)

// chatboxMarker is appended because VRChat expects a terminated input string.
const chatboxMarker = "\x03\xFE"

var (
	holdMu    sync.RWMutex
	holdText  string
	holdUntil time.Time
	holdPin   bool
)

// RuneLimit shortens s to at most n runes without splitting a character.
func RuneLimit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// SetHold shows a custom message for a while. A duration of zero pins it until
// it is cleared.
func SetHold(text string, d time.Duration) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	holdMu.Lock()
	holdText = RuneLimit(text, 143)
	if d <= 0 {
		holdPin = true
		holdUntil = time.Time{}
	} else {
		holdPin = false
		holdUntil = time.Now().Add(d)
	}
	holdMu.Unlock()
	NotifyChange()
}

// ClearHold removes a pinned or temporary message.
func ClearHold() {
	holdMu.Lock()
	holdText = ""
	holdPin = false
	holdUntil = time.Time{}
	holdMu.Unlock()
	NotifyChange()
}

// HoldState reports the active overridden message, if any.
func HoldState() (text string, until int64, pinned bool) {
	holdMu.RLock()
	defer holdMu.RUnlock()
	if holdText == "" {
		return "", 0, false
	}
	if holdPin {
		return holdText, 0, true
	}
	if time.Now().After(holdUntil) {
		return "", 0, false
	}
	return holdText, holdUntil.UnixMilli(), false
}

func holdOverlay() (string, bool) {
	// HoldState returns (text, until, pinned). Using the third value as
	// "active" treated every timed hold as inactive, so only pinned messages
	// ever reached the chatbox. An empty text means there is no overlay.
	text, _, _ := HoldState()
	if text == "" {
		return "", false
	}
	return text, true
}

func moduleText(id string, cfg Config) string {
	switch id {
	case ModuleAFK:
		return afkLine(cfg)
	case ModuleLoading:
		return Loading()
	case ModuleTime:
		return clockText(cfg)
	case ModuleText:
		return textLine(cfg)
	case ModuleMemory:
		return memoryLine(cfg)
	case ModuleCPU:
		return cpuLine(cfg)
	case ModuleMedia:
		return mediaLine(cfg)
	case ModuleWindow:
		return windowLine(cfg)
	case ModuleLogs:
		return logsLine(cfg)
	}
	return ""
}

// withLabel puts an optional prefix in front of a module line.
func withLabel(label, text string) string {
	label = strings.TrimSpace(label)
	if label == "" || text == "" {
		return text
	}
	return label + " " + text
}

func textLine(cfg Config) string {
	return withLabel(cfg.TextOptions.Label, CurrentMessage())
}

func clockText(cfg Config) string {
	now := time.Now()
	layout := "3:04 PM"
	switch {
	case cfg.TimeOptions.Use24h:
		layout = "15:04"
	case cfg.TimeOptions.NoAmPm:
		layout = "3:04"
	}
	if cfg.TimeOptions.Seconds {
		switch {
		case cfg.TimeOptions.Use24h:
			layout = "15:04:05"
		case cfg.TimeOptions.NoAmPm:
			layout = "3:04:05"
		default:
			layout = "3:04:05 PM"
		}
	}
	text := now.Format(layout)
	if label := strings.TrimSpace(cfg.TimeOptions.Label); label != "" {
		text = label + " " + text
	}
	return text
}

// buildLines renders every enabled module in the configured order.
func buildLines(cfg Config) []string {
	lines := make([]string, 0, len(cfg.DisplayOptions.Order))
	maxLines := cfg.DisplayOptions.MaxLines
	if maxLines < 1 {
		maxLines = 9
	}
	for _, id := range cfg.DisplayOptions.Order {
		if !ModuleEnabled(id, cfg.DisplayOptions) {
			continue
		}
		text := strings.TrimSpace(moduleText(id, cfg))
		if text == "" {
			continue
		}
		if len(lines) >= maxLines {
			break
		}
		lines = append(lines, text)
	}
	return lines
}

// PreviewText is what the chatbox will show, without VRChat's marker.
func PreviewText() string {
	if text, ok := holdOverlay(); ok {
		return text
	}
	cfg := GetConfig()
	if !cfg.DisplayOptions.Show {
		return ""
	}
	lines := buildLines(cfg)
	if len(lines) == 0 {
		return ""
	}
	separator := cfg.DisplayOptions.Separator
	if separator == "" {
		separator = "\n"
	}
	text := strings.Join(lines, separator)
	return strings.TrimSpace(RuneLimit(text, cfg.DisplayOptions.MaxChars))
}

// BuildPayload returns the visible text and the exact OSC payload.
func BuildPayload() (body, payload string) {
	body = PreviewText()
	if body == "" {
		return "", ""
	}
	limit := GetConfig().DisplayOptions.MaxChars - 1
	if limit < 8 {
		limit = 8
	}
	body = strings.TrimSpace(RuneLimit(body, limit))
	if body == "" {
		return "", ""
	}
	return body, body + chatboxMarker
}
