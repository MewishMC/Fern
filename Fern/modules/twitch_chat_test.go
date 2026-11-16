package modules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeClock pins streamNow for a test.
func fakeClock(t *testing.T, start time.Time) func(time.Duration) {
	t.Helper()
	now := start
	var mu sync.Mutex
	old := streamNow
	streamNow = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	t.Cleanup(func() { streamNow = old })
	return func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
}

func resetTwitchChatState(t *testing.T) {
	t.Helper()
	clear := func() {
		twitchChatClear()
		tchatMu.Lock()
		tchatSkipGif = map[string]int{}
		tchatMu.Unlock()
		tesMu.Lock()
		tesCovers = map[string]bool{}
		tesMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

func TestParseIRC(t *testing.T) {
	m, ok := parseIRC(`@badges=moderator/1,subscriber/12;display-name=Some\sOne;bits=100;user-id=1 :someone!someone@someone.tmi.twitch.tv PRIVMSG #fern :hello there` + "\r\n")
	if !ok || m.Command != "PRIVMSG" || m.Nick() != "someone" || m.Trailing() != "hello there" {
		t.Fatalf("parsed %+v", m)
	}
	if m.Tags["display-name"] != "Some One" || len(m.Params) != 2 || m.Params[0] != "#fern" {
		t.Errorf("tags/params %+v", m)
	}
	c := chatFromIRC(m)
	if !c.Mod || !c.Sub || c.VIP || c.Bits != 100 || c.Name != "Some One" || c.role() != "mod" {
		t.Errorf("chat %+v", c)
	}
	if p, ok := parseIRC("PING :tmi.twitch.tv"); !ok || p.Command != "PING" || p.Trailing() != "tmi.twitch.tv" {
		t.Errorf("ping %+v", p)
	}
	me, _ := parseIRC(":a!a@a PRIVMSG #fern :\x01ACTION waves\x01")
	if got := chatFromIRC(me).Text; got != "waves" {
		t.Errorf("action = %q", got)
	}
	if _, ok := parseIRC(""); ok {
		t.Error("empty line parsed")
	}
}

func TestChatFilters(t *testing.T) {
	o := defaultTwitchChatOptions()
	msg := func(login, text string) chatMessage { return chatMessage{Login: login, Name: login, Text: text} }
	cases := []struct {
		m    chatMessage
		want bool
	}{
		{msg("viewer", "hi all"), true},
		{msg("nightbot", "hi all"), false},
		{msg("coolbot", "hi all"), false},
		{msg("viewer", "!discord"), false},
		{msg("viewer", "go to https://x.y"), false},
		{msg("viewer", "see example.com now"), false},
		{msg("viewer", ""), false},
	}
	for _, c := range cases {
		if got := chatAllowed(o, c.m); got != c.want {
			t.Errorf("%+v: %v", c.m, got)
		}
	}
	o.IgnoreUsers = []string{"pest"}
	o.BlockWords = []string{"spoiler", "/b[a4]d/"}
	for _, m := range []chatMessage{msg("Pest", "hi"), msg("v", "no SPOILERS"), msg("v", "so b4d")} {
		if chatAllowed(o, m) {
			t.Errorf("%+v should be hidden", m)
		}
	}
	o.OnlyRole = ChatRoleSubs
	if chatAllowed(o, msg("v", "hi")) || !chatAllowed(o, chatMessage{Login: "v", Text: "hi", Sub: true}) {
		t.Error("subs only")
	}
	o.OnlyRole = ChatRoleMods
	if chatAllowed(o, chatMessage{Login: "v", Text: "hi", VIP: true}) || !chatAllowed(o, chatMessage{Login: "v", Text: "hi", Mod: true}) {
		t.Error("mods only")
	}
	o = defaultTwitchChatOptions()
	o.IgnoreCommands, o.IgnoreBots, o.HideLinks = false, false, false
	if !chatAllowed(o, msg("nightbot", "!cmd https://x.y")) {
		t.Error("filters off should let it through")
	}
}

func TestTwitchChatLine(t *testing.T) {
	resetTwitchChatState(t)
	step := fakeClock(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cfg := DefaultConfig()
	cfg.TwitchChatOptions.MaxChars = 12
	noteChat(cfg, chatMessage{Login: "fern", Name: "Fern", Text: "a long message that gets cut"})
	if got := twitchChatLine(cfg); got != "💬 Fern: a long mess…" {
		t.Errorf("line = %q", got)
	}
	step(21 * time.Second)
	if got := twitchChatLine(cfg); got != "" {
		t.Errorf("expired line = %q", got)
	}
	cfg.TwitchChatOptions.ChatSec = 0
	if got := twitchChatLine(cfg); got == "" {
		t.Error("0 should keep the message")
	}
	// An alert takes the line for its time, then chat comes back.
	noteTwitchAlert(cfg, twitchAlert{Kind: twitchRaid, Name: "Raider", Amount: 42}, "es")
	if got := twitchChatLine(cfg); got != "🚀 Raider raided with 42" {
		t.Errorf("alert line = %q", got)
	}
	step(9 * time.Second)
	if got := twitchChatLine(cfg); !strings.HasPrefix(got, "💬 Fern") {
		t.Errorf("after the alert = %q", got)
	}
	cfg.TwitchChatOptions.ShowChat = false
	if got := twitchChatLine(cfg); got != "" {
		t.Errorf("chat off = %q", got)
	}
}

func TestAlertThresholdsAndTemplates(t *testing.T) {
	o := defaultTwitchChatOptions()
	o.MinBits, o.MinRaid = 100, 5
	if alertText(o, twitchAlert{Kind: twitchCheer, Name: "A", Amount: 99}) != "" {
		t.Error("cheer under the minimum")
	}
	if got := alertText(o, twitchAlert{Kind: twitchCheer, Name: "A", Amount: 100}); got != "💎 A cheered 100 bits" {
		t.Errorf("cheer = %q", got)
	}
	if alertText(o, twitchAlert{Kind: twitchRaid, Name: "A", Amount: 4}) != "" {
		t.Error("raid under the minimum")
	}
	o.SubText = "{name} x{months} (tier {tier})"
	if got := alertText(o, twitchAlert{Kind: twitchResub, Name: "A", Months: 7, Tier: "2"}); got != "A x7 (tier 2)" {
		t.Errorf("resub = %q", got)
	}
	o.FollowText = ""
	if alertText(o, twitchAlert{Kind: twitchFollow, Name: "A"}) != "" {
		t.Error("an empty text turns the alert off")
	}
	if alertText(o, twitchAlert{Kind: twitchGifted, Name: "A"}) != "" {
		t.Error("gift recipients have no alert of their own")
	}
}

func TestUserNoticeGiftBombAndRaid(t *testing.T) {
	resetTwitchChatState(t)
	fakeClock(t, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	cfg := DefaultConfig()
	notice := func(tags string) {
		m, ok := parseIRC("@" + tags + " :tmi.twitch.tv USERNOTICE #fern")
		if !ok {
			t.Fatal("bad notice")
		}
		handleIRC(cfg, m)
	}
	notice("msg-id=submysterygift;login=gifter;display-name=Gifter;msg-param-mass-gift-count=2;msg-param-sub-plan=1000")
	notice("msg-id=subgift;login=gifter;display-name=Gifter;msg-param-recipient-display-name=R1;msg-param-recipient-user-name=r1")
	notice("msg-id=subgift;login=gifter;display-name=Gifter;msg-param-recipient-display-name=R2;msg-param-recipient-user-name=r2")
	// A later single gift is announced on its own.
	notice("msg-id=subgift;login=gifter;display-name=Gifter;msg-param-recipient-display-name=R3;msg-param-recipient-user-name=r3")
	notice("msg-id=raid;login=raider;display-name=Raider;msg-param-displayName=Raider;msg-param-viewerCount=12")
	var alerts []string
	for _, e := range tchatRecent.snapshot() {
		if e.Kind != "chat" {
			alerts = append(alerts, e.Text)
		}
	}
	want := []string{"🚀 Raider raided with 12", "🎁 Gifter gifted 1 subs", "🎁 Gifter gifted 2 subs"}
	if strings.Join(alerts, "|") != strings.Join(want, "|") {
		t.Errorf("alerts = %q", alerts)
	}
	// With EventSub delivering raids, the chat copy is skipped.
	tesMu.Lock()
	tesCovers = map[string]bool{twitchRaid: true}
	tesMu.Unlock()
	tchatRecent.clear()
	notice("msg-id=raid;login=raider;display-name=Raider;msg-param-viewerCount=12")
	if n := len(tchatRecent.snapshot()); n != 0 {
		t.Errorf("covered raid still noted: %d", n)
	}
}

func TestAlertQueueOrder(t *testing.T) {
	step := fakeClock(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	var q alertQueue
	for _, s := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		q.push(s, time.Second)
	}
	var seen []string
	for i := 0; i < 6; i++ {
		seen = append(seen, q.current())
		step(1100 * time.Millisecond)
	}
	// Only the newest five are kept.
	if got := strings.Join(seen, ""); got != "cdefg" {
		t.Errorf("shown %q", got)
	}
}

// wsFake serves one websocket handler per connection.
func wsFake(t *testing.T, handle func(c *websocket.Conn, r *http.Request)) (*httptest.Server, string) {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		handle(c, r)
	}))
	t.Cleanup(srv.Close)
	return srv, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestRunTwitchIRC(t *testing.T) {
	resetTwitchChatState(t)
	got := make(chan []string, 1)
	_, url := wsFake(t, func(c *websocket.Conn, _ *http.Request) {
		var lines []string
		for len(lines) < 4 {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			lines = append(lines, strings.TrimSpace(string(data)))
		}
		_ = c.WriteMessage(websocket.TextMessage, []byte(":viewer!viewer@viewer.tmi.twitch.tv JOIN #fern\r\n:justinfan1!justinfan1@x JOIN #fern\r\nPING :tmi.twitch.tv\r\n"))
		_, pong, _ := c.ReadMessage()
		lines = append(lines, strings.TrimSpace(string(pong)))
		_ = c.WriteMessage(websocket.TextMessage, []byte("@display-name=Viewer :viewer!viewer@viewer.tmi.twitch.tv PRIVMSG #fern :hi fern\r\n"))
		_ = c.WriteMessage(websocket.TextMessage, []byte(":tmi.twitch.tv RECONNECT\r\n"))
		got <- lines
		time.Sleep(200 * time.Millisecond)
	})
	var msgs []string
	err := runTwitchIRC(context.Background(), url, ircPlan{channel: "fern", login: "justinfan1"}, func(m ircMessage) {
		if m.Command == "PRIVMSG" {
			msgs = append(msgs, chatFromIRC(m).Name+": "+m.Trailing())
		}
	})
	if err != errIRCReconnect {
		t.Fatalf("err = %v", err)
	}
	lines := <-got
	want := []string{"CAP REQ :twitch.tv/tags twitch.tv/commands", "PASS SCHMOOPIIE", "NICK justinfan1", "JOIN #fern", "PONG :tmi.twitch.tv"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("sent %q", lines)
	}
	if len(msgs) != 1 || msgs[0] != "Viewer: hi fern" {
		t.Errorf("messages %q", msgs)
	}
	if l := CurrentTwitchChat().Chat; l.State != linkConnected || !strings.Contains(l.Detail, "anonymously") {
		t.Errorf("link %+v", l)
	}
}

func TestRunTwitchIRCAuthFailure(t *testing.T) {
	_, url := wsFake(t, func(c *websocket.Conn, _ *http.Request) {
		for i := 0; i < 4; i++ {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
		_ = c.WriteMessage(websocket.TextMessage, []byte(":tmi.twitch.tv NOTICE * :Login authentication failed\r\n"))
		time.Sleep(200 * time.Millisecond)
	})
	err := runTwitchIRC(context.Background(), url, ircPlan{channel: "fern", login: "fern", token: "tok"}, func(ircMessage) {})
	if err != errTwitchAuth {
		t.Fatalf("err = %v", err)
	}
}

func TestEventSubAlerts(t *testing.T) {
	cases := []struct {
		typ, event string
		want       twitchAlert
	}{
		{"channel.follow", `{"user_login":"a","user_name":"A"}`, twitchAlert{Kind: twitchFollow, Name: "A", Login: "a"}},
		{"channel.subscribe", `{"user_login":"a","user_name":"A","tier":"1000","is_gift":true}`, twitchAlert{Kind: twitchGifted, Name: "A", Login: "a", Tier: "1", Months: 1}},
		{"channel.subscription.message", `{"user_login":"a","user_name":"A","tier":"2000","cumulative_months":5,"message":{"text":"hey"}}`, twitchAlert{Kind: twitchResub, Name: "A", Login: "a", Tier: "2", Months: 5, Message: "hey"}},
		{"channel.subscription.gift", `{"user_login":null,"user_name":null,"total":5,"tier":"1000","is_anonymous":true}`, twitchAlert{Kind: twitchGift, Name: "Anonymous", Tier: "1", Amount: 5}},
		{"channel.cheer", `{"user_login":"a","user_name":"A","bits":300,"message":"cheer300 gg"}`, twitchAlert{Kind: twitchCheer, Name: "A", Login: "a", Amount: 300, Message: "cheer300 gg"}},
		{"channel.raid", `{"from_broadcaster_user_login":"r","from_broadcaster_user_name":"R","viewers":9}`, twitchAlert{Kind: twitchRaid, Name: "R", Login: "r", Amount: 9}},
		{"channel.channel_points_custom_reward_redemption.add", `{"user_login":"a","user_name":"A","user_input":"blue","reward":{"title":"Hydrate","cost":500}}`, twitchAlert{Kind: twitchRedeem, Name: "A", Login: "a", Reward: "Hydrate", Amount: 500, Message: "blue"}},
	}
	for _, c := range cases {
		got, ok := esAlert(c.typ, json.RawMessage(c.event))
		if !ok || got != c.want {
			t.Errorf("%s: %+v %v", c.typ, got, ok)
		}
	}
	if _, ok := esAlert("stream.online", json.RawMessage(`{}`)); ok {
		t.Error("online is not an alert")
	}
}

func TestRunTwitchESWelcomeNotifyReconnect(t *testing.T) {
	notify := func(id, typ, event string) []byte {
		return []byte(`{"metadata":{"message_id":"` + id + `","message_type":"notification","subscription_type":"` + typ + `"},"payload":{"subscription":{"type":"` + typ + `"},"event":` + event + `}}`)
	}
	_, second := wsFake(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"metadata":{"message_type":"session_welcome"},"payload":{"session":{"id":"s2","keepalive_timeout_seconds":10}}}`))
		_ = c.WriteMessage(websocket.TextMessage, notify("m3", "channel.raid", `{"from_broadcaster_user_name":"R","viewers":3}`))
		time.Sleep(300 * time.Millisecond)
	})
	_, first := wsFake(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"metadata":{"message_type":"session_welcome"},"payload":{"session":{"id":"s1","keepalive_timeout_seconds":10}}}`))
		_ = c.WriteMessage(websocket.TextMessage, notify("m1", "channel.follow", `{"user_name":"A"}`))
		_ = c.WriteMessage(websocket.TextMessage, notify("m1", "channel.follow", `{"user_name":"A"}`)) // a repeat
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"metadata":{"message_type":"session_keepalive"},"payload":{}}`))
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"metadata":{"message_type":"session_reconnect"},"payload":{"session":{"id":"s1","reconnect_url":"`+second+`"}}}`))
		time.Sleep(time.Second)
	})
	var sessions, events []string
	var mu sync.Mutex
	err := runTwitchES(context.Background(), first, func(s string) error {
		sessions = append(sessions, s)
		return nil
	}, func(typ string, ev json.RawMessage) {
		mu.Lock()
		events = append(events, typ)
		mu.Unlock()
	})
	if err == nil {
		t.Fatal("expected the session to end")
	}
	if strings.Join(sessions, ",") != "s1" {
		t.Errorf("welcome ran for %q; a reconnect must not resubscribe", sessions)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(events, ",") != "channel.follow,channel.raid" {
		t.Errorf("events %q", events)
	}
}

