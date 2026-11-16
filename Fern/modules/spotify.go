package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Spotify Web API adds what Windows media sessions cannot tell: whether
// the track is in your Liked Songs, shuffle and repeat, the device and the
// playlist. It is optional and sits on top of the media line; the track and
// playback time still come from Windows. Fern signs in with the user's own
// Spotify app (client ID) through PKCE, so no client secret is involved, and
// keeps the tokens in the encrypted secrets store.

// SpotifyOptions controls the Spotify Web API link.
type SpotifyOptions struct {
	Enabled      bool   `json:"enabled"`
	ClientID     string `json:"client_id"`
	RedirectPort int    `json:"redirect_port"` // the sign-in callback listens on 127.0.0.1 here
	IntervalSec  int    `json:"interval_sec"`

	// Extras on the media line, shown while the track matches.
	ShowLiked      bool   `json:"show_liked"`
	LikedIcon      string `json:"liked_icon"`
	UnlikedIcon    string `json:"unliked_icon"` // empty shows nothing for a track you have not liked
	ShowShuffle    bool   `json:"show_shuffle"`
	ShuffleIcon    string `json:"shuffle_icon"`
	ShowRepeat     bool   `json:"show_repeat"`
	RepeatIcon     string `json:"repeat_icon"`
	RepeatOneIcon  string `json:"repeat_one_icon"`
	ShowDevice     bool   `json:"show_device"`
	DevicePrefix   string `json:"device_prefix"`
	ShowPlaylist   bool   `json:"show_playlist"`
	PlaylistPrefix string `json:"playlist_prefix"`

	// Params lets avatar parameters drive Spotify, and mirrors the state back
	// so the avatar's toggles match. Empty names are not used.
	Params       bool   `json:"params"`
	ParamLike    string `json:"param_like"`    // bool: liked
	ParamShuffle string `json:"param_shuffle"` // bool: shuffle on
	ParamRepeat  string `json:"param_repeat"`  // int: 0 off, 1 playlist, 2 track
	ParamVolume  string `json:"param_volume"`  // float 0..1
	ParamDevice  string `json:"param_device"`  // int: 1 is the first device in the list
	ParamQueue   string `json:"param_queue"`   // bool: true adds QueueText to the queue
	QueueText    string `json:"queue_text"`    // a track link, URI or search words
}

// DefaultSpotifyRedirectPort is outside the range the panel server uses.
const DefaultSpotifyRedirectPort = 1931

func defaultSpotifyOptions() SpotifyOptions {
	return SpotifyOptions{
		RedirectPort:   DefaultSpotifyRedirectPort,
		IntervalSec:    5,
		ShowLiked:      true,
		LikedIcon:      "♥",
		ShuffleIcon:    "🔀",
		RepeatIcon:     "🔁",
		RepeatOneIcon:  "🔂",
		DevicePrefix:   "on",
		PlaylistPrefix: "from",
		ParamLike:      "SpotifyLike",
		ParamShuffle:   "SpotifyShuffle",
		ParamRepeat:    "SpotifyRepeat",
		ParamVolume:    "SpotifyVolume",
		ParamDevice:    "SpotifyDevice",
		ParamQueue:     "SpotifyQueue",
	}
}

// sanitizeSpotify bounds the Spotify options.
func sanitizeSpotify(o *SpotifyOptions) {
	o.ClientID = onlyRunes(strings.ToLower(o.ClientID), "abcdefghijklmnopqrstuvwxyz0123456789", 64)
	if o.RedirectPort == 0 {
		o.RedirectPort = DefaultSpotifyRedirectPort
	}
	o.RedirectPort = clampInt(o.RedirectPort, 1024, 65535)
	o.IntervalSec = clampInt(o.IntervalSec, 2, 60)
	for _, s := range []*string{&o.LikedIcon, &o.UnlikedIcon, &o.ShuffleIcon, &o.RepeatIcon, &o.RepeatOneIcon} {
		*s = RuneLimit(strings.TrimSpace(*s), 4)
	}
	o.DevicePrefix = RuneLimit(strings.Join(strings.Fields(o.DevicePrefix), " "), 16)
	o.PlaylistPrefix = RuneLimit(strings.Join(strings.Fields(o.PlaylistPrefix), " "), 16)
	for _, s := range []*string{&o.ParamLike, &o.ParamShuffle, &o.ParamRepeat, &o.ParamVolume, &o.ParamDevice, &o.ParamQueue} {
		*s = RuneLimit(strings.TrimPrefix(strings.TrimSpace(*s), AvatarParamPrefix), 128)
	}
	o.QueueText = RuneLimit(strings.Join(strings.Fields(o.QueueText), " "), 200)
}

