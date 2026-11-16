package modules

import (
	"errors"
	"strings"
)

var errNoPage = errors.New("there is no such page")

// Rule hooks for chatbox pages and blocked worlds.
const (
	TriggerPageShown    = "chatbox.page_shown"
	TriggerWorldBlocked = "chatbox.world_blocked"
	ActionShowPage      = "chatbox.page"
	ActionWorldBlock    = "chatbox.world_block"
	SourcePages         = "pages"
)

func init() {
	RegisterRuleSource(SourcePages, func() []RuleOption {
		pages := GetConfig().Motion.Pages
		out := make([]RuleOption, 0, len(pages))
		for _, p := range pages {
			out = append(out, RuleOption{Value: p.Name, Label: p.Name})
		}
		return out
	})
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerPageShown, Label: "Chatbox page shown", Category: "Chatbox",
		Description: "When the chatbox moves on to a page",
		Fields:      []RuleField{{Key: "page", Label: "Page", Type: FieldSelect, Source: SourcePages, Placeholder: "Any page"}},
		Match: func(p RuleParams, ev RuleEvent) bool {
			want := p.String("page")
			return want == "" || strings.EqualFold(want, ev.Data["page"])
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerWorldBlocked, Label: "Chatbox paused in a blocked world", Category: "Chatbox",
		Description: "While you are in a world on the blocked list and the chatbox is paused there",
		Fields:      []RuleField{},
		Check:       func(RuleParams) bool { return worldBlockFor(GetConfig()).On },
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionShowPage, Label: "Show a chatbox page", Category: "Chatbox",
		Description: "Jumps to a page and starts its time over",
		Fields:      []RuleField{{Key: "page", Label: "Page", Type: FieldSelect, Source: SourcePages, Placeholder: "Next page"}},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if !ShowPage(p.String("page")) {
				return nil, errNoPage
			}
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionWorldBlock, Label: "Blocked world: show the chatbox anyway", Category: "Chatbox",
		Description: "Shows the chatbox in the blocked world you are in until Fern closes. Undo pauses it again.",
		Fields: []RuleField{
			{Key: "state", Label: "To", Type: FieldSelect, Options: []RuleOption{{"show", "Show anyway"}, {"pause", "Pause again"}}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			show := p.String("state") != "pause"
			id := currentBlockWorldID()
			if id == "" || WorldBlockOverridden(id) == show {
				return nil, nil
			}
			setWorldBlockOverride(id, show)
			return func() {
				if WorldBlockOverridden(id) == show {
					setWorldBlockOverride(id, !show)
				}
			}, nil
		},
	}))
}
