package modules

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LyricLine is one timed line of synced lyrics. An empty Text marks a break,
// which LRC files use for instrumental parts.
type LyricLine struct {
	At   time.Duration
	Text string
}

var (
	// A time tag: [mm:ss], [mm:ss.xx], [mm:ss.xxx] or [mm:ss:xx].
	lrcTimeTag = regexp.MustCompile(`^\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
	lrcOffset  = regexp.MustCompile(`(?i)^\[offset:\s*([+-]?\d+)\s*\]`)
	// Word timings from the "enhanced" LRC format: <mm:ss.xx>.
	lrcWordTag = regexp.MustCompile(`<\d{1,3}:\d{1,2}(?:[.:]\d{1,3})?>`)
)

// ParseLRC reads synced lyrics in the LRC format. Lines may carry several
// time tags, metadata tags like [ar:] are skipped, and an [offset:] tag is
// applied (a positive offset makes lines come earlier, as the format says).
// The result is sorted by time and is empty when the text has no time tags.
func ParseLRC(text string) []LyricLine {
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	var lines []LyricLine
	offset := time.Duration(0)
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		raw = strings.TrimSpace(raw)
		if m := lrcOffset.FindStringSubmatch(raw); m != nil {
			if ms, err := strconv.Atoi(m[1]); err == nil {
				offset = time.Duration(ms) * time.Millisecond
			}
			continue
		}
		var stamps []time.Duration
		for {
			m := lrcTimeTag.FindStringSubmatch(raw)
			if m == nil {
				break
			}
			stamps = append(stamps, lrcStamp(m[1], m[2], m[3]))
			raw = strings.TrimSpace(raw[len(m[0]):])
		}
		if len(stamps) == 0 {
			continue
		}
		words := strings.Join(strings.Fields(lrcWordTag.ReplaceAllString(raw, "")), " ")
		for _, at := range stamps {
			lines = append(lines, LyricLine{At: at, Text: words})
		}
	}
	for i := range lines {
		lines[i].At -= offset
		if lines[i].At < 0 {
			lines[i].At = 0
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].At < lines[j].At })
	return lines
}

// lrcStamp turns the parts of a time tag into a duration. The fraction is
// hundredths with two digits, milliseconds with three, tenths with one.
func lrcStamp(min, sec, frac string) time.Duration {
	m, _ := strconv.Atoi(min)
	s, _ := strconv.Atoi(sec)
	d := time.Duration(m)*time.Minute + time.Duration(s)*time.Second
	if frac != "" {
		f, _ := strconv.Atoi(frac)
		switch len(frac) {
		case 1:
			d += time.Duration(f) * 100 * time.Millisecond
		case 2:
			d += time.Duration(f) * 10 * time.Millisecond
		default:
			d += time.Duration(f) * time.Millisecond
		}
	}
	return d
}

// lyricAt picks what to show at pos. A line shows from its time until the
// next line, but for no longer than hold; past that, a pause before the next
// line of at least gap shows the marker instead (so do breaks and a long
// intro). A shorter pause keeps the line up rather than flicker. After the
// last line has been held, nothing is shown.
func lyricAt(lines []LyricLine, pos, hold, gap time.Duration, marker string) string {
	if len(lines) == 0 {
		return ""
	}
	i := sort.Search(len(lines), func(i int) bool { return lines[i].At > pos }) - 1
	if i < 0 {
		// Before the first line: an intro long enough gets the marker.
		if gap > 0 && lines[0].At >= gap {
			return marker
		}
		return ""
	}
	line := lines[i]
	if line.Text == "" {
		if i+1 < len(lines) {
			return marker
		}
		return ""
	}
	if pos-line.At <= hold {
		return line.Text
	}
	if i+1 >= len(lines) {
		return ""
	}
	if gap > 0 && lines[i+1].At-line.At >= gap {
		return marker
	}
	return line.Text
}
