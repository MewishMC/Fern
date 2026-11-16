package modules

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Twitch chat is read over Twitch's IRC websocket. Signed in with the
// chat:read scope Fern logs in as the user; otherwise it joins anonymously,
// which can read any channel's chat. Subs, gift subs, raids and cheers also
// arrive in chat, so they work on any channel; EventSub (twitch_eventsub.go)
// adds follows and channel point redeems on your own channel and takes over
// the kinds it covers, so nothing is announced twice.

var twitchIRCURL = "wss://irc-ws.chat.twitch.tv:443"

// ircMessage is one parsed IRC line.
type ircMessage struct {
	Tags    map[string]string
	Prefix  string
	Command string
	Params  []string
}

// Nick is the sender's login, from the prefix.
func (m ircMessage) Nick() string {
	nick, _, _ := strings.Cut(m.Prefix, "!")
	return nick
}

// Trailing is the last parameter, usually the message text.
func (m ircMessage) Trailing() string {
	if len(m.Params) == 0 {
		return ""
	}
	return m.Params[len(m.Params)-1]
}

var ircTagEscapes = strings.NewReplacer(`\s`, " ", `\:`, ";", `\\`, `\`, `\r`, "\r", `\n`, "\n")

// parseIRC parses one IRC line with IRCv3 tags.
func parseIRC(line string) (ircMessage, bool) {
	line = strings.TrimRight(line, "\r\n")
	var m ircMessage
	if strings.HasPrefix(line, "@") {
		tags, rest, ok := strings.Cut(line[1:], " ")
		if !ok {
			return m, false
		}
		m.Tags = map[string]string{}
		for _, kv := range strings.Split(tags, ";") {
			k, v, _ := strings.Cut(kv, "=")
			m.Tags[k] = ircTagEscapes.Replace(v)
		}
		line = strings.TrimLeft(rest, " ")
	}
	if strings.HasPrefix(line, ":") {
		prefix, rest, ok := strings.Cut(line[1:], " ")
		if !ok {
			return m, false
		}
		m.Prefix = prefix
		line = strings.TrimLeft(rest, " ")
	}
	for line != "" {
		if strings.HasPrefix(line, ":") {
			m.Params = append(m.Params, line[1:])
			break
		}
		word, rest, _ := strings.Cut(line, " ")
		if m.Command == "" {
			m.Command = word
		} else {
			m.Params = append(m.Params, word)
		}
		line = strings.TrimLeft(rest, " ")
	}
	return m, m.Command != ""
}

// chatMessage is one Twitch chat message.
type chatMessage struct {
	Login, Name, Text string
	Sub, VIP, Mod     bool
	Broadcaster       bool
	Bits              int
}

func (m chatMessage) role() string {
	switch {
	case m.Broadcaster:
		return "broadcaster"
	case m.Mod:
		return "mod"
	case m.VIP:
		return "vip"
	case m.Sub:
		return "sub"
	}
	return "viewer"
}

// chatFromIRC turns a PRIVMSG into a chat message.
func chatFromIRC(m ircMessage) chatMessage {
	text := m.Trailing()
	// "/me waves" arrives as a CTCP ACTION.
	if strings.HasPrefix(text, "\x01ACTION ") {
		text = strings.TrimSuffix(strings.TrimPrefix(text, "\x01ACTION "), "\x01")
	}
	c := chatMessage{Login: strings.ToLower(firstNonEmpty(m.Tags["login"], m.Nick())), Text: strings.TrimSpace(text)}
	c.Name = firstNonEmpty(m.Tags["display-name"], c.Login)
	badges := map[string]bool{}
	for _, b := range strings.Split(m.Tags["badges"], ",") {
		name, _, _ := strings.Cut(b, "/")
		if name != "" {
			badges[name] = true
		}
	}
	c.Broadcaster = badges["broadcaster"]
	c.Mod = badges["moderator"] || m.Tags["mod"] == "1" || c.Broadcaster
	c.VIP = badges["vip"] || m.Tags["vip"] == "1"
	c.Sub = badges["subscriber"] || badges["founder"] || m.Tags["subscriber"] == "1"
	c.Bits, _ = strconv.Atoi(m.Tags["bits"])
	return c
}

// knownChatBots are left out when "Ignore bots" is on.
var knownChatBots = map[string]bool{
	"nightbot": true, "streamelements": true, "streamlabs": true, "moobot": true, "fossabot": true,
	"wizebot": true, "sery_bot": true, "soundalerts": true, "kofistreambot": true, "botrixoficial": true,
	"commanderroot": true, "pokemoncommunitygame": true, "streamstickers": true, "blerp": true,
	"own3d": true, "lumiastream": true, "frostytoolsdotcom": true, "tangiabot": true,
}

// chatAllowed applies the line's filters to a message.
func chatAllowed(o TwitchChatOptions, m chatMessage) bool {
	if m.Text == "" {
		return false
	}
	if o.IgnoreBots && (knownChatBots[m.Login] || strings.HasSuffix(m.Login, "bot")) {
		return false
	}
	for _, u := range o.IgnoreUsers {
		if strings.EqualFold(u, m.Login) || strings.EqualFold(u, m.Name) {
			return false
		}
	}
	if o.IgnoreCommands && strings.HasPrefix(m.Text, "!") {
		return false
	}
	if o.HideLinks && hasLink(m.Text) {
		return false
	}
	for _, w := range o.BlockWords {
		if w != "" && MatchText(w, m.Text) {
			return false
		}
	}
	switch o.OnlyRole {
	case ChatRoleSubs:
		return m.Sub || m.VIP || m.Mod
	case ChatRoleVIPs:
		return m.VIP || m.Mod
	case ChatRoleMods:
		return m.Mod
	}
	return true
}

func hasLink(s string) bool {
	low := strings.ToLower(s)
	if strings.Contains(low, "http://") || strings.Contains(low, "https://") || strings.Contains(low, "www.") {
		return true
	}
	for _, w := range strings.Fields(low) {
		w = strings.Trim(w, ".,!?()[]<>\"'")
		if i := strings.LastIndexByte(w, '.'); i > 0 && i < len(w)-1 {
			switch w[i+1:] {
			case "com", "net", "org", "tv", "gg", "io", "ly", "me", "co", "xyz":
				return true
			}
		}
	}
	return false
}

// cutRunes shortens s to n runes, marking the cut.
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return Ellipsis
	}
	return strings.TrimSpace(string(r[:n-1])) + Ellipsis
}

// Twitch chat state for the line and the panel.
var (
	tchatMu      sync.Mutex
	tchatLink    = LinkStatus{State: linkOff}
	tchatChannel string
	tchatAnon    bool
	tchatLast    struct {
		text string
		at   time.Time
	}
	tchatAlerts  alertQueue
	tchatRecent  eventLog
	tchatKick    = make(chan struct{}, 1)
	tchatGen     int // bumped to drop the connection
	tchatSkipGif = map[string]int{}
)

func setTwitchChatLink(state, detail string) {
	tchatMu.Lock()
	tchatLink = LinkStatus{State: state, Detail: detail}
	tchatMu.Unlock()
}

func twitchChatReconnect() {
	tchatMu.Lock()
	tchatGen++
	tchatMu.Unlock()
	twitchESReconnect()
	poke(tchatKick)
}

func twitchChatClear() {
	tchatMu.Lock()
	tchatLast.text = ""
	tchatMu.Unlock()
	tchatAlerts.clear()
	tchatRecent.clear()
	NotifyChange()
}

// noteChat stores a chat message for the line and fires the chat trigger.
func noteChat(cfg Config, m chatMessage) {
	if rulesUseTrigger(cfg, TriggerTwitchChat) {
		FireTrigger(TriggerTwitchChat, map[string]string{
			"name": m.Name, "user": m.Login, "message": m.Text, "role": m.role(),
		})
	}
	o := cfg.TwitchChatOptions
	if !chatAllowed(o, m) {
		return
	}
	text := fillTemplate(o.ChatTemplate, map[string]string{
		"name": m.Name, "user": m.Login, "message": cutRunes(m.Text, o.MaxChars),
	})
	tchatMu.Lock()
	tchatLast.text, tchatLast.at = text, streamNow()
	tchatMu.Unlock()
	tchatRecent.add("chat", m.Name+": "+m.Text)
	NotifyChange()
}

// Kinds of Twitch alerts.
const (
	twitchFollow = "follow"
	twitchSub    = "sub"
	twitchResub  = "resub"
	twitchGifted = "gifted" // a sub someone else gifted
	twitchGift   = "gift"
	twitchRaid   = "raid"
	twitchCheer  = "cheer"
	twitchRedeem = "redeem"
)

// twitchAlert is a follow, sub, raid, cheer or redeem from chat or EventSub.
type twitchAlert struct {
	Kind    string
	Name    string // display name; "Anonymous" for anonymous gifts and cheers
	Login   string
	Amount  int // gifted subs, raiders, bits or the reward's cost
	Months  int
	Tier    string // 1, 2, 3 or Prime
	Reward  string
	Message string
}

func (a twitchAlert) data() map[string]string {
	d := map[string]string{"name": a.Name, "user": a.Login, "kind": a.Kind, "amount": strconv.Itoa(a.Amount)}
	if a.Months > 0 {
		d["months"] = strconv.Itoa(a.Months)
	}
	if a.Tier != "" {
		d["tier"] = a.Tier
	}
	if a.Reward != "" {
		d["reward"] = a.Reward
	}
	if a.Message != "" {
		d["message"] = a.Message
	}
	return d
}

// alertText renders an alert with the line's templates, or "" when that
// alert is off or under its minimum.
func alertText(o TwitchChatOptions, a twitchAlert) string {
	var tmpl string
	switch a.Kind {
	case twitchFollow:
		tmpl = o.FollowText
	case twitchSub, twitchResub:
		tmpl = o.SubText
	case twitchGift:
		tmpl = o.GiftText
	case twitchRaid:
		if a.Amount < o.MinRaid {
			return ""
		}
		tmpl = o.RaidText
	case twitchCheer:
		if a.Amount < o.MinBits {
			return ""
		}
		tmpl = o.CheerText
	case twitchRedeem:
		tmpl = o.RedeemText
	}
	if tmpl == "" {
		return ""
	}
	months := ""
	if a.Months > 1 {
		months = strconv.Itoa(a.Months)
	}
	return fillTemplate(tmpl, map[string]string{
		"name": a.Name, "user": a.Login, "amount": strconv.Itoa(a.Amount), "months": months,
		"tier": a.Tier, "reward": a.Reward, "message": cutRunes(a.Message, 60),
	})
}

// noteTwitchAlert shows and fires an alert. source is "irc" or "es": an
// alert from chat is skipped when EventSub delivers that kind.
func noteTwitchAlert(cfg Config, a twitchAlert, source string) {
	if source == "irc" && twitchESCovers(a.Kind) {
		return
	}
	if a.Name == "" {
		a.Name = firstNonEmpty(a.Login, "Anonymous")
	}
	trigger := ""
	switch a.Kind {
	case twitchFollow:
		trigger = TriggerTwitchFollow
	case twitchSub, twitchResub, twitchGifted:
		trigger = TriggerTwitchSub
	case twitchGift:
		trigger = TriggerTwitchGift
	case twitchRaid:
		trigger = TriggerTwitchRaid
	case twitchCheer:
		trigger = TriggerTwitchCheer
	case twitchRedeem:
		trigger = TriggerTwitchRedeem
		noteTwitchReward(a.Reward)
	}
	if trigger != "" {
		FireTrigger(trigger, a.data())
	}
	o := cfg.TwitchChatOptions
	if text := alertText(o, a); text != "" {
		tchatAlerts.push(text, time.Duration(o.AlertSec)*time.Second)
		tchatRecent.add(a.Kind, text)
	} else if a.Kind != twitchGifted {
		tchatRecent.add(a.Kind, a.Name+" "+a.Kind)
	}
}

// tierName turns Twitch's plan ids into 1, 2, 3 or Prime.
func tierName(plan string) string {
	switch plan {
	case "1000":
		return "1"
	case "2000":
		return "2"
	case "3000":
		return "3"
	case "Prime", "prime":
		return "Prime"
	}
	return plan
}

// handleIRC deals with one message from chat.
func handleIRC(cfg Config, m ircMessage) {
	switch m.Command {
	case "PRIVMSG":
		c := chatFromIRC(m)
		if c.Bits > 0 {
			noteTwitchAlert(cfg, twitchAlert{Kind: twitchCheer, Name: c.Name, Login: c.Login, Amount: c.Bits, Message: c.Text}, "irc")
		}
		noteChat(cfg, c)
	case "USERNOTICE":
		handleUserNotice(cfg, m)
	}
}

func handleUserNotice(cfg Config, m ircMessage) {
	t := m.Tags
	num := func(k string) int { n, _ := strconv.Atoi(t["msg-param-"+k]); return n }
	name := firstNonEmpty(t["display-name"], t["login"])
	login := strings.ToLower(t["login"])
	a := twitchAlert{Name: name, Login: login, Tier: tierName(t["msg-param-sub-plan"]), Message: strings.TrimSpace(m.Trailing())}
	if len(m.Params) < 2 {
		a.Message = ""
	}
	switch t["msg-id"] {
	case "sub":
		a.Kind, a.Months = twitchSub, max(num("cumulative-months"), 1)
	case "resub":
		a.Kind, a.Months = twitchResub, num("cumulative-months")
	case "submysterygift":
		a.Kind, a.Amount = twitchGift, num("mass-gift-count")
		// The single gifts of a bomb follow as subgift notices.
		tchatMu.Lock()
		tchatSkipGif[login] += a.Amount
		tchatMu.Unlock()
	case "subgift":
		recipient := twitchAlert{Kind: twitchGifted, Name: t["msg-param-recipient-display-name"],
			Login: strings.ToLower(t["msg-param-recipient-user-name"]), Tier: a.Tier, Months: num("months")}
		noteTwitchAlert(cfg, recipient, "irc")
		tchatMu.Lock()
		skip := tchatSkipGif[login] > 0
		if skip {
			tchatSkipGif[login]--
		}
		tchatMu.Unlock()
		if skip {
			return
		}
		a.Kind, a.Amount = twitchGift, 1
	case "raid":
		a.Kind, a.Amount = twitchRaid, num("viewerCount")
		a.Name = firstNonEmpty(t["msg-param-displayName"], name)
		a.Login = strings.ToLower(firstNonEmpty(t["msg-param-login"], login))
	default:
		return
	}
	if a.Name == "" || strings.EqualFold(login, "ananonymousgifter") {
		a.Name = "Anonymous"
	}
	noteTwitchAlert(cfg, a, "irc")
}

// twitchChatLine renders the newest alert, or else the newest message.
func twitchChatLine(cfg Config) string {
	o := cfg.TwitchChatOptions
	if text := tchatAlerts.current(); text != "" {
		return withLabel(o.Label, text)
	}
	if !o.ShowChat {
		return ""
	}
	tchatMu.Lock()
	text, at := tchatLast.text, tchatLast.at
	tchatMu.Unlock()
	if text == "" || (o.ChatSec > 0 && streamNow().Sub(at) > time.Duration(o.ChatSec)*time.Second) {
		return ""
	}
	return withLabel(o.Label, text)
}

// twitchTestAlert shows a sample follow so the line can be seen.
func twitchTestAlert() {
	cfg := GetConfig()
	o := cfg.TwitchChatOptions
	text := alertText(o, twitchAlert{Kind: twitchFollow, Name: "Fern"})
	if text == "" {
		text = alertText(o, twitchAlert{Kind: twitchSub, Name: "Fern", Months: 3, Tier: "1"})
	}
	if text == "" {
		text = "Fern followed"
	}
	tchatAlerts.push(text, time.Duration(o.AlertSec)*time.Second)
}

// TwitchChatInfo is the chat and alerts page's state.
type TwitchChatInfo struct {
	Chat      LinkStatus    `json:"chat"`
	Events    LinkStatus    `json:"events"`
	Channel   string        `json:"channel"`
	Anonymous bool          `json:"anonymous"`
	Login     string        `json:"login"`
	Missing   []string      `json:"missing"` // scopes the sign-in lacks
	Subs      []ESSubStatus `json:"subs"`
	Recent    []StreamEvent `json:"recent"`
}

// CurrentTwitchChat returns the chat and alerts snapshot.
func CurrentTwitchChat() TwitchChatInfo {
	tchatMu.Lock()
	info := TwitchChatInfo{Chat: tchatLink, Channel: tchatChannel, Anonymous: tchatAnon}
	tchatMu.Unlock()
	es := currentTwitchES()
	info.Events, info.Subs, info.Login, info.Missing = es.link, es.subs, es.login, es.missing
	if info.Subs == nil {
		info.Subs = []ESSubStatus{}
	}
	if info.Missing == nil {
		info.Missing = []string{}
	}
	info.Recent = tchatRecent.snapshot()
	return info
}

// twitchChatWanted reports whether chat should be connected.
func twitchChatWanted(cfg Config) bool {
	return cfg.DisplayOptions.TwitchChat || rulesUseTrigger(cfg, "twitch.")
}

// ircPlan is who to log in as and which channel to join.
type ircPlan struct {
	channel, login, token string
}

func (p ircPlan) key() string { return p.channel + "/" + p.login + "/" + tokenTag(p.token) }

// tokenTag identifies a token without keeping it around in a string that
// could be logged.
func tokenTag(token string) string {
	if token == "" {
		return ""
	}
	h := uint32(2166136261)
	for i := 0; i < len(token); i++ {
		h = (h ^ uint32(token[i])) * 16777619
	}
	return strconv.FormatUint(uint64(h), 36)
}

// twitchChatPlan works out the IRC login, or why there is none.
func twitchChatPlan(cfg Config) (ircPlan, string) {
	o := cfg.TwitchOptions
	plan := ircPlan{channel: o.Channel}
	if o.ClientID != "" && GetSecret(SecretTwitchToken) != "" {
		auth, token, err := twitchSelf(o.ClientID)
		if err == nil {
			if plan.channel == "" {
				plan.channel = strings.ToLower(auth.Login)
			}
			if auth.has(scopeChatRead) {
				plan.login, plan.token = strings.ToLower(auth.Login), token
			}
		} else if plan.channel == "" {
			if errors.Is(err, errTwitchAuth) {
				return plan, "Twitch rejected the sign-in. Sign in again."
			}
			return plan, err.Error()
		}
	}
	if plan.channel == "" {
		return plan, "Sign in with Twitch or set a channel on the Twitch page"
	}
	if plan.login == "" {
		plan.login = "justinfan" + strconv.Itoa(10000+rand.IntN(89999))
	}
	return plan, ""
}

var errIRCReconnect = errors.New("twitch asked to reconnect")

func startTwitchChatWorker() {
	go func() {
		backoff := time.Second
		for {
			cfg := GetConfig()
			if !twitchChatWanted(cfg) {
				setTwitchChatLink(linkOff, "")
				sleepOrKick(2*time.Second, tchatKick)
				continue
			}
			plan, why := twitchChatPlan(cfg)
			if why != "" {
				setTwitchChatLink(linkWaiting, why)
				sleepOrKick(5*time.Second, tchatKick)
				continue
			}
			tchatMu.Lock()
			tchatChannel, tchatAnon = plan.channel, plan.token == ""
			gen := tchatGen
			tchatMu.Unlock()
			setTwitchChatLink(linkWaiting, "Connecting to #"+plan.channel)
			ctx, cancel := context.WithCancel(context.Background())
			go watchPlan(ctx, cancel, plan.key(), gen)
			started := time.Now()
			err := runTwitchIRC(ctx, twitchIRCURL, plan, func(m ircMessage) { handleIRC(GetConfig(), m) })
			cancel()
			if ctx.Err() != nil || errors.Is(err, errIRCReconnect) {
				backoff = time.Second
				continue
			}
			if errors.Is(err, errTwitchAuth) {
				forgetTwitchAuth()
				setTwitchChatLink(linkError, "Twitch chat refused the sign-in. Sign in again.")
			} else if err != nil {
				setTwitchChatLink(linkWaiting, "Chat dropped: "+err.Error()+". Reconnecting.")
			}
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
			sleepOrKick(backoff, tchatKick)
			backoff = min(backoff*2, time.Minute)
		}
	}()
}

// watchPlan cancels the session when the channel, the sign-in or the switch
// changes, or a reconnect is asked for.
func watchPlan(ctx context.Context, cancel context.CancelFunc, key string, gen int) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := GetConfig()
		tchatMu.Lock()
		g := tchatGen
		tchatMu.Unlock()
		if g != gen || !twitchChatWanted(cfg) {
			cancel()
			return
		}
		if plan, why := twitchChatPlan(cfg); why != "" || plan.channel+"/"+tokenTag(plan.token) != keyChannelToken(key) {
			cancel()
			return
		}
	}
}

// keyChannelToken drops the login from a plan key: an anonymous login is
// random, so only the channel and token tell whether the plan changed.
func keyChannelToken(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return key
	}
	return parts[0] + "/" + parts[2]
}

// runTwitchIRC reads one chat connection until it fails or ctx ends.
func runTwitchIRC(ctx context.Context, target string, plan ircPlan, handle func(ircMessage)) error {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return errors.New("could not reach Twitch chat")
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 20)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	write := func(line string) error {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.TextMessage, []byte(line+"\r\n"))
	}
	pass := "SCHMOOPIIE"
	if plan.token != "" {
		pass = "oauth:" + plan.token
	}
	for _, line := range []string{"CAP REQ :twitch.tv/tags twitch.tv/commands", "PASS " + pass, "NICK " + plan.login, "JOIN #" + plan.channel} {
		if err := write(line); err != nil {
			return errors.New("could not log in to Twitch chat")
		}
	}
	for {
		// Twitch pings about every five minutes.
		_ = conn.SetReadDeadline(time.Now().Add(6 * time.Minute))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the connection closed")
		}
		for _, line := range strings.Split(string(data), "\n") {
			m, ok := parseIRC(line)
			if !ok {
				continue
			}
			switch m.Command {
			case "PING":
				if err := write("PONG :" + m.Trailing()); err != nil {
					return errors.New("the connection closed")
				}
			case "RECONNECT":
				return errIRCReconnect
			case "NOTICE":
				text := strings.ToLower(m.Trailing())
				if strings.Contains(text, "authentication failed") || strings.Contains(text, "improperly formatted auth") {
					return errTwitchAuth
				}
			case "JOIN":
				if strings.EqualFold(m.Nick(), plan.login) {
					who := "anonymously"
					if plan.token != "" {
						who = "as " + plan.login
					}
					setTwitchChatLink(linkConnected, fmt.Sprintf("Reading #%s %s", plan.channel, who))
				}
			default:
				handle(m)
			}
		}
	}
}
