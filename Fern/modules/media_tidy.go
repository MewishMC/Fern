package modules

import (
	"regexp"
	"strings"
	"unicode"
)

// Words that only describe the upload, never the song. A bracketed part or a
// trailing "| ..." part made of nothing else is dropped by TidyTrack.
var tidyJunkWords = map[string]bool{
	"official": true, "officiel": true, "oficial": true, "music": true, "video": true,
	"videoclip": true, "clip": true, "audio": true, "lyric": true, "lyrics": true,
	"lyrical": true, "letra": true, "hd": true, "hq": true, "4k": true, "8k": true,
	"1080p": true, "720p": true, "mv": true, "pv": true, "visualizer": true,
	"visualiser": true, "explicit": true, "with": true, "color": true, "colour": true,
	"coded": true, "high": true, "quality": true, "animated": true, "vertical": true,
}

var (
	// Bracketed parts: (...), [...], 【...】.
	tidyBrackets = regexp.MustCompile(`\s*(\([^()]*\)|\[[^\[\]]*\]|【[^【】]*】)`)
	// Trailing parts after a pipe, a dash or a double slash.
	tidyTail = regexp.MustCompile(`\s+(\||//|-|–|—)\s+([^|/\-–—]*)$`)
	// Featured artists, bracketed or running to the end of the title.
	tidyFeatBracket = regexp.MustCompile(`(?i)\s*[(\[]\s*(?:feat\.?|ft\.?|featuring|with)\s+([^()\[\]]+)[)\]]`)
	tidyFeatTail    = regexp.MustCompile(`(?i)\s+(?:feat\.?|ft\.?|featuring)\s+(.+)$`)
	tidyNameSplit   = regexp.MustCompile(`(?i)\s*(?:,|&|;|\+|\band\b|\bx\b)\s*`)
	tidySpaces      = regexp.MustCompile(`\s{2,}`)
)

// TidyTrack cleans up a title and artist the way they arrive from video sites:
// "(Official Video)", "[HD]", "| Official Music Video" and similar are
// removed, a featured artist already named in the artist is not repeated, a
// "VEVO" or " - Topic" channel name becomes the artist, and an "Artist - "
// prefix that repeats the artist is dropped. Anything it isn't sure about is
// left alone, and it never empties a title.
func TidyTrack(title, artist string) (string, string) {
	origTitle := strings.TrimSpace(title)
	artist = tidyArtist(artist)
	title = origTitle

	title = tidyBrackets.ReplaceAllStringFunc(title, func(part string) string {
		runes := []rune(strings.TrimSpace(part))
		if isTidyJunk(string(runes[1 : len(runes)-1])) {
			return ""
		}
		return part
	})
	for {
		m := tidyTail.FindStringSubmatchIndex(title)
		if m == nil || !isTidyJunk(title[m[4]:m[5]]) {
			break
		}
		title = title[:m[0]]
	}

	// "Artist - Song" with the artist repeated: keep the song, and take the
	// spelling from the title, which tends to be nicer than a channel name.
	for _, dash := range []string{" - ", " – ", " — "} {
		if i := strings.Index(title, dash); i > 0 && artist != "" && sameName(title[:i], artist) {
			artist = strings.TrimSpace(title[:i])
			title = title[i+len(dash):]
			break
		}
	}

	title = tidyFeatBracket.ReplaceAllStringFunc(title, func(part string) string {
		names := tidyFeatBracket.FindStringSubmatch(part)[1]
		if allCredited(names, artist) {
			return ""
		}
		return part
	})
	if m := tidyFeatTail.FindStringSubmatch(title); m != nil && allCredited(m[1], artist) {
		title = strings.TrimSuffix(title, m[0])
	}

	title = strings.TrimSpace(tidySpaces.ReplaceAllString(title, " "))
	title = strings.TrimSpace(strings.TrimRight(title, "|-–—/ "))
	if title == "" {
		title = origTitle
	}
	return title, artist
}

// tidyArtist turns channel names like "Artist - Topic" or "ArtistVEVO" into
// the artist.
func tidyArtist(artist string) string {
	artist = strings.TrimSpace(artist)
	for _, suffix := range []string{" - Topic", "VEVO", " Official"} {
		if len(artist) > len(suffix) && strings.HasSuffix(artist, suffix) {
			artist = strings.TrimSpace(strings.TrimSuffix(artist, suffix))
		}
	}
	return artist
}

// isTidyJunk reports whether a title part is made only of upload words.
func isTidyJunk(s string) bool {
	s = strings.ToLower(strings.ReplaceAll(s, "m/v", "mv"))
	words := strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !tidyJunkWords[w] {
			return false
		}
	}
	return true
}

// allCredited reports whether every featured name already appears in artist.
func allCredited(names, artist string) bool {
	if strings.TrimSpace(artist) == "" {
		return false
	}
	have := squashName(artist)
	found := false
	for _, n := range tidyNameSplit.Split(names, -1) {
		n = squashName(n)
		if n == "" {
			continue
		}
		if !strings.Contains(have, n) {
			return false
		}
		found = true
	}
	return found
}

func sameName(a, b string) bool {
	a, b = squashName(a), squashName(b)
	return a != "" && a == b
}

// squashName keeps only lower-case letters and digits, so "The Weeknd" and
// "TheWeeknd" compare equal.
func squashName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
