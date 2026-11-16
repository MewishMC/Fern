package modules

import "testing"

func TestSanitizeLinePrefixSuffix(t *testing.T) {
	c := DefaultConfig()
	c.DisplayOptions.LinePrefix = "» "
	c.DisplayOptions.LineSuffix = " ✦\r\nmore text that is far too long"
	c.sanitize()
	if c.DisplayOptions.LinePrefix != "» " {
		t.Errorf("prefix %q, the space must stay", c.DisplayOptions.LinePrefix)
	}
	if got, want := c.DisplayOptions.LineSuffix, " ✦more text that"; got != want {
		t.Errorf("suffix %q, want %q", got, want)
	}
}

func TestPrefixSuffixWrapEveryLine(t *testing.T) {
	got := FitLines([]FitLine{plain(ModuleTime, "3:04"), plain(ModuleText, "hi")}, DefaultDropOrder, "\n", "» ", " «", 143, 9)
	if got.Text != "» 3:04 «\n» hi «" {
		t.Errorf("got %q", got.Text)
	}
	got = FitLines([]FitLine{plain(ModuleTime, "3:04"), plain(ModuleText, "hi")}, DefaultDropOrder, " | ", "[", "]", 143, 9)
	if got.Text != "[3:04] | [hi]" {
		t.Errorf("got %q", got.Text)
	}
}
