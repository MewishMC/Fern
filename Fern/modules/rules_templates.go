package modules

import (
	"errors"
	"strings"
)

// Ready-made rules the panel can add in one click.

// UseGroupInWorld makes the custom messages use group while you are in
// world, by adding a rule "in this world → use this group, undo on leave".
// If a rule for that world already sets a group, it is pointed at group
// instead of adding another. An empty group means every hearted message.
// It returns the rule's name and whether it was new.
func UseGroupInWorld(group string, w WorldInfo) (name string, created bool, err error) {
	group = strings.TrimSpace(group)
	if w.ID == "" {
		return "", false, errors.New("join a world first")
	}
	world := w.Name
	if world == "" {
		world = w.ID
	}
	label := group
	if label == "" {
		label = "All messages"
	}
	UpdateConfig(func(c *Config) {
		if group != "" && !hasGroup(c.TextOptions.Groups, group) {
			err = errors.New("there is no group called " + group)
			return
		}
		for _, g := range c.TextOptions.Groups {
			if strings.EqualFold(g, group) {
				group = g
				label = g
			}
		}
		name = RuneLimit(label+" in "+world, 60)
		for i := range c.Rules.List {
			r := &c.Rules.List[i]
			if r.Trigger.Type != TriggerWorld || r.Trigger.Params.String("world") != w.ID {
				continue
			}
			for j := range r.Actions {
				if r.Actions[j].Type == ActionGroup {
					r.Actions[j].Params["group"] = group
					r.Enabled, r.Undo = true, true
					r.Name = name
					return
				}
			}
		}
		if len(c.Rules.List) >= MaxRules {
			err = errors.New("Fern keeps up to 100 rules")
			return
		}
		c.Rules.List = append(c.Rules.List, Rule{
			ID:      newRuleID(),
			Name:    name,
			Enabled: true,
			Undo:    true,
			Trigger: RuleStep{Type: TriggerWorld, Params: RuleParams{"world": w.ID}},
			Actions: []RuleStep{{Type: ActionGroup, Params: RuleParams{"group": group, "show": true}}},
		})
		created = true
	})
	if err == nil {
		NudgeRules()
	}
	return name, created, err
}
