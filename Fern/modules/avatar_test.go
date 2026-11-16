package modules

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// sentLog captures avatar parameters safely from timers and goroutines.
type sentLog struct {
	mu   sync.Mutex
	msgs []*osc.Message
}

func (s *sentLog) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.msgs))
	for _, m := range s.msgs {
		out = append(out, strings.TrimPrefix(m.Address, AvatarParamPrefix)+"="+fmtArg(m.Arguments[0]))
	}
	return out
}

func fmtArg(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func captureSent(t *testing.T) *sentLog {
	t.Helper()
	log := &sentLog{}
	old := avatarSend
	avatarSend = func(_ OSCOptions, m *osc.Message) error {
		log.mu.Lock()
		log.msgs = append(log.msgs, m)
		log.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { avatarSend = old })
	return log
}

func useTempAvatars(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "avatars.json")
	reset := func(file string) {
		avatarsMu.Lock()
		if avatarsSaveAt != nil {
			avatarsSaveAt.Stop()
			avatarsSaveAt = nil
		}
		avatarList, avatarsLoaded, avatarsFile = nil, false, file
		avatarsMu.Unlock()
		avMu.Lock()
		avSent = map[string]AvatarSentValue{}
		avState, avMediaOn, avAutoSave = nil, nil, nil
		avChangedAt, avChangeID, avNameAtSwap, avSettleTill = time.Time{}, "", "", time.Time{}
		avStatus = AvatarAutoStatus{}
		avMu.Unlock()
		avatarOpts.Store(nil)
	}
	reset(path)
	t.Cleanup(func() { reset("") })
	return path
}

