package modules

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TikTok has no public API for live events, so Fern reads them from a bridge
// the user runs: TikFinity's local websocket (ws://localhost:21213/), or a
// TikTok-Live-Connector style server. Both send one JSON object per event,
// {"event": "gift", "data": {...}}, with the field names TikTok-Live-Connector
// uses; parseTikTok also accepts "type" or "eventName" for the name and flat
// objects without "data".

// Rule triggers for TikTok Live.
const (
	TriggerTikTokFollow    = "tiktok.follow"
	TriggerTikTokGift      = "tiktok.gift"
	TriggerTikTokLikes     = "tiktok.likes"
	TriggerTikTokShare     = "tiktok.share"
	TriggerTikTokChat      = "tiktok.chat"
	TriggerTikTokMilestone = "tiktok.milestone"
	TriggerTikTokViewers   = "tiktok.viewers"
)

// tiktokEvent is one normalised bridge event.
type tiktokEvent struct {
	Kind     string // chat, gift, like, follow, share, join, viewers, end
	User     string // unique id
	Name     string // nickname
	Text     string
	Gift     string
	Count    int // gift repeats or likes in this event
	Diamonds int // per gift
	Total    int // total likes or viewers
	Ongoing  bool
}

// jsonObj reads loosely typed bridge JSON.
type jsonObj map[string]any

func (o jsonObj) obj(key string) jsonObj {
	if v, ok := o[key].(map[string]any); ok {
		return v
	}
	return nil
}

