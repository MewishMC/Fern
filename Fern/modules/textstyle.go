package modules

import "strings"

// Unicode text styles: the same Latin letters and digits drawn from other
// Unicode blocks, which VRChat's chatbox font renders. Each style is a plain
// rune-for-text table so the panel can be handed the exact same tables (see
// TextStyleTables) and both sides style text identically.

// TextStyle describes one style for pickers.
type TextStyle struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TextStyles lists the styles in the order a picker should show them. The
// "plain" style turns styled letters back into ordinary ones.
var TextStyles = []TextStyle{
	{"plain", "Plain"},
	{"smallcaps", "Small caps"},
	{"superscript", "Superscript"},
	{"bold", "Bold"},
	{"italic", "Italic"},
	{"bolditalic", "Bold italic"},
	{"sans", "Sans bold"},
	{"mono", "Monospace"},
	{"double", "Double struck"},
	{"fullwidth", "Fullwidth"},
}

var textStyleTables = buildTextStyles()

// alphabet fills a table from consecutive code points, with exceptions for
// the letters Unicode placed elsewhere (the "holes" in the math alphabets).
func alphabet(table map[rune]string, from rune, first rune, count int, holes map[rune]rune) {
	for i := 0; i < count; i++ {
		r := from + rune(i)
		if hole, ok := holes[r]; ok {
			table[r] = string(hole)
			continue
		}
		table[r] = string(first + rune(i))
	}
}

func fromPairs(plain, styled string) map[rune]string {
	table := map[rune]string{}
	styledRunes := []rune(styled)
	for i, r := range []rune(plain) {
		if i < len(styledRunes) && styledRunes[i] != r {
			table[r] = string(styledRunes[i])
		}
	}
	return table
}

func buildTextStyles() map[string]map[rune]string {
	t := map[string]map[rune]string{}

	small := fromPairs("abcdefghijklmnopqrstuvwxyz", "ᴀʙᴄᴅᴇғɢʜɪᴊᴋʟᴍɴᴏᴘǫʀsᴛᴜᴠᴡxʏᴢ")
	// Loop over the letters, not the map: keys added while ranging over a
	// map may be visited too, and 'K'-'a'+'A' is '+'.
	for r := 'a'; r <= 'z'; r++ {
		if s, ok := small[r]; ok {
			small[r-'a'+'A'] = s
		}
	}
	t["smallcaps"] = small

	// There is no superscript q; capitals use the lower case forms, since
	// most capital superscripts do not exist.
	sup := fromPairs("abcdefghijklmnoprstuvwxyz0123456789+-=()", "ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐⁿᵒᵖʳˢᵗᵘᵛʷˣʸᶻ⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾")
	for r := 'a'; r <= 'z'; r++ {
		if s, ok := sup[r]; ok {
			sup[r-'a'+'A'] = s
		}
	}
	t["superscript"] = sup

	math := func(upper, lower, digits rune, holes map[rune]rune) map[rune]string {
		table := map[rune]string{}
		alphabet(table, 'A', upper, 26, holes)
		alphabet(table, 'a', lower, 26, holes)
		if digits != 0 {
			alphabet(table, '0', digits, 10, nil)
		}
		return table
	}
	t["bold"] = math(0x1D400, 0x1D41A, 0x1D7CE, nil)
	t["italic"] = math(0x1D434, 0x1D44E, 0, map[rune]rune{'h': 0x210E})
	t["bolditalic"] = math(0x1D468, 0x1D482, 0, nil)
	t["sans"] = math(0x1D5D4, 0x1D5EE, 0x1D7EC, nil)
	t["mono"] = math(0x1D670, 0x1D68A, 0x1D7F6, nil)
	t["double"] = math(0x1D538, 0x1D552, 0x1D7D8, map[rune]rune{
		'C': 0x2102, 'H': 0x210D, 'N': 0x2115, 'P': 0x2119, 'Q': 0x211A, 'R': 0x211D, 'Z': 0x2124,
	})

	full := map[rune]string{' ': "　"}
	for r := rune('!'); r <= '~'; r++ {
		full[r] = string(r + 0xFEE0)
	}
	t["fullwidth"] = full

	// plain maps every styled form back to its letter. Small caps and
	// superscript use one form for both cases; those turn back into lower
	// case.
	plain := map[rune]string{}
	for _, table := range t {
		for r, s := range table {
			styled := []rune(s)
			if len(styled) != 1 || styled[0] == r {
				continue
			}
			if prev, taken := plain[styled[0]]; taken && !(r >= 'a' && r <= 'z' && prev >= "A" && prev <= "Z") {
				continue
			}
			plain[styled[0]] = string(r)
		}
	}
	t["plain"] = plain
	return t
}

// StyleText rewrites text in one of the TextStyles. Unknown styles and
// characters a style has no form for are left as they are. Placeholders in
// braces, like {time}, are kept intact so styled templates still work.
func StyleText(text, style string) string {
	table := textStyleTables[style]
	if table == nil || text == "" {
		return text
	}
	var b strings.Builder
	depth := 0
	for _, r := range text {
		switch {
		case r == '{':
			depth++
		case r == '}' && depth > 0:
			depth--
		case depth == 0:
			if s, ok := table[r]; ok {
				b.WriteString(s)
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TextStyleTables returns every style as plain character to styled text, for
// the panel's copy of the styler.
func TextStyleTables() map[string]map[string]string {
	out := make(map[string]map[string]string, len(textStyleTables))
	for name, table := range textStyleTables {
		m := make(map[string]string, len(table))
		for r, s := range table {
			m[string(r)] = s
		}
		out[name] = m
	}
	return out
}
