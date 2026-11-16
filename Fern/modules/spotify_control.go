package modules

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Spotify controls shared by the panel, rules and avatar parameters. Each one
// is a web request, so callers run them off any loop that must stay quick.
// After a change the snapshot is updated straight away, so the chatbox and
// the avatar show it before the next poll confirms it.

// Modes for the toggles.
const (
	spotifyOn     = "on"
	spotifyOff    = "off"
	spotifyToggle = "toggle"
	spotifyCycle  = "cycle"
	spotifySet    = "set"
	spotifyUp     = "up"
	spotifyDown   = "down"
)

// SpotifySetLiked adds the playing track to Liked Songs or removes it. mode
// is on, off or toggle.
func SpotifySetLiked(mode string) (bool, error) {
	p := spotifySnapshot()
	if p.trackID == "" || !isTrackURI(p.trackURI) {
		return false, errors.New("no Spotify song is playing")
	}
	want := mode == spotifyOn
	if mode == spotifyToggle {
		want = !(p.likedKnown && p.liked)
	}
	method := http.MethodPut
	if !want {
		method = http.MethodDelete
	}
	err := spotifyDo(func(c spotifyClient) error {
		_, err := c.call(method, "/me/tracks", url.Values{"ids": {p.trackID}}, nil, nil)
		return err
	})
	if err != nil {
		return false, err
	}
	spotifyUpdate(func(s *spotifyPlayer) {
		if s.trackID == p.trackID {
			s.likedKnown, s.liked = true, want
		}
	})
	spotifyAfterControl()
	return want, nil
}

// SpotifySetShuffle turns shuffle on, off or over.
func SpotifySetShuffle(mode string) (bool, error) {
	p := spotifySnapshot()
	want := mode == spotifyOn
	if mode == spotifyToggle {
		want = !p.shuffle
	}
	err := spotifyDo(func(c spotifyClient) error {
		_, err := c.call(http.MethodPut, "/me/player/shuffle", url.Values{"state": {strconv.FormatBool(want)}}, nil, nil)
		return err
	})
	if err != nil {
		return false, err
	}
	spotifyUpdate(func(s *spotifyPlayer) { s.shuffle = want })
	spotifyAfterControl()
	return want, nil
}

// nextRepeat is the repeat button's order: off, playlist, track.
func nextRepeat(cur string) string {
	switch cur {
	case spotifyRepeatOff, "":
		return spotifyRepeatContext
	case spotifyRepeatContext:
		return spotifyRepeatTrack
	}
	return spotifyRepeatOff
}

// SpotifySetRepeat sets repeat to off, context or track; cycle steps it.
func SpotifySetRepeat(mode string) (string, error) {
	want := mode
	if mode == spotifyCycle {
		want = nextRepeat(spotifySnapshot().repeat)
	}
	switch want {
	case spotifyRepeatOff, spotifyRepeatContext, spotifyRepeatTrack:
	default:
		return "", fmt.Errorf("unknown repeat mode %q", mode)
	}
	err := spotifyDo(func(c spotifyClient) error {
		_, err := c.call(http.MethodPut, "/me/player/repeat", url.Values{"state": {want}}, nil, nil)
		return err
	})
	if err != nil {
		return "", err
	}
	spotifyUpdate(func(s *spotifyPlayer) { s.repeat = want })
	spotifyAfterControl()
	return want, nil
}

// SpotifySetVolume sets the volume in percent, or moves it up or down by
// amount, and returns the new volume.
func SpotifySetVolume(mode string, amount int) (int, error) {
	p := spotifySnapshot()
	if p.active && !p.device.SupportsVolume {
		return 0, fmt.Errorf("%s does not let Spotify change its volume", firstNonEmpty(p.device.Name, "This device"))
	}
	want := amount
	if mode == spotifyUp || mode == spotifyDown {
		if p.device.Volume < 0 || !p.active {
			return 0, errors.New("the Spotify volume is not known yet")
		}
		if mode == spotifyUp {
			want = p.device.Volume + amount
		} else {
			want = p.device.Volume - amount
		}
	}
	want = clampInt(want, 0, 100)
	err := spotifyDo(func(c spotifyClient) error {
		_, err := c.call(http.MethodPut, "/me/player/volume", url.Values{"volume_percent": {strconv.Itoa(want)}}, nil, nil)
		return err
	})
	if err != nil {
		return 0, err
	}
	spotifyUpdate(func(s *spotifyPlayer) { s.device.Volume = want })
	spotifyAfterControl()
	return want, nil
}

// findSpotifyDevice picks a device by id, exact name, or MatchText pattern.
func findSpotifyDevice(list []SpotifyDevice, want string) (SpotifyDevice, bool) {
	want = strings.TrimSpace(want)
	if want == "" {
		return SpotifyDevice{}, false
	}
	for _, d := range list {
		if d.ID == want {
			return d, true
		}
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, want) {
			return d, true
		}
	}
	for _, d := range list {
		if MatchText(want, d.Name) {
			return d, true
		}
	}
	return SpotifyDevice{}, false
}

// SpotifyDevices is the last known device list.
func SpotifyDevices() []SpotifyDevice {
	spotifyMu.Lock()
	defer spotifyMu.Unlock()
	return append([]SpotifyDevice{}, spotifyDevices...)
}

