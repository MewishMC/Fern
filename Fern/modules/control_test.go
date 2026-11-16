package modules

import (
	"strings"
	"testing"
)

func TestControlAPIAndClipboardDefaults(t *testing.T) {
	d := DefaultConfig()
	if d.ControlAPI.Enabled || d.ControlAPI.Port != DefaultAPIPort {
		t.Errorf("control API default %+v", d.ControlAPI)
	}
	if d.ControlAPI.Port == d.SpotifyOptions.RedirectPort {
		t.Errorf("control API and Spotify sign-in share default port %d", d.ControlAPI.Port)
	}
	if d.Clipboard.Hotkey != "" || d.Clipboard.MaxChars != 143 || d.Clipboard.TooLong != ClipCut || d.Clipboard.Seconds != 10 {
		t.Errorf("clipboard default %+v", d.Clipboard)
	}

	c := DefaultConfig()
	c.ControlAPI.Port = 80
	c.Clipboard = ClipboardOptions{Hotkey: "ctrl+shift+v", MaxChars: 3, TooLong: "explode", Seconds: 9999}
	c.sanitize()
	if c.ControlAPI.Port != DefaultAPIPort {
		t.Errorf("port %d", c.ControlAPI.Port)
	}
	if c.Clipboard.Hotkey != "Ctrl+Shift+V" || c.Clipboard.MaxChars != 8 || c.Clipboard.TooLong != ClipCut || c.Clipboard.Seconds != 600 {
		t.Errorf("clipboard sanitized to %+v", c.Clipboard)
	}
	c.Clipboard.Hotkey = "not a key"
	c.sanitize()
	if c.Clipboard.Hotkey != "" {
		t.Errorf("bad hotkey kept: %q", c.Clipboard.Hotkey)
	}
}

func TestClipboardMessage(t *testing.T) {
	o := ClipboardOptions{MaxChars: 10, TooLong: ClipCut}
	if got, err := ClipboardMessage("  hello\r\n\tworld  ", ClipboardOptions{MaxChars: 11}); err != nil || got != "hello world" {
		t.Errorf("one line: %q, %v", got, err)
	}
	if got, err := ClipboardMessage("a message that is too long", o); err != nil || got != "a message…" {
		t.Errorf("cut: %q, %v", got, err)
	}
	o.TooLong = ClipSkip
	if _, err := ClipboardMessage("a message that is too long", o); err == nil {
		t.Error("skip sent a long message")
	}
	if _, err := ClipboardMessage(" \n ", o); err == nil {
		t.Error("blank clipboard sent")
	}
}

func TestSendClipboard(t *testing.T) {
	c := DefaultConfig()
	c.Clipboard.Seconds = 0
	c.ChatOptions.KeepHistory = false
	useTestConfig(t, c)
	old := readClipboard
	readClipboard = func() (string, error) { return "copied\ntext", nil }
	t.Cleanup(func() { readClipboard = old; ClearHold() })
	got, err := SendClipboard()
	if err != nil || got != "copied text" {
		t.Fatalf("SendClipboard: %q, %v", got, err)
	}
	if text, _, pinned := HoldState(); text != "copied text" || !pinned {
		t.Errorf("hold %q pinned %v", text, pinned)
	}
}

func TestAPIToken(t *testing.T) {
	useTempSecrets(t)
	if CheckAPIToken("") || CheckAPIToken("anything") {
		t.Error("accepted a token before one exists")
	}
	tok := APIToken()
	if len(tok) < 30 || APIToken() != tok {
		t.Fatalf("token %q not stable", tok)
	}
	if !CheckAPIToken(tok) || CheckAPIToken(tok+"x") || CheckAPIToken("") {
		t.Error("token check")
	}
	fresh, err := NewAPIToken()
	if err != nil || fresh == tok || CheckAPIToken(tok) || !CheckAPIToken(fresh) {
		t.Errorf("regenerate: %v", err)
	}
}

