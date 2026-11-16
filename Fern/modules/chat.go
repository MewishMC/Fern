package modules

import (
	"strings"
	"time"
)

// SetLive shows a message while it is being typed on the Live page. Every
// change replaces the text without counting as a new message, so it does not
// play the chatbox sound; empty text takes a live message down again.
func SetLive(text string, d time.Duration) {
	if strings.TrimSpace(text) == "" {
		holdMu.RLock()
		live := holdLive
		holdMu.RUnlock()
		if live {
			ClearHold()
		}
		return
	}
	setHold(text, d, true)
}

// ChatHistoryLimit is how many sent messages are remembered.
const ChatHistoryLimit = 20

// AddChatHistory remembers a message sent from the Live page, moving a repeat
// to the front instead of storing it twice.
func AddChatHistory(text string) {
	text = chatHistoryText(text)
	if text == "" {
		return
	}
	cfg := GetConfig()
	if !cfg.ChatOptions.KeepHistory {
		return
	}
	if len(cfg.ChatOptions.History) > 0 && cfg.ChatOptions.History[0] == text {
		return
	}
	UpdateConfig(func(c *Config) {
		c.ChatOptions.History = append([]string{text}, c.ChatOptions.History...)
	})
}

// ClearChatHistory forgets every remembered message.
func ClearChatHistory() {
	UpdateConfig(func(c *Config) { c.ChatOptions.History = []string{} })
}

// chatHistoryText is a message the way it is sent and remembered.
func chatHistoryText(text string) string {
	return RuneLimit(strings.TrimSpace(strings.ReplaceAll(text, "\n", " ")), 143)
}

// cleanChatHistory drops blanks and repeats (keeping the newest) and keeps
// at most ChatHistoryLimit messages, or none when history is off.
func cleanChatHistory(list []string, keep bool) []string {
	out := make([]string, 0, ChatHistoryLimit)
	if !keep {
		return out
	}
	seen := map[string]bool{}
	for _, m := range list {
		m = chatHistoryText(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
		if len(out) == ChatHistoryLimit {
			break
		}
	}
	return out
}
