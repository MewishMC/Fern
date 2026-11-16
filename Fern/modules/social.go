package modules

import (
	"strconv"
	"strings"
	"time"
)

// Shared helpers for the app integrations (Discord, Twitch, Soundpad,
// Voicemod). Each one runs in its own background worker and only publishes a
// snapshot under a mutex, so rendering a line never waits on a pipe, socket or
// web request and a missing app never slows the chatbox down.

// Integration states shown as the badge on each page.
const (
	linkOff       = "off"       // switched off
	linkWaiting   = "waiting"   // on, but the app is not running or not set up
	linkConnected = "connected" // talking to the app
	linkError     = "error"     // the app answered with a problem the user must fix
)

// LinkStatus is one integration's connection state for the panel.
type LinkStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// SocialInfo is the integrations part of the panel state. Secrets only says
// which secrets are set; their values never leave the backend.
type SocialInfo struct {
	Discord  DiscordInfo     `json:"discord"`
	Twitch   TwitchInfo      `json:"twitch"`
	Soundpad SoundpadInfo    `json:"soundpad"`
	Voicemod VoicemodInfo    `json:"voicemod"`
	Secrets  map[string]bool `json:"secrets"`
}

// CurrentSocial gathers every integration's snapshot.
func CurrentSocial(cfg Config) SocialInfo {
	return SocialInfo{
		Discord:  CurrentDiscord(cfg),
		Twitch:   CurrentTwitch(),
		Soundpad: CurrentSoundpad(),
		Voicemod: CurrentVoicemod(),
		Secrets:  SecretsSet(),
	}
}

// fillTemplate replaces {name} placeholders, turns a typed \n into a line
// break and drops lines that end up empty.
func fillTemplate(tmpl string, vals map[string]string) string {
	var b strings.Builder
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] == '{' {
			if end := strings.IndexByte(tmpl[i:], '}'); end > 0 {
				if v, ok := vals[tmpl[i+1:i+end]]; ok {
					b.WriteString(v)
					i += end
					continue
				}
			}
		}
		b.WriteByte(tmpl[i])
	}
	out := strings.ReplaceAll(b.String(), `\n`, "\n")
	lines := strings.Split(out, "\n")
	kept := lines[:0]
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// compactCount shortens big numbers: 999, 1.2k, 12k, 1.5M.
func compactCount(n int) string {
	if n < 0 {
		n = 0
	}
	short := func(v float64, unit string) string {
		if v < 10 {
			s := strconv.FormatFloat(float64(int(v*10))/10, 'f', -1, 64)
			return s + unit
		}
		return strconv.Itoa(int(v)) + unit
	}
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		if n >= 999_950 {
			return "1M"
		}
		return short(float64(n)/1000, "k")
	default:
		return short(float64(n)/1_000_000, "M")
	}
}

// sleepOrKick waits for d, returning early when kick fires.
func sleepOrKick(d time.Duration, kick <-chan struct{}) {
	select {
	case <-time.After(d):
	case <-kick:
	}
}

// poke wakes a worker without blocking.
func poke(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// sanitizeSocial bounds the integration options.
func (c *Config) sanitizeSocial() {
	def := DefaultConfig()
	trim := func(s string, n int) string { return RuneLimit(strings.TrimSpace(s), n) }
	orDefault := func(s *string, d string) {
		if *s == "" {
			*s = d
		}
	}

	d := &c.DiscordOptions
	d.ClientID = onlyRunes(d.ClientID, "0123456789", 24)
	d.RedirectURI = trim(d.RedirectURI, 200)
	d.Details = trim(d.Details, 128)
	d.State = trim(d.State, 128)
	d.LargeImage = trim(d.LargeImage, 300)
	d.LargeText = trim(d.LargeText, 128)
	d.JoinLabel = trim(d.JoinLabel, 32)
	orDefault(&d.JoinLabel, def.DiscordOptions.JoinLabel)
	d.ButtonLabel = trim(d.ButtonLabel, 32)
	d.ButtonURL = trim(d.ButtonURL, 512)
	if !strings.HasPrefix(d.ButtonURL, "https://") {
		d.ButtonURL = ""
	}
	d.Template = trim(d.Template, 200)
	orDefault(&d.Template, def.DiscordOptions.Template)
	d.QuietText = trim(d.QuietText, 40)
	d.NotInVCText = trim(d.NotInVCText, 60)
	d.MaxSpeakers = clampInt(d.MaxSpeakers, 1, 10)
	d.DebounceMS = clampInt(d.DebounceMS, 200, 2000)
	d.MuteText = trim(d.MuteText, 16)
	d.DeafText = trim(d.DeafText, 16)

	t := &c.TwitchOptions
	t.ClientID = onlyRunes(strings.ToLower(t.ClientID), "abcdefghijklmnopqrstuvwxyz0123456789", 64)
	ch := strings.ToLower(strings.TrimSpace(t.Channel))
	ch = strings.TrimPrefix(strings.TrimPrefix(ch, "https://"), "www.")
	ch = strings.TrimPrefix(strings.TrimPrefix(ch, "twitch.tv/"), "@")
	t.Channel = onlyRunes(ch, "abcdefghijklmnopqrstuvwxyz0123456789_", 25)
	t.IntervalSec = clampInt(t.IntervalSec, 15, 3600)
	t.LiveText = trim(t.LiveText, 24)
	t.OfflineText = trim(t.OfflineText, 60)
	t.ViewerLabel = trim(t.ViewerLabel, 24)
	t.GamePrefix = trim(t.GamePrefix, 24)
	t.FollowerLabel = trim(t.FollowerLabel, 24)
	// Spaces around the separator matter, so it is only shortened.
	t.Separator = RuneLimit(strings.ReplaceAll(t.Separator, "\n", " "), 8)
	if t.Separator == "" {
		t.Separator = def.TwitchOptions.Separator
	}
	t.Template = trim(t.Template, 200)

	s := &c.SoundpadOptions
	s.Icon = trim(s.Icon, 4)
	s.DurationSec = clampInt(s.DurationSec, 2, 60)

	v := &c.VoicemodOptions
	v.SoundSec = clampInt(v.SoundSec, 2, 30)
	v.SoundIcon = trim(v.SoundIcon, 4)
	v.VoiceIcon = trim(v.VoiceIcon, 4)
}

// onlyRunes keeps the characters of s found in allowed, up to n of them.
func onlyRunes(s, allowed string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(s) {
		if count >= n {
			break
		}
		if strings.ContainsRune(allowed, r) {
			b.WriteRune(r)
			count++
		}
	}
	return b.String()
}
