package modules

// The local control API's settings and the module side of what it can do:
// its token, switching lines, running a single rule action, and the rule
// trigger and action it adds. The HTTP and WebSocket server is in the main
// package (Fern/api.go).

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SecretAPIToken is the control API's token in the secrets store.
const SecretAPIToken = "api_token"

// DefaultAPIPort is where the control API listens unless told otherwise. It
// is outside the range the panel uses, so it stays the same between runs, and
// is not the Spotify sign-in port (1931).
const DefaultAPIPort = 1930

// ControlAPIOptions switches the local control API on. It is kept per PC.
type ControlAPIOptions struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
}

func defaultControlAPI() ControlAPIOptions {
	return ControlAPIOptions{Port: DefaultAPIPort}
}

func (o *ControlAPIOptions) sanitize() {
	if o.Port < 1024 || o.Port > 65535 {
		o.Port = DefaultAPIPort
	}
}

// APIToken is the control API's token, made on first use.
func APIToken() string {
	if t := GetSecret(SecretAPIToken); t != "" {
		return t
	}
	t, err := NewAPIToken()
	if err != nil {
		return ""
	}
	return t
}

// NewAPIToken replaces the control API's token with a fresh random one, so
// every tool using the old one stops working.
func NewAPIToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	t := base64.RawURLEncoding.EncodeToString(b)
	if err := SetSecret(SecretAPIToken, t); err != nil {
		return "", err
	}
	return t, nil
}

// CheckAPIToken reports, in constant time, whether got is the token.
func CheckAPIToken(got string) bool {
	want := GetSecret(SecretAPIToken)
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// FindLine resolves a line by ID or by its name, ignoring case.
func FindLine(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if knownModules[name] {
		return name, true
	}
	for _, id := range DefaultOrder {
		if strings.EqualFold(id, name) || strings.EqualFold(LineLabel(id), name) {
			return id, true
		}
	}
	return "", false
}

// SetLine turns line id on ("on"), off ("off") or the other way ("toggle")
// and returns whether it is on now.
func SetLine(id, state string) (bool, error) {
	if !knownModules[id] {
		return false, fmt.Errorf("unknown line %q", id)
	}
	switch state {
	case "on", "off", "toggle":
	default:
		return false, errors.New(`state must be "on", "off" or "toggle"`)
	}
	on := false
	UpdateConfig(func(c *Config) {
		flag := moduleFlag(&c.DisplayOptions, id)
		switch state {
		case "on":
			*flag = true
		case "off":
			*flag = false
		default:
			*flag = !*flag
		}
		on = *flag
	})
	return on, nil
}

// FindRule resolves a rule by ID or by its name, ignoring case.
func FindRule(name string) (Rule, bool) {
	name = strings.TrimSpace(name)
	list := GetConfig().Rules.List
	for _, r := range list {
		if r.ID == name {
			return r, true
		}
	}
	for _, r := range list {
		if r.Name != "" && strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return Rule{}, false
}

// RunRuleAction runs one registered rule action now, as if a rule called
// "API" had fired with an event carrying data. params are coerced to the
// action's fields like a saved rule's. "Undo when it ends" does not apply.
func RunRuleAction(id string, params RuleParams, data map[string]string) error {
	a := lookupAction(id)
	if a == nil {
		return fmt.Errorf("unknown action %q", id)
	}
	p := SanitizeRuleParams(a.Fields, coerceParams(a.Fields, params))
	rulesEng.mu.Lock()
	defer rulesEng.mu.Unlock()
	ctx := RuleContext{RuleID: "api", RuleName: "API", Event: RuleEvent{Type: TriggerAPI, Data: data}, eng: rulesEng}
	_, err := safeRun(a, ctx, p)
	return err
}

// coerceParams turns text values, as a URL query carries them, into the
// types of the fields: numbers, true/false and comma-separated lists.
func coerceParams(fields []RuleField, in RuleParams) RuleParams {
	out := make(RuleParams, len(in))
	for k, v := range in {
		out[k] = v
	}
	for _, f := range fields {
		s, ok := in[f.Key].(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		switch f.Type {
		case FieldNumber:
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				out[f.Key] = n
			}
		case FieldBool:
			if b, err := strconv.ParseBool(s); err == nil {
				out[f.Key] = b
			} else if s == "on" || s == "yes" {
				out[f.Key] = true
			} else if s == "off" || s == "no" {
				out[f.Key] = false
			}
		case FieldMulti:
			list := []string{}
			for _, part := range strings.Split(s, ",") {
				if part = strings.TrimSpace(part); part != "" {
					list = append(list, part)
				}
			}
			out[f.Key] = list
		}
	}
	return out
}

// Rule trigger and action of the control API and custom lines.
const (
	TriggerAPI       = "api.event"
	ActionCustomLine = "custom_line.set"
)

func init() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerAPI, Label: "Control API call", Category: "Fern",
		Description: "When a tool such as a Stream Deck calls /api/event; see the API page",
		Fields: []RuleField{{Key: "name", Label: "Event name", Type: FieldText, MaxLen: 64,
			Placeholder: "e.g. brb", Hint: "The name= the call sends. Contains, or /regex/. Empty is any call."}},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return MatchText(p.String("name"), ev.Data["name"])
		},
	}))
	lines := make([]RuleOption, 0, MaxCustomLines)
	for i, id := range CustomLineIDs {
		lines = append(lines, RuleOption{Value: id, Label: defaultCustomLine(i).Name})
	}
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionCustomLine, Label: "Set a custom line", Category: "Chatbox",
		Description: "Gives a custom line whose text comes from the API new text; undo clears it",
		Fields: []RuleField{
			{Key: "line", Label: "Line", Type: FieldSelect, Options: lines, Default: ModuleCustom1},
			{Key: "text", Label: "Text", Type: FieldText, MaxLen: 143, Placeholder: "e.g. Playing with {name}", Hint: "{name} is the player or event name, {world} the world, {profile} the profile. Empty clears the line."},
			{Key: "seconds", Label: "Seconds", Type: FieldNumber, Min: 0, Max: 86400, Step: 1, Default: 0.0, Hint: "0 keeps it until it is replaced"},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			id := p.String("line")
			text := strings.TrimSpace(expandRuleText(p.String("text"), ctx.Event))
			if err := SetCustomLineText(id, text, time.Duration(p.Number("seconds"))*time.Second); err != nil {
				return nil, err
			}
			set := CustomLineText(id)
			if set == "" {
				return nil, nil
			}
			return func() {
				if CustomLineText(id) == set {
					_ = SetCustomLineText(id, "", 0)
				}
			}, nil
		},
	}))
}
