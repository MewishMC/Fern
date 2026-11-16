package main

import "Fern/modules"

// musicState is the Spotify and Last.fm part of the panel state.
type musicState struct {
	Spotify modules.SpotifyInfo `json:"spotify"`
	LastFM  modules.LastFMInfo  `json:"lastfm"`
}

func currentMusic(cfg modules.Config) musicState {
	sp := modules.CurrentSpotify(cfg.SpotifyOptions)
	sp.Devices = orEmpty(sp.Devices)
	return musicState{Spotify: sp, LastFM: modules.CurrentLastFM()}
}

// handleSpotifyCommand runs {type:"spotify", action, text, enabled}:
// signin, cancel, signout, refresh, like, unlike, toggleLike, shuffle
// (enabled, or text "toggle"), repeat (text off|context|track, empty
// cycles), volume (text percent), device (text id or name) and queue (text).
func handleSpotifyCommand(cmd command) {
	modules.SpotifyCommand(cmd.Action, cmd.Text, cmd.Enabled)
}

// handleLastFMCommand runs {type:"lastfm", action:"refresh"}.
func handleLastFMCommand(cmd command) {
	if cmd.Action == "refresh" {
		modules.LastFMRefreshNow()
	}
}
