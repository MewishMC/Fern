package modules

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Word replacements and the censor list. They rewrite text on its way out:
// what you type and send, what dictation hears, and what text to speech reads.
// Each of those three is switched on its own.

// Text filter surfaces, for FilterText.
const (
	SurfaceTyped    = "typed"    // Send now and live typing
	SurfaceDictated = "dictated" // speech to text results
	SurfaceSpeech   = "speech"   // text to speech
)

// Limits of the text filter.
const (
	MaxWordReplacements = 100
	MaxCensorWords      = 200
	maxFilterPattern    = 100
	maxFilterReplace    = 144
)

// WordReplacement swaps one word or phrase for another. From is plain text,
// matched case-insensitively as a whole word, or /regex/ (case-insensitive,
// with $1 style groups usable in To).
type WordReplacement struct {
	On   bool   `json:"on"`
	From string `json:"from"`
	To   string `json:"to"`
}

// TextFilterOptions configures word replacements and the censor list.
type TextFilterOptions struct {
	Typed    bool              `json:"typed"`
	Dictated bool              `json:"dictated"`
	Speech   bool              `json:"speech"`
	Replace  []WordReplacement `json:"replace"`
	Censor   []string          `json:"censor"` // plain words or /regex/
	Mask     string            `json:"mask"`   // one character that covers each letter
}

func defaultTextFilterOptions() TextFilterOptions {
	return TextFilterOptions{
		Typed:    true,
		Dictated: true,
		Speech:   true,
		Replace:  []WordReplacement{},
		Censor:   []string{},
		Mask:     "*",
	}
}

func (o TextFilterOptions) clone() TextFilterOptions {
	o.Replace = append([]WordReplacement{}, o.Replace...)
	o.Censor = append([]string{}, o.Censor...)
	return o
}

// filterLine keeps one line of a pattern: no newlines or control characters.
func filterLine(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return RuneLimit(s, n)
}

func (o *TextFilterOptions) sanitize() {
	def := defaultTextFilterOptions()
	if o.Replace == nil {
		o.Replace = def.Replace
	}
	if o.Censor == nil {
		o.Censor = def.Censor
	}
	replace := make([]WordReplacement, 0, len(o.Replace))
	for _, r := range o.Replace {
		if len(replace) >= MaxWordReplacements {
			break
		}
		r.From = filterLine(r.From, maxFilterPattern)
		r.To = filterLine(r.To, maxFilterReplace)
		// An empty From is a row still being filled in; keep it so the panel
		// does not lose it, it simply never matches.
		replace = append(replace, r)
	}
	o.Replace = replace
	censor := make([]string, 0, len(o.Censor))
	seen := map[string]bool{}
	for _, w := range o.Censor {
		if len(censor) >= MaxCensorWords {
			break
		}
		w = strings.TrimSpace(filterLine(w, maxFilterPattern))
		if w == "" || seen[strings.ToLower(w)] {
			continue
		}
		seen[strings.ToLower(w)] = true
		censor = append(censor, w)
	}
	o.Censor = censor
	mask := strings.TrimSpace(o.Mask)
	r, _ := utf8.DecodeRuneInString(mask)
	if mask == "" || r == utf8.RuneError || unicode.IsControl(r) {
		o.Mask = def.Mask
	} else {
		o.Mask = string(r)
	}
}

// surfaceOn reports whether the filter applies to a surface.
func (o TextFilterOptions) surfaceOn(surface string) bool {
	switch surface {
	case SurfaceTyped:
		return o.Typed
	case SurfaceDictated:
		return o.Dictated
	case SurfaceSpeech:
		return o.Speech
	}
	return false
}

// FilterText applies the current word replacements and censor list to text
// headed for surface, when the filter is on for it.
func FilterText(surface, text string) string {
	o := GetConfig().TextFilter
	if !o.surfaceOn(surface) {
		return text
	}
	return o.apply(text, surface == SurfaceSpeech)
}

