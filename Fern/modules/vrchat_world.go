package modules

import "net/url"

// PresenceWorld is the instance shown in Discord Rich Presence, taken from
// the VRChat log parser in vrclog.go.
type PresenceWorld struct {
	Name       string `json:"name"`
	WorldID    string `json:"worldId"`
	InstanceID string `json:"instanceId"` // everything after the colon, tags included
	Type       string `json:"type"`       // Public, Friends+, Invite, Group, ...
	Region     string `json:"region"`     // US West, EU, ...
	Since      int64  `json:"since"`      // unix ms of the join
}

// currentPresenceWorld returns the world VRChat is in, or a zero value when
// unknown.
func currentPresenceWorld() PresenceWorld {
	w, ok := CurrentWorld()
	if !ok {
		return PresenceWorld{}
	}
	return PresenceWorld{Name: w.Name, WorldID: w.ID, InstanceID: w.Instance, Type: w.Type, Region: w.Region, Since: w.JoinedAt}
}

// JoinURL is a vrchat.com link that opens this instance, offered only for
// public instances where anyone may join.
func (w PresenceWorld) JoinURL() string {
	if w.WorldID == "" || w.InstanceID == "" || w.Type != "Public" {
		return ""
	}
	return "https://vrchat.com/home/launch?worldId=" + url.QueryEscape(w.WorldID) +
		"&instanceId=" + url.QueryEscape(w.InstanceID)
}
