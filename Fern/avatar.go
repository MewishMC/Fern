package main

// The Avatar page: avatars Fern has seen, the profile and saved parameters
// of each, and every parameter Fern reads and sends.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"Fern/modules"
)

// maxPanelParams caps how many of the current avatar's parameters the page
// lists.
const maxPanelParams = 300

type avatarState struct {
	Current avatarCurrent                      `json:"current"`
	Avatars []modules.AvatarEntry              `json:"avatars"`
	Params  []modules.AvatarParamRow           `json:"params"`
	Sent    map[string]modules.AvatarSentValue `json:"sent"`
	Auto    modules.AvatarAutoStatus           `json:"auto"`
	Notice  noticeState                        `json:"notice"`
	Limits  map[string]int                     `json:"limits"`
	// Selected is the avatar the "This avatar" section shows.
	Selected avatarSelected `json:"selected"`
	// Catalog is the avatar picker's list, only while it is open.
	Catalog      []modules.AvatarCatalogItem `json:"catalog"`
	CatalogTotal int                         `json:"catalogTotal"`
}

// avatarSelected is the picked avatar and the parameters its OSC config
// lists (or, without one, the ones VRChat sent while you wear it).
type avatarSelected struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Worn   bool   `json:"worn"`
	Known  bool   `json:"known"`  // Fern has an entry for it
	Config bool   `json:"config"` // VRChat's OSC config was found
	// Params matching the page's filter, up to maxSelectedParams, of
	// ParamTotal.
	Params     []avatarParamEdit `json:"params"`
	ParamTotal int               `json:"paramTotal"`
	ParamCount int               `json:"paramCount"` // before the filter
}

// avatarParamEdit is one row of the parameter editor.
type avatarParamEdit struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Input bool   `json:"input"`           // can be set over OSC
	Value any    `json:"value,omitempty"` // live, while worn
}

const (
	maxSelectedParams = 200
	maxCatalogItems   = 60
)

// avatarView is what the panel picked: the avatar ("" follows the one you
// wear), the picker's search and the parameter filter.
var (
	avatarViewMu  sync.Mutex
	avatarPick    string
	avatarBrowse  bool
	avatarQuery   string
	avatarPFilter string
)

// avatarEdit is the "send" and "saveValue" actions' config.
type avatarEdit struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	Value float64 `json:"value"`
}

// avatarBrowseCfg is the "browse" action's config.
type avatarBrowseCfg struct {
	Open  bool   `json:"open"`
	Query string `json:"query"`
}

func currentSelected(worn string) avatarSelected {
	avatarViewMu.Lock()
	id, filter := avatarPick, strings.ToLower(strings.TrimSpace(avatarPFilter))
	avatarViewMu.Unlock()
	if id == "" {
		id = worn
	}
	sel := avatarSelected{ID: id, Worn: id != "" && id == worn, Params: []avatarParamEdit{}}
	if id == "" {
		return sel
	}
	sel.Name = modules.AvatarDisplayName(id)
	_, sel.Known = modules.AvatarInfo(id)
	var rows []avatarParamEdit
	live := map[string]any{}
	if sel.Worn {
		for _, p := range modules.CurrentAvatarParams(0) {
			live[p.Name] = p.Value
		}
	}
	if cfg, ok := modules.AvatarConfig(id); ok {
		sel.Config = true
		for _, p := range cfg.Params {
			rows = append(rows, avatarParamEdit{Name: p.Name, Type: p.Type, Input: p.Input})
		}
	} else if sel.Worn {
		for _, p := range modules.CurrentAvatarParams(0) {
			rows = append(rows, avatarParamEdit{Name: p.Name, Type: p.Type, Input: true})
		}
	}
	sel.ParamCount = len(rows)
	for _, r := range rows {
		if filter != "" && !strings.Contains(strings.ToLower(r.Name), filter) {
			continue
		}
		sel.ParamTotal++
		if len(sel.Params) < maxSelectedParams {
			r.Value = live[r.Name]
			sel.Params = append(sel.Params, r)
		}
	}
	return sel
}

type avatarCurrent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var avatarNotice noticeState