// wearAvatar makes id the current avatar with these cached parameters.
func wearAvatar(t *testing.T, id string, params map[string]any) {
	t.Helper()
	resetAvatarCache(t)
	paramMu.Lock()
	avatarID = id
	for k, v := range params {
		paramCache[k] = v
	}
	paramMu.Unlock()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAvatarOptionsDefaultsAndSanitize(t *testing.T) {
	d := DefaultConfig().Avatar
	if d.AutoProfile || d.Media.Enabled || d.Events.Enabled || d.Stats.Enabled {
		t.Fatal("avatar automation should start off")
	}
	if d.Media.PlayPause != "FernMediaPlayPause" || d.Stats.HourFloat != "FernHourF" || !d.Sync.Restore {
		t.Fatalf("defaults: %+v", d)
	}
	o := d
	o.Media.Next = "  /avatar/parameters/VF/Next #1 "
	o.Events.PulseMS = 99999
	o.Sync.DelayMS = 5
	o.Stats.CPU = strings.Repeat("x", 100)
	o.FallbackProfile = strings.Repeat("p", 80)
	o.sanitize()
	if o.Media.Next != "VF/Next1" {
		t.Errorf("name = %q", o.Media.Next)
	}
	if o.Events.PulseMS != maxAvatarPulseMS || o.Sync.DelayMS != minSyncDelayMS {
		t.Errorf("bounds: pulse %d delay %d", o.Events.PulseMS, o.Sync.DelayMS)
	}
	if len(o.Stats.CPU) != maxParamNameLen || len([]rune(o.FallbackProfile)) != maxProfileName {
		t.Errorf("limits: %d %d", len(o.Stats.CPU), len(o.FallbackProfile))
	}
	// An emptied name stays empty: it means "don't send".
	o.Stats.GPU = ""
	o.sanitize()
	if o.Stats.GPU != "" {
		t.Error("an empty name should stay empty")
	}
	// The section is per PC: a profile never carries it.
	for _, s := range profileSkipSections {
		if s == "Avatar" {
			return
		}
	}
	t.Error("Avatar should be in profileSkipSections")
}

func TestAvatarOptionsSurviveOldConfig(t *testing.T) {
	c := DefaultConfig()
	if err := json.Unmarshal([]byte(`{"Avatar":{"media":{"enabled":true,"next":"Skip"}}}`), &c); err != nil {
		t.Fatal(err)
	}
	c.sanitize()
	if !c.Avatar.Media.Enabled || c.Avatar.Media.Next != "Skip" || c.Avatar.Media.Previous != "FernMediaPrevious" || c.Avatar.Events.PulseMS != 1000 {
		t.Fatalf("got %+v", c.Avatar)
	}
}

func TestAvatarStoreSyncFlow(t *testing.T) {
	path := useTempAvatars(t)
	sent := captureSent(t)
	wearAvatar(t, "avtr_a", map[string]any{"Hat": true, "Hue": float32(0.25), "Outfit": int32(3), "Other": true})

	if _, err := SaveAvatarParams("avtr_a"); err == nil {
		t.Fatal("saving with nothing picked should fail")
	}
	if err := SetAvatarSyncParams("avtr_a", []string{"Hat", "Hue", "Outfit", "Hat", " ", "Missing"}); err != nil {
		t.Fatal(err)
	}
	n, err := SaveAvatarParams("avtr_a")
	if err != nil || n != 3 {
		t.Fatalf("save: %d %v", n, err)
	}
	if _, err := SaveAvatarParams("avtr_b"); err == nil {
		t.Fatal("only the worn avatar's values are known")
	}
	e, _ := AvatarInfo("avtr_a")
	if !reflect.DeepEqual(e.Params, []string{"Hat", "Hue", "Outfit", "Missing"}) || len(e.Values) != 3 || e.Values["Outfit"] != (SavedParam{"int", 3}) {
		t.Fatalf("entry %+v", e)
	}

	if n, err := RestoreAvatarParams("avtr_a"); err != nil || n != 3 {
		t.Fatalf("restore: %d %v", n, err)
	}
	if got := sent.list(); !reflect.DeepEqual(got, []string{"Hat=true", "Hue=0.25", "Outfit=3"}) {
		t.Fatalf("sent %q", got)
	}

	if err := CopyAvatarParams("avtr_a", "avtr_b"); err != nil {
		t.Fatal(err)
	}
	b, ok := AvatarInfo("avtr_b")
	if !ok || !b.Restore || len(b.Values) != 3 {
		t.Fatalf("copy: %+v", b)
	}
	if err := CopyAvatarParams("avtr_a", "avtr_a"); err == nil {
		t.Fatal("copying onto itself should fail")
	}

	// Unpicking a parameter drops its saved value.
	_ = SetAvatarSyncParams("avtr_a", []string{"Hue"})
	if e, _ := AvatarInfo("avtr_a"); len(e.Values) != 1 {
		t.Fatalf("values %+v", e.Values)
	}

	if err := SaveAvatarsNow(); err != nil {
		t.Fatal(err)
	}
	avatarsMu.Lock()
	avatarList, avatarsLoaded = nil, false
	avatarsMu.Unlock()
	if e, ok := AvatarInfo("avtr_b"); !ok || e.Values["Hue"] != (SavedParam{"float", 0.25}) {
		t.Fatalf("after reload from %s: %+v", path, e)
	}
}

func TestAvatarStoreTrims(t *testing.T) {
	useTempAvatars(t)
	for i := 0; i < maxRecentAvatars+5; i++ {
		noteAvatarWorn("avtr_" + string(rune('a'+i)))
	}
	if len(AvatarEntries()) != maxRecentAvatars {
		t.Fatalf("kept %d", len(AvatarEntries()))
	}
	// The oldest one left gets a name by hand, so it is kept from now on.
	if err := RenameAvatar("avtr_f", "Kept"); err != nil {
		t.Fatal(err)
	}
	noteAvatarWorn("avtr_z2")
	list := AvatarEntries()
	if len(list) != maxRecentAvatars+1 || list[0].ID != "avtr_z2" {
		t.Fatalf("kept %d, newest %s", len(list), list[0].ID)
	}
	if e, ok := AvatarInfo("avtr_f"); !ok || e.Name != "Kept" || !e.NameSet {
		t.Errorf("a named avatar should stay: %+v", e)
	}
	noteAvatarWorn("avtr_z3")
	if _, ok := AvatarInfo("avtr_g"); ok || len(AvatarEntries()) != maxRecentAvatars+1 {
		t.Error("the oldest plain avatar should be dropped")
	}
	noteAvatarName("avtr_f", "From log")
	if e, _ := AvatarInfo("avtr_f"); e.Name != "Kept" {
		t.Error("the log must not rename a hand-named avatar")
	}
	if err := SetAvatarProfile("avtr_f", "Nope"); err == nil {
		t.Error("an unknown profile should be refused")
	}
	if err := RenameAvatar("bad id", "x"); err == nil {
		t.Error("IDs with spaces aren't avatar IDs")
	}
	if err := ForgetAvatar("avtr_f"); err != nil {
		t.Fatal(err)
	}
	if _, ok := AvatarInfo("avtr_f"); ok {
		t.Error("forgotten")
	}
}

func TestAvatarChangeSwitchesProfileAndRestores(t *testing.T) {
	useTempAvatars(t)
	sent := captureSent(t)
	c := DefaultConfig()
	c.Avatar.AutoProfile = true
	c.Avatar.FallbackProfile = "Default"
	c.Avatar.Sync.DelayMS = 200
	useTestConfig(t, c)

	var switched []string
	old := avatarSwitchFn
	avatarSwitchFn = func(name string) error {
		switched = append(switched, name)
		if name == "Broken" {
			return errors.New("there is no profile called Broken")
		}
		return nil
	}
	t.Cleanup(func() { avatarSwitchFn = old })

	// Pick a profile and saved values for avtr_club by hand.
	_ = editAvatar("avtr_club", true, func(e *AvatarEntry) error {
		e.Profile = "Club"
		e.Params = []string{"Glow"}
		e.Values = map[string]SavedParam{"Glow": {"float", 0.75}}
		e.Restore = true
		return nil
	})
	wearAvatar(t, "avtr_club", nil)
	handleAvatarChange("avtr_club")
	waitUntil(t, "restore", func() bool { return len(sent.list()) == 1 })
	if got := sent.list(); got[0] != "Glow=0.75" {
		t.Fatalf("restored %q", got)
	}
	if st := CurrentAvatarAuto(); st.Restored != 1 || st.Profile != "Club" || st.ProfileErr != "" {
		t.Fatalf("status %+v", st)
	}

	// An avatar without a pick uses the fallback.
	wearAvatar(t, "avtr_new", nil)
	handleAvatarChange("avtr_new")
	if !reflect.DeepEqual(switched, []string{"Club", "Default"}) {
		t.Fatalf("switched %q", switched)
	}
	if list := AvatarEntries(); list[0].ID != "avtr_new" || list[1].ID != "avtr_club" {
		t.Fatalf("recent order %v", list)
	}

	_ = editAvatar("avtr_bad", true, func(e *AvatarEntry) error { e.Profile = "Broken"; return nil })
	wearAvatar(t, "avtr_bad", nil)
	handleAvatarChange("avtr_bad")
	if st := CurrentAvatarAuto(); st.ProfileErr == "" {
		t.Fatal("a failed switch should be reported")
	}
}

func TestAvatarAutoSaveIgnoresTheLoadBurst(t *testing.T) {
	useTempAvatars(t)
	c := DefaultConfig()
	c.Avatar.Sync.DelayMS = 200
	useTestConfig(t, c)
	_ = editAvatar("avtr_a", true, func(e *AvatarEntry) error {
		e.Params, e.AutoSave = []string{"Hat"}, true
		return nil
	})
	now := time.Unix(1000, 0)
	oldNow := avatarNow
	avatarNow = func() time.Time { return now }
	t.Cleanup(func() { avatarNow = oldNow })

	wearAvatar(t, "avtr_a", nil)
	handleAvatarChange("avtr_a")
	onAvatarParamMsg(osc.NewMessage(AvatarParamPrefix+"Hat", false)) // the avatar's default
	time.Sleep(50 * time.Millisecond)
	if e, _ := AvatarInfo("avtr_a"); len(e.Values) != 0 {
		t.Fatalf("a value in the load burst was saved: %+v", e.Values)
	}
	now = now.Add(3 * time.Second)
	onAvatarParamMsg(osc.NewMessage(AvatarParamPrefix+"Hat", true))
	onAvatarParamMsg(osc.NewMessage(AvatarParamPrefix+"Other", true))
	waitUntil(t, "auto-save", func() bool { e, _ := AvatarInfo("avtr_a"); return len(e.Values) == 1 })
	if e, _ := AvatarInfo("avtr_a"); e.Values["Hat"] != (SavedParam{"bool", 1}) {
		t.Fatalf("saved %+v", e.Values)
	}
}

func TestMediaFromAvatarParams(t *testing.T) {
	useTempAvatars(t)
	c := DefaultConfig()
	c.Avatar.Media.Enabled = true
	c.Avatar.Media.Next = "Skip"
	c.Avatar.sanitize()
	avatarOpts.Store(&c.Avatar)

	var mu sync.Mutex
	var got []string
	old := avatarMediaFn
	avatarMediaFn = func(a string) { mu.Lock(); got = append(got, a); mu.Unlock() }
	t.Cleanup(func() { avatarMediaFn = old })
	actions := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }

	msg := func(name string, v any) { onAvatarParamMsg(osc.NewMessage(AvatarParamPrefix+name, v)) }
	msg("Skip", true)
	msg("Skip", true) // still held: not another press
	msg("Skip", false)
	msg("Skip", int32(1))
	msg("FernVolumeUp", float32(0.9))
	msg("FernMediaPlayPause", float32(0.2)) // below half: not on
	msg("Unrelated", true)
	waitUntil(t, "media actions", func() bool { return len(actions()) == 3 })
	time.Sleep(20 * time.Millisecond)
	if a := actions(); len(a) != 3 || strings.Count(strings.Join(a, " "), MediaNext) != 2 || !containsString(a, MediaVolumeUp) {
		t.Fatalf("actions %q", a)
	}

	c.Avatar.Media.Enabled = false
	avatarOpts.Store(&c.Avatar)
	msg("FernMediaNext", true)
	time.Sleep(20 * time.Millisecond)
	if len(actions()) != 3 {
		t.Fatal("media parameters should do nothing while off")
	}
}