// Secrets of the Spotify sign-in.
const (
	SecretSpotifyToken   = "spotify_token"
	SecretSpotifyRefresh = "spotify_refresh"
)

// spotifyScopes covers reading playback, the controls and Liked Songs, and
// private playlists for their names.
const spotifyScopes = "user-read-playback-state user-modify-playback-state user-read-currently-playing " +
	"user-library-read user-library-modify playlist-read-private playlist-read-collaborative"

// Repeat states as Spotify names them.
const (
	spotifyRepeatOff     = "off"
	spotifyRepeatContext = "context"
	spotifyRepeatTrack   = "track"
)

// SpotifyDevice is one Spotify Connect device.
type SpotifyDevice struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Active         bool   `json:"active"`
	Volume         int    `json:"volume"` // -1 when the device does not say
	SupportsVolume bool   `json:"supportsVolume"`
}

// SpotifySignIn is a pending "Sign in with Spotify".
type SpotifySignIn struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// SpotifyNotice is the result of the last control, for a toast.
type SpotifyNotice struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
	Bad  bool   `json:"bad"`
}

// SpotifyInfo is what the panel shows.
type SpotifyInfo struct {
	Link        LinkStatus      `json:"link"`
	SignedIn    bool            `json:"signedIn"`
	RedirectURI string          `json:"redirectUri"`
	SignIn      *SpotifySignIn  `json:"signIn"`
	Active      bool            `json:"active"` // a device is playing or paused
	Playing     bool            `json:"playing"`
	Title       string          `json:"title"`
	Artist      string          `json:"artist"`
	Liked       *bool           `json:"liked"` // null while unknown
	Shuffle     bool            `json:"shuffle"`
	Repeat      string          `json:"repeat"`
	Device      SpotifyDevice   `json:"device"`
	Devices     []SpotifyDevice `json:"devices"`
	Playlist    string          `json:"playlist"`
	CheckedAt   int64           `json:"checkedAt"`
	Notice      SpotifyNotice   `json:"notice"`
}

// spotifyPlayer is the last /me/player answer, cut down.
type spotifyPlayer struct {
	active     bool
	playing    bool
	trackID    string
	trackURI   string
	title      string
	artist     string
	shuffle    bool
	repeat     string
	device     SpotifyDevice
	contextURI string
	likedKnown bool
	liked      bool
	playlist   string
	checkedAt  time.Time
}

var (
	spotifyMu        sync.Mutex
	spotifyLink      = LinkStatus{State: linkOff}
	spotifyNow       spotifyPlayer
	spotifyDevices   []SpotifyDevice
	spotifyDevicesAt time.Time
	spotifyPlaylists = map[string]string{} // context URI -> name ("" when Spotify would not say)
	spotifySignInNow *SpotifySignIn
	spotifyNote      SpotifyNotice
	spotifyBackoff   time.Time // no polling before this after a 429
	spotifyKickCh    = make(chan struct{}, 1)
	spotifyTokenMu   sync.Mutex // one refresh at a time

	spotifyAPIBase      = "https://api.spotify.com/v1"
	spotifyAccountsBase = "https://accounts.spotify.com"
	spotifyHTTP         = &http.Client{Timeout: 10 * time.Second}
)

var errSpotifyAuth = errors.New("spotify: token rejected")

// spotifyAPIError is an answer other than success or 401.
type spotifyAPIError struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *spotifyAPIError) Error() string {
	switch {
	case e.Status == http.StatusTooManyRequests:
		return "Spotify asked Fern to slow down"
	case e.Status == http.StatusForbidden && strings.Contains(strings.ToLower(e.Message), "premium"):
		return "This needs Spotify Premium"
	case e.Status == http.StatusNotFound && strings.Contains(strings.ToLower(e.Message), "device"):
		return "No active Spotify device. Start playing on one first."
	case e.Message != "":
		return "Spotify: " + e.Message
	}
	return fmt.Sprintf("Spotify answered HTTP %d", e.Status)
}

func setSpotifyLink(state, detail string) {
	spotifyMu.Lock()
	changed := spotifyLink.State != state || spotifyLink.Detail != detail
	spotifyLink = LinkStatus{State: state, Detail: detail}
	spotifyMu.Unlock()
	if changed {
		NotifyChange()
	}
}