func currentAvatar() avatarState {
	worn := modules.CurrentAvatarID()
	avatarViewMu.Lock()
	browse, query := avatarBrowse, avatarQuery
	avatarViewMu.Unlock()
	catalog, total := []modules.AvatarCatalogItem{}, 0
	if browse {
		catalog, total = modules.AvatarCatalog(query, maxCatalogItems)
	}
	return avatarState{
		Current:      avatarCurrent{ID: worn, Name: modules.AvatarDisplayName(worn)},
		Selected:     currentSelected(worn),
		Catalog:      orEmpty(catalog),
		CatalogTotal: total,
		Avatars:      orEmpty(modules.AvatarEntries()),
		Params:       orEmpty(modules.CurrentAvatarParams(maxPanelParams)),
		Sent:         modules.AvatarSent(),
		Auto:         modules.CurrentAvatarAuto(),
		Notice:       readNotice(&avatarNotice),
		Limits:       map[string]int{"params": 64},
	}
}

// avatarFlags is the "flags" action's config.
type avatarFlags struct {
	AutoSave bool `json:"autoSave"`
	Restore  bool `json:"restore"`
}

// avatarParamRule is the "paramRule" action's config: a rule "while this
// parameter is on, do this".
type avatarParamRule struct {
	Param  string           `json:"param"`
	Action modules.RuleStep `json:"action"`
}

// handleAvatarCommand runs an Avatar page action. Module is the avatar ID.
func handleAvatarCommand(cmd command) {
	var err error
	done := ""
	switch cmd.Action {
	case "profile":
		if err = modules.SetAvatarProfile(cmd.Module, cmd.Text); err == nil {
			if cmd.Text == "" {
				done = "This avatar no longer switches profile"
			} else {
				done = "This avatar switches to " + cmd.Text
			}
		}
	case "rename":
		err = modules.RenameAvatar(cmd.Module, cmd.Text)
	case "forget":
		if err = modules.ForgetAvatar(cmd.Module); err == nil {
			done = "Forgot the avatar"
		}
	case "select":
		avatarViewMu.Lock()
		if cmd.Module != avatarPick {
			avatarPFilter = ""
		}
		avatarPick = strings.TrimSpace(cmd.Module)
		avatarViewMu.Unlock()
	case "browse":
		var b avatarBrowseCfg
		if err = json.Unmarshal(cmd.Config, &b); err == nil {
			avatarViewMu.Lock()
			avatarBrowse, avatarQuery = b.Open, modules.RuneLimit(b.Query, 100)
			avatarViewMu.Unlock()
		}
	case "paramFilter":
		avatarViewMu.Lock()
		avatarPFilter = modules.RuneLimit(cmd.Text, 100)
		avatarViewMu.Unlock()
	case "send":
		var v avatarEdit
		if err = json.Unmarshal(cmd.Config, &v); err == nil {
			err = modules.SendAvatarValue(cmd.Module, v.Name, v.Type, v.Value)
		}
	case "saveValue":
		var v avatarEdit
		if err = json.Unmarshal(cmd.Config, &v); err == nil {
			if err = modules.SetAvatarSavedValue(cmd.Module, v.Name, v.Type, v.Value); err == nil {
				done = "Saved " + v.Name + "; it's put back when you change into this avatar"
			}
		}
	case "params":
		var names []string
		if err = json.Unmarshal(cmd.Config, &names); err == nil {
			err = modules.SetAvatarSyncParams(cmd.Module, names)
		}
	case "flags":
		var f avatarFlags
		if err = json.Unmarshal(cmd.Config, &f); err == nil {
			err = modules.SetAvatarSyncFlags(cmd.Module, f.AutoSave, f.Restore)
		}
	case "save":
		var n int
		if n, err = modules.SaveAvatarParams(cmd.Module); err == nil {
			done = fmt.Sprintf("Saved %d %s", n, plural(n, "value", "values"))
		}
	case "restore":
		var n int
		if n, err = modules.RestoreAvatarParams(cmd.Module); err == nil {
			done = fmt.Sprintf("Sent %d saved %s", n, plural(n, "value", "values"))
		}
	case "copy":
		if err = modules.CopyAvatarParams(cmd.Module, cmd.Text); err == nil {
			done = "Copied; they're put back when you wear that avatar"
			if cmd.Text == modules.CurrentAvatarID() {
				if n, rerr := modules.RestoreAvatarParams(cmd.Text); rerr == nil {
					done = fmt.Sprintf("Copied and sent %d %s", n, plural(n, "value", "values"))
				}
			}
		}
	case "paramRule":
		var r avatarParamRule
		if err = json.Unmarshal(cmd.Config, &r); err == nil {
			var name string
			if name, err = modules.AddParamRule(r.Param, r.Action); err == nil {
				done = "Added the rule " + name + " (Rules page)"
			}
		}
	default:
		return
	}
	if err != nil {
		setNotice(&avatarNotice, err.Error(), true)
	} else if done != "" {
		setNotice(&avatarNotice, done, false)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
