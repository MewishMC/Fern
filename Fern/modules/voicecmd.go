package modules

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// Voice commands: a rules trigger that fires when dictation hears a phrase.
// The phrase is checked as soon as a clip is transcribed, before translation
// and before the text reaches the Send now box, so a command can keep its
// words out of the chatbox.

// Voice rule IDs.
const (
	TriggerVoiceCommand = "voice.command"
	ActionSpeak         = "voice.speak"
	ActionListen        = "voice.listen"
)

// Voice command match modes.
const (
	VoiceMatchContains = "contains"
	VoiceMatchExact    = "exact"
	VoiceMatchStarts   = "starts"
	VoiceMatchRegex    = "regex"
)

func init() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerVoiceCommand, Label: "I say a voice command", Category: "You",
		Description: "Dictation (the mic button under Send now) heard a phrase. In a chat message, {name} is what you said after the phrase.",
		Fields: []RuleField{
			{Key: "phrase", Label: "Phrase", Type: FieldText, MaxLen: 100, Placeholder: "e.g. dance mode",
				Hint: "Case and punctuation don't matter; with regex, write it without slashes"},
			{Key: "match", Label: "Match", Type: FieldSelect, Default: VoiceMatchContains, Options: []RuleOption{
				{Value: VoiceMatchContains, Label: "Said anywhere"},
				{Value: VoiceMatchExact, Label: "The whole thing I said"},
				{Value: VoiceMatchStarts, Label: "What I said starts with it"},
				{Value: VoiceMatchRegex, Label: "Regular expression"},
			}},
			{Key: "ptt", Label: "Only while holding the mic button", Type: FieldBool,
				Hint: "Push to talk only; a single click that stops on silence is ignored"},
			{Key: "consume", Label: "Don't send it to the chatbox", Type: FieldBool, Default: true,
				Hint: "The words are used for the command and never reach Send now"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			ok, _ := matchVoiceCommand(p, ev.Data["text"], ev.Data["ptt"] == "1")
			return ok
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpeak, Label: "Read text aloud", Category: "Chatbox",
		Description: "Speaks with the text to speech voice on the Assist page",
		Fields: []RuleField{
			{Key: "text", Label: "Text", Type: FieldText, MaxLen: 300, Placeholder: "e.g. Welcome {name}!",
				Hint: "{name} is the player (or what you said after a voice command), {world} the world, {profile} the profile"},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			text := strings.TrimSpace(expandRuleText(p.String("text"), ctx.Event))
			if text == "" {
				return nil, errors.New("the text is empty")
			}
			go SpeakPreview(text)
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionListen, Label: "Start dictation", Category: "Chatbox",
		Description: "Records like a click on the mic button: it stops by itself when you go quiet",
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			go StartListening(false)
			return nil, nil
		},
	}))
}

// normalizeSpeech lowercases text and turns punctuation into spaces, so
// "Dance mode!" and "dance, mode" both read "dance mode".
func normalizeSpeech(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
			return unicode.ToLower(r)
		case r == '\'' || r == '’':
			return -1 // "don't" and "dont" are the same command
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// matchVoiceCommand checks a transcript against a voice command trigger's
// settings. rest is what was said after the phrase (or a regex's first
// group), for {name}.
func matchVoiceCommand(p RuleParams, text string, ptt bool) (ok bool, rest string) {
	phrase := strings.TrimSpace(p.String("phrase"))
	if phrase == "" || strings.TrimSpace(text) == "" {
		return false, ""
	}
	if p.Bool("ptt") && !ptt {
		return false, ""
	}
	if p.String("match") == VoiceMatchRegex {
		expr := phrase
		if isRegexPattern(expr) {
			expr = expr[1 : len(expr)-1]
		}
		re, err := cachedRegexp(expr)
		if err != nil {
			return false, ""
		}
		m := re.FindStringSubmatchIndex(text)
		if m == nil {
			return false, ""
		}
		if len(m) >= 4 && m[2] >= 0 {
			return true, strings.TrimSpace(text[m[2]:m[3]])
		}
		return true, strings.TrimSpace(strings.TrimLeft(text[m[1]:], " ,.;:!?-"))
	}
	said, want := normalizeSpeech(text), normalizeSpeech(phrase)
	if want == "" {
		return false, ""
	}
	padded := " " + said + " "
	switch p.String("match") {
	case VoiceMatchExact:
		return said == want, ""
	case VoiceMatchStarts:
		if said == want {
			return true, ""
		}
		if strings.HasPrefix(said, want+" ") {
			return true, said[len(want)+1:]
		}
		return false, ""
	}
	i := strings.Index(padded, " "+want+" ")
	if i < 0 {
		return false, ""
	}
	return true, strings.TrimSpace(padded[i+len(want)+2:])
}

// VoiceCommandHit is a voice command that matched what was said.
type VoiceCommandHit struct {
	Rule    string `json:"rule"` // rule ID
	Name    string `json:"name"`
	Consume bool   `json:"consume"`
}

// voiceCommandHits lists the enabled voice command rules that match text.
func voiceCommandHits(o RulesOptions, text string, ptt bool) (hits []VoiceCommandHit, rest string) {
	if !o.Enabled {
		return nil, ""
	}
	for _, r := range o.List {
		if !r.Enabled || r.Trigger.Type != TriggerVoiceCommand {
			continue
		}
		ok, after := matchVoiceCommand(r.Trigger.Params, text, ptt)
		if !ok {
			continue
		}
		if len(hits) == 0 {
			rest = after
		}
		name := r.Name
		if name == "" {
			name = "“" + r.Trigger.Params.String("phrase") + "”"
		}
		hits = append(hits, VoiceCommandHit{Rule: r.ID, Name: name, Consume: r.Trigger.Params.Bool("consume")})
	}
	return hits, rest
}

// VoiceHeard is the last transcript that matched a voice command.
type VoiceHeard struct {
	Text  string            `json:"text"`
	At    int64             `json:"at"` // unix ms
	Hits  []VoiceCommandHit `json:"hits"`
	PTT   bool              `json:"ptt"`
	Count uint64            `json:"count"` // commands heard since startup
}

var lastVoiceHeard VoiceHeard // guarded by sttMu

// LastVoiceCommand returns the last transcript that matched a command.
func LastVoiceCommand() VoiceHeard {
	sttMu.Lock()
	defer sttMu.Unlock()
	out := lastVoiceHeard
	out.Hits = append([]VoiceCommandHit{}, lastVoiceHeard.Hits...)
	return out
}

// handleVoiceCommand fires the voice command trigger for a transcript and
// reports whether a matching command keeps the text out of the chatbox, with
// the name to show for it.
func handleVoiceCommand(text string, ptt bool) (consumed bool, name string) {
	hits, rest := voiceCommandHits(GetConfig().Rules, text, ptt)
	flag := "0"
	if ptt {
		flag = "1"
	}
	FireTrigger(TriggerVoiceCommand, map[string]string{"text": text, "ptt": flag, "name": rest})
	if len(hits) == 0 {
		return false, ""
	}
	names := make([]string, 0, len(hits))
	for _, h := range hits {
		names = append(names, h.Name)
		consumed = consumed || h.Consume
	}
	sttMu.Lock()
	lastVoiceHeard = VoiceHeard{Text: text, At: time.Now().UnixMilli(), Hits: hits, PTT: ptt, Count: lastVoiceHeard.Count + 1}
	sttMu.Unlock()
	return consumed, strings.Join(names, ", ")
}
