package modules

// OBS triggers, actions and option sources for the rules engine.

import "strings"

// IDs of the OBS triggers.
const (
	TriggerOBSOutput       = "obs.output"        // event: the stream or recording started or stopped
	TriggerOBSSceneChanged = "obs.scene_changed" // event: the program scene changed
	TriggerOBSLive         = "obs.live"          // state: streaming and/or recording
	TriggerOBSScene        = "obs.scene"         // state: a matching scene is on
)

// IDs of the OBS actions.
const (
	ActionOBSStream = "obs.stream"
	ActionOBSRecord = "obs.record"
	ActionOBSScene  = "obs.scene"
	ActionOBSSource = "obs.source"
)

// Names of the OBS option sources.
const (
	SourceOBSScenes  = "obs.scenes"
	SourceOBSSources = "obs.sources"
)

const obsCategory = "OBS"

func init() {
	registerOBSRules()
}

func obsConnected() bool {
	obsMu.Lock()
	defer obsMu.Unlock()
	return obsLink.State == linkConnected
}

func registerOBSRules() {
	outputs := []RuleOption{{"stream", "Stream"}, {"record", "Recording"}, {"either", "Stream or recording"}}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerOBSOutput, Label: "OBS started or stopped", Category: obsCategory,
		Description: "When the stream or the recording starts or stops",
		Fields: []RuleField{
			{Key: "output", Label: "Output", Type: FieldSelect, Options: outputs, Default: "stream"},
			{Key: "state", Label: "When it", Type: FieldSelect, Options: []RuleOption{{"started", "Starts"}, {"stopped", "Stops"}}, Default: "started"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			out := p.String("output")
			return (out == "either" || out == ev.Data["output"]) && p.String("state") == ev.Data["state"]
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerOBSSceneChanged, Label: "OBS scene changed", Category: obsCategory,
		Description: "When OBS switches to a matching scene",
		Fields: []RuleField{{Key: "scene", Label: "Scene", Type: FieldText, Source: SourceOBSScenes, MaxLen: 200,
			Hint: "Contains, or /regex/. Empty is any scene.", Placeholder: "e.g. BRB"}},
		Match: func(p RuleParams, ev RuleEvent) bool { return MatchText(p.String("scene"), ev.Data["scene"]) },
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerOBSLive, Label: "OBS is live or recording", Category: obsCategory,
		Description: "While OBS streams or records",
		Fields:      []RuleField{{Key: "output", Label: "Output", Type: FieldSelect, Options: outputs, Default: "stream"}},
		Check: func(p RuleParams) bool {
			if !obsConnected() {
				return false
			}
			switch p.String("output") {
			case "record":
				return obsOutputActive("record")
			case "either":
				return obsOutputActive("record") || obsOutputActive("stream")
			}
			return obsOutputActive("stream")
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerOBSScene, Label: "In an OBS scene", Category: obsCategory,
		Description: "While a matching OBS scene is on",
		Fields: []RuleField{{Key: "scene", Label: "Scene", Type: FieldText, Source: SourceOBSScenes, MaxLen: 200,
			Hint: "Contains, or /regex/.", Placeholder: "e.g. Just chatting"}},
		Check: func(p RuleParams) bool {
			scene := CurrentOBSScene()
			return obsConnected() && scene != "" && MatchText(p.String("scene"), scene)
		},
	}))

	outputAction := func(id, label, output string, extra []RuleOption) RuleAction {
		states := append([]RuleOption{{"start", "Start"}, {"stop", "Stop"}, {"toggle", "Toggle"}}, extra...)
		return RuleAction{
			ID: id, Label: label, Category: obsCategory,
			Description: "Undo puts it back the way it was",
			Fields:      []RuleField{{Key: "state", Label: "To", Type: FieldSelect, Options: states, Default: "start"}},
			Run: func(_ RuleContext, p RuleParams) (func(), error) {
				if !obsConnected() {
					return nil, errOBSOffline
				}
				want := p.String("state")
				before := obsOutputActive(output)
				after := before
				switch want {
				case "start":
					after = true
				case "stop":
					after = false
				case "toggle":
					after = !before
				default:
					// Pause and resume have no undo.
					return nil, OBSOutput(output, want)
				}
				if before == after {
					return nil, nil
				}
				if err := OBSOutput(output, map[bool]string{true: "start", false: "stop"}[after]); err != nil {
					return nil, err
				}
				return func() {
					if obsOutputActive(output) == after {
						_ = OBSOutput(output, map[bool]string{true: "start", false: "stop"}[before])
					}
				}, nil
			},
		}
	}
	mustRegister(RegisterRuleAction(outputAction(ActionOBSStream, "OBS stream", "stream", nil)))
	mustRegister(RegisterRuleAction(outputAction(ActionOBSRecord, "OBS recording", "record",
		[]RuleOption{{"pause", "Pause"}, {"resume", "Resume"}})))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionOBSScene, Label: "Switch OBS scene", Category: obsCategory,
		Description: "Undo switches back to the scene that was on",
		Fields:      []RuleField{{Key: "scene", Label: "Scene", Type: FieldSelect, Source: SourceOBSScenes, Placeholder: "Pick a scene"}},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			scene := p.String("scene")
			before := CurrentOBSScene()
			if before == scene {
				return nil, nil
			}
			if err := OBSSetScene(scene); err != nil {
				return nil, err
			}
			return func() {
				if before != "" && CurrentOBSScene() == scene {
					_ = OBSSetScene(before)
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionOBSSource, Label: "Show or hide an OBS source", Category: obsCategory,
		Description: "Undo puts the source back if nothing else changed it",
		Fields: []RuleField{
			{Key: "scene", Label: "Scene", Type: FieldSelect, Source: SourceOBSScenes, Placeholder: "Current scene"},
			{Key: "source", Label: "Source", Type: FieldText, Source: SourceOBSSources, MaxLen: 200, Hint: "The source's name in that scene", Placeholder: "e.g. Webcam"},
			{Key: "state", Label: "To", Type: FieldSelect, Options: []RuleOption{{"on", "Show"}, {"off", "Hide"}, {"toggle", "Toggle"}}, Default: "toggle"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			res, err := OBSSetSource(p.String("scene"), p.String("source"), p.String("state"))
			if err != nil {
				return nil, err
			}
			return res.undo, nil
		},
	}))

	RegisterRuleSource(SourceOBSScenes, func() []RuleOption {
		obsMu.Lock()
		defer obsMu.Unlock()
		out := make([]RuleOption, 0, len(obsScenes))
		for _, s := range obsScenes {
			out = append(out, RuleOption{Value: s, Label: s})
		}
		return out
	})
	RegisterRuleSource(SourceOBSSources, func() []RuleOption {
		obsMu.Lock()
		defer obsMu.Unlock()
		out := make([]RuleOption, 0, len(obsItems))
		seen := map[string]bool{}
		for _, it := range obsItems {
			if k := strings.ToLower(it.Name); !seen[k] {
				seen[k] = true
				out = append(out, RuleOption{Value: it.Name, Label: it.Name})
			}
		}
		return out
	})
}