func TestAvatarStateParams(t *testing.T) {
	c := DefaultConfig()
	c.Avatar.Events.Enabled = true
	c.Avatar.Media.Enabled = true
	c.Avatar.Stats.Enabled = true
	c.Avatar.Stats.RAM = ""
	at := time.Date(2026, 1, 2, 18, 30, 15, 0, time.Local)
	read := avatarReadings{afk: true, players: 300, playing: true, cpu: 50, gpu: 120, hasGPU: true, ram: 40}
	got := map[string]any{}
	for _, p := range avatarStateParams(c, at, read) {
		got[p.Name] = p.Value
	}
	want := map[string]any{
		"FernAFK": true, "FernPlayerCount": 255, "FernMediaPlaying": true,
		"FernHour": 18, "FernMinute": 30, "FernSecond": 15,
		"FernHourF": round4(18.5 / 24), "FernMinuteF": round4(30.25 / 60), "FernSecondF": 0.25,
		"FernCPU": 0.5, "FernGPU": 1.0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}

	c.Avatar.Stats.Hour12 = true
	for _, p := range avatarStateParams(c, time.Date(2026, 1, 2, 0, 15, 0, 0, time.Local), read) {
		if p.Name == "FernHour" && p.Value != 12 || p.Name == "FernHourF" && p.Value != round4(0.25/12) {
			t.Errorf("12-hour %s = %v", p.Name, p.Value)
		}
	}
	c.Avatar.Stats.Enabled, c.Avatar.Events.Enabled, c.Avatar.Media.Enabled = false, false, false
	if len(avatarStateParams(c, at, read)) != 0 {
		t.Error("nothing should be sent while off")
	}
}

