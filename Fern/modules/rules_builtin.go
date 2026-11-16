package modules

// Fern's own rule triggers, actions and option sources.

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// IDs of Fern's own triggers.
const (
	TriggerStarted         = "started"
	TriggerWorld           = "world"
	TriggerAvatar          = "avatar"
	TriggerVRMode          = "vr_mode"
	TriggerTime            = "time"
	TriggerAfk             = "afk"
	TriggerAfkEnd          = "afk_end"
	TriggerApp             = "app"
	TriggerAvatarParam     = "avatar_param"
	TriggerPlayerJoined    = "player_joined"
	TriggerPlayerLeft      = "player_left"
	TriggerProfileSwitched = "profile_switched"
	TriggerDND             = "dnd"
)

// IDs of Fern's own actions.
const (
	ActionProfile     = "profile"
	ActionLine        = "line"
	ActionChat        = "chat"
	ActionAvatarParam = "avatar_param"
	ActionGroup       = "group"
	ActionDND         = "dnd"
	ActionRunRule     = "run_rule"
)

// Names of Fern's own option sources.
const (
	SourceProfiles = "profiles"
	SourceLines    = "lines"
	SourceGroups   = "groups"
	SourceRules    = "rules"
	SourceApps     = "apps"
	SourceParams   = "params"
	SourcePlayers  = "players"
)

// selfAvatar is the name of the avatar the local user wears, from the log.
// Guarded by logMu.
var selfAvatar string

// noteAvatarSwitch reads "Switching <name> to avatar <avatar>". The caller
// holds logMu.
func noteAvatarSwitch(rest string) {
	who, avatar, ok := strings.Cut(rest, " to avatar ")
	if !ok {
		return
	}
	if selfName == "" || strings.TrimSpace(who) == selfName {
		selfAvatar = strings.TrimSpace(avatar)
	}
}

// CurrentAvatarName is the name of the avatar you wear, as VRChat's log
// last named it, or "".
func CurrentAvatarName() string {
	logMu.Lock()
	defer logMu.Unlock()
	return selfAvatar
}

// liveLogLine reports whether a log line was written just now, so old lines
// read when Fern starts do not fire rules.
func liveLogLine(at time.Time) bool {
	d := nowFunc().Sub(at)
	return d < 30*time.Second && d > -30*time.Second
}

// Display names of the chatbox lines, for the rule editor.
var lineLabels = map[string]string{
	ModuleAFK: "AFK", ModuleLoading: "Loading", ModuleTime: "Time", ModuleText: "Text",
	ModuleMemory: "Memory", ModuleCPU: "CPU", ModuleMedia: "Media", ModuleWindow: "Active window",
	ModuleLogs: "VRChat log", ModuleBattery: "Tracker battery", ModuleVRPerf: "VR performance",
	ModuleHeart: "Heart rate", ModuleLyrics: "Lyrics", ModuleWeather: "Weather",
	ModuleDiscord: "Discord", ModuleTwitch: "Twitch", ModuleSoundpad: "Soundpad",
	ModuleVoicemod: "Voicemod", ModuleNetwork: "Network", ModuleGPU: "GPU", ModuleWorld: "VRChat world",
}

// LineLabel is a chatbox line's display name.
func LineLabel(id string) string {
	if l, ok := lineLabels[id]; ok {
		return l
	}
	return id
}

var weekdays = []RuleOption{
	{"mon", "Mon"}, {"tue", "Tue"}, {"wed", "Wed"}, {"thu", "Thu"}, {"fri", "Fri"}, {"sat", "Sat"}, {"sun", "Sun"},
}

func allWeekdays() []string {
	out := make([]string, len(weekdays))
	for i, d := range weekdays {
		out[i] = d.Value
	}
	return out
}

var weekdayKey = map[time.Weekday]string{
	time.Monday: "mon", time.Tuesday: "tue", time.Wednesday: "wed", time.Thursday: "thu",
	time.Friday: "fri", time.Saturday: "sat", time.Sunday: "sun",
}