// spotifyNotify records the outcome of a control for the panel.
func spotifyNotify(text string, bad bool) {
	spotifyMu.Lock()
	spotifyNote = SpotifyNotice{Seq: spotifyNote.Seq + 1, Text: text, Bad: bad}
	spotifyMu.Unlock()
	NotifyChange()
}

// SpotifyRedirectURI is what the user registers in their Spotify app.
func SpotifyRedirectURI(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/callback", port)
}

// CurrentSpotify returns the Spotify snapshot for the panel.
func CurrentSpotify(o SpotifyOptions) SpotifyInfo {
	signedIn := GetSecret(SecretSpotifyRefresh) != "" || GetSecret(SecretSpotifyToken) != ""
	spotifyMu.Lock()
	defer spotifyMu.Unlock()
	p := spotifyNow
	info := SpotifyInfo{
		Link:        spotifyLink,
		SignedIn:    signedIn,
		RedirectURI: SpotifyRedirectURI(o.RedirectPort),
		Active:      p.active,
		Playing:     p.playing,
		Title:       p.title,
		Artist:      p.artist,
		Shuffle:     p.shuffle,
		Repeat:      p.repeat,
		Device:      p.device,
		Devices:     append([]SpotifyDevice{}, spotifyDevices...),
		Playlist:    p.playlist,
		Notice:      spotifyNote,
	}
	if info.Repeat == "" {
		info.Repeat = spotifyRepeatOff
	}
	if p.likedKnown {
		liked := p.liked
		info.Liked = &liked
	}
	if !p.checkedAt.IsZero() {
		info.CheckedAt = p.checkedAt.UnixMilli()
	}
	if spotifySignInNow != nil {
		s := *spotifySignInNow
		info.SignIn = &s
	}
	return info
}

func spotifySnapshot() spotifyPlayer {
	spotifyMu.Lock()
	defer spotifyMu.Unlock()
	return spotifyNow
}

// spotifyUpdate changes the snapshot under the lock and wakes the chatbox.
func spotifyUpdate(fn func(p *spotifyPlayer)) {
	spotifyMu.Lock()
	fn(&spotifyNow)
	spotifyMu.Unlock()
	NotifyChange()
}

func resetSpotifyState() {
	spotifyMu.Lock()
	spotifyNow = spotifyPlayer{}
	spotifyDevices = nil
	spotifyDevicesAt = time.Time{}
	spotifyMu.Unlock()
}

// SpotifyRefreshNow polls without waiting for the next interval.
func SpotifyRefreshNow() {
	spotifyMu.Lock()
	spotifyDevicesAt = time.Time{}
	spotifyMu.Unlock()
	poke(spotifyKickCh)
}

// spotifyResyncSoon polls again shortly after a control, once Spotify has
// caught up with it.
func spotifyResyncSoon() {
	time.AfterFunc(1200*time.Millisecond, func() { poke(spotifyKickCh) })
}

// SpotifySignOut forgets the Spotify tokens.
func SpotifySignOut() {
	stopSpotifySignIn()
	_ = SetSecret(SecretSpotifyToken, "")
	_ = SetSecret(SecretSpotifyRefresh, "")
	resetSpotifyState()
	poke(spotifyKickCh)
	NotifyChange()
}

// spotifyClient talks to the Web API and the accounts service.
type spotifyClient struct {
	http     *http.Client
	api      string
	accounts string
	clientID string
	token    string
}

func newSpotifyClient(clientID, token string) spotifyClient {
	return spotifyClient{http: spotifyHTTP, api: spotifyAPIBase, accounts: spotifyAccountsBase, clientID: clientID, token: token}
}

// call sends one Web API request. A 204 leaves out untouched and reports
// the status, so callers can tell "nothing playing" from an answer.
func (c spotifyClient) call(method, path string, q url.Values, body, out any) (int, error) {
	u := c.api + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, u, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	} else if method != http.MethodGet {
		// Spotify wants a length on PUT and POST, even an empty one.
		req.ContentLength = 0
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, errors.New("could not reach Spotify")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return resp.StatusCode, errSpotifyAuth
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		e := &spotifyAPIError{Status: resp.StatusCode}
		var parsed struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &parsed) == nil {
			e.Message = parsed.Error.Message
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			e.RetryAfter = 30 * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
				e.RetryAfter = time.Duration(s) * time.Second
			}
		}
		return resp.StatusCode, e
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, errors.New("Spotify sent something Fern could not read")
		}
	}
	return resp.StatusCode, nil
}

type spotifyTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// token posts to the accounts token endpoint. Spotify's error text, like
// "invalid_grant", is kept; no token ever is.
func (c spotifyClient) tokenRequest(form url.Values) (spotifyTokens, error) {
	var t spotifyTokens
	form.Set("client_id", c.clientID)
	req, err := http.NewRequest(http.MethodPost, c.accounts+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return t, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return t, errors.New("could not reach Spotify")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &e)
		return t, fmt.Errorf("Spotify refused the sign-in: %s", firstNonEmpty(e.Description, e.Error, resp.Status))
	}
	if err := json.Unmarshal(data, &t); err != nil || t.AccessToken == "" {
		return t, errors.New("Spotify sent no token")
	}
	return t, nil
}

func (c spotifyClient) refresh(refreshToken string) (spotifyTokens, error) {
	return c.tokenRequest(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
}

func storeSpotifyTokens(t spotifyTokens) {
	_ = SetSecret(SecretSpotifyToken, t.AccessToken)
	if t.RefreshToken != "" {
		_ = SetSecret(SecretSpotifyRefresh, t.RefreshToken)
	}
}

// spotifyDo runs fn with a working access token, refreshing it once when
// Spotify rejects the current one.
func spotifyDo(fn func(c spotifyClient) error) error {
	o := GetConfig().SpotifyOptions
	if o.ClientID == "" {
		return errors.New("add your Spotify client ID first")
	}
	token := GetSecret(SecretSpotifyToken)
	if token == "" && GetSecret(SecretSpotifyRefresh) == "" {
		return errors.New("sign in with Spotify first")
	}
	if token != "" {
		err := fn(newSpotifyClient(o.ClientID, token))
		if !errors.Is(err, errSpotifyAuth) {
			return err
		}
	}
	token, err := spotifyRefreshToken(o.ClientID, token)
	if err != nil {
		return err
	}
	return fn(newSpotifyClient(o.ClientID, token))
}

// spotifyRefreshToken swaps the refresh token for a new access token, unless
// another goroutine already did since stale was read.
func spotifyRefreshToken(clientID, stale string) (string, error) {
	spotifyTokenMu.Lock()
	defer spotifyTokenMu.Unlock()
	if cur := GetSecret(SecretSpotifyToken); cur != "" && cur != stale {
		return cur, nil
	}
	refresh := GetSecret(SecretSpotifyRefresh)
	if refresh == "" {
		return "", errSpotifyAuth
	}
	t, err := newSpotifyClient(clientID, "").refresh(refresh)
	if err != nil {
		return "", errSpotifyAuth
	}
	storeSpotifyTokens(t)
	return t.AccessToken, nil
}

// spotifyPlayerJSON is the part of GET /me/player Fern reads.
type spotifyPlayerJSON struct {
	Device struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		IsActive       bool   `json:"is_active"`
		VolumePercent  *int   `json:"volume_percent"`
		SupportsVolume bool   `json:"supports_volume"`
	} `json:"device"`
	ShuffleState bool   `json:"shuffle_state"`
	RepeatState  string `json:"repeat_state"`
	IsPlaying    bool   `json:"is_playing"`
	Context      *struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"context"`
	Item *struct {
		ID      string `json:"id"`
		URI     string `json:"uri"`
		Name    string `json:"name"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Show *struct {
			Name string `json:"name"`
		} `json:"show"`
	} `json:"item"`
}

func (j spotifyPlayerJSON) player() spotifyPlayer {
	p := spotifyPlayer{
		active:  true,
		playing: j.IsPlaying,
		shuffle: j.ShuffleState,
		repeat:  j.RepeatState,
		device: SpotifyDevice{
			ID: j.Device.ID, Name: j.Device.Name, Type: j.Device.Type, Active: true,
			Volume: -1, SupportsVolume: j.Device.SupportsVolume,
		},
	}
	if j.Device.VolumePercent != nil {
		p.device.Volume = *j.Device.VolumePercent
	}
	switch p.repeat {
	case spotifyRepeatContext, spotifyRepeatTrack:
	default:
		p.repeat = spotifyRepeatOff
	}
	if j.Context != nil && j.Context.Type == "playlist" {
		p.contextURI = j.Context.URI
	}
	if it := j.Item; it != nil {
		p.trackID, p.trackURI, p.title = it.ID, it.URI, it.Name
		names := make([]string, 0, len(it.Artists))
		for _, a := range it.Artists {
			names = append(names, a.Name)
		}
		p.artist = strings.Join(names, ", ")
		if p.artist == "" && it.Show != nil {
			p.artist = it.Show.Name
		}
	}
	return p
}

