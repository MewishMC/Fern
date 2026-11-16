package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TwitchDevice is a pending "Sign in with Twitch" (OAuth device code flow):
// the user opens URI and confirms Code there.
type TwitchDevice struct {
	Code      string `json:"code"`
	URI       string `json:"uri"`
	ExpiresAt int64  `json:"expiresAt"`
	Error     string `json:"error"`
}

// TwitchInfo is what the Twitch page shows.
type TwitchInfo struct {
	Link      LinkStatus    `json:"link"`
	Channel   string        `json:"channel"`
	Live      bool          `json:"live"`
	Viewers   int           `json:"viewers"`
	Followers int           `json:"followers"`
	Game      string        `json:"game"`
	Title     string        `json:"title"`
	CheckedAt int64         `json:"checkedAt"`
	Device    *TwitchDevice `json:"device"`
}

type twitchData struct {
	ok        bool
	channel   string
	live      bool
	viewers   int
	followers int
	game      string
	title     string
	checkedAt time.Time
}

var (
	twitchMu     sync.Mutex
	twitchLink   = LinkStatus{State: linkOff}
	twitchStream twitchData
	twitchDev    *TwitchDevice
	twitchUser   struct{ key, id, name string } // cached channel lookup
	twitchKickCh = make(chan struct{}, 1)
	twitchGen    atomic.Int64 // bumps to cancel a pending sign-in

	twitchAPIBase = "https://api.twitch.tv/helix"
	twitchIDBase  = "https://id.twitch.tv/oauth2"
	twitchHTTP    = &http.Client{Timeout: 15 * time.Second}
)

// twitchScopes is what the sign-in asks for: the follower list, chat, the
// alerts and the announcement and shoutout actions (see twitch_api.go). The
// follower total itself needs no scope.
var twitchScopes = strings.Join(twitchAllScopes, " ")

var errTwitchAuth = errors.New("twitch: token rejected")

func setTwitchLink(state, detail string) {
	twitchMu.Lock()
	twitchLink = LinkStatus{State: state, Detail: detail}
	twitchMu.Unlock()
}

func resetTwitchUser() {
	twitchMu.Lock()
	twitchUser.key, twitchUser.id, twitchUser.name = "", "", ""
	twitchStream = twitchData{}
	twitchMu.Unlock()
	poke(twitchKickCh)
}

// TwitchRefreshNow polls without waiting for the next interval.
func TwitchRefreshNow() { poke(twitchKickCh) }

// TwitchSignOut forgets the Twitch tokens.
func TwitchSignOut() {
	twitchGen.Add(1)
	_ = SetSecret(SecretTwitchToken, "")
	_ = SetSecret(SecretTwitchRefresh, "")
	twitchMu.Lock()
	twitchDev = nil
	twitchMu.Unlock()
	resetTwitchUser()
}

// CurrentTwitch returns the Twitch snapshot for the panel.
func CurrentTwitch() TwitchInfo {
	twitchMu.Lock()
	defer twitchMu.Unlock()
	info := TwitchInfo{
		Link:      twitchLink,
		Channel:   twitchStream.channel,
		Live:      twitchStream.live,
		Viewers:   twitchStream.viewers,
		Followers: twitchStream.followers,
		Game:      twitchStream.game,
		Title:     twitchStream.title,
	}
	if !twitchStream.checkedAt.IsZero() {
		info.CheckedAt = twitchStream.checkedAt.UnixMilli()
	}
	if twitchDev != nil {
		d := *twitchDev
		info.Device = &d
	}
	return info
}

// twitchClient talks to Helix and the Twitch OAuth server.
type twitchClient struct {
	http     *http.Client
	api, id  string
	clientID string
	token    string
}

func newTwitchClient(clientID, token string) twitchClient {
	return twitchClient{http: twitchHTTP, api: twitchAPIBase, id: twitchIDBase, clientID: clientID, token: token}
}

func (c twitchClient) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("could not reach Twitch")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errTwitchAuth
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Twitch answered %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c twitchClient) helix(path string, q url.Values, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.api+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Authorization", "Bearer "+c.token)
	return c.do(req, out)
}

// resolve finds the broadcaster id: the named channel, or the token's owner.
func (c twitchClient) resolve(channel string) (id, name string, err error) {
	if channel == "" {
		req, err := http.NewRequest(http.MethodGet, c.id+"/validate", nil)
		if err != nil {
			return "", "", err
		}
		req.Header.Set("Authorization", "OAuth "+c.token)
		var v struct {
			UserID string `json:"user_id"`
			Login  string `json:"login"`
		}
		if err := c.do(req, &v); err != nil {
			return "", "", err
		}
		return v.UserID, v.Login, nil
	}
	var users struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := c.helix("/users", url.Values{"login": {channel}}, &users); err != nil {
		return "", "", err
	}
	if len(users.Data) == 0 {
		return "", "", fmt.Errorf("no Twitch channel called %q", channel)
	}
	return users.Data[0].ID, users.Data[0].DisplayName, nil
}

