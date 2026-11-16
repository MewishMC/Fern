package modules

import (
	"net/url"
	"strings"
	"sync"
	"time"
)

// WorldInfo is the world and instance VRChat last joined, from its log.
type WorldInfo struct {
	Name       string `json:"name"`
	WorldID    string `json:"worldId"`
	InstanceID string `json:"instanceId"` // everything after the colon, tags included
	Type       string `json:"type"`       // Public, Friends+, Invite, Group, ...
	Region     string `json:"region"`     // US West, Europe, ...
	Since      int64  `json:"since"`      // unix ms of the join
}

var (
	worldMu  sync.Mutex
	curWorld WorldInfo
)

// CurrentWorld returns the world VRChat is in, or a zero value when unknown.
func CurrentWorld() WorldInfo {
	worldMu.Lock()
	defer worldMu.Unlock()
	return curWorld
}

// parseWorldLine follows world changes in the VRChat log.
func parseWorldLine(line string) {
	switch {
	case strings.Contains(line, "[Behaviour] Joining wrld_"):
		idx := strings.Index(line, "wrld_")
		w := parseInstance(strings.TrimSpace(line[idx:]))
		w.Since = time.Now().UnixMilli()
		worldMu.Lock()
		curWorld = w
		worldMu.Unlock()
	case strings.Contains(line, "[Behaviour] Entering Room:"),
		strings.Contains(line, "[Behaviour] Joining or Creating Room:"):
		name := line[strings.LastIndex(line, "Room:")+len("Room:"):]
		worldMu.Lock()
		curWorld.Name = strings.TrimSpace(name)
		if curWorld.Since == 0 {
			curWorld.Since = time.Now().UnixMilli()
		}
		worldMu.Unlock()
	case strings.Contains(line, "[Behaviour] OnLeftRoom"):
		worldMu.Lock()
		curWorld = WorldInfo{}
		worldMu.Unlock()
	}
}

// parseInstance reads "wrld_x:12345~hidden(usr_y)~region(eu)".
func parseInstance(s string) WorldInfo {
	if sp := strings.IndexAny(s, " \t\r"); sp >= 0 {
		s = s[:sp]
	}
	var w WorldInfo
	id, inst, _ := strings.Cut(s, ":")
	w.WorldID = id
	w.InstanceID = inst
	tags := map[string]string{}
	parts := strings.Split(inst, "~")
	for _, p := range parts[1:] {
		key, val, _ := strings.Cut(p, "(")
		tags[key] = strings.TrimSuffix(val, ")")
	}
	_, canRequest := tags["canRequestInvite"]
	switch {
	case tags["hidden"] != "":
		w.Type = "Friends+"
	case tags["friends"] != "":
		w.Type = "Friends"
	case tags["private"] != "" && canRequest:
		w.Type = "Invite+"
	case tags["private"] != "":
		w.Type = "Invite"
	case tags["group"] != "":
		switch tags["groupAccessType"] {
		case "public":
			w.Type = "Group Public"
		case "plus":
			w.Type = "Group+"
		default:
			w.Type = "Group"
		}
	default:
		w.Type = "Public"
	}
	switch tags["region"] {
	case "", "us":
		w.Region = "US West"
	case "use":
		w.Region = "US East"
	case "eu":
		w.Region = "Europe"
	case "jp":
		w.Region = "Japan"
	default:
		w.Region = strings.ToUpper(tags["region"])
	}
	return w
}

// JoinURL is a vrchat.com link that opens this instance, offered only for
// public instances where anyone may join.
func (w WorldInfo) JoinURL() string {
	if w.WorldID == "" || w.InstanceID == "" || w.Type != "Public" {
		return ""
	}
	return "https://vrchat.com/home/launch?worldId=" + url.QueryEscape(w.WorldID) +
		"&instanceId=" + url.QueryEscape(w.InstanceID)
}
