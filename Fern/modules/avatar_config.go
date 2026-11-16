package modules

import "strings"

// Avatar automation: what Fern reads from and sends to the avatar you wear,
// besides the chatbox. The settings are kept per PC, like Rules, so a profile
// switch (which an avatar change may cause) never changes them. The
// per-avatar data (which profile, saved parameter values) lives in
// avatars.json; see avatar_store.go.

// AvatarOptions is the "Avatar" section of config.json.
type AvatarOptions struct {
	// AutoProfile switches to the profile picked for an avatar when you
	// change into it. FallbackProfile is used for avatars with no pick; ""
	// leaves the profile alone.
	AutoProfile     bool   `json:"auto_profile"`
	FallbackProfile string `json:"fallback_profile"`

	Media  AvatarMediaOptions `json:"media"`
	Events AvatarEventOptions `json:"events"`
	Stats  AvatarStatsOptions `json:"stats"`
	Sync   AvatarSyncSettings `json:"sync"`
}

// AvatarMediaOptions maps avatar parameters to media keys. A parameter acts
// when it turns on: a bool becoming true, an int above 0 or a float above
// 0.5. Playing is sent the other way: true while media plays.
type AvatarMediaOptions struct {
	Enabled    bool   `json:"enabled"`
	PlayPause  string `json:"play_pause"`
	Next       string `json:"next"`
	Previous   string `json:"previous"`
	VolumeUp   string `json:"volume_up"`
	VolumeDown string `json:"volume_down"`
	Mute       string `json:"mute"`
	Playing    string `json:"playing"`
}

// AvatarEventOptions sends what happens in VRChat to the avatar. The pulse
// parameters are bools that are true for PulseMS.
type AvatarEventOptions struct {
	Enabled      bool   `json:"enabled"`
	PlayerJoined string `json:"player_joined"`
	PlayerLeft   string `json:"player_left"`
	WorldLoaded  string `json:"world_loaded"`
	Screenshot   string `json:"screenshot"`
	AFK          string `json:"afk"`          // bool, true while AFK
	PlayerCount  string `json:"player_count"` // int, 0..255
	PulseMS      int    `json:"pulse_ms"`
}

// AvatarStatsOptions sends the time and the PC's load. Floats are 0..1.
type AvatarStatsOptions struct {
	Enabled bool `json:"enabled"`
	// Hour12 makes Hour 1..12 and HourFloat a 12-hour dial.
	Hour12      bool   `json:"hour12"`
	Hour        string `json:"hour"`
	Minute      string `json:"minute"`
	Second      string `json:"second"`
	HourFloat   string `json:"hour_float"`
	MinuteFloat string `json:"minute_float"`
	SecondFloat string `json:"second_float"`
	CPU         string `json:"cpu"`
	GPU         string `json:"gpu"`
	RAM         string `json:"ram"`
}

// AvatarSyncSettings controls putting saved parameter values back.
type AvatarSyncSettings struct {
	// Restore sends an avatar's saved values when you change into it, for
	// avatars that have restoring on.
	Restore bool `json:"restore"`
	// DelayMS waits for VRChat to finish loading the avatar's own values.
	DelayMS int `json:"delay_ms"`
}

const (
	minAvatarPulseMS = 100
	maxAvatarPulseMS = 5000
	minSyncDelayMS   = 200
	maxSyncDelayMS   = 10000
	maxParamNameLen  = 64
)

func defaultAvatarOptions() AvatarOptions {
	return AvatarOptions{
		Media: AvatarMediaOptions{
			PlayPause: "FernMediaPlayPause", Next: "FernMediaNext", Previous: "FernMediaPrevious",
			VolumeUp: "FernVolumeUp", VolumeDown: "FernVolumeDown", Mute: "FernVolumeMute",
			Playing: "FernMediaPlaying",
		},
		Events: AvatarEventOptions{
			PlayerJoined: "FernPlayerJoined", PlayerLeft: "FernPlayerLeft", WorldLoaded: "FernWorldLoaded",
			Screenshot: "FernScreenshot", AFK: "FernAFK", PlayerCount: "FernPlayerCount", PulseMS: 1000,
		},
		Stats: AvatarStatsOptions{
			Hour: "FernHour", Minute: "FernMinute", Second: "FernSecond",
			HourFloat: "FernHourF", MinuteFloat: "FernMinuteF", SecondFloat: "FernSecondF",
			CPU: "FernCPU", GPU: "FernGPU", RAM: "FernRAM",
		},
		Sync: AvatarSyncSettings{Restore: true, DelayMS: 1500},
	}
}

// cleanParamName makes a parameter name safe to send: no prefix, spaces or
// OSC pattern characters, at most 64 characters. Slashes inside the name
// stay, since avatars often group parameters like "VF/Glasses".
func cleanParamName(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), AvatarParamPrefix)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
		case strings.ContainsRune(" #*,?[]{}", r):
		default:
			b.WriteRune(r)
		}
	}
	return RuneLimit(strings.Trim(b.String(), "/"), maxParamNameLen)
}

func (o *AvatarOptions) sanitize() {
	o.FallbackProfile = RuneLimit(strings.TrimSpace(o.FallbackProfile), maxProfileName)
	for _, p := range []*string{
		&o.Media.PlayPause, &o.Media.Next, &o.Media.Previous, &o.Media.VolumeUp, &o.Media.VolumeDown,
		&o.Media.Mute, &o.Media.Playing,
		&o.Events.PlayerJoined, &o.Events.PlayerLeft, &o.Events.WorldLoaded, &o.Events.Screenshot,
		&o.Events.AFK, &o.Events.PlayerCount,
		&o.Stats.Hour, &o.Stats.Minute, &o.Stats.Second, &o.Stats.HourFloat, &o.Stats.MinuteFloat,
		&o.Stats.SecondFloat, &o.Stats.CPU, &o.Stats.GPU, &o.Stats.RAM,
	} {
		*p = cleanParamName(*p)
	}
	if o.Events.PulseMS == 0 {
		o.Events.PulseMS = 1000
	}
	o.Events.PulseMS = clampInt(o.Events.PulseMS, minAvatarPulseMS, maxAvatarPulseMS)
	if o.Sync.DelayMS == 0 {
		o.Sync.DelayMS = 1500
	}
	o.Sync.DelayMS = clampInt(o.Sync.DelayMS, minSyncDelayMS, maxSyncDelayMS)
}