func TestTickAvatarSendsChanges(t *testing.T) {
	useTempAvatars(t)
	sent := captureSent(t)
	c := DefaultConfig()
	c.Avatar.Events.Enabled = true
	c.Avatar.Events.AFK = ""
	now := time.Unix(5000, 0)
	read := avatarReadings{players: 4}
	oldNow, oldRead := avatarNow, avatarReadFn
	avatarNow = func() time.Time { return now }
	avatarReadFn = func(Config) avatarReadings { return read }
	t.Cleanup(func() { avatarNow, avatarReadFn = oldNow, oldRead })

	tickAvatar(c)
	if len(sent.list()) != 0 {
		t.Fatal("nothing is sent before VRChat reports an avatar")
	}
	wearAvatar(t, "avtr_a", nil)
	tickAvatar(c)
	tickAvatar(c)
	read.players = 5
	tickAvatar(c)
	if got := sent.list(); !reflect.DeepEqual(got, []string{"FernPlayerCount=4", "FernPlayerCount=5"}) {
		t.Fatalf("sent %q", got)
	}
	if v := AvatarSent()["FernPlayerCount"]; v.Value != 5 {
		t.Fatalf("recorded %+v", v)
	}
	// After an avatar change everything is sent again, once things settle.
	avMu.Lock()
	avState, avChangedAt = nil, now
	avMu.Unlock()
	tickAvatar(c)
	now = now.Add(2 * time.Second)
	tickAvatar(c)
	if got := sent.list(); len(got) != 3 || got[2] != "FernPlayerCount=5" {
		t.Fatalf("after change %q", got)
	}
}

