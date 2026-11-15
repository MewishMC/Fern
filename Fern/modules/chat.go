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
