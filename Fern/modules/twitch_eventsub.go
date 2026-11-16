package modules

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// EventSub over a websocket brings follows, subs, cheers, raids and channel
// point redeems as they happen. Fern opens the socket, waits for the welcome
// and then subscribes to each event through Helix with the session id. Most
// events need the token to belong to the broadcaster (or a moderator, for
// follows), and each needs its scope; whatever cannot be subscribed is shown
// on the panel with the reason.

var twitchESURL = "wss://eventsub.wss.twitch.tv/ws"

// esSubDef is one EventSub subscription Fern makes.
type esSubDef struct {
	Kind    string
	Type    string
	Version string
	Scope   string // "" needs none
	OwnOnly bool   // only the broadcaster's own token may subscribe
	Mod     bool   // condition also names the token's user as moderator
	To      bool   // condition names the broadcaster as to_broadcaster_user_id
}

// Kinds that are not alerts.
const (
	esOnline  = "online"
	esOffline = "offline"
)

var esSubDefs = []esSubDef{
	{Kind: twitchFollow, Type: "channel.follow", Version: "2", Scope: scopeFollowers, Mod: true},
	{Kind: twitchSub, Type: "channel.subscribe", Version: "1", Scope: scopeSubs, OwnOnly: true},
	{Kind: twitchResub, Type: "channel.subscription.message", Version: "1", Scope: scopeSubs, OwnOnly: true},
	{Kind: twitchGift, Type: "channel.subscription.gift", Version: "1", Scope: scopeSubs, OwnOnly: true},
	{Kind: twitchCheer, Type: "channel.cheer", Version: "1", Scope: scopeBits, OwnOnly: true},
	{Kind: twitchRaid, Type: "channel.raid", Version: "1", To: true},
	{Kind: twitchRedeem, Type: "channel.channel_points_custom_reward_redemption.add", Version: "1", Scope: scopeRedemptions, OwnOnly: true},
	{Kind: esOnline, Type: "stream.online", Version: "1"},
	{Kind: esOffline, Type: "stream.offline", Version: "1"},
}

func (d esSubDef) condition(broadcaster, self string) map[string]string {
	switch {
	case d.To:
		return map[string]string{"to_broadcaster_user_id": broadcaster}
	case d.Mod:
		return map[string]string{"broadcaster_user_id": broadcaster, "moderator_user_id": self}
	}
	return map[string]string{"broadcaster_user_id": broadcaster}
}

