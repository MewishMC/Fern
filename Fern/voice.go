package main

// The Voice page's state: voice commands heard and the word filter's broken
// patterns. Text to speech and speech to text themselves are in the Assist
// state.

import "Fern/modules"

type voiceState struct {
	// Heard is the last transcript that matched a voice command.
	Heard modules.VoiceHeard `json:"heard"`
	// FilterErrors maps "replace.<i>" or "censor.<i>" to why that /regex/
	// can't be used.
	FilterErrors map[string]string `json:"filterErrors"`
	Limits       map[string]int    `json:"limits"`
}

func currentVoice(cfg modules.Config) voiceState {
	return voiceState{
		Heard:        modules.LastVoiceCommand(),
		FilterErrors: modules.TextFilterErrors(cfg.TextFilter),
		Limits: map[string]int{
			"replace": modules.MaxWordReplacements,
			"censor":  modules.MaxCensorWords,
		},
	}
}
