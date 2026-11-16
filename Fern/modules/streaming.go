package modules

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Streaming platforms besides the Twitch status line: Twitch chat and alerts
// (twitch_chat.go, twitch_eventsub.go), TikTok Live through a local bridge
// (tiktok.go), and Kick and YouTube live status (kick.go, youtube.go). Each
// runs on its own worker and publishes a snapshot under a mutex, like the
// other app integrations, so a slow site never holds up the chatbox.

// Ids of the streaming lines.
const (
	ModuleTwitchChat = "twitchchat"
	ModuleTikTok     = "tiktok"
	ModuleKick       = "kick"
	ModuleYouTube    = "youtube"
)

// SecretYouTubeKey is the YouTube Data API key.
const SecretYouTubeKey = "youtube_key"

func init() {
	panelSecrets[SecretYouTubeKey] = true
	lineLabels[ModuleTwitchChat] = "Twitch chat"
	lineLabels[ModuleTikTok] = "TikTok Live"
	lineLabels[ModuleKick] = "Kick"
	lineLabels[ModuleYouTube] = "YouTube"
}

// streamNow is the clock of the streaming lines, swapped in tests.
var streamNow = time.Now

// TwitchChatOptions controls the Twitch chat and alerts line.
type TwitchChatOptions struct {
	Label          string   `json:"label"`
	ShowChat       bool     `json:"show_chat"`
	ChatTemplate   string   `json:"chat_template"` // {name} {user} {message}
	ChatSec        int      `json:"chat_sec"`      // how long a message stays; 0 until the next one
	MaxChars       int      `json:"max_chars"`     // the message is cut to this
	IgnoreCommands bool     `json:"ignore_commands"`
	IgnoreBots     bool     `json:"ignore_bots"`
	IgnoreUsers    []string `json:"ignore_users"`
	BlockWords     []string `json:"block_words"` // words or /regex/ that hide a message
	HideLinks      bool     `json:"hide_links"`
	OnlyRole       string   `json:"only_role"` // all, subs, vips, mods
	AlertSec       int      `json:"alert_sec"`
	// Alert texts; an empty one turns that alert off.
	FollowText string `json:"follow_text"`
	SubText    string `json:"sub_text"`
	GiftText   string `json:"gift_text"`
	RaidText   string `json:"raid_text"`
	CheerText  string `json:"cheer_text"`
	RedeemText string `json:"redeem_text"`
	MinBits    int    `json:"min_bits"`
	MinRaid    int    `json:"min_raid"`
}

// Values of TwitchChatOptions.OnlyRole.
const (
	ChatRoleAll  = "all"
	ChatRoleSubs = "subs"
	ChatRoleVIPs = "vips"
	ChatRoleMods = "mods"
)

func defaultTwitchChatOptions() TwitchChatOptions {
	return TwitchChatOptions{
		ShowChat:       true,
		ChatTemplate:   "💬 {name}: {message}",
		ChatSec:        20,
		MaxChars:       100,
		IgnoreCommands: true,
		IgnoreBots:     true,
		IgnoreUsers:    []string{},
		BlockWords:     []string{},
		HideLinks:      true,
		OnlyRole:       ChatRoleAll,
		AlertSec:       8,
		FollowText:     "💜 {name} followed",
		SubText:        "⭐ {name} subscribed",
		GiftText:       "🎁 {name} gifted {amount} subs",
		RaidText:       "🚀 {name} raided with {amount}",
		CheerText:      "💎 {name} cheered {amount} bits",
		RedeemText:     "🎯 {name} redeemed {reward}",
	}
}

func (o TwitchChatOptions) clone() TwitchChatOptions {
	o.IgnoreUsers = append([]string{}, o.IgnoreUsers...)
	o.BlockWords = append([]string{}, o.BlockWords...)
	return o
}