// isTrackURI reports whether uri names a song, the only thing Liked Songs holds.
func isTrackURI(uri string) bool { return strings.HasPrefix(uri, "spotify:track:") }

// spotifyPollOnce reads the player and fills in what changed with it: the
// liked state per track, the playlist name per playlist and the devices.
func spotifyPollOnce(c spotifyClient) error {
	var j spotifyPlayerJSON
	status, err := c.call(http.MethodGet, "/me/player", url.Values{"additional_types": {"track,episode"}}, nil, &j)
	if err != nil {
		return err
	}
	prev := spotifySnapshot()
	var p spotifyPlayer
	if status != http.StatusNoContent && j.Device.ID+j.Device.Name != "" {
		p = j.player()
	}
	p.checkedAt = time.Now()

	// Liked state only changes with the track or through Fern, so it is
	// asked once per track.
	if p.trackID != "" && p.trackID == prev.trackID && prev.likedKnown {
		p.likedKnown, p.liked = true, prev.liked
	} else if p.trackID != "" && isTrackURI(p.trackURI) {
		var liked []bool
		if _, err := c.call(http.MethodGet, "/me/tracks/contains", url.Values{"ids": {p.trackID}}, nil, &liked); err == nil && len(liked) == 1 {
			p.likedKnown, p.liked = true, liked[0]
		} else if errors.Is(err, errSpotifyAuth) {
			return err
		}
	}

	if p.contextURI != "" {
		spotifyMu.Lock()
		name, known := spotifyPlaylists[p.contextURI]
		spotifyMu.Unlock()
		if !known {
			name = spotifyPlaylistName(c, p.contextURI)
			spotifyMu.Lock()
			if len(spotifyPlaylists) > 64 {
				clear(spotifyPlaylists)
			}
			spotifyPlaylists[p.contextURI] = name
			spotifyMu.Unlock()
		}
		p.playlist = name
	}

	spotifyMu.Lock()
	needDevices := time.Since(spotifyDevicesAt) > 30*time.Second
	spotifyMu.Unlock()
	if needDevices {
		_ = spotifyLoadDevices(c)
	}

	spotifyMu.Lock()
	spotifyNow = p
	// The device list may be older than the player; keep its active mark honest.
	for i := range spotifyDevices {
		spotifyDevices[i].Active = p.device.ID != "" && spotifyDevices[i].ID == p.device.ID
		if spotifyDevices[i].Active && p.device.Volume >= 0 {
			spotifyDevices[i].Volume = p.device.Volume
		}
	}
	spotifyMu.Unlock()
	if p.trackID != prev.trackID && p.title != "" {
		FireTrigger(TriggerSpotifyTrack, map[string]string{"name": p.title, "title": p.title, "artist": p.artist})
	}
	if p.likedKnown && prev.likedKnown && p.trackID == prev.trackID && p.liked != prev.liked {
		NudgeRules()
	}
	NotifyChange()
	return nil
}

// spotifyPlaylistName asks for a playlist's name; "" when Spotify will not
// say, as for its own generated mixes.
func spotifyPlaylistName(c spotifyClient, uri string) string {
	id, ok := strings.CutPrefix(uri, "spotify:playlist:")
	if !ok || id == "" {
		return ""
	}
	var pl struct {
		Name string `json:"name"`
	}
	if _, err := c.call(http.MethodGet, "/playlists/"+url.PathEscape(id), url.Values{"fields": {"name"}}, nil, &pl); err != nil {
		return ""
	}
	return pl.Name
}

// spotifyLoadDevices refreshes the device list, sorted by name so an avatar
// parameter's device number stays put.
func spotifyLoadDevices(c spotifyClient) error {
	var d struct {
		Devices []struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			Type           string `json:"type"`
			IsActive       bool   `json:"is_active"`
			IsRestricted   bool   `json:"is_restricted"`
			VolumePercent  *int   `json:"volume_percent"`
			SupportsVolume bool   `json:"supports_volume"`
		} `json:"devices"`
	}
	if _, err := c.call(http.MethodGet, "/me/player/devices", nil, nil, &d); err != nil {
		return err
	}
	list := make([]SpotifyDevice, 0, len(d.Devices))
	for _, x := range d.Devices {
		if x.ID == "" || x.IsRestricted {
			continue
		}
		dev := SpotifyDevice{ID: x.ID, Name: x.Name, Type: x.Type, Active: x.IsActive, Volume: -1, SupportsVolume: x.SupportsVolume}
		if x.VolumePercent != nil {
			dev.Volume = *x.VolumePercent
		}
		list = append(list, dev)
	}
	sort.SliceStable(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	spotifyMu.Lock()
	spotifyDevices = list
	spotifyDevicesAt = time.Now()
	spotifyMu.Unlock()
	return nil
}

