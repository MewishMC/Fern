package modules

import (
	"errors"
	"strings"
)

// Rule triggers, actions and sources of the avatar automation.

// IDs of the avatar automation's triggers and actions.
const (
	TriggerScreenshot  = "vrchat.screenshot"
	TriggerWorldLoaded = "vrchat.world_loaded"

	ActionMedia         = "media.control"
	ActionAvatarRestore = "avatar.restore"

	SourceAvatars = "avatars"
)

func init() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerScreenshot, Label: "Screenshot taken", Category: "VRChat",
		Description: "You take a picture with VRChat's camera",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerWorldLoaded, Label: "World loaded", Category: "VRChat",
		Description: "A world finishes loading after you join it",
		Fields:      []RuleField{},
	}))

	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionMedia, Label: "Control media", Category: "You",
		Description: "Play, pause or skip what is playing, or change the volume",
		Fields: []RuleField{
			{Key: "action", Label: "Do", Type: FieldSelect, Options: []RuleOption{
				{MediaPlayPause, "Play or pause"}, {"play", "Play"}, {"pause", "Pause"}, {MediaNext, "Next track"},
				{MediaPrev, "Previous track"}, {MediaVolumeUp, "Volume up"}, {MediaVolumeDown, "Volume down"}, {MediaMute, "Mute or unmute"},
			}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			go avatarMediaFn(p.String("action"))
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionAvatarRestore, Label: "Put back saved parameters", Category: "Avatar",
		Description: "Sends the values saved for the avatar you wear (Avatar page)",
		Fields:      []RuleField{},
		Run: func(RuleContext, RuleParams) (func(), error) {
			id := CurrentAvatarID()
			e, ok := AvatarInfo(id)
			if id == "" || !ok || len(e.Values) == 0 {
				return nil, errors.New("nothing is saved for this avatar")
			}
			go sendAvatarValues(avatarParamValues(e))
			return nil, nil
		},
	}))

	RegisterRuleSource(SourceAvatars, func() []RuleOption {
		list := AvatarEntries()
		out := make([]RuleOption, 0, len(list))
		for _, e := range list {
			label := e.Name
			if label == "" {
				label = e.ID
			}
			out = append(out, RuleOption{Value: e.ID, Label: label})
		}
		return out
	})
}

// AddParamRule adds a rule "while the avatar parameter is on, do action",
// for the Avatar page's quick setups. Undo is on, so a held key or an
// opened app is let go when the parameter turns off. It returns the
// rule's name.
func AddParamRule(param string, action RuleStep) (string, error) {
	param = cleanParamName(param)
	if param == "" {
		return "", errors.New("name the parameter")
	}
	def, ok := ruleActionDef(action.Type)
	if !ok {
		return "", errors.New("unknown action")
	}
	action.Params = SanitizeRuleParams(def.Fields, action.Params)
	var what string
	switch action.Type {
	case ActionKeys:
		combo, err := ParseKeyCombo(action.Params.String("keys"))
		if err != nil {
			return "", err
		}
		action.Params["keys"] = combo.Canonical
		what = "press " + combo.Canonical
	case ActionAppOpen:
		path, err := cleanAppPath(action.Params.String("path"))
		if err != nil {
			return "", err
		}
		what = "open " + exeName(path)
	case ActionAppClose:
		if exeName(action.Params.String("app")) == "" {
			return "", errors.New("name the app, e.g. obs64.exe")
		}
		what = "close " + exeName(action.Params.String("app"))
	default:
		what = strings.ToLower(def.Label)
	}
	name := RuneLimit(param+" → "+what, 60)
	_, err := SaveRule(Rule{
		Name:    name,
		Enabled: true,
		Undo:    true,
		Trigger: RuleStep{Type: TriggerAvatarParam, Params: RuleParams{"name": param, "op": "true", "value": 0.0}},
		Actions: []RuleStep{action},
	})
	return name, err
}

func ruleActionDef(id string) (RuleAction, bool) {
	ruleRegMu.RLock()
	defer ruleRegMu.RUnlock()
	a, ok := ruleActions[id]
	if !ok {
		return RuleAction{}, false
	}
	return *a, true
}

func withSource(f RuleField, source string) RuleField {
	f.Source = source
	return f
}