// poll fetches stream state and, when wanted, the follower total.
func (c twitchClient) poll(id string, followers bool) (twitchData, error) {
	var d twitchData
	var streams struct {
		Data []struct {
			Type        string `json:"type"`
			ViewerCount int    `json:"viewer_count"`
			GameName    string `json:"game_name"`
			Title       string `json:"title"`
		} `json:"data"`
	}
	if err := c.helix("/streams", url.Values{"user_id": {id}}, &streams); err != nil {
		return d, err
	}
	if len(streams.Data) > 0 && streams.Data[0].Type == "live" {
		s := streams.Data[0]
		d.live, d.viewers, d.game, d.title = true, s.ViewerCount, s.GameName, s.Title
	}
	if followers {
		var f struct {
			Total int `json:"total"`
		}
		if err := c.helix("/channels/followers", url.Values{"broadcaster_id": {id}, "first": {"1"}}, &f); err != nil {
			return d, err
		}
		d.followers = f.Total
	}
	d.ok = true
	d.checkedAt = time.Now()
	return d, nil
}

// postForm posts to the OAuth server and decodes JSON, keeping the error
// message Twitch gives (like "authorization_pending") without any token.
func (c twitchClient) postForm(path string, form url.Values, out any) (status int, message string, err error) {
	resp, err := c.http.PostForm(c.id+path, form)
	if err != nil {
		return 0, "", errors.New("could not reach Twitch")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Message, nil
	}
	return resp.StatusCode, "", json.NewDecoder(resp.Body).Decode(out)
}

type twitchTokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (c twitchClient) refresh(refreshToken string) (twitchTokens, error) {
	var t twitchTokens
	status, msg, err := c.postForm("/token", url.Values{
		"client_id":     {c.clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}, &t)
	if err != nil {
		return t, err
	}
	if status != http.StatusOK || t.AccessToken == "" {
		return t, fmt.Errorf("refresh failed: %s", firstNonEmpty(msg, strconv.Itoa(status)))
	}
	return t, nil
}

func startTwitchWorker() {
	go func() {
		for {
			cfg := GetConfig()
			o := cfg.TwitchOptions
			wait := time.Duration(o.IntervalSec) * time.Second
			switch {
			case !cfg.DisplayOptions.Twitch:
				setTwitchLink(linkOff, "")
				wait = 2 * time.Second
			case o.ClientID == "":
				setTwitchLink(linkWaiting, "Add your Twitch client ID")
				wait = 2 * time.Second
			case GetSecret(SecretTwitchToken) == "":
				setTwitchLink(linkWaiting, "Sign in with Twitch")
				wait = 2 * time.Second
			default:
				if err := twitchCycle(o); err != nil {
					wait = min(wait, 30*time.Second)
				}
			}
			sleepOrKick(wait, twitchKickCh)
		}
	}()
}

// twitchCycle runs one poll, refreshing the token once if Twitch rejects it.
func twitchCycle(o TwitchOptions) error {
	for attempt := 0; attempt < 2; attempt++ {
		c := newTwitchClient(o.ClientID, GetSecret(SecretTwitchToken))
		err := twitchPollOnce(c, o)
		if err == nil {
			setTwitchLink(linkConnected, "")
			return nil
		}
		if !errors.Is(err, errTwitchAuth) {
			setTwitchLink(linkWaiting, err.Error())
			return err
		}
		if attempt > 0 || twitchRefreshToken(o.ClientID, c.token) != nil {
			break
		}
	}
	setTwitchLink(linkError, "Twitch rejected the sign-in. Sign in again.")
	return errTwitchAuth
}

func twitchPollOnce(c twitchClient, o TwitchOptions) error {
	twitchMu.Lock()
	cached := twitchUser
	twitchMu.Unlock()
	key := o.ClientID + "/" + o.Channel
	if cached.key != key || cached.id == "" {
		id, name, err := c.resolve(o.Channel)
		if err != nil {
			return err
		}
		cached.key, cached.id, cached.name = key, id, name
		twitchMu.Lock()
		twitchUser = cached
		twitchMu.Unlock()
	}
	wantFollowers := o.ShowFollowers || strings.Contains(o.Template, "{follower")
	d, err := c.poll(cached.id, wantFollowers)
	if err != nil {
		return err
	}
	d.channel = cached.name
	twitchMu.Lock()
	twitchStream = d
	twitchMu.Unlock()
	NotifyChange()
	return nil
}

// TwitchStartSignIn begins the device code flow. The panel shows the code and
// opens the activation page; this keeps polling until it is approved.
func TwitchStartSignIn() {
	o := GetConfig().TwitchOptions
	gen := twitchGen.Add(1)
	if o.ClientID == "" {
		setTwitchDevice(&TwitchDevice{Error: "Add your Twitch client ID first"})
		return
	}
	setTwitchDevice(nil)
	go twitchDeviceFlow(newTwitchClient(o.ClientID, ""), gen)
}

// TwitchCancelSignIn abandons a pending device sign-in.
func TwitchCancelSignIn() {
	twitchGen.Add(1)
	setTwitchDevice(nil)
}

func setTwitchDevice(d *TwitchDevice) {
	twitchMu.Lock()
	twitchDev = d
	twitchMu.Unlock()
	NotifyChange()
}

func twitchDeviceFlow(c twitchClient, gen int64) {
	var dev struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	status, msg, err := c.postForm("/device", url.Values{"client_id": {c.clientID}, "scopes": {twitchScopes}}, &dev)
	if err != nil || status != http.StatusOK || dev.DeviceCode == "" {
		reason := firstNonEmpty(msg, "could not start the sign-in")
		if err != nil {
			reason = err.Error()
		}
		if strings.Contains(strings.ToLower(reason), "client") {
			reason += ". Check the client ID and that the app's client type is Public."
		}
		setTwitchDevice(&TwitchDevice{Error: reason})
		return
	}
	expires := time.Now().Add(time.Duration(dev.ExpiresIn) * time.Second)
	setTwitchDevice(&TwitchDevice{Code: dev.UserCode, URI: dev.VerificationURI, ExpiresAt: expires.UnixMilli()})
	interval := time.Duration(max(dev.Interval, 1)) * time.Second
	for time.Now().Before(expires) {
		time.Sleep(interval)
		if twitchGen.Load() != gen {
			return
		}
		var tok twitchTokens
		status, msg, err := c.postForm("/token", url.Values{
			"client_id":   {c.clientID},
			"scopes":      {twitchScopes},
			"device_code": {dev.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &tok)
		if twitchGen.Load() != gen {
			return
		}
		switch {
		case err != nil:
			continue
		case status == http.StatusOK && tok.AccessToken != "":
			_ = SetSecret(SecretTwitchToken, tok.AccessToken)
			_ = SetSecret(SecretTwitchRefresh, tok.RefreshToken)
			setTwitchDevice(nil)
			resetTwitchUser()
			return
		case msg == "authorization_pending":
		case msg == "slow_down":
			interval += 5 * time.Second
		default:
			setTwitchDevice(&TwitchDevice{Error: "Sign-in failed: " + firstNonEmpty(msg, strconv.Itoa(status))})
			return
		}
	}
	setTwitchDevice(&TwitchDevice{Error: "The sign-in code expired. Try again."})
}

// twitchLine renders the stream status.
func twitchLine(cfg Config) string {
	o := cfg.TwitchOptions
	twitchMu.Lock()
	d := twitchStream
	twitchMu.Unlock()
	if !d.ok {
		return ""
	}
	if !d.live {
		return o.OfflineText
	}
	count := strconv.Itoa
	if o.Compact {
		count = compactCount
	}
	labelled := func(n int, label string) string {
		return strings.TrimSpace(count(n) + " " + label)
	}
	if o.Template != "" {
		return fillTemplate(o.Template, map[string]string{
			"live":          o.LiveText,
			"viewers":       labelled(d.viewers, o.ViewerLabel),
			"viewerCount":   count(d.viewers),
			"game":          d.game,
			"title":         d.title,
			"followers":     labelled(d.followers, o.FollowerLabel),
			"followerCount": count(d.followers),
			"channel":       d.channel,
		})
	}
	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	if o.ShowLive {
		add(o.LiveText)
	}
	if o.ShowViewers {
		add(labelled(d.viewers, o.ViewerLabel))
	}
	if o.ShowGame && d.game != "" {
		add(o.GamePrefix + " " + d.game)
	}
	if o.ShowTitle {
		add(d.title)
	}
	if o.ShowFollowers {
		add(labelled(d.followers, o.FollowerLabel))
	}
	return strings.Join(parts, o.Separator)
}