func startSpotifyWorker() {
	startSpotifyParams()
	go func() {
		for {
			o := GetConfig().SpotifyOptions
			wait := time.Duration(o.IntervalSec) * time.Second
			signedIn := GetSecret(SecretSpotifyRefresh) != "" || GetSecret(SecretSpotifyToken) != ""
			spotifyMu.Lock()
			backoff := time.Until(spotifyBackoff)
			spotifyMu.Unlock()
			switch {
			case !o.Enabled:
				setSpotifyLink(linkOff, "")
				resetSpotifyState()
				wait = 2 * time.Second
			case o.ClientID == "":
				setSpotifyLink(linkWaiting, "Add your Spotify client ID")
				wait = 2 * time.Second
			case !signedIn:
				setSpotifyLink(linkWaiting, "Sign in with Spotify")
				resetSpotifyState()
				wait = 2 * time.Second
			case backoff > 0:
				wait = backoff
			default:
				spotifyPollCycle()
			}
			refreshSpotifyParamSubs(o)
			if o.Enabled && o.Params {
				spotifySyncParams(o)
			}
			sleepOrKick(wait, spotifyKickCh)
		}
	}()
}

// spotifyPollCycle runs one poll and records how it went.
func spotifyPollCycle() {
	err := spotifyDo(spotifyPollOnce)
	var apiErr *spotifyAPIError
	switch {
	case err == nil:
		setSpotifyLink(linkConnected, "")
	case errors.Is(err, errSpotifyAuth):
		setSpotifyLink(linkError, "Spotify rejected the sign-in. Sign in again.")
	case errors.As(err, &apiErr) && apiErr.RetryAfter > 0:
		spotifyMu.Lock()
		spotifyBackoff = time.Now().Add(apiErr.RetryAfter)
		spotifyMu.Unlock()
		setSpotifyLink(linkWaiting, err.Error())
	default:
		setSpotifyLink(linkWaiting, err.Error())
	}
}

// spotifyTitlesMatch reports whether the Windows media title and the
// Spotify track are the same song, so the extras never describe another one.
func spotifyTitlesMatch(windows, spotify string) bool {
	a := strings.ToLower(strings.Join(strings.Fields(windows), " "))
	b := strings.ToLower(strings.Join(strings.Fields(spotify), " "))
	if a == "" || b == "" {
		return false
	}
	// One may carry a suffix the other lacks, like " - Remastered 2011".
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// spotifyMediaExtras is the Spotify part of the media line: liked, shuffle
// and repeat marks, then the device and the playlist.
func spotifyMediaExtras(o SpotifyOptions, info MediaInfo) string {
	if !o.Enabled || !info.Available {
		return ""
	}
	return formatSpotifyExtras(o, spotifySnapshot(), info.Title)
}

func formatSpotifyExtras(o SpotifyOptions, p spotifyPlayer, title string) string {
	if !p.active || !spotifyTitlesMatch(title, p.title) {
		return ""
	}
	var icons []string
	if o.ShowLiked && p.likedKnown {
		if p.liked {
			icons = append(icons, o.LikedIcon)
		} else {
			icons = append(icons, o.UnlikedIcon)
		}
	}
	if o.ShowShuffle && p.shuffle {
		icons = append(icons, o.ShuffleIcon)
	}
	if o.ShowRepeat {
		switch p.repeat {
		case spotifyRepeatContext:
			icons = append(icons, o.RepeatIcon)
		case spotifyRepeatTrack:
			icons = append(icons, o.RepeatOneIcon)
		}
	}
	parts := []string{joinNonEmpty(" ", icons...)}
	if o.ShowDevice && p.device.Name != "" {
		parts = append(parts, joinNonEmpty(" ", o.DevicePrefix, p.device.Name))
	}
	if o.ShowPlaylist && p.playlist != "" {
		parts = append(parts, joinNonEmpty(" ", o.PlaylistPrefix, RuneLimit(p.playlist, 40)))
	}
	return joinNonEmpty(" · ", parts...)
}