// ESSubStatus is one EventSub subscription for the panel.
type ESSubStatus struct {
	Kind  string `json:"kind"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type tesSnapshot struct {
	link    LinkStatus
	subs    []ESSubStatus
	login   string
	missing []string
}

var (
	tesMu      sync.Mutex
	tesLink    = LinkStatus{State: linkOff}
	tesSubs    []ESSubStatus
	tesCovers  = map[string]bool{}
	tesLogin   string
	tesMissing []string
	tesGen     int
	tesKick    = make(chan struct{}, 1)
	tesRewards []string
	tesLive    struct {
		known, live bool
		at          time.Time
	}
)

func currentTwitchES() tesSnapshot {
	tesMu.Lock()
	defer tesMu.Unlock()
	return tesSnapshot{link: tesLink, subs: slices.Clone(tesSubs), login: tesLogin, missing: slices.Clone(tesMissing)}
}

func setTwitchESLink(state, detail string) {
	tesMu.Lock()
	tesLink = LinkStatus{State: state, Detail: detail}
	if state != linkConnected {
		tesCovers = map[string]bool{}
	}
	tesMu.Unlock()
}

func twitchESReconnect() {
	tesMu.Lock()
	tesGen++
	tesMu.Unlock()
	poke(tesKick)
}

// twitchESCovers reports whether EventSub delivers an alert kind right now.
func twitchESCovers(kind string) bool {
	tesMu.Lock()
	defer tesMu.Unlock()
	if kind == twitchGifted {
		kind = twitchSub
	}
	return tesCovers[kind]
}

// noteTwitchReward remembers a reward name for the rule editor.
func noteTwitchReward(title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	tesMu.Lock()
	defer tesMu.Unlock()
	if !slices.Contains(tesRewards, title) && len(tesRewards) < 100 {
		tesRewards = append(tesRewards, title)
	}
}

func twitchRewards() []string {
	tesMu.Lock()
	defer tesMu.Unlock()
	return slices.Clone(tesRewards)
}

// twitchLiveNow is whether the channel is live, from whichever of the
// stream poll and EventSub's online/offline heard last.
func twitchLiveNow() (live bool, viewers int, known bool) {
	twitchMu.Lock()
	poll := twitchStream
	twitchMu.Unlock()
	tesMu.Lock()
	es := tesLive
	tesMu.Unlock()
	if es.known && (!poll.ok || es.at.After(poll.checkedAt)) {
		return es.live, poll.viewers, true
	}
	return poll.live, poll.viewers, poll.ok
}

// esPlan is what one EventSub session subscribes to.
type esPlan struct {
	clientID, token string
	auth            twitchAuth
	broadcaster     string // user id
	channel         string // login
}

func startTwitchESWorker() {
	go func() {
		backoff := time.Second
		for {
			cfg := GetConfig()
			o := cfg.TwitchOptions
			switch {
			case !twitchChatWanted(cfg):
				setTwitchESLink(linkOff, "")
				sleepOrKick(2*time.Second, tesKick)
				continue
			case o.ClientID == "" || GetSecret(SecretTwitchToken) == "":
				setTwitchESLink(linkWaiting, "Sign in on the Twitch page for follows and channel point redeems")
				sleepOrKick(5*time.Second, tesKick)
				continue
			}
			plan, err := twitchESPlan(o)
			if err != nil {
				if errors.Is(err, errTwitchAuth) {
					setTwitchESLink(linkError, "Twitch rejected the sign-in. Sign in again.")
				} else {
					setTwitchESLink(linkWaiting, err.Error())
				}
				sleepOrKick(backoff, tesKick)
				backoff = min(backoff*2, time.Minute)
				continue
			}
			tesMu.Lock()
			tesLogin, tesMissing = plan.auth.Login, twitchMissingScopes(plan.auth)
			gen := tesGen
			tesMu.Unlock()
			setTwitchESLink(linkWaiting, "Connecting to EventSub")
			ctx, cancel := context.WithCancel(context.Background())
			go watchESPlan(ctx, cancel, o.ClientID+"/"+o.Channel+"/"+tokenTag(plan.token), gen)
			started := time.Now()
			err = runTwitchES(ctx, twitchESURL, func(session string) error {
				return subscribeES(plan, session)
			}, handleESNotification)
			cancel()
			if ctx.Err() != nil {
				backoff = time.Second
				continue
			}
			if errors.Is(err, errTwitchAuth) {
				forgetTwitchAuth()
				setTwitchESLink(linkError, "Twitch rejected the sign-in. Sign in again.")
			} else if err != nil {
				setTwitchESLink(linkWaiting, "EventSub dropped: "+err.Error()+". Reconnecting.")
			}
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
			sleepOrKick(backoff, tesKick)
			backoff = min(backoff*2, time.Minute)
		}
	}()
}

func twitchESPlan(o TwitchOptions) (esPlan, error) {
	auth, token, err := twitchSelf(o.ClientID)
	if err != nil {
		return esPlan{}, err
	}
	plan := esPlan{clientID: o.ClientID, token: token, auth: auth, broadcaster: auth.UserID, channel: strings.ToLower(auth.Login)}
	if o.Channel != "" && !strings.EqualFold(o.Channel, auth.Login) {
		id, _, err := newTwitchClient(o.ClientID, token).userID(o.Channel)
		if err != nil {
			return esPlan{}, err
		}
		plan.broadcaster, plan.channel = id, o.Channel
	}
	return plan, nil
}

func watchESPlan(ctx context.Context, cancel context.CancelFunc, key string, gen int) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := GetConfig()
		o := cfg.TwitchOptions
		tesMu.Lock()
		g := tesGen
		tesMu.Unlock()
		if g != gen || !twitchChatWanted(cfg) || o.ClientID+"/"+o.Channel+"/"+tokenTag(GetSecret(SecretTwitchToken)) != key {
			cancel()
			return
		}
	}
}

// subscribeES makes every subscription the token allows on a new session.
func subscribeES(plan esPlan, session string) error {
	c := newTwitchClient(plan.clientID, plan.token)
	own := plan.broadcaster == plan.auth.UserID
	statuses := make([]ESSubStatus, 0, len(esSubDefs))
	covers := map[string]bool{}
	for _, d := range esSubDefs {
		st := ESSubStatus{Kind: d.Kind}
		switch {
		case d.Scope != "" && !plan.auth.has(d.Scope):
			st.Error = "Sign in again to allow this"
		case d.OwnOnly && !own:
			st.Error = "Only on your own channel"
		default:
			body := map[string]any{
				"type": d.Type, "version": d.Version,
				"condition": d.condition(plan.broadcaster, plan.auth.UserID),
				"transport": map[string]string{"method": "websocket", "session_id": session},
			}
			err := c.helixSend(http.MethodPost, "/eventsub/subscriptions", nil, body, nil)
			var apiErr *twitchAPIError
			switch {
			case err == nil:
				st.OK = true
				covers[d.Kind] = true
			case errors.Is(err, errTwitchAuth):
				return errTwitchAuth
			case errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden:
				st.Error = "Not allowed: you need to be the broadcaster or a moderator"
			default:
				st.Error = err.Error()
			}
		}
		statuses = append(statuses, st)
	}
	setTwitchESLink(linkConnected, "Alerts for #"+plan.channel)
	tesMu.Lock()
	tesSubs, tesCovers = statuses, covers
	tesMu.Unlock()
	if own && plan.auth.has(scopeRedemptions) {
		go fetchTwitchRewards(c, plan.broadcaster)
	}
	NotifyChange()
	return nil
}

// fetchTwitchRewards loads the channel's reward names for the rule editor.
func fetchTwitchRewards(c twitchClient, broadcaster string) {
	var out struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := c.helixSend(http.MethodGet, "/channel_points/custom_rewards", url.Values{"broadcaster_id": {broadcaster}}, nil, &out); err != nil {
		return
	}
	for _, r := range out.Data {
		noteTwitchReward(r.Title)
	}
}

// esEnvelope is an EventSub websocket message.
type esEnvelope struct {
	Metadata struct {
		MessageID        string `json:"message_id"`
		MessageType      string `json:"message_type"`
		SubscriptionType string `json:"subscription_type"`
	} `json:"metadata"`
	Payload struct {
		Session struct {
			ID                      string `json:"id"`
			KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
			ReconnectURL            string `json:"reconnect_url"`
		} `json:"session"`
		Subscription struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"subscription"`
		Event json.RawMessage `json:"event"`
	} `json:"payload"`
}