// apply rewrites text. Replacements run first, in order, so a replacement
// cannot bring back a censored word. For speech, censored words are left out
// instead of masked, since a voice would read the mask characters aloud.
func (o TextFilterOptions) apply(text string, speech bool) string {
	if text == "" {
		return text
	}
	out := text
	for _, r := range o.Replace {
		if !r.On || strings.TrimSpace(r.From) == "" {
			continue
		}
		re, regex := filterPattern(r.From)
		if re == nil {
			continue
		}
		if regex {
			out = re.ReplaceAllString(out, r.To)
			continue
		}
		to := r.To
		out = re.ReplaceAllStringFunc(out, func(m string) string { return matchCase(m, to) })
	}
	changed := false
	for _, w := range o.Censor {
		re, _ := filterPattern(w)
		if re == nil {
			continue
		}
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			changed = true
			if speech {
				return ""
			}
			return maskWord(m, o.Mask)
		})
	}
	if speech && changed {
		out = strings.Join(strings.Fields(out), " ")
	}
	return out
}

// maskWord covers every letter and digit of m, keeping spaces and
// punctuation so the shape of a phrase stays readable.
func maskWord(m, mask string) string {
	var b strings.Builder
	for _, r := range m {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			b.WriteString(mask)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchCase gives a plain replacement the capitalisation of what it
// replaced: "BRB" becomes "BE RIGHT BACK", "Brb" becomes "Be right back".
func matchCase(matched, to string) string {
	letters, upper := 0, 0
	for _, r := range matched {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters > 1 && upper == letters && strings.ToLower(to) == to {
		return strings.ToUpper(to)
	}
	first, _ := utf8.DecodeRuneInString(matched)
	tf, size := utf8.DecodeRuneInString(to)
	if unicode.IsUpper(first) && unicode.IsLower(tf) {
		return string(unicode.ToUpper(tf)) + to[size:]
	}
	return to
}

// isRegexPattern reports whether p is written as /regex/.
func isRegexPattern(p string) bool {
	return len(p) >= 3 && strings.HasPrefix(p, "/") && strings.HasSuffix(p, "/")
}

// filterPattern compiles a filter entry: /regex/ as written, plain text as a
// case-insensitive whole word (word edges only where the text starts or ends
// with a letter or digit, so "c++" and ":)" still match).
func filterPattern(p string) (re *regexp.Regexp, regex bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil, false
	}
	if isRegexPattern(p) {
		re, err := cachedRegexp(p[1 : len(p)-1])
		if err != nil {
			return nil, true
		}
		return re, true
	}
	expr := regexp.QuoteMeta(p)
	// \s+ inside a phrase also matches a double space.
	expr = strings.Join(strings.Fields(expr), `\s+`)
	first, _ := utf8.DecodeRuneInString(p)
	last, _ := utf8.DecodeLastRuneInString(p)
	if isWordRune(first) {
		expr = `\b` + expr
	}
	if isWordRune(last) {
		expr += `\b`
	}
	re, err := cachedRegexp(expr)
	if err != nil {
		return nil, false
	}
	return re, false
}

// isWordRune is what \b treats as a word character. Go's \b is ASCII only,
// so a plain pattern that starts or ends with another letter gets no edge.
func isWordRune(r rune) bool {
	return r < utf8.RuneSelf && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))
}

// TextFilterErrors returns, by entry, why a /regex/ entry cannot be used:
// "replace.3" or "censor.0" mapped to the error. Empty when all are fine.
func TextFilterErrors(o TextFilterOptions) map[string]string {
	out := map[string]string{}
	check := func(key, p string) {
		p = strings.TrimSpace(p)
		if !isRegexPattern(p) {
			return
		}
		if _, err := cachedRegexp(p[1 : len(p)-1]); err != nil {
			out[key] = strings.TrimPrefix(err.Error(), "error parsing regexp: ")
		}
	}
	for i, r := range o.Replace {
		check("replace."+strconv.Itoa(i), r.From)
	}
	for i, w := range o.Censor {
		check("censor."+strconv.Itoa(i), w)
	}
	return out
}
