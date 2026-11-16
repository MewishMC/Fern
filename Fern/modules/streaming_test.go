package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParseTikTokFormats(t *testing.T) {
	cases := []struct {
		raw  string
		want tiktokEvent
	}{
		// TikFinity
		{`{"event":"gift","data":{"uniqueId":"u1","nickname":"Nick","giftName":"Rose","repeatCount":5,"diamondCount":1,"giftType":1,"repeatEnd":true}}`,
			tiktokEvent{Kind: "gift", User: "u1", Name: "Nick", Gift: "Rose", Count: 5, Diamonds: 1}},
		{`{"event":"gift","data":{"uniqueId":"u1","giftName":"Rose","repeatCount":3,"diamondCount":1,"giftType":1,"repeatEnd":false}}`,
			tiktokEvent{Kind: "gift", User: "u1", Name: "u1", Gift: "Rose", Count: 3, Diamonds: 1, Ongoing: true}},
		{`{"event":"like","data":{"uniqueId":"u2","likeCount":15,"totalLikeCount":1200}}`,
			tiktokEvent{Kind: "like", User: "u2", Name: "u2", Count: 15, Total: 1200}},
		{`{"event":"roomUser","data":{"viewerCount":87}}`, tiktokEvent{Kind: "viewers", Total: 87}},
		{`{"event":"social","data":{"uniqueId":"u3","displayType":"pm_main_follow_message_viewer_2"}}`,
			tiktokEvent{Kind: "follow", User: "u3", Name: "u3"}},
		{`{"event":"chat","data":{"uniqueId":"u4","nickname":"Four","comment":" hello "}}`,
			tiktokEvent{Kind: "chat", User: "u4", Name: "Four", Text: "hello"}},
		// TikTok-Live-Connector style servers with nested users and other names.
		{`{"type":"FollowEvent","data":{"user":{"uniqueId":"u5","nickname":"Five"}}}`,
			tiktokEvent{Kind: "follow", User: "u5", Name: "Five"}},
		{`{"eventName":"room_user_seq","viewerCount":"42"}`, tiktokEvent{Kind: "viewers", Total: 42}},
		{`{"event":"streamEnd"}`, tiktokEvent{Kind: "end"}},
	}
	for _, c := range cases {
		got, ok := parseTikTok([]byte(c.raw))
		if !ok || got != c.want {
			t.Errorf("%s:\n got %+v %v\nwant %+v", c.raw, got, ok, c.want)
		}
	}
	for _, bad := range []string{`not json`, `{"event":"unknownThing"}`, `{"event":"social","data":{"displayType":"other"}}`} {
		if _, ok := parseTikTok([]byte(bad)); ok {
			t.Errorf("%s parsed", bad)
		}
	}
}

