package modules

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Rule triggers and actions for Twitch.
const (
	TriggerTwitchChat   = "twitch.chat"
	TriggerTwitchFollow = "twitch.follow"
	TriggerTwitchSub    = "twitch.sub"
	TriggerTwitchGift   = "twitch.gift"
	TriggerTwitchRaid   = "twitch.raid"
	TriggerTwitchCheer  = "twitch.cheer"
	TriggerTwitchRedeem = "twitch.redeem"
	TriggerTwitchLive   = "twitch.live"

	ActionTwitchAnnounce = "twitch.announce"
	ActionTwitchShoutout = "twitch.shoutout"

	SourceTwitchRewards = "twitch_rewards"
)

func init() {
	registerTwitchRules()
}

// eventNumber reads a number from event data, 0 when missing.
func eventNumber(ev RuleEvent, key string) float64 {
	n, _ := strconv.ParseFloat(ev.Data[key], 64)
	return n
}

// expandStreamText fills {name}, {world} and {profile} like the chat action,
// then any other {key} the event carries, like {amount} or {reward}.
func expandStreamText(text string, ev RuleEvent) string {
	text = expandRuleText(text, ev)
	if !strings.Contains(text, "{") || len(ev.Data) == 0 {
		return text
	}
	pairs := make([]string, 0, 2*len(ev.Data))
	for k, v := range ev.Data {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

func registerTwitchRules() {
	const cat = "Streaming"
	atLeast := func(key, label, hint string) RuleField {
		return RuleField{Key: key, Label: label, Type: FieldNumber, Min: 0, Max: 1_000_000, Step: 1, Hint: hint}
	}
	minMatch := func(key string) func(RuleParams, RuleEvent) bool {
		return func(p RuleParams, ev RuleEvent) bool { return eventNumber(ev, "amount") >= p.Number(key) }
	}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchChat, Label: "Twitch chat message", Category: cat,
		Description: "A message in your Twitch chat. {name} is who sent it, {message} the text.",
		Fields: []RuleField{
			{Key: "message", Label: "Message", Type: FieldText, MaxLen: 200, Placeholder: "e.g. !lights", Hint: "Contains, or /regex/. Empty is any message."},
			{Key: "user", Label: "From", Type: FieldText, MaxLen: 100, Placeholder: "anyone", Hint: "Name contains, or /regex/"},
			{Key: "role", Label: "Who", Type: FieldSelect, Options: []RuleOption{
				{"any", "Anyone"}, {"sub", "Subscribers and up"}, {"vip", "VIPs and up"}, {"mod", "Moderators"}, {"broadcaster", "Only you"},
			}},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			if !MatchText(p.String("message"), ev.Data["message"]) {
				return false
			}
			if u := p.String("user"); u != "" && !MatchText(u, ev.Data["name"]) && !MatchText(u, ev.Data["user"]) {
				return false
			}
			rank := map[string]int{"viewer": 0, "sub": 1, "vip": 2, "mod": 3, "broadcaster": 4}
			need := map[string]int{"any": 0, "sub": 1, "vip": 2, "mod": 3, "broadcaster": 4}[p.String("role")]
			return rank[ev.Data["role"]] >= need
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchFollow, Label: "New Twitch follower", Category: cat,
		Description: "Needs the sign-in on your own channel, or as one of its moderators. {name} is the follower.",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchSub, Label: "Twitch subscription", Category: cat,
		Description: "{name} subscribed; {months} and {tier} say more",
		Fields: []RuleField{
			{Key: "kind", Label: "Kind", Type: FieldSelect, Options: []RuleOption{
				{"any", "New subs and resubs"}, {"sub", "New subs"}, {"resub", "Resubs"}, {"gifted", "Received as a gift"},
			}},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			switch want, kind := p.String("kind"), ev.Data["kind"]; want {
			case "any":
				return kind == twitchSub || kind == twitchResub
			default:
				return kind == want
			}
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchGift, Label: "Twitch gift subs", Category: cat,
		Description: "{name} gifted {amount} subs",
		Fields:      []RuleField{atLeast("min", "At least", "Gifted subs")},
		Match:       minMatch("min"),
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchRaid, Label: "Twitch raid", Category: cat,
		Description: "{name} raided with {amount} viewers",
		Fields:      []RuleField{atLeast("min", "At least", "Viewers in the raid")},
		Match:       minMatch("min"),
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchCheer, Label: "Twitch cheer", Category: cat,
		Description: "{name} cheered {amount} bits",
		Fields:      []RuleField{atLeast("min", "At least", "Bits")},
		Match:       minMatch("min"),
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchRedeem, Label: "Channel points redeem", Category: cat,
		Description: "Needs the sign-in on your own channel. {name} redeemed {reward}; {message} is what they typed.",
		Fields: []RuleField{
			{Key: "reward", Label: "Reward", Type: FieldText, Source: SourceTwitchRewards, MaxLen: 100, Placeholder: "any reward", Hint: "Contains, or /regex/"},
			{Key: "input", Label: "Their text", Type: FieldText, MaxLen: 200, Placeholder: "anything", Hint: "Contains, or /regex/"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return MatchText(p.String("reward"), ev.Data["reward"]) && MatchText(p.String("input"), ev.Data["message"])
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTwitchLive, Label: "Live on Twitch", Category: cat,
		Description: "While the channel is live, from the Twitch line or the chat sign-in",
		Fields:      []RuleField{atLeast("viewers", "Viewers at least", "0 is any; needs the Twitch line on")},
		Check: func(p RuleParams) bool {
			live, viewers, _ := twitchLiveNow()
			return live && float64(viewers) >= p.Number("viewers")
		},
	}))

	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionTwitchAnnounce, Label: "Send a Twitch announcement", Category: cat,
		Description: "A highlighted message in your chat. Needs the sign-in as the broadcaster or a moderator.",
		Fields: []RuleField{
			{Key: "message", Label: "Message", Type: FieldText, MaxLen: 500, Placeholder: "e.g. Thanks for the raid, {name}!", Hint: "{name}, {amount}, {reward} and {message} come from the event"},
			{Key: "color", Label: "Colour", Type: FieldSelect, Options: []RuleOption{
				{"primary", "Channel colour"}, {"blue", "Blue"}, {"green", "Green"}, {"orange", "Orange"}, {"purple", "Purple"},
			}},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			text := strings.TrimSpace(expandStreamText(p.String("message"), ctx.Event))
			if text == "" {
				return nil, errors.New("the message is empty")
			}
			if err := twitchActionReady(); err != nil {
				return nil, err
			}
			color := p.String("color")
			go twitchAction("announcement", func(c twitchClient, broadcaster, self string) error {
				return c.helixSend(http.MethodPost, "/chat/announcements",
					url.Values{"broadcaster_id": {broadcaster}, "moderator_id": {self}},
					map[string]string{"message": text, "color": color}, nil)
			})
			return nil, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionTwitchShoutout, Label: "Give a Twitch shoutout", Category: cat,
		Description: "Twitch's /shoutout. Works while you are live, at most once every 2 minutes.",
		Fields: []RuleField{
			{Key: "channel", Label: "Channel", Type: FieldText, MaxLen: 100, Default: "{user}", Placeholder: "{user}", Hint: "{user} is whoever fired the rule, like the raider"},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			login := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(expandStreamText(p.String("channel"), ctx.Event), "@")))
			login = onlyRunes(login, "abcdefghijklmnopqrstuvwxyz0123456789_", 25)
			if login == "" {
				return nil, errors.New("no channel to shout out")
			}
			if err := twitchActionReady(); err != nil {
				return nil, err
			}
			go twitchAction("shoutout to "+login, func(c twitchClient, broadcaster, self string) error {
				to, _, err := c.userID(login)
				if err != nil {
					return err
				}
				return c.helixSend(http.MethodPost, "/chat/shoutouts",
					url.Values{"from_broadcaster_id": {broadcaster}, "to_broadcaster_id": {to}, "moderator_id": {self}}, nil, nil)
			})
			return nil, nil
		},
	}))

	RegisterRuleSource(SourceTwitchRewards, func() []RuleOption {
		list := twitchRewards()
		out := make([]RuleOption, 0, len(list))
		for _, r := range list {
			out = append(out, RuleOption{Value: r, Label: r})
		}
		return out
	})
}