// inTimeWindow reports whether now is between from and to (minutes after
// midnight) on one of days. A window that ends before it starts runs past
// midnight and counts for the day it started on; from == to is all day.
// No days at all means every day.
func inTimeWindow(now time.Time, from, to int, days []string) bool {
	dayOK := func(t time.Time) bool {
		if len(days) == 0 {
			return true
		}
		key := weekdayKey[t.Weekday()]
		for _, d := range days {
			if d == key {
				return true
			}
		}
		return false
	}
	m := now.Hour()*60 + now.Minute()
	switch {
	case from == to:
		return dayOK(now)
	case from < to:
		return m >= from && m < to && dayOK(now)
	case m >= from:
		return dayOK(now)
	case m < to:
		return dayOK(now.AddDate(0, 0, -1))
	}
	return false
}

// compareParam applies an avatar parameter test.
func compareParam(op string, v, want float64) bool {
	const eps = 1e-4
	switch op {
	case "true":
		return v != 0
	case "false":
		return v == 0
	case "eq":
		return math.Abs(v-want) < eps
	case "ne":
		return math.Abs(v-want) >= eps
	case "gt":
		return v > want+eps
	case "lt":
		return v < want-eps
	}
	return false
}

var (
	ruleNowFn  = time.Now
	ruleAfkFn  = func() bool { active, _ := AfkInfo(); return active }
	ruleVRFn   = InVR
	ruleFgFn   = func() foreground { return currentForeground() }
	ruleWorld  = CurrentWorld
	ruleAvatar = func() (id, name string) { return CurrentAvatarID(), CurrentAvatarName() }
)

func init() {
	registerBuiltinTriggers()
	registerBuiltinActions()
	registerBuiltinSources()
}

