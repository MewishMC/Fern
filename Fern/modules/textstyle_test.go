package modules

import "testing"

func TestStyleText(t *testing.T) {
	cases := []struct{ text, style, want string }{
		{"AFK", "smallcaps", "ᴀғᴋ"},
		{"5m 12s", "superscript", "⁵ᵐ ¹²ˢ"},
		{"Hi 1", "bold", "𝐇𝐢 𝟏"},
		{"hi", "italic", "ℎ𝑖"},
		{"AFK 5", "fullwidth", "ＡＦＫ　５"},
		{"CQ", "double", "ℂℚ"},
		{"away {time}", "mono", "𝚊𝚠𝚊𝚢 {time}"},
		{"ᴀғᴋ 𝟓ᵐ", "plain", "afk 5m"},
		{"unchanged", "nope", "unchanged"},
	}
	for _, c := range cases {
		if got := StyleText(c.text, c.style); got != c.want {
			t.Errorf("StyleText(%q, %q) = %q, want %q", c.text, c.style, got, c.want)
		}
	}
	tables := TextStyleTables()
	for _, s := range TextStyles {
		if len(tables[s.ID]) == 0 {
			t.Errorf("style %q has no table", s.ID)
		}
	}
}