// TikTokOptions controls the TikTok Live bridge and its line.
type TikTokOptions struct {
	URL            string `json:"url"`      // the bridge's websocket
	Template       string `json:"template"` // {viewers} {likes} {follows} {shares} {diamonds} {gifts}
	Compact        bool   `json:"compact"`
	AlertSec       int    `json:"alert_sec"`
	FollowText     string `json:"follow_text"` // empty turns the alert off
	GiftText       string `json:"gift_text"`
	ShareText      string `json:"share_text"`
	MilestoneText  string `json:"milestone_text"`
	MinDiamonds    int    `json:"min_diamonds"`
	MilestoneEvery int    `json:"milestone_every"` // new followers per milestone; 0 is off
}

// TikFinityURL is TikFinity's local websocket.
const TikFinityURL = "ws://localhost:21213/"

func defaultTikTokOptions() TikTokOptions {
	return TikTokOptions{
		URL:            TikFinityURL,
		Template:       "👀 {viewers} · ❤️ {likes}",
		Compact:        true,
		AlertSec:       8,
		FollowText:     "➕ {name} followed",
		GiftText:       "🎁 {name} sent {gift} ×{amount}",
		MilestoneText:  "🎉 {amount} new followers this stream!",
		MilestoneEvery: 10,
	}
}

// LiveStatusOptions controls a Kick or YouTube live status line.
type LiveStatusOptions struct {
	Channel     string `json:"channel"`
	IntervalSec int    `json:"interval_sec"`
	LiveText    string `json:"live_text"`
	ViewerLabel string `json:"viewer_label"`
	Template    string `json:"template"`
	OfflineText string `json:"offline_text"` // empty hides the line while offline
	Compact     bool   `json:"compact"`
}

func defaultKickOptions() LiveStatusOptions {
	return LiveStatusOptions{IntervalSec: 60, LiveText: "🟢 LIVE", ViewerLabel: "watching", Template: "{live} · {viewers}", Compact: true}
}

func defaultYouTubeOptions() LiveStatusOptions {
	return LiveStatusOptions{IntervalSec: 120, LiveText: "🔴 LIVE", ViewerLabel: "watching", Template: "{live} · {viewers}", Compact: true}
}

// sanitizeStreaming bounds the streaming options.
func (c *Config) sanitizeStreaming() {
	trim := func(s string, n int) string { return RuneLimit(strings.TrimSpace(s), n) }
	oneLine := func(s string, n int) string { return trim(strings.Join(strings.Fields(s), " "), n) }

	def := defaultTwitchChatOptions()
	t := &c.TwitchChatOptions
	t.Label = trim(t.Label, 16)
	t.ChatTemplate = trim(t.ChatTemplate, 120)
	if t.ChatTemplate == "" {
		t.ChatTemplate = def.ChatTemplate
	}
	t.ChatSec = clampInt(t.ChatSec, 0, 600)
	t.MaxChars = clampInt(t.MaxChars, 10, 144)
	t.IgnoreUsers = cleanList(t.IgnoreUsers, 100, 40, true)
	t.BlockWords = cleanList(t.BlockWords, 100, 80, false)
	switch t.OnlyRole {
	case ChatRoleAll, ChatRoleSubs, ChatRoleVIPs, ChatRoleMods:
	default:
		t.OnlyRole = ChatRoleAll
	}
	t.AlertSec = clampInt(t.AlertSec, 2, 120)
	for _, s := range []*string{&t.FollowText, &t.SubText, &t.GiftText, &t.RaidText, &t.CheerText, &t.RedeemText} {
		*s = oneLine(*s, 100)
	}
	t.MinBits = clampInt(t.MinBits, 0, 1_000_000)
	t.MinRaid = clampInt(t.MinRaid, 0, 1_000_000)

	k := &c.TikTokOptions
	k.URL = strings.TrimSpace(k.URL)
	if !validWSURL(k.URL) {
		k.URL = TikFinityURL
	}
	k.URL = RuneLimit(k.URL, 300)
	k.Template = trim(k.Template, 120)
	k.AlertSec = clampInt(k.AlertSec, 2, 120)
	for _, s := range []*string{&k.FollowText, &k.GiftText, &k.ShareText, &k.MilestoneText} {
		*s = oneLine(*s, 100)
	}
	k.MinDiamonds = clampInt(k.MinDiamonds, 0, 1_000_000)
	k.MilestoneEvery = clampInt(k.MilestoneEvery, 0, 100_000)

	sanitizeLiveStatus(&c.KickOptions, defaultKickOptions(), kickSlug, 30)
	sanitizeLiveStatus(&c.YouTubeOptions, defaultYouTubeOptions(), youTubeChannelRef, 60)
}