// SpotifyTransfer moves playback to a device, named by id or name. Playback
// keeps its play or pause state.
func SpotifyTransfer(device string) (string, error) {
	var picked SpotifyDevice
	err := spotifyDo(func(c spotifyClient) error {
		d, ok := findSpotifyDevice(SpotifyDevices(), device)
		if !ok {
			if err := spotifyLoadDevices(c); err != nil {
				return err
			}
			if d, ok = findSpotifyDevice(SpotifyDevices(), device); !ok {
				return fmt.Errorf("no Spotify device called %q is online", device)
			}
		}
		picked = d
		body := map[string]any{"device_ids": []string{d.ID}}
		_, err := c.call(http.MethodPut, "/me/player", nil, body, nil)
		return err
	})
	if err != nil {
		return "", err
	}
	spotifyUpdate(func(s *spotifyPlayer) {
		if s.active {
			s.device = picked
			s.device.Active = true
		}
	})
	spotifyMu.Lock()
	for i := range spotifyDevices {
		spotifyDevices[i].Active = spotifyDevices[i].ID == picked.ID
	}
	spotifyMu.Unlock()
	spotifyAfterControl()
	return picked.Name, nil
}

var spotifyLinkRe = regexp.MustCompile(`^https?://open\.spotify\.com/(?:intl-[a-z-]+/)?(track|episode)/([A-Za-z0-9]{22})`)

// spotifyItemURI turns a link or URI into a spotify:track: / spotify:episode:
// URI; ok is false for anything else, which is then searched for.
func spotifyItemURI(text string) (string, bool) {
	text = strings.TrimSpace(text)
	for _, kind := range []string{"track", "episode"} {
		if id, ok := strings.CutPrefix(text, "spotify:"+kind+":"); ok && len(id) == 22 {
			return text, true
		}
	}
	if m := spotifyLinkRe.FindStringSubmatch(text); m != nil {
		return "spotify:" + m[1] + ":" + m[2], true
	}
	return "", false
}

// SpotifyQueue adds a track to the queue: a link, a URI, or the first
// search result for the words. It returns what was added.
func SpotifyQueue(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("say what to add to the queue")
	}
	added := text
	err := spotifyDo(func(c spotifyClient) error {
		uri, ok := spotifyItemURI(text)
		if !ok {
			var res struct {
				Tracks struct {
					Items []struct {
						URI     string `json:"uri"`
						Name    string `json:"name"`
						Artists []struct {
							Name string `json:"name"`
						} `json:"artists"`
					} `json:"items"`
				} `json:"tracks"`
			}
			q := url.Values{"q": {text}, "type": {"track"}, "limit": {"1"}}
			if _, err := c.call(http.MethodGet, "/search", q, nil, &res); err != nil {
				return err
			}
			if len(res.Tracks.Items) == 0 {
				return fmt.Errorf("Spotify found no song for %q", text)
			}
			it := res.Tracks.Items[0]
			uri, added = it.URI, it.Name
			if len(it.Artists) > 0 {
				added += " — " + it.Artists[0].Name
			}
		}
		_, err := c.call(http.MethodPost, "/me/player/queue", url.Values{"uri": {uri}}, nil, nil)
		return err
	})
	if err != nil {
		return "", err
	}
	return added, nil
}

// spotifyAfterControl mirrors the new state to the avatar and asks Spotify
// again shortly.
func spotifyAfterControl() {
	if o := GetConfig().SpotifyOptions; o.Enabled && o.Params {
		spotifySyncParams(o)
	}
	NudgeRules()
	spotifyResyncSoon()
}

// SpotifyCommand runs a panel control in the background and reports the
// result as a notice.
func SpotifyCommand(action, text string, enabled bool) {
	switch action {
	case "signin":
		SpotifyStartSignIn()
		return
	case "cancel":
		SpotifyCancelSignIn()
		return
	case "signout":
		SpotifySignOut()
		return
	case "refresh":
		SpotifyRefreshNow()
		return
	}
	go func() {
		msg, err := spotifyRun(action, text, enabled)
		if err != nil {
			spotifyNotify(err.Error(), true)
			return
		}
		if msg != "" {
			spotifyNotify(msg, false)
		}
	}()
}

func spotifyRun(action, text string, enabled bool) (string, error) {
	switch action {
	case "like", "unlike", "toggleLike":
		mode := map[string]string{"like": spotifyOn, "unlike": spotifyOff, "toggleLike": spotifyToggle}[action]
		liked, err := SpotifySetLiked(mode)
		if err != nil {
			return "", err
		}
		if liked {
			return "Added to Liked Songs", nil
		}
		return "Removed from Liked Songs", nil
	case "shuffle":
		mode := spotifyOff
		if enabled {
			mode = spotifyOn
		}
		if text == spotifyToggle {
			mode = spotifyToggle
		}
		on, err := SpotifySetShuffle(mode)
		if err != nil {
			return "", err
		}
		return "Shuffle " + map[bool]string{true: "on", false: "off"}[on], nil
	case "repeat":
		mode := text
		if mode == "" {
			mode = spotifyCycle
		}
		r, err := SpotifySetRepeat(mode)
		if err != nil {
			return "", err
		}
		return "Repeat: " + repeatLabel(r), nil
	case "volume":
		v, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil {
			return "", errors.New("the volume must be a number")
		}
		_, err = SpotifySetVolume(spotifySet, v)
		return "", err
	case "device":
		name, err := SpotifyTransfer(text)
		if err != nil {
			return "", err
		}
		return "Playing on " + name, nil
	case "queue":
		added, err := SpotifyQueue(text)
		if err != nil {
			return "", err
		}
		return "Queued " + added, nil
	}
	return "", fmt.Errorf("unknown Spotify action %q", action)
}

func repeatLabel(r string) string {
	switch r {
	case spotifyRepeatContext:
		return "playlist"
	case spotifyRepeatTrack:
		return "this song"
	}
	return "off"
}