func TestSubscribeESStatuses(t *testing.T) {
	var mu sync.Mutex
	var made []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/helix/eventsub/subscriptions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Type      string            `json:"type"`
			Condition map[string]string `json:"condition"`
			Transport map[string]string `json:"transport"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Transport["session_id"] != "sess" || r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		made = append(made, body.Type)
		mu.Unlock()
		if body.Type == "channel.follow" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"subscription missing proper authorization"}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	oldAPI := twitchAPIBase
	twitchAPIBase = srv.URL + "/helix"
	t.Cleanup(func() { twitchAPIBase = oldAPI; setTwitchESLink(linkOff, "") })

	// Watching someone else's channel, signed in without the sub scope.
	plan := esPlan{clientID: "cid", token: "tok", broadcaster: "99", channel: "other",
		auth: twitchAuth{UserID: "1", Login: "me", Scopes: []string{scopeFollowers, scopeBits}}}
	if err := subscribeES(plan, "sess"); err != nil {
		t.Fatal(err)
	}
	byKind := map[string]ESSubStatus{}
	for _, s := range currentTwitchES().subs {
		byKind[s.Kind] = s
	}
	if s := byKind[twitchFollow]; s.OK || !strings.Contains(s.Error, "moderator") {
		t.Errorf("follow %+v", s)
	}
	if s := byKind[twitchSub]; s.OK || !strings.Contains(s.Error, "Sign in again") {
		t.Errorf("sub %+v", s)
	}
	if s := byKind[twitchCheer]; s.OK || !strings.Contains(s.Error, "own channel") {
		t.Errorf("cheer %+v", s)
	}
	if s := byKind[twitchRaid]; !s.OK {
		t.Errorf("raid %+v", s)
	}
	if !twitchESCovers(twitchRaid) || twitchESCovers(twitchFollow) {
		t.Error("covers")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(made, ",") != "channel.follow,channel.raid,stream.online,stream.offline" {
		t.Errorf("subscribed %q", made)
	}
}

func TestMissingScopes(t *testing.T) {
	if got := twitchMissingScopes(twitchAuth{Scopes: []string{scopeFollowers}}); len(got) != len(twitchAllScopes)-1 || got[0] != scopeChatRead {
		t.Errorf("missing %q", got)
	}
	if !strings.Contains(twitchScopes, scopeShoutouts) || !strings.Contains(twitchScopes, scopeChatRead) {
		t.Errorf("sign-in scopes %q", twitchScopes)
	}
}

func TestTwitchRuleMatches(t *testing.T) {
	trig := func(id string) *RuleTrigger {
		tr := lookupTrigger(id)
		if tr == nil {
			t.Fatalf("no trigger %s", id)
		}
		return tr
	}
	chat := trig(TriggerTwitchChat)
	p := SanitizeRuleParams(chat.Fields, RuleParams{"message": "!lights", "role": "vip"})
	ev := RuleEvent{Data: map[string]string{"message": "!lights on", "name": "A", "role": "sub"}}
	if chat.Match(p, ev) {
		t.Error("a sub is not a VIP")
	}
	ev.Data["role"] = "mod"
	if !chat.Match(p, ev) {
		t.Error("mods are above VIPs")
	}
	sub := trig(TriggerTwitchSub)
	any := SanitizeRuleParams(sub.Fields, RuleParams{})
	if !sub.Match(any, RuleEvent{Data: map[string]string{"kind": "resub"}}) || sub.Match(any, RuleEvent{Data: map[string]string{"kind": "gifted"}}) {
		t.Error("sub kinds")
	}
	raid := trig(TriggerTwitchRaid)
	p = SanitizeRuleParams(raid.Fields, RuleParams{"min": 10.0})
	if raid.Match(p, RuleEvent{Data: map[string]string{"amount": "9"}}) || !raid.Match(p, RuleEvent{Data: map[string]string{"amount": "10"}}) {
		t.Error("raid minimum")
	}
	if got := expandStreamText("Thanks {name} for {amount}!", RuleEvent{Data: map[string]string{"name": "A", "amount": "5"}}); got != "Thanks A for 5!" {
		t.Errorf("expand %q", got)
	}
}