// runTwitchES reads EventSub until it fails or ctx ends. welcome runs once,
// on the first session; a reconnect carries the subscriptions over.
func runTwitchES(ctx context.Context, target string, welcome func(session string) error, handle func(subType string, event json.RawMessage)) error {
	dial := func(u string) (*websocket.Conn, error) {
		dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
		conn, resp, err := dialer.DialContext(ctx, u, nil)
		if err != nil {
			if resp != nil {
				_ = resp.Body.Close()
			}
			return nil, errors.New("could not reach Twitch EventSub")
		}
		conn.SetReadLimit(1 << 20)
		return conn, nil
	}
	conn, err := dial(target)
	if err != nil {
		return err
	}
	var connMu sync.Mutex
	current := func() *websocket.Conn { connMu.Lock(); defer connMu.Unlock(); return conn }
	defer func() { _ = current().Close() }()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = current().Close()
		case <-stop:
		}
	}()

	keepalive := 30 * time.Second
	subscribed := false
	seen := make([]string, 0, 64)
	for {
		c := current()
		_ = c.SetReadDeadline(time.Now().Add(keepalive + 10*time.Second))
		_, data, err := c.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the connection closed")
		}
		var env esEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		switch env.Metadata.MessageType {
		case "session_welcome":
			if s := env.Payload.Session.KeepaliveTimeoutSeconds; s > 0 {
				keepalive = time.Duration(s) * time.Second
			}
			if !subscribed {
				subscribed = true
				if err := welcome(env.Payload.Session.ID); err != nil {
					return err
				}
			}
		case "session_reconnect":
			next := env.Payload.Session.ReconnectURL
			if !strings.HasPrefix(next, "wss://") && !strings.HasPrefix(next, "ws://") {
				return errors.New("bad reconnect address")
			}
			nc, err := dial(next)
			if err != nil {
				return err
			}
			connMu.Lock()
			old := conn
			conn = nc
			connMu.Unlock()
			_ = old.Close()
		case "notification":
			id := env.Metadata.MessageID
			if id != "" && slices.Contains(seen, id) {
				continue
			}
			if len(seen) == cap(seen) {
				seen = seen[1:]
			}
			seen = append(seen, id)
			handle(firstNonEmpty(env.Metadata.SubscriptionType, env.Payload.Subscription.Type), env.Payload.Event)
		case "revocation":
			noteESRevoked(env.Payload.Subscription.Type, env.Payload.Subscription.Status)
		}
	}
}

