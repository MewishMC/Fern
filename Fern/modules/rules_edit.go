package modules

import (
	"errors"
	"fmt"
)

// Editing the rule list. Every function goes through UpdateConfig, so the
// result is sanitized and saved, and returns a user-readable error without
// changing anything when it fails.

func ruleIndex(list []Rule, id string) int {
	for i, r := range list {
		if r.ID == id {
			return i
		}
	}
	return -1
}

// SaveRule replaces the rule with r's ID, or adds r when there is none (an
// empty ID gets a new one). It returns the saved rule's ID.
func SaveRule(r Rule) (string, error) {
	var err error
	id := r.ID
	UpdateConfig(func(c *Config) {
		i := -1
		if id != "" {
			i = ruleIndex(c.Rules.List, id)
		}
		if i >= 0 {
			c.Rules.List[i] = r.clone()
			return
		}
		if len(c.Rules.List) >= MaxRules {
			err = fmt.Errorf("Fern keeps up to %d rules", MaxRules)
			return
		}
		if id == "" || !ruleRowID.MatchString(id) {
			id = newRuleID()
		}
		r = r.clone()
		r.ID = id
		c.Rules.List = append(c.Rules.List, r)
	})
	if err == nil {
		NudgeRules()
	}
	return id, err
}

// DeleteRule removes a rule. Anything it changed is put back if it was
// holding with Undo on.
func DeleteRule(id string) error {
	return editRule(id, func(c *Config, i int) error {
		c.Rules.List = append(c.Rules.List[:i], c.Rules.List[i+1:]...)
		return nil
	})
}

// SetRuleEnabled switches one rule on or off.
func SetRuleEnabled(id string, on bool) error {
	return editRule(id, func(c *Config, i int) error {
		c.Rules.List[i].Enabled = on
		return nil
	})
}

// MoveRule moves a rule up (delta -1) or down (1). Rules are checked in
// list order, so a later rule's actions win over an earlier one's.
func MoveRule(id string, delta int) error {
	return editRule(id, func(c *Config, i int) error {
		j := i + delta
		if j < 0 || j >= len(c.Rules.List) {
			return nil
		}
		c.Rules.List[i], c.Rules.List[j] = c.Rules.List[j], c.Rules.List[i]
		return nil
	})
}

// DuplicateRule copies a rule below itself, switched off. It returns the
// copy's ID.
func DuplicateRule(id string) (string, error) {
	newID := newRuleID()
	err := editRule(id, func(c *Config, i int) error {
		if len(c.Rules.List) >= MaxRules {
			return fmt.Errorf("Fern keeps up to %d rules", MaxRules)
		}
		cp := c.Rules.List[i].clone()
		cp.ID, cp.Enabled = newID, false
		cp.Name = RuneLimit(cp.Name+" copy", 60)
		list := append([]Rule{}, c.Rules.List[:i+1]...)
		list = append(list, cp)
		c.Rules.List = append(list, c.Rules.List[i+1:]...)
		return nil
	})
	return newID, err
}

// SetRulesEnabled is the master switch for every rule.
func SetRulesEnabled(on bool) {
	UpdateConfig(func(c *Config) { c.Rules.Enabled = on })
	NudgeRules()
}

func editRule(id string, fn func(c *Config, i int) error) error {
	var err error
	UpdateConfig(func(c *Config) {
		i := ruleIndex(c.Rules.List, id)
		if i < 0 {
			err = errors.New("that rule no longer exists")
			return
		}
		next := c.Rules.clone()
		tmp := *c
		tmp.Rules = next
		if err = fn(&tmp, i); err == nil {
			c.Rules = tmp.Rules
		}
	})
	if err == nil {
		NudgeRules()
	}
	return err
}
