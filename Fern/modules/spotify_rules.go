package modules

import (
	"errors"
	"math"
	"strings"
)

// Rule triggers, actions and the device list for Spotify. Actions only check
// their settings on the rules goroutine and make the web request in the
// background, since the engine lock is held while they run.

// IDs of the Spotify triggers and actions.
const (
	TriggerSpotifyTrack  = "spotify.track"
	TriggerSpotifyLiked  = "spotify.liked"
	TriggerSpotifyDevice = "spotify.device"

	ActionSpotifyLike    = "spotify.like"
	ActionSpotifyShuffle = "spotify.shuffle"
	ActionSpotifyRepeat  = "spotify.repeat"
	ActionSpotifyVolume  = "spotify.volume"
	ActionSpotifyDevice  = "spotify.device"
	ActionSpotifyQueue   = "spotify.queue"

	SourceSpotifyDevices = "spotify_devices"
)

const spotifyCategory = "Spotify"

// spotifyRuleRun is how actions start their request; tests run it inline.
var spotifyRuleRun = func(fn func() error) {
	go func() {
		if err := fn(); err != nil {
			spotifyNotify(err.Error(), true)
		}
	}()
}

// spotifyReady is the quick check an action makes before it is queued.
func spotifyReady() error {
	o := GetConfig().SpotifyOptions
	switch {
	case !o.Enabled:
		return errors.New("the Spotify link is off")
	case GetSecret(SecretSpotifyRefresh) == "" && GetSecret(SecretSpotifyToken) == "":
		return errors.New("sign in with Spotify first")
	}
	return nil
}

