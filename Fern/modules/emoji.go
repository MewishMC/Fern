package modules

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// The emoji picker remembers what was used last and what was starred. The
// emoji data itself lives in the panel (UI/assets/emoji.js); Fern only keeps
// the two short lists, so they survive a restart and travel with backups.
const (
	EmojiRecentLimit   = 30
	EmojiFavoriteLimit = 60
	// emojiMaxRunes is enough for a long kaomoji or a family emoji with its
	// joiners, and keeps a pasted paragraph out of the list.
	emojiMaxRunes = 32
)

// cleanEmojiList trims every entry and drops blanks, entries with control
// characters or line breaks, entries over emojiMaxRunes and repeats (keeping
// the first, which is the newest), then keeps at most limit of them.
func cleanEmojiList(list []string, limit int) []string {
	out := make([]string, 0, min(len(list), limit))
	seen := make(map[string]bool, len(list))
	for _, item := range list {
		if len(out) == limit {
			break
		}
		item = strings.TrimSpace(item)
		if item == "" || seen[item] || !utf8.ValidString(item) || utf8.RuneCountInString(item) > emojiMaxRunes {
			continue
		}
		if strings.IndexFunc(item, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