func TestSetLineAndFind(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	if id, ok := FindLine("tracker battery"); !ok || id != ModuleBattery {
		t.Errorf("find by name: %q %v", id, ok)
	}
	if id, ok := FindLine("custom2"); !ok || id != ModuleCustom2 {
		t.Errorf("find by id: %q %v", id, ok)
	}
	if _, ok := FindLine("nope"); ok {
		t.Error("found an unknown line")
	}
	on, err := SetLine(ModuleCustom1, "toggle")
	if err != nil || !on || !GetConfig().DisplayOptions.Custom1 {
		t.Errorf("toggle: %v %v", on, err)
	}
	if on, _ := SetLine(ModuleCustom1, "off"); on {
		t.Error("off left it on")
	}
	if _, err := SetLine(ModuleCustom1, "maybe"); err == nil {
		t.Error("bad state accepted")
	}
	if _, err := SetLine("nope", "on"); err == nil {
		t.Error("unknown line accepted")
	}
}

func TestFindRule(t *testing.T) {
	c := DefaultConfig()
	c.Rules.List = []Rule{{ID: "r1", Name: "Stream start", Trigger: RuleStep{Type: TriggerAPI}}}
	useConfig(t, c)
	if r, ok := FindRule("stream START"); !ok || r.ID != "r1" {
		t.Errorf("by name: %+v %v", r, ok)
	}
	if r, ok := FindRule("r1"); !ok || r.Name != "Stream start" {
		t.Errorf("by id: %+v %v", r, ok)
	}
	if _, ok := FindRule("missing"); ok {
		t.Error("found a missing rule")
	}
}

func TestRunRuleActionSetsCustomLine(t *testing.T) {
	resetCustomLines(t)
	useConfig(t, DefaultConfig())
	err := RunRuleAction(ActionCustomLine, RuleParams{"line": ModuleCustom2, "text": "hi {name}", "seconds": 0}, map[string]string{"name": "deck"})
	if err != nil {
		t.Fatal(err)
	}
	if got := CustomLineText(ModuleCustom2); got != "hi deck" {
		t.Errorf("custom line %q", got)
	}
	if err := RunRuleAction("no.such", nil, nil); err == nil {
		t.Error("unknown action ran")
	}
	if err := RunRuleAction(ActionCustomLine, RuleParams{"line": "bogus", "text": "x"}, nil); err != nil {
		// A bad select value falls back to its default line, as in a saved rule.
		t.Errorf("sanitized params should run: %v", err)
	}
}

func TestCoerceParams(t *testing.T) {
	fields := []RuleField{
		{Key: "n", Type: FieldNumber, Min: 0, Max: 100},
		{Key: "b", Type: FieldBool},
		{Key: "m", Type: FieldMulti, Options: weekdays},
		{Key: "s", Type: FieldText},
	}
	p := SanitizeRuleParams(fields, coerceParams(fields, RuleParams{"n": "42", "b": "on", "m": "mon, fri", "s": "7"}))
	if p.Number("n") != 42 || !p.Bool("b") || strings.Join(p.List("m"), ",") != "mon,fri" || p.String("s") != "7" {
		t.Errorf("coerced %v", p)
	}
}

func TestAPITriggerMatches(t *testing.T) {
	trig := lookupTrigger(TriggerAPI)
	if trig == nil || trig.Check != nil {
		t.Fatal("api trigger missing or not an event trigger")
	}
	ev := RuleEvent{Type: TriggerAPI, Data: map[string]string{"name": "BRB"}}
	for pattern, want := range map[string]bool{"": true, "brb": true, "/^b.b$/": true, "live": false} {
		if got := trig.Match(RuleParams{"name": pattern}, ev); got != want {
			t.Errorf("%q: %v", pattern, got)
		}
	}
	if a := lookupAction(ActionCustomLine); a == nil || !strings.Contains(a.Label, "custom") {
		t.Error("custom line action missing")
	}
}