func init() {
	onOffToggle := []RuleOption{{spotifyOn, "On"}, {spotifyOff, "Off"}, {spotifyToggle, "Toggle"}}

	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSpotifyTrack, Label: "Spotify song changes", Category: spotifyCategory,
		Description: "When Spotify starts a new song. {name} in a chat message is the song.",
		Fields: []RuleField{
			{Key: "title", Label: "Song", Type: FieldText, Hint: "Empty for any; /regex/ works", Placeholder: "any song", MaxLen: 200},
			{Key: "artist", Label: "Artist", Type: FieldText, Hint: "Empty for any; /regex/ works", Placeholder: "any artist", MaxLen: 200},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return MatchText(p.String("title"), ev.Data["title"]) && MatchText(p.String("artist"), ev.Data["artist"])
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSpotifyLiked, Label: "Song is in Liked Songs", Category: spotifyCategory,
		Description: "While the Spotify song playing is one you liked",
		Fields:      []RuleField{},
		Check: func(RuleParams) bool {
			p := spotifySnapshot()
			return p.active && p.likedKnown && p.liked
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSpotifyDevice, Label: "Spotify plays on a device", Category: spotifyCategory,
		Description: "While Spotify plays on the device named",
		Fields: []RuleField{
			{Key: "device", Label: "Device", Type: FieldText, Source: SourceSpotifyDevices, Hint: "Part of the name; /regex/ works", MaxLen: 100},
		},
		Check: func(p RuleParams) bool {
			s := spotifySnapshot()
			return s.active && s.device.Name != "" && MatchText(p.String("device"), s.device.Name)
		},
	}))

	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyLike, Label: "Like the Spotify song", Category: spotifyCategory,
		Description: "Adds the playing song to Liked Songs, removes it, or flips it",
		Fields: []RuleField{
			{Key: "mode", Label: "Liked", Type: FieldSelect, Options: onOffToggle, Default: spotifyOn},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			mode := p.String("mode")
			spotifyRuleRun(func() error { _, err := SpotifySetLiked(mode); return err })
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyShuffle, Label: "Spotify shuffle", Category: spotifyCategory,
		Description: "Turns shuffle on, off or over. Undo puts it back.",
		Fields: []RuleField{
			{Key: "mode", Label: "Shuffle", Type: FieldSelect, Options: onOffToggle, Default: spotifyOn},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			before := spotifySnapshot().shuffle
			want := p.String("mode") == spotifyOn
			if p.String("mode") == spotifyToggle {
				want = !before
			}
			spotifyRuleRun(func() error { _, err := SpotifySetShuffle(onOff(want)); return err })
			return func() {
				if spotifySnapshot().shuffle == want && want != before {
					spotifyRuleRun(func() error { _, err := SpotifySetShuffle(onOff(before)); return err })
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyRepeat, Label: "Spotify repeat", Category: spotifyCategory,
		Description: "Repeat off, the playlist or this song. Undo puts it back.",
		Fields: []RuleField{
			{Key: "mode", Label: "Repeat", Type: FieldSelect, Default: spotifyRepeatContext, Options: []RuleOption{
				{spotifyRepeatOff, "Off"}, {spotifyRepeatContext, "Playlist or album"}, {spotifyRepeatTrack, "This song"}, {spotifyCycle, "Next setting"},
			}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			before := spotifySnapshot().repeat
			want := p.String("mode")
			if want == spotifyCycle {
				want = nextRepeat(before)
			}
			spotifyRuleRun(func() error { _, err := SpotifySetRepeat(want); return err })
			return func() {
				if spotifySnapshot().repeat == want && want != before && before != "" {
					spotifyRuleRun(func() error { _, err := SpotifySetRepeat(before); return err })
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyVolume, Label: "Spotify volume", Category: spotifyCategory,
		Description: "Sets the volume, or turns it up or down. Undo puts it back.",
		Fields: []RuleField{
			{Key: "mode", Label: "Change", Type: FieldSelect, Default: spotifySet, Options: []RuleOption{
				{spotifySet, "Set to"}, {spotifyUp, "Up by"}, {spotifyDown, "Down by"},
			}},
			{Key: "amount", Label: "Percent", Type: FieldNumber, Min: 0, Max: 100, Step: 1, Default: 50},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			s := spotifySnapshot()
			before := s.device.Volume
			mode, amount := p.String("mode"), int(math.Round(p.Number("amount")))
			want := amount
			switch mode {
			case spotifyUp:
				want = before + amount
			case spotifyDown:
				want = before - amount
			}
			if mode != spotifySet && before < 0 {
				return nil, errors.New("the Spotify volume is not known yet")
			}
			want = clampInt(want, 0, 100)
			spotifyRuleRun(func() error { _, err := SpotifySetVolume(spotifySet, want); return err })
			if before < 0 || before == want {
				return nil, nil
			}
			return func() {
				if spotifySnapshot().device.Volume == want {
					spotifyRuleRun(func() error { _, err := SpotifySetVolume(spotifySet, before); return err })
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyDevice, Label: "Play Spotify on a device", Category: spotifyCategory,
		Description: "Moves playback to another Spotify Connect device. Undo moves it back.",
		Fields: []RuleField{
			{Key: "device", Label: "Device", Type: FieldSelect, Source: SourceSpotifyDevices, Placeholder: "Pick a device"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			want := strings.TrimSpace(p.String("device"))
			if want == "" {
				return nil, errors.New("pick a device")
			}
			before := spotifySnapshot().device
			spotifyRuleRun(func() error { _, err := SpotifyTransfer(want); return err })
			if before.ID == "" || strings.EqualFold(before.Name, want) {
				return nil, nil
			}
			return func() {
				if now := spotifySnapshot().device; strings.EqualFold(now.Name, want) {
					spotifyRuleRun(func() error { _, err := SpotifyTransfer(before.ID); return err })
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionSpotifyQueue, Label: "Add to the Spotify queue", Category: spotifyCategory,
		Description: "A song link, a spotify: URI or search words; the first match is queued. {name}, {world} and {profile} are filled in.",
		Fields: []RuleField{
			{Key: "text", Label: "Song", Type: FieldText, Placeholder: "https://open.spotify.com/track/… or words", MaxLen: 200},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			if err := spotifyReady(); err != nil {
				return nil, err
			}
			text := strings.TrimSpace(expandRuleText(p.String("text"), ctx.Event))
			if text == "" {
				return nil, errors.New("say what to add to the queue")
			}
			spotifyRuleRun(func() error {
				added, err := SpotifyQueue(text)
				if err == nil {
					spotifyNotify("Queued "+added, false)
				}
				return err
			})
			return nil, nil
		},
	}))

	RegisterRuleSource(SourceSpotifyDevices, func() []RuleOption {
		list := SpotifyDevices()
		out := make([]RuleOption, 0, len(list))
		for _, d := range list {
			out = append(out, RuleOption{Value: d.Name, Label: d.Name})
		}
		return out
	})
}

func onOff(on bool) string {
	if on {
		return spotifyOn
	}
	return spotifyOff
}