func noteESRevoked(subType, status string) {
	tesMu.Lock()
	defer tesMu.Unlock()
	for _, d := range esSubDefs {
		if d.Type != subType {
			continue
		}
		delete(tesCovers, d.Kind)
		for i := range tesSubs {
			if tesSubs[i].Kind == d.Kind {
				tesSubs[i].OK, tesSubs[i].Error = false, "Twitch ended it: "+status
			}
		}
	}
}

// esEvent holds the fields Fern reads from any EventSub event.
type esEvent struct {
	UserLogin   string          `json:"user_login"`
	UserName    string          `json:"user_name"`
	Tier        string          `json:"tier"`
	IsGift      bool            `json:"is_gift"`
	IsAnonymous bool            `json:"is_anonymous"`
	Total       int             `json:"total"`
	Bits        int             `json:"bits"`
	Viewers     int             `json:"viewers"`
	Cumulative  int             `json:"cumulative_months"`
	UserInput   string          `json:"user_input"`
	FromLogin   string          `json:"from_broadcaster_user_login"`
	FromName    string          `json:"from_broadcaster_user_name"`
	Message     json.RawMessage `json:"message"` // a string for cheers, {text} for resubs
	Reward      struct {
		Title string `json:"title"`
		Cost  int    `json:"cost"`
	} `json:"reward"`
}

// esMessageText reads a message that is a string (cheers) or {text} (resubs).
func esMessageText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Text
}

// esAlert turns a notification into an alert; ok is false for other events.
func esAlert(subType string, raw json.RawMessage) (twitchAlert, bool) {
	var e esEvent
	if json.Unmarshal(raw, &e) != nil {
		return twitchAlert{}, false
	}
	a := twitchAlert{Name: e.UserName, Login: e.UserLogin, Tier: tierName(e.Tier), Message: strings.TrimSpace(esMessageText(e.Message))}
	switch subType {
	case "channel.follow":
		a.Kind = twitchFollow
	case "channel.subscribe":
		a.Kind, a.Months = twitchSub, 1
		if e.IsGift {
			a.Kind = twitchGifted
		}
	case "channel.subscription.message":
		a.Kind, a.Months = twitchResub, e.Cumulative
	case "channel.subscription.gift":
		a.Kind, a.Amount = twitchGift, e.Total
	case "channel.cheer":
		a.Kind, a.Amount = twitchCheer, e.Bits
	case "channel.raid":
		a.Kind, a.Amount, a.Name, a.Login = twitchRaid, e.Viewers, e.FromName, e.FromLogin
	case "channel.channel_points_custom_reward_redemption.add":
		a.Kind, a.Reward, a.Amount, a.Message = twitchRedeem, e.Reward.Title, e.Reward.Cost, strings.TrimSpace(e.UserInput)
	default:
		return twitchAlert{}, false
	}
	if e.IsAnonymous {
		a.Name, a.Login = "Anonymous", ""
	}
	return a, true
}

func handleESNotification(subType string, raw json.RawMessage) {
	switch subType {
	case "stream.online", "stream.offline":
		tesMu.Lock()
		tesLive.known, tesLive.live, tesLive.at = true, subType == "stream.online", time.Now()
		tesMu.Unlock()
		TwitchRefreshNow()
		NudgeRules()
		return
	}
	if a, ok := esAlert(subType, raw); ok {
		noteTwitchAlert(GetConfig(), a, "es")
	}
}
