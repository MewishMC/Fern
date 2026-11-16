package modules

import "testing"

func voiceParams(phrase, match string, ptt, consume bool) RuleParams {
	return SanitizeRuleParams(lookupTrigger(TriggerVoiceCommand).Fields,
		RuleParams{"phrase": phrase, "match": match, "ptt": ptt, "consume": consume})
}

func TestVoiceCommandRegistered(t *testing.T) {
	tr := lookupTrigger(TriggerVoiceCommand)
	if tr == nil || tr.Check != nil || tr.Match == nil {
		t.Fatalf("voice command trigger: %+v", tr)
	}
	p := SanitizeRuleParams(tr.Fields, RuleParams{"phrase": "lights"})
	if p.String("match") != VoiceMatchContains || !p.Bool("consume") || p.Bool("ptt") {
		t.Errorf("defaults = %v", p)
	}
	if !tr.Match(p, RuleEvent{Data: map[string]string{"text": "Turn the lights on.", "ptt": "0"}}) {
		t.Error("Match should accept the phrase")
	}
	for _, id := range []string{ActionSpeak, ActionListen} {
		if lookupAction(id) == nil {
			t.Errorf("action %s not registered", id)
		}
	}
}

func TestMatchVoiceCommand(t *testing.T) {
	cases := []struct {
		name          string
		phrase, match string
		pttOnly       bool
		text          string
		ptt           bool
		ok            bool
		rest          string
	}{
		{"contains", "dance mode", VoiceMatchContains, false, "Okay, dance mode please!", false, true, "please"},
		{"contains needs whole words", "dance", VoiceMatchContains, false, "I like dancers", false, false, ""},
		{"exact ignores punctuation and case", "Dance mode", VoiceMatchExact, false, "dance mode.", false, true, ""},
		{"exact rejects more", "dance mode", VoiceMatchExact, false, "dance mode now", false, false, ""},
		{"starts with gives the rest", "say", VoiceMatchStarts, false, "Say, hello everyone!", false, true, "hello everyone"},
		{"starts with must start", "say", VoiceMatchStarts, false, "I say hello", false, false, ""},
		{"apostrophes", "dont move", VoiceMatchExact, false, "Don't move!", false, true, ""},
		{"regex group", `^switch to (\w+)`, VoiceMatchRegex, false, "Switch to chill.", false, true, "chill"},
		{"regex with slashes", `/lights? (on|off)/`, VoiceMatchRegex, false, "light off", false, true, "off"},
		{"regex no group", `muted?`, VoiceMatchRegex, false, "mute, now", false, true, "now"},
		{"broken regex", `(`, VoiceMatchRegex, false, "(", false, false, ""},
		{"ptt only rejects a click", "dance", VoiceMatchContains, true, "dance", false, false, ""},
		{"ptt only accepts holding", "dance", VoiceMatchContains, true, "dance", true, true, ""},
		{"empty phrase", "", VoiceMatchContains, false, "anything", false, false, ""},
		{"unicode", "café", VoiceMatchContains, false, "Le CAFÉ, s'il vous plaît", false, true, "sil vous plaît"},
	}
	for _, tc := range cases {
		ok, rest := matchVoiceCommand(voiceParams(tc.phrase, tc.match, tc.pttOnly, true), tc.text, tc.ptt)
		if ok != tc.ok || rest != tc.rest {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.name, ok, rest, tc.ok, tc.rest)
		}
	}
}

func TestVoiceCommandHits(t *testing.T) {
	rule := func(id, name string, on bool, phrase string, consume bool) Rule {
		return Rule{ID: id, Name: name, Enabled: on, Trigger: RuleStep{Type: TriggerVoiceCommand, Params: voiceParams(phrase, VoiceMatchStarts, false, consume)}}
	}
	o := RulesOptions{Enabled: true, List: []Rule{
		rule("a", "Say it", true, "say", false),
		rule("b", "", true, "say", true),
		rule("c", "Off", false, "say", true),
		rule("d", "Other", true, "lights", true),
		{ID: "e", Enabled: true, Trigger: RuleStep{Type: TriggerStarted}},
	}}
	hits, rest := voiceCommandHits(o, "say hi there", false)
	if len(hits) != 2 || hits[0].Rule != "a" || hits[0].Consume || hits[1].Rule != "b" || !hits[1].Consume {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[1].Name != "“say”" {
		t.Errorf("unnamed rule is shown by its phrase, got %q", hits[1].Name)
	}
	if rest != "hi there" {
		t.Errorf("rest = %q", rest)
	}
	o.Enabled = false
	if hits, _ := voiceCommandHits(o, "say hi", false); len(hits) != 0 {
		t.Errorf("rules switched off still matched: %+v", hits)
	}
}

func TestHandleVoiceCommandConsumes(t *testing.T) {
	c := DefaultConfig()
	c.Rules.List = []Rule{{ID: "r1", Name: "Lights", Enabled: true,
		Trigger: RuleStep{Type: TriggerVoiceCommand, Params: voiceParams("lights", VoiceMatchContains, false, true)}}}
	useTestConfig(t, c)
	before := LastVoiceCommand().Count
	consumed, name := handleVoiceCommand("lights please", true)
	if !consumed || name != "Lights" {
		t.Fatalf("got (%v, %q)", consumed, name)
	}
	heard := LastVoiceCommand()
	if heard.Count != before+1 || heard.Text != "lights please" || !heard.PTT || len(heard.Hits) != 1 {
		t.Errorf("heard = %+v", heard)
	}
	if consumed, _ := handleVoiceCommand("nothing here", false); consumed {
		t.Error("a transcript without a command was consumed")
	}
}