func (o jsonObj) str(keys ...string) string {
	for _, k := range keys {
		switch v := o[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

func (o jsonObj) num(keys ...string) (int, bool) {
	for _, k := range keys {
		switch v := o[k].(type) {
		case float64:
			return int(v), true
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func (o jsonObj) boolean(key string) (bool, bool) {
	switch v := o[key].(type) {
	case bool:
		return v, true
	case float64:
		return v != 0, true
	case string:
		return v == "true" || v == "1", true
	}
	return false, false
}

// tiktokKinds maps bridge event names, lowercased with "_", "-" and an
// "event" suffix removed, to Fern's kinds.
var tiktokKinds = map[string]string{
	"chat": "chat", "comment": "chat",
	"gift":   "gift",
	"like":   "like",
	"follow": "follow",
	"share":  "share",
	"member": "join", "join": "join",
	"roomuser": "viewers", "roomuserseq": "viewers", "viewercount": "viewers", "viewerupdate": "viewers", "viewer": "viewers",
	"streamend": "end", "liveend": "end",
	"social": "social",
}

// parseTikTok reads one bridge message.
func parseTikTok(raw []byte) (tiktokEvent, bool) {
	var top jsonObj
	if json.Unmarshal(raw, &top) != nil {
		return tiktokEvent{}, false
	}
	name := top.str("event", "type", "eventName", "eventType", "name")
	data := top.obj("data")
	if data == nil {
		data = top.obj("payload")
	}
	if data == nil {
		data = top
	}
	key := strings.ToLower(name)
	key = strings.NewReplacer("_", "", "-", "", " ", "").Replace(key)
	key = strings.TrimSuffix(key, "event")
	key = strings.TrimPrefix(key, "on")
	kind := tiktokKinds[key]
	if kind == "" {
		return tiktokEvent{}, false
	}
	user := data.obj("user")
	if user == nil {
		user = jsonObj{}
	}
	e := tiktokEvent{
		Kind: kind,
		User: firstNonEmpty(data.str("uniqueId", "unique_id", "username"), user.str("uniqueId", "unique_id", "username")),
		Name: firstNonEmpty(data.str("nickname", "displayName"), user.str("nickname", "displayName")),
	}
	if e.Name == "" {
		e.Name = e.User
	}
	switch kind {
	case "social":
		label := strings.ToLower(data.str("displayType", "label"))
		switch {
		case strings.Contains(label, "follow"):
			e.Kind = "follow"
		case strings.Contains(label, "share"):
			e.Kind = "share"
		default:
			return tiktokEvent{}, false
		}
	case "chat":
		e.Text = strings.TrimSpace(data.str("comment", "text", "message"))
	case "gift":
		gift := data.obj("gift")
		if gift == nil {
			gift = jsonObj{}
		}
		e.Gift = firstNonEmpty(data.str("giftName"), gift.str("name", "giftName"), data.str("describe"))
		e.Count, _ = data.num("repeatCount", "repeat_count", "count")
		if e.Count <= 0 {
			e.Count = 1
		}
		if d, ok := data.num("diamondCount", "diamond_count", "diamonds"); ok {
			e.Diamonds = d
		} else {
			e.Diamonds, _ = gift.num("diamond_count", "diamondCount")
		}
		// Streakable gifts (giftType 1) repeat with a growing count until
		// repeatEnd; only the end counts.
		giftType, _ := data.num("giftType", "gift_type")
		if giftType == 0 {
			giftType, _ = gift.num("type", "giftType")
		}
		if end, ok := data.boolean("repeatEnd"); ok && giftType == 1 && !end {
			e.Ongoing = true
		}
	case "like":
		e.Count, _ = data.num("likeCount", "like_count", "count")
		e.Total, _ = data.num("totalLikeCount", "total_like_count", "totalLikes", "total")
	case "viewers":
		e.Total, _ = data.num("viewerCount", "viewer_count", "viewers", "total", "count")
	}
	return e, true
}

// tiktokState is the live session as seen through the bridge.
type tiktokState struct {
	viewers, likes, follows, shares, diamonds, gifts int
	followed                                         map[string]bool
	since                                            time.Time
}

var (
	ttMu       sync.Mutex
	ttLink     = LinkStatus{State: linkOff}
	ttState    = tiktokState{followed: map[string]bool{}}
	ttAlerts   alertQueue
	ttRecent   eventLog
	tiktokKick = make(chan struct{}, 1)
	ttGen      int
)

func setTikTokLink(state, detail string) {
	ttMu.Lock()
	ttLink = LinkStatus{State: state, Detail: detail}
	ttMu.Unlock()
}

// tiktokReset starts the session counts over.
func tiktokReset() {
	ttMu.Lock()
	ttState = tiktokState{followed: map[string]bool{}, since: streamNow()}
	ttMu.Unlock()
	ttAlerts.clear()
	ttRecent.clear()
	NotifyChange()
}

func tiktokAlert(o TikTokOptions, tmpl string, vals map[string]string) {
	if tmpl == "" {
		return
	}
	text := fillTemplate(tmpl, vals)
	ttAlerts.push(text, time.Duration(o.AlertSec)*time.Second)
	ttRecent.add("alert", text)
}

// noteTikTok applies one event to the session and fires its triggers.
func noteTikTok(cfg Config, e tiktokEvent) {
	o := cfg.TikTokOptions
	data := map[string]string{"name": e.Name, "user": e.User}
	ttMu.Lock()
	s := &ttState
	if s.followed == nil {
		s.followed = map[string]bool{}
	}
	switch e.Kind {
	case "viewers":
		s.viewers = max(e.Total, 0)
		ttMu.Unlock()
		NudgeRules()
		NotifyChange()
		return
	case "like":
		before := s.likes
		if e.Total > 0 {
			s.likes = max(s.likes, e.Total)
		} else {
			s.likes += max(e.Count, 0)
		}
		after := s.likes
		ttMu.Unlock()
		if after > before && rulesUseTrigger(cfg, TriggerTikTokLikes) {
			data["amount"], data["before"], data["added"] = strconv.Itoa(after), strconv.Itoa(before), strconv.Itoa(after-before)
			FireTrigger(TriggerTikTokLikes, data)
		}
		NotifyChange()
		return
	case "follow":
		key := strings.ToLower(firstNonEmpty(e.User, e.Name))
		if key != "" && s.followed[key] {
			ttMu.Unlock()
			return // TikTok repeats follows now and then
		}
		if key != "" {
			s.followed[key] = true
		}
		s.follows++
		count := s.follows
		ttMu.Unlock()
		FireTrigger(TriggerTikTokFollow, data)
		tiktokAlert(o, o.FollowText, map[string]string{"name": e.Name, "user": e.User})
		if o.MilestoneEvery > 0 && count%o.MilestoneEvery == 0 {
			FireTrigger(TriggerTikTokMilestone, map[string]string{"amount": strconv.Itoa(count)})
			tiktokAlert(o, o.MilestoneText, map[string]string{"amount": strconv.Itoa(count)})
		}
		return
	case "gift":
		if e.Ongoing {
			ttMu.Unlock()
			return
		}
		s.gifts += e.Count
		total := e.Diamonds * e.Count
		s.diamonds += total
		ttMu.Unlock()
		data["gift"], data["amount"], data["diamonds"] = e.Gift, strconv.Itoa(e.Count), strconv.Itoa(total)
		FireTrigger(TriggerTikTokGift, data)
		if total >= o.MinDiamonds {
			tiktokAlert(o, o.GiftText, map[string]string{"name": e.Name, "user": e.User, "gift": e.Gift,
				"amount": strconv.Itoa(e.Count), "diamonds": strconv.Itoa(total)})
		}
		return
	case "share":
		s.shares++
		ttMu.Unlock()
		FireTrigger(TriggerTikTokShare, data)
		tiktokAlert(o, o.ShareText, map[string]string{"name": e.Name, "user": e.User})
		return
	case "chat":
		ttMu.Unlock()
		if e.Text != "" {
			ttRecent.add("chat", e.Name+": "+e.Text)
			if rulesUseTrigger(cfg, TriggerTikTokChat) {
				data["message"] = e.Text
				FireTrigger(TriggerTikTokChat, data)
			}
		}
		return
	case "end":
		s.viewers = 0
		ttMu.Unlock()
		ttRecent.add("end", "The live ended")
		NudgeRules()
		return
	}
	ttMu.Unlock()
}

// tiktokLine renders the alert being shown, or the session numbers.
func tiktokLine(cfg Config) string {
	o := cfg.TikTokOptions
	if text := ttAlerts.current(); text != "" {
		return text
	}
	ttMu.Lock()
	s, link := ttState, ttLink
	ttMu.Unlock()
	if link.State != linkConnected || o.Template == "" {
		return ""
	}
	n := func(v int) string { return streamCount(v, o.Compact) }
	return fillTemplate(o.Template, map[string]string{
		"viewers": n(s.viewers), "likes": n(s.likes), "follows": n(s.follows),
		"shares": n(s.shares), "diamonds": n(s.diamonds), "gifts": n(s.gifts),
	})
}

func tiktokTestAlert() {
	o := GetConfig().TikTokOptions
	tmpl := firstNonEmpty(o.FollowText, o.GiftText, "{name} followed")
	ttAlerts.push(fillTemplate(tmpl, map[string]string{"name": "Fern", "user": "fern", "gift": "Rose", "amount": "5", "diamonds": "5"}),
		time.Duration(o.AlertSec)*time.Second)
}

// TikTokInfo is the TikTok page's state.
type TikTokInfo struct {
	Link     LinkStatus    `json:"link"`
	Viewers  int           `json:"viewers"`
	Likes    int           `json:"likes"`
	Follows  int           `json:"follows"`
	Shares   int           `json:"shares"`
	Diamonds int           `json:"diamonds"`
	Gifts    int           `json:"gifts"`
	Since    int64         `json:"since"`
	Recent   []StreamEvent `json:"recent"`
}

// CurrentTikTok returns the TikTok snapshot.
func CurrentTikTok() TikTokInfo {
	ttMu.Lock()
	s := ttState
	info := TikTokInfo{Link: ttLink, Viewers: s.viewers, Likes: s.likes, Follows: s.follows,
		Shares: s.shares, Diamonds: s.diamonds, Gifts: s.gifts}
	ttMu.Unlock()
	if !s.since.IsZero() {
		info.Since = s.since.UnixMilli()
	}
	info.Recent = ttRecent.snapshot()
	return info
}

func tiktokWanted(cfg Config) bool {
	return cfg.DisplayOptions.TikTok || rulesUseTrigger(cfg, "tiktok.")
}

func startTikTokWorker() {
	go func() {
		for {
			cfg := GetConfig()
			if !tiktokWanted(cfg) {
				setTikTokLink(linkOff, "")
				sleepOrKick(2*time.Second, tiktokKick)
				continue
			}
			target := cfg.TikTokOptions.URL
			setTikTokLink(linkWaiting, "Connecting to "+target)
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				t := time.NewTicker(2 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
					}
					if c := GetConfig(); !tiktokWanted(c) || c.TikTokOptions.URL != target {
						cancel()
						return
					}
				}
			}()
			err := runTikTok(ctx, target, func(e tiktokEvent) { noteTikTok(GetConfig(), e) })
			cancel()
			if ctx.Err() != nil && err == nil {
				continue
			}
			if err != nil && ctx.Err() == nil {
				setTikTokLink(linkWaiting, err.Error())
			}
			NudgeRules()
			sleepOrKick(5*time.Second, tiktokKick)
		}
	}()
}

// runTikTok reads the bridge until it closes or ctx ends.
func runTikTok(ctx context.Context, target string, handle func(tiktokEvent)) error {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, target, nil)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			return errors.New("the bridge answered HTTP " + strconv.Itoa(resp.StatusCode))
		}
		return errors.New("no bridge at " + target + ". Start TikFinity, or your TikTok-Live-Connector server, and connect it to your live.")
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
	ttMu.Lock()
	if ttState.since.IsZero() {
		ttState.since = streamNow()
	}
	ttMu.Unlock()
	setTikTokLink(linkConnected, "Reading "+target)
	NotifyChange()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				return errors.New("the bridge closed the connection")
			}
			return errors.New("lost the bridge")
		}
		if e, ok := parseTikTok(data); ok {
			handle(e)
		}
	}
}