// twitchActionReady says quickly why a Twitch action cannot run.
func twitchActionReady() error {
	o := GetConfig().TwitchOptions
	if o.ClientID == "" || GetSecret(SecretTwitchToken) == "" {
		return errors.New("sign in on the Twitch page first")
	}
	return nil
}

// twitchAction runs a Helix call for an action off the rules goroutine and
// notes the outcome on the Twitch chat page.
func twitchAction(what string, call func(c twitchClient, broadcaster, self string) error) {
	o := GetConfig().TwitchOptions
	err := func() error {
		auth, token, err := twitchSelf(o.ClientID)
		if err != nil {
			return err
		}
		c := newTwitchClient(o.ClientID, token)
		broadcaster := auth.UserID
		if o.Channel != "" && !strings.EqualFold(o.Channel, auth.Login) {
			if broadcaster, _, err = c.userID(o.Channel); err != nil {
				return err
			}
		}
		return call(c, broadcaster, auth.UserID)
	}()
	switch {
	case err == nil:
		tchatRecent.add("action", "Sent the "+what)
	case errors.Is(err, errTwitchAuth):
		tchatRecent.add("error", "Twitch "+what+" failed: sign in again")
	default:
		msg := err.Error()
		var apiErr *twitchAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized {
			msg = "sign in again to allow it"
		}
		tchatRecent.add("error", fmt.Sprintf("Twitch %s failed: %s", what, msg))
		fmt.Println("twitch:", what, "failed:", msg)
	}
	NotifyChange()
}