func sanitizeLiveStatus(o *LiveStatusOptions, def LiveStatusOptions, channel func(string) string, minInterval int) {
	trim := func(s string, n int) string { return RuneLimit(strings.TrimSpace(s), n) }
	o.Channel = channel(o.Channel)
	o.IntervalSec = clampInt(o.IntervalSec, minInterval, 3600)
	o.LiveText = trim(o.LiveText, 24)
	o.ViewerLabel = trim(o.ViewerLabel, 24)
	o.Template = trim(o.Template, 200)
	if o.Template == "" {
		o.Template = def.Template
	}
	o.OfflineText = trim(o.OfflineText, 60)
}

func validWSURL(s string) bool {
	return (strings.HasPrefix(s, "ws://") || strings.HasPrefix(s, "wss://")) && !strings.ContainsAny(s, " \t\r\n")
}

// cleanList trims entries, drops empty ones and repeats, and bounds the list.
func cleanList(in []string, maxItems, maxLen int, lower bool) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		s = RuneLimit(strings.TrimSpace(s), maxLen)
		if lower {
			s = strings.ToLower(strings.TrimPrefix(s, "@"))
		}
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
		if len(out) == maxItems {
			break
		}
	}
	return out
}

// rulesUseTrigger reports whether an enabled rule uses a trigger whose id
// starts with prefix, as its trigger or a condition. A connection that only
// feeds rules stays closed while nothing would listen.
func rulesUseTrigger(c Config, prefix string) bool {
	if !c.Rules.Enabled {
		return false
	}
	for _, r := range c.Rules.List {
		if !r.Enabled {
			continue
		}
		if strings.HasPrefix(r.Trigger.Type, prefix) {
			return true
		}
		for _, cond := range r.Conditions {
			if strings.HasPrefix(cond.Type, prefix) {
				return true
			}
		}
	}
	return false
}

// alertQueue shows short alerts one after another on a line.
type alertQueue struct {
	mu    sync.Mutex
	queue []queuedAlert
	cur   string
	until time.Time
}

type queuedAlert struct {
	text string
	d    time.Duration
}

// maxQueuedAlerts keeps a raid or gift bomb from filling the next minutes.
const maxQueuedAlerts = 5

func (q *alertQueue) push(text string, d time.Duration) {
	text = strings.TrimSpace(text)
	if text == "" || d <= 0 {
		return
	}
	q.mu.Lock()
	q.queue = append(q.queue, queuedAlert{text, d})
	if len(q.queue) > maxQueuedAlerts {
		q.queue = q.queue[len(q.queue)-maxQueuedAlerts:]
	}
	q.mu.Unlock()
	NotifyChange()
}

// current returns the alert to show now, moving on to the next queued one
// when the shown one has had its time.
func (q *alertQueue) current() string {
	now := streamNow()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.cur != "" && now.Before(q.until) {
		return q.cur
	}
	q.cur = ""
	if len(q.queue) > 0 {
		next := q.queue[0]
		q.queue = q.queue[1:]
		q.cur, q.until = next.text, now.Add(next.d)
	}
	return q.cur
}

func (q *alertQueue) clear() {
	q.mu.Lock()
	q.queue, q.cur = nil, ""
	q.mu.Unlock()
}

// streamCount formats a count, short when asked: 1.2k instead of 1234.
func streamCount(n int, compact bool) string {
	if compact {
		return compactCount(n)
	}
	return strconv.Itoa(max(n, 0))
}

// liveStatus is a Kick or YouTube channel as last polled.
type liveStatus struct {
	ok        bool
	channel   string
	live      bool
	viewers   int
	followers int // followers on Kick, subscribers on YouTube
	title     string
	category  string
	checkedAt time.Time
}