// tiktokViewers is the current viewer count, and whether the bridge is on.
func tiktokViewers() (int, bool) {
	ttMu.Lock()
	defer ttMu.Unlock()
	return ttState.viewers, ttLink.State == linkConnected
}

func init() {
	registerTikTokRules()
}

func registerTikTokRules() {
	const cat = "Streaming"
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokFollow, Label: "New TikTok follower", Category: cat,
		Description: "{name} followed during your live", Fields: []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokGift, Label: "TikTok gift", Category: cat,
		Description: "{name} sent {amount}× {gift}, worth {diamonds} diamonds",
		Fields: []RuleField{
			{Key: "gift", Label: "Gift", Type: FieldText, MaxLen: 100, Placeholder: "any gift", Hint: "Name contains, or /regex/"},
			{Key: "min", Label: "Diamonds at least", Type: FieldNumber, Min: 0, Max: 1_000_000, Step: 1},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return MatchText(p.String("gift"), ev.Data["gift"]) && eventNumber(ev, "diamonds") >= p.Number("min")
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokLikes, Label: "TikTok likes reach", Category: cat,
		Description: "Each time the live's likes pass another multiple. {amount} is the total.",
		Fields:      []RuleField{{Key: "every", Label: "Every", Type: FieldNumber, Min: 1, Max: 10_000_000, Step: 1, Default: 1000.0, Hint: "Likes"}},
		Match: func(p RuleParams, ev RuleEvent) bool {
			every := p.Number("every")
			if every < 1 {
				return false
			}
			before, after := eventNumber(ev, "before"), eventNumber(ev, "amount")
			return int(after/every) > int(before/every)
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokShare, Label: "TikTok share", Category: cat,
		Description: "{name} shared your live", Fields: []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokChat, Label: "TikTok comment", Category: cat,
		Description: "{name} commented; {message} is the text",
		Fields: []RuleField{
			{Key: "message", Label: "Message", Type: FieldText, MaxLen: 200, Placeholder: "any comment", Hint: "Contains, or /regex/"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool { return MatchText(p.String("message"), ev.Data["message"]) },
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokMilestone, Label: "TikTok follower milestone", Category: cat,
		Description: "Every so many new followers in one live, set on the TikTok page. {amount} is the count.",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTikTokViewers, Label: "TikTok viewers", Category: cat,
		Description: "While the live has at least this many viewers",
		Fields:      []RuleField{{Key: "min", Label: "At least", Type: FieldNumber, Min: 0, Max: 10_000_000, Step: 1, Default: 10.0}},
		Check: func(p RuleParams) bool {
			n, on := tiktokViewers()
			return on && float64(n) >= p.Number("min")
		},
	}))
}