func TestVRChatEventPulses(t *testing.T) {
	useTempAvatars(t)
	sent := captureSent(t)
	c := DefaultConfig()
	c.Avatar.Events.Enabled = true
	c.Avatar.Events.PulseMS = 100
	avatarOpts.Store(&c.Avatar)

	handleAvatarLogEvent(avatarLogEvent{kind: EventJoin, self: true})
	if len(sent.list()) != 0 {
		t.Fatal("your own join isn't a player joining")
	}
	handleAvatarLogEvent(avatarLogEvent{kind: EventJoin})
	handleAvatarLogEvent(avatarLogEvent{kind: EventScreenshot})
	waitUntil(t, "pulses to end", func() bool { return len(sent.list()) == 4 })
	got := sent.list()
	want := map[string]bool{"FernPlayerJoined=true": true, "FernScreenshot=true": true, "FernPlayerJoined=false": true, "FernScreenshot=false": true}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("sent %q", got)
		}
	}
	if !strings.HasSuffix(got[3], "false") || !strings.HasSuffix(got[0], "true") {
		t.Fatalf("order %q", got)
	}
}

func TestAddParamRule(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	name, err := AddParamRule("/avatar/parameters/Mic", RuleStep{Type: ActionKeys, Params: RuleParams{"keys": "ctrl+shift+m", "mode": KeysDown}})
	if err != nil || name != "Mic → press Ctrl+Shift+M" {
		t.Fatalf("%q %v", name, err)
	}
	list := GetConfig().Rules.List
	if len(list) != 1 {
		t.Fatalf("rules %d", len(list))
	}
	r := list[0]
	if !r.Enabled || !r.Undo || r.Trigger.Type != TriggerAvatarParam || r.Trigger.Params.String("name") != "Mic" ||
		r.Trigger.Params.String("op") != "true" || r.Actions[0].Params.String("keys") != "Ctrl+Shift+M" {
		t.Fatalf("rule %+v", r)
	}
	if _, err := AddParamRule("Mic", RuleStep{Type: ActionKeys, Params: RuleParams{"keys": "Ctrl+"}}); err == nil {
		t.Error("a bad combo should fail")
	}
	if _, err := AddParamRule("", RuleStep{Type: ActionAppOpen, Params: RuleParams{"path": "x.exe"}}); err == nil {
		t.Error("a missing parameter should fail")
	}
	if name, err := AddParamRule("Stream", RuleStep{Type: ActionAppOpen, Params: RuleParams{"path": `C:\OBS\obs64.exe`}}); err != nil || name != "Stream → open obs64.exe" {
		t.Errorf("%q %v", name, err)
	}
}

func TestAvatarRuleRegistry(t *testing.T) {
	for _, id := range []string{ActionKeys, ActionAppOpen, ActionAppClose, ActionMedia, ActionAvatarRestore} {
		if _, ok := ruleActionDef(id); !ok {
			t.Errorf("action %s missing", id)
		}
	}
	found := 0
	for _, tr := range RuleTriggers() {
		if tr.ID == TriggerScreenshot || tr.ID == TriggerWorldLoaded {
			found++
			if tr.State {
				t.Errorf("%s is an event", tr.ID)
			}
		}
		if tr.ID == TriggerAvatar && tr.Fields[0].Source != SourceAvatars {
			t.Error("the avatar trigger should suggest recent avatars")
		}
	}
	if found != 2 {
		t.Errorf("triggers found %d", found)
	}
}

func TestCurrentAvatarParamsRows(t *testing.T) {
	wearAvatar(t, "avtr_a", map[string]any{"B": true, "A": float32(0.123456), "C": int32(7)})
	rows := CurrentAvatarParams(2)
	if len(rows) != 2 || rows[0].Name != "A" || rows[0].Type != "float" || rows[0].Value != 0.1235 || rows[1].Type != "bool" {
		t.Fatalf("rows %+v", rows)
	}
}