func TestTikTokSessionAndLine(t *testing.T) {
	tiktokReset()
	t.Cleanup(tiktokReset)
	step := fakeClock(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	setTikTokLink(linkConnected, "")
	t.Cleanup(func() { setTikTokLink(linkOff, "") })
	cfg := DefaultConfig()
	cfg.TikTokOptions.MilestoneEvery = 2
	cfg.TikTokOptions.MinDiamonds = 5
	o := &cfg.TikTokOptions

	noteTikTok(cfg, tiktokEvent{Kind: "viewers", Total: 1234})
	noteTikTok(cfg, tiktokEvent{Kind: "like", Count: 10})
	noteTikTok(cfg, tiktokEvent{Kind: "like", Count: 5, Total: 900})
	if got := tiktokLine(cfg); got != "👀 1.2k · ❤️ 900" {
		t.Errorf("line = %q", got)
	}
	noteTikTok(cfg, tiktokEvent{Kind: "follow", User: "a", Name: "A"})
	noteTikTok(cfg, tiktokEvent{Kind: "follow", User: "a", Name: "A"}) // repeated by TikTok
	noteTikTok(cfg, tiktokEvent{Kind: "follow", User: "b", Name: "B"})
	noteTikTok(cfg, tiktokEvent{Kind: "gift", Name: "G", Gift: "Rose", Count: 3, Diamonds: 1, Ongoing: true})
	noteTikTok(cfg, tiktokEvent{Kind: "gift", Name: "G", Gift: "Rose", Count: 4, Diamonds: 1})
	noteTikTok(cfg, tiktokEvent{Kind: "gift", Name: "G", Gift: "Lion", Count: 1, Diamonds: 50})
	info := CurrentTikTok()
	if info.Follows != 2 || info.Gifts != 5 || info.Diamonds != 54 || info.Viewers != 1234 {
		t.Errorf("session %+v", info)
	}
	var shown []string
	for i := 0; i < 5; i++ {
		shown = append(shown, tiktokLine(cfg))
		step(time.Duration(o.AlertSec)*time.Second + time.Millisecond)
	}
	want := []string{"➕ A followed", "➕ B followed", "🎉 2 new followers this stream!", "🎁 G sent Lion ×1", "👀 1.2k · ❤️ 900"}
	if strings.Join(shown, "|") != strings.Join(want, "|") {
		t.Errorf("shown %q", shown)
	}
	setTikTokLink(linkWaiting, "")
	if got := tiktokLine(cfg); got != "" {
		t.Errorf("disconnected line = %q", got)
	}
}

func TestTikTokRuleTriggers(t *testing.T) {
	likes := lookupTrigger(TriggerTikTokLikes)
	p := SanitizeRuleParams(likes.Fields, RuleParams{"every": 1000.0})
	ev := func(before, after string) RuleEvent {
		return RuleEvent{Data: map[string]string{"before": before, "amount": after}}
	}
	if likes.Match(p, ev("900", "999")) || !likes.Match(p, ev("999", "1000")) || !likes.Match(p, ev("1500", "2100")) {
		t.Error("likes crossing")
	}
	gift := lookupTrigger(TriggerTikTokGift)
	p = SanitizeRuleParams(gift.Fields, RuleParams{"gift": "lion", "min": 10.0})
	if !gift.Match(p, RuleEvent{Data: map[string]string{"gift": "Lion", "diamonds": "29999"}}) || gift.Match(p, RuleEvent{Data: map[string]string{"gift": "Rose", "diamonds": "50"}}) {
		t.Error("gift match")
	}
}

func TestRunTikTokReadsBridge(t *testing.T) {
	tiktokReset()
	t.Cleanup(func() { tiktokReset(); setTikTokLink(linkOff, "") })
	_, url := wsFake(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"event":"roomUser","data":{"viewerCount":5}}`))
		_ = c.WriteMessage(websocket.TextMessage, []byte(`garbage`))
		_ = c.WriteMessage(websocket.TextMessage, []byte(`{"event":"follow","data":{"uniqueId":"x"}}`))
	})
	var kinds []string
	err := runTikTok(context.Background(), url, func(e tiktokEvent) { kinds = append(kinds, e.Kind) })
	if err == nil {
		t.Error("expected the bridge to close")
	}
	if strings.Join(kinds, ",") != "viewers,follow" {
		t.Errorf("kinds %q", kinds)
	}
	if err := runTikTok(context.Background(), "ws://127.0.0.1:1/", func(tiktokEvent) {}); err == nil || !strings.Contains(err.Error(), "TikFinity") {
		t.Errorf("no bridge: %v", err)
	}
}

func TestPollKick(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels/fern":
			_, _ = w.Write([]byte(`{"slug":"fern","followers_count":5400,"user":{"username":"Fern"},"livestream":{"is_live":true,"viewer_count":321,"session_title":"vrchat","categories":[{"name":"VRChat"}]}}`))
		case "/channels/off":
			_, _ = w.Write([]byte(`{"slug":"off","followers_count":3,"user":{"username":"Off"},"livestream":null}`))
		case "/channels/blocked":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := kickAPIBase
	kickAPIBase = srv.URL + "/channels/"
	t.Cleanup(func() { kickAPIBase = old })

	cfg := DefaultConfig()
	cfg.KickOptions.Channel = "fern"
	d, err := pollKick(cfg)
	if err != nil || !d.live || d.viewers != 321 || d.followers != 5400 || d.category != "VRChat" || d.channel != "Fern" {
		t.Fatalf("live %+v %v", d, err)
	}
	d.ok = true
	if got := liveStatusLine(cfg.KickOptions, d, "follower"); got != "🟢 LIVE · 321 watching" {
		t.Errorf("line %q", got)
	}
	cfg.KickOptions.Template = "{channel} {viewerCount} {followers} {category}"
	if got := liveStatusLine(cfg.KickOptions, d, "follower"); got != "Fern 321 5.4k VRChat" {
		t.Errorf("template %q", got)
	}
	cfg.KickOptions.Channel = "off"
	if d, err := pollKick(cfg); err != nil || d.live {
		t.Errorf("offline %+v %v", d, err)
	} else if d.ok = true; liveStatusLine(cfg.KickOptions, d, "follower") != "" {
		t.Error("offline without text should hide")
	}
	cfg.KickOptions.Channel = "nobody"
	if _, err := pollKick(cfg); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Errorf("missing %v", err)
	}
	cfg.KickOptions.Channel = "blocked"
	if _, err := pollKick(cfg); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("blocked %v", err)
	}
	cfg.KickOptions.Channel = ""
	if _, err := pollKick(cfg); err == nil {
		t.Error("empty channel")
	}
}

func TestPollYouTube(t *testing.T) {
	useTempSecrets(t)
	const key = "AIzaSECRETKEY"
	if err := SetSecret(SecretYouTubeKey, key); err != nil {
		t.Fatal(err)
	}
	live := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") != key {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","errors":[{"reason":"keyInvalid"}]}}`))
			return
		}
		switch r.URL.Path {
		case "/yt/channels":
			if q.Get("forHandle") != "@fern" {
				_, _ = w.Write([]byte(`{"items":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"UCxxxxxxxxxxxxxxxxxxxxxx","snippet":{"title":"Fern"},"statistics":{"subscriberCount":"12500"},"contentDetails":{"relatedPlaylists":{"uploads":"UUxx"}}}]}`))
		case "/yt/playlistItems":
			_, _ = w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"v1"}},{"contentDetails":{"videoId":"v2"}}]}`))
		case "/yt/videos":
			if q.Get("id") != "v1,v2" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			state := "none"
			if live {
				state = "live"
			}
			_, _ = w.Write([]byte(`{"items":[{"snippet":{"title":"old","liveBroadcastContent":"none"}},{"snippet":{"title":"Live now","liveBroadcastContent":"` + state + `"},"liveStreamingDetails":{"concurrentViewers":"77"}}]}`))
		}
	}))
	defer srv.Close()
	old := youtubeAPIBase
	youtubeAPIBase = srv.URL + "/yt"
	t.Cleanup(func() { youtubeAPIBase = old })

	cfg := DefaultConfig()
	cfg.YouTubeOptions.Channel = youTubeChannelRef("https://www.youtube.com/@fern")
	d, err := pollYouTube(cfg)
	if err != nil || !d.live || d.viewers != 77 || d.followers != 12500 || d.title != "Live now" {
		t.Fatalf("live %+v %v", d, err)
	}
	d.ok = true
	cfg.YouTubeOptions.Template = "{live} {viewers} · {subscribers} subs"
	if got := liveStatusLine(cfg.YouTubeOptions, d, "subscriber"); got != "🔴 LIVE 77 watching · 12k subs" {
		t.Errorf("line %q", got)
	}
	live = false
	if d, err := pollYouTube(cfg); err != nil || d.live {
		t.Errorf("offline %+v %v", d, err)
	}
	cfg.YouTubeOptions.Channel = "@nobody"
	if _, err := pollYouTube(cfg); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Errorf("unknown channel %v", err)
	}
	_ = SetSecret(SecretYouTubeKey, "wrong")
	_, err = pollYouTube(cfg)
	var bad badConfigError
	if err == nil || !strings.Contains(err.Error(), "not valid") || strings.Contains(err.Error(), "wrong") {
		t.Errorf("bad key %v", err)
	} else if !errorsAs(err, &bad) {
		t.Errorf("a bad key should be a config error: %T", err)
	}
	_ = SetSecret(SecretYouTubeKey, "")
	if _, err := pollYouTube(cfg); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("no key %v", err)
	}
}

func errorsAs(err error, target *badConfigError) bool {
	b, ok := err.(badConfigError)
	if ok {
		*target = b
	}
	return ok
}

func TestStreamErrorsHideTheKey(t *testing.T) {
	// The transport error of a request must not carry the address.
	old := youtubeAPIBase
	youtubeAPIBase = "http://127.0.0.1:1/yt"
	t.Cleanup(func() { youtubeAPIBase = old })
	err := ytGet("/channels", map[string][]string{}, "AIzaSECRET", &struct{}{})
	if err == nil || strings.Contains(err.Error(), "AIzaSECRET") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("err %v", err)
	}
}

func TestStreamingChannelRefs(t *testing.T) {
	for in, want := range map[string]string{
		"https://kick.com/Fern_Live": "fern_live", "@fern": "fern", "kick.com/fern/videos": "fern", " Fern ": "fern",
	} {
		if got := kickSlug(in); got != want {
			t.Errorf("kickSlug(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{
		"https://www.youtube.com/@Fern.VR": "@Fern.VR",
		"fern":                             "@fern",
		"youtube.com/channel/UCabcdefghijklmnopqrstuv":           "UCabcdefghijklmnopqrstuv",
		"https://m.youtube.com/channel/UCabcdefghijklmnopqrstuv": "UCabcdefghijklmnopqrstuv",
		"not a handle!": "",
		"":              "",
	} {
		if got := youTubeChannelRef(in); got != want {
			t.Errorf("youTubeChannelRef(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStreamingSanitize(t *testing.T) {
	c := DefaultConfig()
	c.sanitize()
	def := DefaultConfig()
	if c.TwitchChatOptions.ChatTemplate != def.TwitchChatOptions.ChatTemplate || c.TikTokOptions.URL != TikFinityURL ||
		c.KickOptions.IntervalSec != 60 || c.YouTubeOptions.IntervalSec != 120 || c.TikTokOptions.MilestoneEvery != 10 {
		t.Errorf("defaults changed by sanitize: %+v %+v", c.TwitchChatOptions, c.TikTokOptions)
	}
	c.TwitchChatOptions = TwitchChatOptions{OnlyRole: "kings", ChatSec: -5, MaxChars: 1, AlertSec: 9999,
		IgnoreUsers: []string{" @Nightbot ", "nightbot", ""}, FollowText: "a\n\nb"}
	c.TikTokOptions = TikTokOptions{URL: "http://localhost:21213", AlertSec: 0, MilestoneEvery: -1}
	c.KickOptions = LiveStatusOptions{Channel: "https://kick.com/Some.One", IntervalSec: 1}
	c.YouTubeOptions = LiveStatusOptions{Channel: "bad handle!", IntervalSec: 99999}
	c.sanitize()
	tc := c.TwitchChatOptions
	if tc.OnlyRole != ChatRoleAll || tc.ChatSec != 0 || tc.MaxChars != 10 || tc.AlertSec != 120 || tc.ChatTemplate == "" ||
		len(tc.IgnoreUsers) != 1 || tc.IgnoreUsers[0] != "nightbot" || tc.FollowText != "a b" {
		t.Errorf("twitch chat %+v", tc)
	}
	if c.TikTokOptions.URL != TikFinityURL || c.TikTokOptions.AlertSec != 2 || c.TikTokOptions.MilestoneEvery != 0 {
		t.Errorf("tiktok %+v", c.TikTokOptions)
	}
	if c.KickOptions.Channel != "someone" || c.KickOptions.IntervalSec != 30 || c.KickOptions.Template == "" {
		t.Errorf("kick %+v", c.KickOptions)
	}
	if c.YouTubeOptions.Channel != "" || c.YouTubeOptions.IntervalSec != 3600 {
		t.Errorf("youtube %+v", c.YouTubeOptions)
	}
	for _, id := range []string{ModuleTwitchChat, ModuleTikTok, ModuleKick, ModuleYouTube} {
		if !knownModules[id] || moduleFlag(&c.DisplayOptions, id) == nil || LineLabel(id) == id {
			t.Errorf("line %s is not wired", id)
		}
		found := false
		for _, d := range c.DisplayOptions.DropOrder {
			found = found || d == id
		}
		if !found {
			t.Errorf("%s missing from the drop order", id)
		}
	}
	if !panelSecrets[SecretYouTubeKey] {
		t.Error("the panel cannot set the YouTube key")
	}
}

func TestRulesUseTrigger(t *testing.T) {
	c := DefaultConfig()
	if rulesUseTrigger(c, "twitch.") {
		t.Error("no rules")
	}
	c.Rules.List = []Rule{{Enabled: true, Trigger: RuleStep{Type: TriggerTime}, Conditions: []RuleStep{{Type: TriggerTikTokViewers}}}}
	if !rulesUseTrigger(c, "tiktok.") || rulesUseTrigger(c, "twitch.") {
		t.Error("a condition counts")
	}
	c.Rules.List[0].Enabled = false
	if rulesUseTrigger(c, "tiktok.") {
		t.Error("disabled rules do not count")
	}
}