func registerBuiltinTriggers() {
	match := func(key, label, hint, placeholder string) RuleField {
		return RuleField{Key: key, Label: label, Type: FieldText, Hint: hint, Placeholder: placeholder, MaxLen: 200}
	}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerWorld, Label: "In a world", Category: "VRChat",
		Description: "While you are in a matching world, once it has loaded",
		Fields:      []RuleField{match("world", "World", "Name or wrld_ ID. Contains, or /regex/. Empty is any world.", "e.g. The Black Cat")},
		Check: func(p RuleParams) bool {
			w, ok := ruleWorld()
			if !ok || w.Loading {
				return false
			}
			pat := strings.TrimSpace(p.String("world"))
			if pat == "" {
				return true
			}
			return MatchText(pat, w.ID) || (w.Name != "" && MatchText(pat, w.Name))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerPlayerJoined, Label: "Player joined", Category: "VRChat",
		Description: "Someone joins your instance",
		Fields: []RuleField{
			{Key: "name", Label: "Player", Type: FieldText, Source: SourcePlayers, Hint: "Display name. Contains, or /regex/. Empty is anyone.", MaxLen: 200},
			{Key: "self", Label: "Count yourself", Type: FieldBool, Hint: "VRChat logs your own join too"},
		},
		Match: matchPlayer,
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerPlayerLeft, Label: "Player left", Category: "VRChat",
		Description: "Someone leaves your instance",
		Fields: []RuleField{
			{Key: "name", Label: "Player", Type: FieldText, Source: SourcePlayers, Hint: "Display name. Contains, or /regex/. Empty is anyone.", MaxLen: 200},
		},
		Match: matchPlayer,
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerAvatar, Label: "Wearing an avatar", Category: "Avatar",
		Description: "While you wear a matching avatar",
		Fields:      []RuleField{match("avatar", "Avatar", "Name or avtr_ ID. Contains, or /regex/. Empty is any avatar.", "e.g. avtr_… or Fern")},
		Check: func(p RuleParams) bool {
			id, name := ruleAvatar()
			if id == "" && name == "" {
				return false
			}
			pat := strings.TrimSpace(p.String("avatar"))
			return pat == "" || (id != "" && MatchText(pat, id)) || (name != "" && MatchText(pat, name))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerAvatarParam, Label: "Avatar parameter", Category: "Avatar",
		Description: "While an avatar parameter VRChat sends matches",
		Fields: []RuleField{
			{Key: "name", Label: "Parameter", Type: FieldText, Source: SourceParams, Placeholder: "e.g. MuteSelf", Hint: "As VRChat names it, without /avatar/parameters/", MaxLen: 128},
			{Key: "op", Label: "Test", Type: FieldSelect, Options: []RuleOption{
				{"true", "is on (becomes true)"}, {"false", "is off"}, {"eq", "equals"}, {"ne", "is not"}, {"gt", "is greater than"}, {"lt", "is less than"},
			}},
			{Key: "value", Label: "Value", Type: FieldNumber, Min: -1, Max: 255, Step: 0.01, ShowIf: &RuleShowIf{Key: "op", Values: []string{"eq", "ne", "gt", "lt"}}},
		},
		Check: func(p RuleParams) bool {
			name := strings.TrimPrefix(strings.TrimSpace(p.String("name")), AvatarParamPrefix)
			if name == "" {
				return false
			}
			v, ok := AvatarParamFloat(name)
			return ok && compareParam(p.String("op"), v, p.Number("value"))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerVRMode, Label: "VR or desktop", Category: "You",
		Description: "While a VR runtime is running, or while it isn't",
		Fields:      []RuleField{{Key: "mode", Label: "Mode", Type: FieldSelect, Options: []RuleOption{{"vr", "In VR"}, {"desktop", "On desktop"}}}},
		Check: func(p RuleParams) bool {
			return ruleVRFn() == (p.String("mode") != "desktop")
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerAfk, Label: "AFK", Category: "You",
		Description: "While you count as AFK. Undo puts things back when you return.",
		Fields:      []RuleField{},
		Check:       func(RuleParams) bool { return ruleAfkFn() },
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerAfkEnd, Label: "Back from AFK", Category: "You",
		Description: "The moment you stop being AFK",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerApp, Label: "Focused app", Category: "You",
		Description: "While a matching app is in the foreground",
		Fields: []RuleField{
			{Key: "app", Label: "App", Type: FieldText, Source: SourceApps, Placeholder: "e.g. spotify.exe", Hint: "Executable, name or window title. Contains, or /regex/.", MaxLen: 200},
		},
		Check: func(p RuleParams) bool {
			pat := strings.TrimSpace(p.String("app"))
			fg := ruleFgFn()
			if pat == "" || fg.Exe == "" && fg.Title == "" {
				return false
			}
			return (fg.Exe != "" && MatchText(pat, fg.Exe)) || (fg.Title != "" && MatchText(pat, fg.Title)) ||
				(fg.Exe != "" && MatchText(pat, appName(fg)))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerTime, Label: "Time of day", Category: "Time",
		Description: "Between two times on the chosen days. A window past midnight belongs to the day it starts.",
		Fields: []RuleField{
			{Key: "from", Label: "From", Type: FieldTime, Default: "22:00"},
			{Key: "to", Label: "Until", Type: FieldTime, Default: "06:00"},
			{Key: "days", Label: "Days", Type: FieldMulti, Options: weekdays, Default: allWeekdays()},
		},
		Check: func(p RuleParams) bool {
			from, ok1 := parseClock(p.String("from"))
			to, ok2 := parseClock(p.String("to"))
			if !ok1 || !ok2 {
				return false
			}
			days := p.List("days")
			if len(days) == 0 {
				return false
			}
			return inTimeWindow(ruleNowFn(), from, to, days)
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerStarted, Label: "Fern started", Category: "Fern",
		Description: "Once, a few seconds after Fern opens",
		Fields:      []RuleField{},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerProfileSwitched, Label: "Profile switched", Category: "Fern",
		Description: "A profile is switched to, from anywhere",
		Fields: []RuleField{
			{Key: "profile", Label: "Profile", Type: FieldSelect, Source: SourceProfiles, Hint: "Any profile when empty"},
		},
		Match: func(p RuleParams, ev RuleEvent) bool {
			want := p.String("profile")
			return want == "" || foldName(want) == foldName(ev.Data["profile"])
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerDND, Label: "Do not disturb", Category: "Fern",
		Description: "While do not disturb is on",
		Fields:      []RuleField{},
		Check:       func(RuleParams) bool { return DNDActive() },
	}))
}

func matchPlayer(p RuleParams, ev RuleEvent) bool {
	if ev.Data["self"] == "1" && !p.Bool("self") {
		return false
	}
	return MatchText(p.String("name"), ev.Data["name"])
}

func foldName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func registerBuiltinActions() {
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionProfile, Label: "Switch profile", Category: "Fern",
		Description: "Undo switches back to the profile that was active",
		Fields:      []RuleField{{Key: "profile", Label: "Profile", Type: FieldSelect, Source: SourceProfiles, Hint: "Empty stops saving into a profile"}},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			name := p.String("profile")
			before := ActiveProfile()
			if name == "" {
				return nil, DeactivateProfile()
			}
			if foldName(before) == foldName(name) {
				return nil, nil
			}
			if err := SwitchProfile(name); err != nil {
				return nil, err
			}
			now := ActiveProfile()
			return func() {
				// Only if nothing else switched since.
				if before != "" && ActiveProfile() == now {
					_ = SwitchProfile(before)
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionLine, Label: "Turn a line on or off", Category: "Chatbox",
		Fields: []RuleField{
			{Key: "line", Label: "Line", Type: FieldSelect, Source: SourceLines, Default: ModuleText},
			{Key: "state", Label: "To", Type: FieldSelect, Options: []RuleOption{{"on", "On"}, {"off", "Off"}, {"toggle", "Toggle"}}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			id := p.String("line")
			if !knownModules[id] {
				return nil, errors.New("pick a line")
			}
			var before, after bool
			UpdateConfig(func(c *Config) {
				flag := moduleFlag(&c.DisplayOptions, id)
				before = *flag
				switch p.String("state") {
				case "off":
					*flag = false
				case "toggle":
					*flag = !*flag
				default:
					*flag = true
				}
				after = *flag
			})
			if before == after {
				return nil, nil
			}
			return func() {
				UpdateConfig(func(c *Config) {
					if flag := moduleFlag(&c.DisplayOptions, id); *flag == after {
						*flag = before
					}
				})
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionChat, Label: "Send a chatbox message", Category: "Chatbox",
		Description: "Shows a message for a while, like Send on the Live page",
		Fields: []RuleField{
			{Key: "text", Label: "Message", Type: FieldText, MaxLen: 143, Placeholder: "e.g. Welcome {name}!", Hint: "{name} is the player, {world} the world, {profile} the profile"},
			{Key: "seconds", Label: "Seconds", Type: FieldNumber, Min: 0, Max: 600, Step: 1, Default: 10.0, Hint: "0 pins it until cleared"},
		},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			text := strings.TrimSpace(expandRuleText(p.String("text"), ctx.Event))
			if text == "" {
				return nil, errors.New("the message is empty")
			}
			SetHold(text, time.Duration(p.Number("seconds"))*time.Second)
			held, _, _ := HoldState()
			return func() {
				if cur, _, _ := HoldState(); cur != "" && cur == held {
					ClearHold()
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionAvatarParam, Label: "Set an avatar parameter", Category: "Avatar",
		Description: "Sends it to VRChat over OSC. Undo sends the value it had before.",
		Fields: []RuleField{
			{Key: "name", Label: "Parameter", Type: FieldText, Source: SourceParams, Placeholder: "e.g. Glasses", MaxLen: 128},
			{Key: "kind", Label: "Type", Type: FieldSelect, Options: []RuleOption{{"bool", "Bool"}, {"int", "Int (0–255)"}, {"float", "Float (−1–1)"}}},
			{Key: "on", Label: "On", Type: FieldBool, Default: true, ShowIf: &RuleShowIf{Key: "kind", Values: []string{"bool"}}},
			{Key: "value", Label: "Value", Type: FieldNumber, Min: -1, Max: 255, Step: 0.01, ShowIf: &RuleShowIf{Key: "kind", Values: []string{"int", "float"}}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			name := strings.TrimPrefix(strings.TrimSpace(p.String("name")), AvatarParamPrefix)
			if name == "" {
				return nil, errors.New("name the parameter")
			}
			var value any
			switch p.String("kind") {
			case "int":
				value = int(math.Round(p.Number("value")))
			case "float":
				value = p.Number("value")
			default:
				value = p.Bool("on")
			}
			prev, hadPrev := AvatarParam(name)
			if err := SendAvatarParam(name, value); err != nil {
				return nil, err
			}
			if !hadPrev {
				return nil, nil
			}
			return func() { _ = SendAvatarParam(name, prev) }, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionGroup, Label: "Use a message group", Category: "Chatbox",
		Description: "Limits the custom messages to one group",
		Fields: []RuleField{
			{Key: "group", Label: "Group", Type: FieldSelect, Source: SourceGroups, Hint: "Empty cycles through every hearted message"},
			{Key: "show", Label: "Turn the text line on", Type: FieldBool, Default: true},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			group := strings.TrimSpace(p.String("group"))
			var prevGroup, nowGroup string
			var prevShow bool
			missing := false
			UpdateConfig(func(c *Config) {
				prevGroup, prevShow = c.TextOptions.Group, c.DisplayOptions.Text
				if group != "" && !hasGroup(c.TextOptions.Groups, group) {
					missing = true
					return
				}
				c.TextOptions.Group = group
				if p.Bool("show") {
					c.DisplayOptions.Text = true
				}
			})
			if missing {
				return nil, errors.New("there is no group called " + group)
			}
			nowGroup = GetConfig().TextOptions.Group
			return func() {
				UpdateConfig(func(c *Config) {
					if strings.EqualFold(c.TextOptions.Group, nowGroup) {
						c.TextOptions.Group = prevGroup
						if p.Bool("show") {
							c.DisplayOptions.Text = prevShow
						}
					}
				})
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionDND, Label: "Do not disturb", Category: "Chatbox",
		Description: "Hides every line except the ones you allow",
		Fields: []RuleField{
			{Key: "state", Label: "To", Type: FieldSelect, Options: []RuleOption{{"on", "On"}, {"off", "Off"}, {"toggle", "Toggle"}}},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			before := DNDActive()
			after := before
			switch p.String("state") {
			case "off":
				after = false
			case "toggle":
				after = !before
			default:
				after = true
			}
			if before == after {
				return nil, nil
			}
			SetDND(after)
			return func() {
				if DNDActive() == after {
					SetDND(before)
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionRunRule, Label: "Run another rule's actions", Category: "Fern",
		Description: "Ignores that rule's trigger, conditions and cooldown",
		Fields:      []RuleField{{Key: "rule", Label: "Rule", Type: FieldSelect, Source: SourceRules}},
		Run: func(ctx RuleContext, p RuleParams) (func(), error) {
			if ctx.eng == nil {
				return nil, errors.New("rules aren't running")
			}
			return ctx.eng.runOther(ctx, p.String("rule"))
		},
	}))
}

func hasGroup(groups []string, g string) bool {
	for _, x := range groups {
		if strings.EqualFold(x, g) {
			return true
		}
	}
	return false
}

// expandRuleText fills {name}, {world} and {profile} in a rule's message.
func expandRuleText(text string, ev RuleEvent) string {
	if !strings.Contains(text, "{") {
		return text
	}
	world := ""
	if w, ok := ruleWorld(); ok {
		world = w.Name
	}
	return strings.NewReplacer(
		"{name}", ev.Data["name"],
		"{world}", world,
		"{profile}", ActiveProfile(),
	).Replace(text)
}

func registerBuiltinSources() {
	RegisterRuleSource(SourceProfiles, func() []RuleOption {
		list := ListProfiles()
		out := make([]RuleOption, 0, len(list))
		for _, p := range list {
			out = append(out, RuleOption{Value: p.Name, Label: p.Name})
		}
		return out
	})
	RegisterRuleSource(SourceLines, func() []RuleOption {
		out := make([]RuleOption, 0, len(DefaultOrder))
		for _, id := range DefaultOrder {
			out = append(out, RuleOption{Value: id, Label: LineLabel(id)})
		}
		return out
	})
	RegisterRuleSource(SourceGroups, func() []RuleOption {
		groups := GetConfig().TextOptions.Groups
		out := make([]RuleOption, 0, len(groups))
		for _, g := range groups {
			out = append(out, RuleOption{Value: g, Label: g})
		}
		return out
	})
	RegisterRuleSource(SourceRules, func() []RuleOption {
		list := GetConfig().Rules.List
		out := make([]RuleOption, 0, len(list))
		for i, r := range list {
			label := r.Name
			if label == "" {
				label = "Rule " + strconv.Itoa(i+1)
			}
			out = append(out, RuleOption{Value: r.ID, Label: label})
		}
		return out
	})
	RegisterRuleSource(SourceApps, func() []RuleOption {
		apps := RecentWindowApps()
		out := make([]RuleOption, 0, len(apps))
		for _, a := range apps {
			out = append(out, RuleOption{Value: a.App, Label: a.Name})
		}
		return out
	})
	RegisterRuleSource(SourceParams, func() []RuleOption {
		params := AvatarParams()
		out := make([]RuleOption, 0, len(params))
		for name := range params {
			out = append(out, RuleOption{Value: name, Label: name})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
		if len(out) > 200 {
			out = out[:200]
		}
		return out
	})
	RegisterRuleSource(SourcePlayers, func() []RuleOption {
		joins := JoinLogs()
		out := make([]RuleOption, 0, len(joins))
		for _, j := range joins {
			out = append(out, RuleOption{Value: j.Text, Label: j.Text})
		}
		return out
	})
}

/* ---------------- event sources ---------------- */

// ruleParamNames holds the avatar parameters some rule tests, so the OSC
// handler only nudges the engine for those.
var ruleParamNames atomic.Pointer[map[string]bool]

func refreshRuleParams(o RulesOptions) {
	names := map[string]bool{}
	for _, r := range o.List {
		steps := append([]RuleStep{r.Trigger}, r.Conditions...)
		for _, s := range steps {
			if s.Type == TriggerAvatarParam {
				names[strings.TrimPrefix(strings.TrimSpace(s.Params.String("name")), AvatarParamPrefix)] = true
			}
		}
	}
	ruleParamNames.Store(&names)
}

var afkWatch struct {
	sync.Mutex
	known, on bool
}

// noteAfk fires "Back from AFK" when AFK ends. The first call only learns
// the state.
func noteAfk(on bool) {
	afkWatch.Lock()
	fire := afkWatch.known && afkWatch.on && !on
	afkWatch.known, afkWatch.on = true, on
	afkWatch.Unlock()
	if fire {
		FireTrigger(TriggerAfkEnd, nil)
	}
}

// watchRuleSources connects the rest of Fern to the engine.
func watchRuleSources() {
	OnProfileChange(func(ev ProfileEvent) {
		if ev.Kind == ProfileSwitched && ev.Active != "" {
			FireTrigger(TriggerProfileSwitched, map[string]string{"profile": ev.Active, "previous": ev.Previous})
		}
	})
	OnAvatarChange(func(string) { NudgeRules() })
	SubscribeOSCPrefix(AvatarParamPrefix, func(msg *osc.Message) {
		names := ruleParamNames.Load()
		if names != nil && (*names)[strings.TrimPrefix(msg.Address, AvatarParamPrefix)] {
			NudgeRules()
		}
	})
	go func() {
		for range time.Tick(time.Second) {
			refreshRuleParams(GetConfig().Rules)
			noteAfk(ruleAfkFn())
		}
	}()
}

// playerEvent is the data of a join or leave. The caller holds logMu.
func playerEvent(name string) map[string]string {
	data := map[string]string{"name": name}
	if name == selfName {
		data["self"] = "1"
	}
	return data
}
