package modules

import (
	"strings"
	"unicode/utf8"
)

// DefaultDropOrder is which line goes first when the chatbox is too long:
// the clock first, your own text last.
var DefaultDropOrder = []string{
	ModuleTime, ModuleWeather, ModuleNetwork, ModuleCPU, ModuleGPU, ModuleMemory, ModuleVRPerf, ModuleBattery,
	ModuleDisk, ModuleUptime, ModuleSensors, ModulePing, ModulePCBattery,
	ModuleWindow, ModuleLogs, ModuleWorld, ModuleCustom3, ModuleCustom2, ModuleCustom1, ModuleCounter, ModuleLoading, ModuleKick, ModuleYouTube, ModuleTikTok, ModuleTwitch,
	ModuleTwitchChat, ModuleSoundpad, ModuleVoicemod,
	ModuleDiscord, ModuleLastFM, ModuleLyrics, ModuleHeart, ModuleTimer, ModuleAFK, ModuleMedia, ModuleSchedule, ModuleText,
}

// Ellipsis marks a line that had to be cut.
const Ellipsis = "…"

// FitLine is one rendered chatbox line with the ways it can get shorter.
type FitLine struct {
	ID string
	// Variants holds the full line first, then shorter versions that lose
	// decoration (a progress bar, credits). Empty means the line is skipped.
	Variants []string
	// Trim, when set, returns a version of the line of at most max runes, or
	// "" when the line cannot be cut that far and still say something useful.
	// It is tried just before the line would be dropped.
	Trim func(max int) string
}

// Fit is the outcome of fitting lines into the chatbox.
type Fit struct {
	Text    string   `json:"text"`
	Kept    []string `json:"kept"`    // ids that made it, in display order
	Dropped []string `json:"dropped"` // ids dropped to make room, in drop order
	Shrunk  []string `json:"shrunk"`  // ids shown in a shorter form
	Cut     bool     `json:"cut"`     // the last line was cut with an ellipsis
}

type fitState struct {
	line    FitLine
	variant int
	trimmed string // set once Trim was used
	dropped bool
}

func (s *fitState) text() string {
	if s.trimmed != "" {
		return s.trimmed
	}
	return s.line.Variants[s.variant]
}

// FitLines joins lines into at most maxChars runes and maxRows rows.
//
// When the text is too long it first swaps lines for their shorter variants,
// going through dropOrder (first entry first), then drops whole lines in the
// same order, trimming a line with Trim instead of dropping it when that is
// enough. The last line left is never dropped: it is cut with an ellipsis.
// Ids missing from dropOrder are dropped after the listed ones, last line
// first. prefix and suffix wrap every line.
func FitLines(lines []FitLine, dropOrder []string, sep, prefix, suffix string, maxChars, maxRows int) Fit {
	states := make([]*fitState, 0, len(lines))
	for _, l := range lines {
		if len(l.Variants) == 0 || l.Variants[0] == "" {
			continue
		}
		states = append(states, &fitState{line: l})
	}
	if len(states) == 0 {
		return Fit{}
	}

	render := func() string {
		parts := make([]string, 0, len(states))
		for _, s := range states {
			if !s.dropped {
				parts = append(parts, prefix+s.text()+suffix)
			}
		}
		return strings.Join(parts, sep)
	}
	charsOver := func() bool { return maxChars > 0 && utf8.RuneCountInString(render()) > maxChars }
	rowsOver := func() bool { return maxRows > 0 && strings.Count(render(), "\n")+1 > maxRows }
	fits := func() bool { return !charsOver() && !rowsOver() }
	kept := func() int {
		n := 0
		for _, s := range states {
			if !s.dropped {
				n++
			}
		}
		return n
	}

	// Rank the lines: listed ids in drop order, then the rest from the bottom up.
	ranked := make([]*fitState, 0, len(states))
	used := make(map[*fitState]bool, len(states))
	for _, id := range dropOrder {
		for _, s := range states {
			if s.line.ID == id && !used[s] {
				used[s] = true
				ranked = append(ranked, s)
			}
		}
	}
	for i := len(states) - 1; i >= 0; i-- {
		if !used[states[i]] {
			ranked = append(ranked, states[i])
		}
	}

	// Shorter forms first: losing a progress bar beats losing a line. Only
	// for length, since a shorter line still takes up its row.
	for _, s := range ranked {
		for charsOver() && s.variant < len(s.line.Variants)-1 {
			s.variant++
		}
	}

	var dropped []string
	for _, s := range ranked {
		if fits() || kept() == 1 {
			break
		}
		if !rowsOver() && s.line.Trim != nil {
			others := utf8.RuneCountInString(render()) - utf8.RuneCountInString(s.text())
			if t := s.line.Trim(maxChars - others); t != "" && utf8.RuneCountInString(t) <= maxChars-others {
				s.trimmed = t
				continue
			}
		}
		s.dropped = true
		dropped = append(dropped, s.line.ID)
	}

	// Dropping a long line can free more room than was needed: give the most
	// important lines back their fuller forms where they fit again.
	if len(dropped) > 0 {
		for i := len(ranked) - 1; i >= 0; i-- {
			s := ranked[i]
			for !s.dropped && s.trimmed == "" && s.variant > 0 {
				s.variant--
				if !fits() {
					s.variant++
					break
				}
			}
		}
	}

	out := Fit{Dropped: dropped}
	if !fits() {
		// One line left that is still too long.
		for _, s := range states {
			if s.dropped {
				continue
			}
			text := s.text()
			if maxRows > 0 {
				if rows := strings.Split(text, "\n"); len(rows) > maxRows {
					text = strings.Join(rows[:maxRows], "\n")
				}
			}
			if maxChars > 0 {
				budget := maxChars - utf8.RuneCountInString(prefix+suffix)
				if budget < 1 {
					out.Text = RuneLimit(prefix+text+suffix, maxChars)
					out.Cut = true
					out.Kept = []string{s.line.ID}
					return out
				}
				if utf8.RuneCountInString(text) > budget {
					text = ellipsize(text, budget)
					out.Cut = true
				}
			}
			s.trimmed = text
		}
	}

	for _, s := range states {
		if s.dropped {
			continue
		}
		out.Kept = append(out.Kept, s.line.ID)
		if s.variant > 0 || s.trimmed != "" && s.trimmed != s.line.Variants[0] {
			out.Shrunk = append(out.Shrunk, s.line.ID)
		}
	}
	out.Text = render()
	return out
}

// ellipsize cuts s to at most n runes, ending in an ellipsis.
func ellipsize(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n <= 1 {
		return RuneLimit(Ellipsis, n)
	}
	return strings.TrimRight(RuneLimit(s, n-1), " \t\n·—-|,") + Ellipsis
}
