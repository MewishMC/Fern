package modules

import (
	"slices"
	"testing"
)

func TestModuleEnabledFollowsDisplaySwitches(t *testing.T) {
	d := DisplayOptions{Time: true, ActiveWindow: true, VRChatLogs: true}
	for _, id := range DefaultOrder {
		want := id == ModuleTime || id == ModuleWindow || id == ModuleLogs
		if got := ModuleEnabled(id, d); got != want {
			t.Errorf("ModuleEnabled(%q) = %v, want %v", id, got, want)
		}
	}
	if ModuleEnabled("nope", DisplayOptions{Show: true}) {
		t.Error("unknown module reported as enabled")
	}
}

func TestSanitizeRepairsOutOfRangeValues(t *testing.T) {
	c := Config{}
	c.DisplayOptions.Order = []string{ModuleMedia, "bogus", ModuleMedia, ModuleTime}
	c.DisplayOptions.Separator = "weird"
	c.LoadingOptions.Text = "  brb ...  "
	c.MediaOptions.ProgressStyle = "sparkles"
	c.OSCOptions.Port = 70000
	c.sanitize()

	if c.DisplayOptions.Order[0] != ModuleMedia || c.DisplayOptions.Order[1] != ModuleTime ||
		len(c.DisplayOptions.Order) != len(DefaultOrder) {
		t.Errorf("order = %v", c.DisplayOptions.Order)
	}
	checks := []struct {
		name      string
		got, want any
	}{
		{"separator", c.DisplayOptions.Separator, "\n"},
		{"max chars", c.DisplayOptions.MaxChars, 32},
		{"max lines", c.DisplayOptions.MaxLines, 1},
		{"loading rate", c.LoadingOptions.RateMS, 1000},
		{"loading text", c.LoadingOptions.Text, "brb "},
		{"loading dots", c.LoadingOptions.Dots, 6},
		{"text speed", c.TextOptions.Speed, 400},
		{"media max chars", c.MediaOptions.MaxChars, 4},
		{"progress style", c.MediaOptions.ProgressStyle, "both"},
		{"afk message", c.AfkOptions.AFKMessage, "AFK"},
		{"timer style", c.AfkOptions.TimerStyle, "full"},
		{"osc host", c.OSCOptions.Host, "127.0.0.1"},
		{"osc port", c.OSCOptions.Port, 9000},
	}
	for _, ck := range checks {
		if ck.got != ck.want {
			t.Errorf("%s = %#v, want %#v", ck.name, ck.got, ck.want)
		}
	}
}

func TestLeaveLogsNewestFirst(t *testing.T) {
	logMu.Lock()
	left = map[string]LogEvent{"a": {Time: 2, Text: "a"}, "b": {Time: 3, Text: "b"}, "c": {Time: 1, Text: "c"}}
	logMu.Unlock()
	t.Cleanup(func() { left = map[string]LogEvent{} })

	var names []string
	for _, ev := range LeaveLogs() {
		names = append(names, ev.Text)
	}
	if !slices.Equal(names, []string{"b", "a", "c"}) {
		t.Errorf("LeaveLogs order = %v", names)
	}
}