// liveStatusLine renders a Kick or YouTube line. followWord names the
// follower placeholders: "follower" or "subscriber".
func liveStatusLine(o LiveStatusOptions, d liveStatus, followWord string) string {
	if !d.ok {
		return ""
	}
	if !d.live {
		return o.OfflineText
	}
	count := func(n int) string { return streamCount(n, o.Compact) }
	return fillTemplate(o.Template, map[string]string{
		"live":               o.LiveText,
		"viewers":            strings.TrimSpace(count(d.viewers) + " " + o.ViewerLabel),
		"viewerCount":        count(d.viewers),
		"title":              d.title,
		"category":           d.category,
		"channel":            d.channel,
		followWord + "s":     count(d.followers),
		followWord + "Count": count(d.followers),
	})
}

// LiveStatusInfo is a Kick or YouTube channel for the panel.
type LiveStatusInfo struct {
	Link      LinkStatus `json:"link"`
	Channel   string     `json:"channel"`
	Live      bool       `json:"live"`
	Viewers   int        `json:"viewers"`
	Followers int        `json:"followers"`
	Title     string     `json:"title"`
	Category  string     `json:"category"`
	CheckedAt int64      `json:"checkedAt"`
}

func (d liveStatus) info(link LinkStatus) LiveStatusInfo {
	info := LiveStatusInfo{Link: link, Channel: d.channel, Live: d.live, Viewers: d.viewers,
		Followers: d.followers, Title: d.title, Category: d.category}
	if !d.checkedAt.IsZero() {
		info.CheckedAt = d.checkedAt.UnixMilli()
	}
	return info
}

// StreamEvent is one recent chat message or alert, for the panel.
type StreamEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

// eventLog keeps the latest few events for the panel.
type eventLog struct {
	mu   sync.Mutex
	list []StreamEvent
}

const maxEventLog = 12

func (l *eventLog) add(kind, text string) {
	l.mu.Lock()
	l.list = append(l.list, StreamEvent{Kind: kind, Text: text, At: streamNow().UnixMilli()})
	if len(l.list) > maxEventLog {
		l.list = l.list[len(l.list)-maxEventLog:]
	}
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []StreamEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]StreamEvent, len(l.list))
	// Newest first.
	for i, e := range l.list {
		out[len(l.list)-1-i] = e
	}
	return out
}

func (l *eventLog) clear() {
	l.mu.Lock()
	l.list = nil
	l.mu.Unlock()
}

// StreamingInfo is the streaming part of the panel state.
type StreamingInfo struct {
	TwitchChat TwitchChatInfo `json:"twitchChat"`
	TikTok     TikTokInfo     `json:"tiktok"`
	Kick       LiveStatusInfo `json:"kick"`
	YouTube    LiveStatusInfo `json:"youtube"`
}

// CurrentStreaming gathers the streaming snapshots.
func CurrentStreaming() StreamingInfo {
	return StreamingInfo{
		TwitchChat: CurrentTwitchChat(),
		TikTok:     CurrentTikTok(),
		Kick:       CurrentKick(),
		YouTube:    CurrentYouTube(),
	}
}

// StreamCommand runs a panel action for one of the streaming pages.
func StreamCommand(module, action string) {
	switch module {
	case ModuleTwitchChat:
		switch action {
		case "reconnect":
			twitchChatReconnect()
		case "clear":
			twitchChatClear()
		case "test":
			twitchTestAlert()
		}
	case ModuleTikTok:
		switch action {
		case "reconnect":
			poke(tiktokKick)
		case "clear":
			tiktokReset()
		case "test":
			tiktokTestAlert()
		}
	case ModuleKick:
		if action == "refresh" {
			poke(kickKickCh)
		}
	case ModuleYouTube:
		if action == "refresh" {
			poke(youtubeKickCh)
		}
	}
}

func startStreamingWorkers() {
	startTwitchChatWorker()
	startTwitchESWorker()
	startTikTokWorker()
	startKickWorker()
	startYouTubeWorker()
}
