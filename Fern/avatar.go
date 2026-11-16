package main

// The Avatar page: avatars Fern has seen, the profile and saved parameters
// of each, and every parameter Fern reads and sends.

import (
	"encoding/json"
	"fmt"

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
}

type avatarCurrent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var avatarNotice noticeState

func currentAvatar() avatarState {
	return avatarState{
		Current: avatarCurrent{ID: modules.CurrentAvatarID(), Name: modules.CurrentAvatarName()},
		Avatars: orEmpty(modules.AvatarEntries()),
		Params:  orEmpty(modules.CurrentAvatarParams(maxPanelParams)),
		Sent:    modules.AvatarSent(),
		Auto:    modules.CurrentAvatarAuto(),
		Notice:  readNotice(&avatarNotice),
		Limits:  map[string]int{"params": 64},
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
