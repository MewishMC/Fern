package main

// Chatbox pages and blocked worlds from the panel's side.

import (
	"Fern/modules"
)

type worldBlockState struct {
	modules.WorldBlockStatus
	Source string      `json:"source"` // where the community list comes from
	Notice noticeState `json:"notice"`
}

var worldBlockNotice noticeState

func currentWorldBlock(cfg modules.Config) worldBlockState {
	return worldBlockState{
		WorldBlockStatus: modules.CurrentWorldBlock(cfg),
		Source:           modules.CommunityBlocklistURL,
		Notice:           readNotice(&worldBlockNotice),
	}
}

// handlePageCommand jumps to a chatbox page: {type:"page", text:<name>}, or
// the next one with no name.
func handlePageCommand(cmd command) {
	if !modules.ShowPage(cmd.Text) {
		setNotice(&worldBlockNotice, "There is no page called "+cmd.Text, true)
	}
}

// handleWorldBlockCommand runs a blocked worlds action:
// show (anyway, until Fern closes), pause (again), block / unblock (the
// world you are in) and refresh (download the community list now).
func handleWorldBlockCommand(cmd command) {
	var err error
	done := ""
	switch cmd.Action {
	case "show":
		err, done = modules.SetWorldBlockOverride(true), "The chatbox shows in this world until Fern closes"
	case "pause":
		err, done = modules.SetWorldBlockOverride(false), "The chatbox is paused in this world again"
	case "block":
		err, done = modules.BlockCurrentWorld(true), "This world is on your blocked list"
	case "unblock":
		err, done = modules.BlockCurrentWorld(false), "This world is no longer blocked"
	case "refresh":
		modules.RefreshWorldBlocklist()
		return
	default:
		return
	}
	if err != nil {
		setNotice(&worldBlockNotice, err.Error(), true)
		return
	}
	setNotice(&worldBlockNotice, done, false)
}

// clearsChatbox reports whether the send loop should blank the chatbox
// once: a blocked world just paused it, and the last text is still up.
func clearsChatbox(box modules.Chatbox, body, lastBody string) bool {
	return body == "" && box.Blocked != "" && !box.Hold && lastBody != ""
}
