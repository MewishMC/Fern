package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DiscordInfo is what the Discord page shows.
type DiscordInfo struct {
	Link     LinkStatus `json:"link"`
	User     string     `json:"user"`     // signed-in Discord user, once known
	Presence string     `json:"presence"` // the presence being shown, "" when none
	Voice    LinkStatus `json:"voice"`
	Channel  string     `json:"channel"`
	Count    int        `json:"count"`
	Speaking []string   `json:"speaking"`
	Mute     bool       `json:"mute"`
	Deaf     bool       `json:"deaf"`
	World    WorldInfo  `json:"world"`
}

// discordVoice is the voice channel as last reported over RPC.
type discordVoice struct {
	in       bool
	channel  string
	members  map[string]string    // user id -> shown name
	since    map[string]time.Time // speaking user id -> when they started
	stopped  map[string]time.Time // user id -> when they stopped speaking
	selfID   string
	mute     bool
	deaf     bool
	signedIn bool
}

var (
	discordMu       sync.Mutex
	discordLink     = LinkStatus{State: linkOff}
	discordVoiceSt  = LinkStatus{State: linkOff}
	discordUser     string
	discordPresence string
	dVoice          discordVoice

	discordKickCh   = make(chan struct{}, 1)
	discordAuthReq  atomic.Bool
	discordTokenURL = "https://discord.com/api/oauth2/token"
	discordHTTP     = &http.Client{Timeout: 15 * time.Second}
)

func setDiscordLink(state, detail string) {
	discordMu.Lock()
	discordLink = LinkStatus{State: state, Detail: detail}
	discordMu.Unlock()
}

func setDiscordVoice(state, detail string) {
	discordMu.Lock()
	discordVoiceSt = LinkStatus{State: state, Detail: detail}
	discordMu.Unlock()
}

// softDiscordVoice sets a waiting hint without hiding an error or an expired
// sign-in the user has not acted on yet.
func softDiscordVoice(detail string) {
	discordMu.Lock()
	defer discordMu.Unlock()
	if discordVoiceSt.State == linkError || strings.Contains(discordVoiceSt.Detail, "expired") {
		return
	}
	discordVoiceSt = LinkStatus{State: linkWaiting, Detail: detail}
}

// discordKick ends the current session so new settings or secrets apply.
func discordKick() { poke(discordKickCh) }

// DiscordAuthorize asks the running session to start the voice sign-in.
func DiscordAuthorize() {
	discordAuthReq.Store(true)
	setDiscordVoice(linkWaiting, "Connecting…")
}

// DiscordSignOut forgets the voice tokens.
func DiscordSignOut() {
	_ = SetSecret(SecretDiscordAccess, "")
	_ = SetSecret(SecretDiscordRefresh, "")
	discordKick()
}

// CurrentDiscord returns the Discord snapshot for the panel.
func CurrentDiscord(cfg Config) DiscordInfo {
	discordMu.Lock()
	info := DiscordInfo{
		Link:     discordLink,
		Voice:    discordVoiceSt,
		User:     discordUser,
		Presence: discordPresence,
		Channel:  dVoice.channel,
		Count:    len(dVoice.members),
		Mute:     dVoice.mute,
		Deaf:     dVoice.deaf,
	}
	discordMu.Unlock()
	info.Speaking = discordSpeakers(cfg.DiscordOptions, time.Now())
	if info.Speaking == nil {
		info.Speaking = []string{}
	}
	info.World = CurrentWorld()
	return info
}

// discordSpeakers lists who is talking, oldest speaker first. A name stays for
// the debounce time after they stop so the line does not flicker per syllable.
func discordSpeakers(o DiscordOptions, now time.Time) []string {
	discordMu.Lock()
	defer discordMu.Unlock()
	type sp struct {
		name  string
		since time.Time
	}
	var list []sp
	debounce := time.Duration(o.DebounceMS) * time.Millisecond
	for id, since := range dVoice.since {
		if o.HideSelf && id == dVoice.selfID {
			continue
		}
		if stop, ok := dVoice.stopped[id]; ok && now.Sub(stop) > debounce {
			continue
		}
		name := dVoice.members[id]
		if name == "" {
			continue
		}
		list = append(list, sp{name, since})
	}
	slices.SortFunc(list, func(a, b sp) int {
		if c := a.since.Compare(b.since); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.name)
	}
	return out
}

// discordLine renders the voice channel line.
func discordLine(cfg Config) string {
	o := cfg.DiscordOptions
	discordMu.Lock()
	in, signed := dVoice.in, dVoice.signedIn
	channel, count := dVoice.channel, len(dVoice.members)
	mute, deaf := dVoice.mute, dVoice.deaf
	discordMu.Unlock()
	if !signed {
		return ""
	}
	if !in {
		return o.NotInVCText
	}
	speakers := discordSpeakers(o, time.Now())
	speaking := o.QuietText
	if len(speakers) > 0 {
		if o.CountOnly {
			speaking = strconv.Itoa(len(speakers)) + " speaking"
		} else {
			shown := speakers
			if len(shown) > o.MaxSpeakers {
				shown = shown[:o.MaxSpeakers]
			}
			speaking = strings.Join(shown, ", ")
			if extra := len(speakers) - len(shown); extra > 0 {
				speaking += " +" + strconv.Itoa(extra)
			}
		}
	}
	muteState := ""
	if o.ShowMute {
		switch {
		case deaf:
			muteState = o.DeafText
		case mute:
			muteState = o.MuteText
		}
	}
	text := fillTemplate(o.Template, map[string]string{
		"channel":        channel,
		"count":          strconv.Itoa(count),
		"speaking":       speaking,
		"speaking_count": strconv.Itoa(len(speakers)),
		"mute_emoji":     muteState,
		"mute_state":     muteState,
	})
	// A template without {mute_emoji} still shows the state at the end.
	if muteState != "" && !strings.Contains(o.Template, "{mute_") {
		text = strings.TrimSpace(text + " " + muteState)
	}
	return text
}

// presenceActivity builds the Rich Presence activity, or nil for none.
func presenceActivity(cfg Config, w WorldInfo, vrchatRunning bool, players int, inVR bool) map[string]any {
	o := cfg.DiscordOptions
	if !o.Presence || (o.OnlyInVRChat && !vrchatRunning) {
		return nil
	}
	mode := "Desktop"
	if inVR {
		mode = "VR"
	}
	world := w.Name
	vals := map[string]string{
		"world":  world,
		"type":   w.Type,
		"region": w.Region,
		"count":  strconv.Itoa(players),
		"mode":   mode,
	}
	if world == "" {
		vals["world"] = "VRChat"
	}
	details := presenceText(fillTemplate(o.Details, vals))
	state := presenceText(fillTemplate(o.State, vals))
	if w.WorldID == "" {
		// No instance known yet: keep it short rather than "0 players • ".
		state = presenceText(mode)
	}
	act := map[string]any{"instance": false}
	if details != "" {
		act["details"] = details
	}
	if state != "" {
		act["state"] = state
	}
	if o.ShowElapsed {
		start := w.Since
		if start == 0 {
			start = vrchatSince()
		}
		act["timestamps"] = map[string]any{"start": start}
	}
	assets := map[string]any{}
	if o.LargeImage != "" {
		assets["large_image"] = o.LargeImage
		if o.LargeText != "" {
			assets["large_text"] = presenceText(o.LargeText)
		}
	}
	if len(assets) > 0 {
		act["assets"] = assets
	}
	var buttons []map[string]string
	if o.JoinButton {
		if link := w.JoinURL(); link != "" {
			buttons = append(buttons, map[string]string{"label": RuneLimit(o.JoinLabel, 32), "url": link})
		}
	}
	if o.ButtonLabel != "" && o.ButtonURL != "" {
		buttons = append(buttons, map[string]string{"label": RuneLimit(o.ButtonLabel, 32), "url": o.ButtonURL})
	}
	if len(buttons) > 0 {
		act["buttons"] = buttons
	}
	return act
}

// presenceText fits Discord's 2 to 128 character limit for presence lines.
func presenceText(s string) string {
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
	s = RuneLimit(s, 128)
	if len([]rune(s)) == 1 {
		s += " "
	}
	return s
}

var fernStartedAt = time.Now()

// vrchatSince is the elapsed-time start when no world is known.
func vrchatSince() int64 { return fernStartedAt.UnixMilli() }

func startDiscordWorker() {
	go func() {
		for {
			cfg := GetConfig()
			o := cfg.DiscordOptions
			if !o.Presence && !cfg.DisplayOptions.Discord {
				setDiscordLink(linkOff, "")
				setDiscordVoice(linkOff, "")
				clearDiscordVoice()
				sleepOrKick(2*time.Second, discordKickCh)
				continue
			}
			if o.ClientID == "" {
				setDiscordLink(linkWaiting, "Add your Discord application ID")
				sleepOrKick(2*time.Second, discordKickCh)
				continue
			}
			t, err := dialDiscord()
			if err != nil {
				setDiscordLink(linkWaiting, "Discord isn't running")
				clearDiscordVoice()
				sleepOrKick(5*time.Second, discordKickCh)
				continue
			}
			s := newDiscordSession(t, GetConfig)
			err = s.run()
			_ = t.Close()
			clearDiscordVoice()
			discordMu.Lock()
			discordPresence = ""
			discordMu.Unlock()
			if err != nil && !errors.Is(err, errSessionDone) {
				setDiscordLink(linkError, err.Error())
				sleepOrKick(5*time.Second, discordKickCh)
			}
		}
	}()
}

func clearDiscordVoice() {
	discordMu.Lock()
	dVoice = discordVoice{}
	discordMu.Unlock()
}

var errSessionDone = errors.New("session ended")

// discordSession is one connection to the Discord client.
type discordSession struct {
	t        ipcTransport
	cfgFn    func() Config
	clientID string
	nonce    int
	pending  map[string]func(rpcMessage)
	ready    bool
	opened   time.Time

	authBusy     bool
	authed       bool
	triedRefresh bool
	tokenCh      chan tokenResult
	channelID    string

	lastPresence   string
	lastPresenceAt time.Time

	// hooks, swapped in tests
	world   func() WorldInfo
	running func() bool
	players func() int
	inVR    func() bool
}

type tokenResult struct {
	access, refresh string
	err             error
	refreshed       bool
}

func newDiscordSession(t ipcTransport, cfgFn func() Config) *discordSession {
	return &discordSession{
		t:        t,
		cfgFn:    cfgFn,
		clientID: cfgFn().DiscordOptions.ClientID,
		pending:  map[string]func(rpcMessage){},
		tokenCh:  make(chan tokenResult, 1),
		world:    CurrentWorld,
		running:  func() bool { return CurrentVRChatStatus().Running },
		players:  PlayerCount,
		inVR:     InVR,
	}
}

func (s *discordSession) send(cmd string, args any, evt string, done func(rpcMessage)) error {
	s.nonce++
	nonce := strconv.Itoa(s.nonce)
	if done != nil {
		s.pending[nonce] = done
	}
	return encodeFrame(s.t, ipcOpFrame, rpcMessage{Cmd: cmd, Nonce: nonce, Args: args, Evt: evt})
}

// run handshakes and then services the pipe until it closes or the settings
// no longer want a session. Reads happen only when data is waiting, so writes
// are never stuck behind a blocked read.
func (s *discordSession) run() error {
	if err := encodeFrame(s.t, ipcOpHandshake, map[string]any{"v": 1, "client_id": s.clientID}); err != nil {
		return err
	}
	s.opened = time.Now()
	setDiscordLink(linkWaiting, "Waiting for Discord to answer")
	for {
		select {
		case <-discordKickCh:
			return errSessionDone
		default:
		}
		cfg := s.cfgFn()
		if cfg.DiscordOptions.ClientID != s.clientID || (!cfg.DiscordOptions.Presence && !cfg.DisplayOptions.Discord) {
			return errSessionDone
		}
		n, err := s.t.Available()
		if err != nil {
			return errors.New("Discord closed the connection")
		}
		if n > 0 {
			op, body, err := decodeFrame(s.t)
			if err != nil {
				return errors.New("Discord closed the connection")
			}
			if err := s.handle(op, body); err != nil {
				return err
			}
			continue
		}
		if !s.ready && time.Since(s.opened) > 15*time.Second {
			return errors.New("Discord did not answer the handshake")
		}
		if s.ready {
			if err := s.tick(cfg); err != nil {
				return err
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (s *discordSession) handle(op uint32, body []byte) error {
	switch op {
	case ipcOpClose:
		var e rpcError
		_ = json.Unmarshal(body, &e)
		if e.Code == 4000 || strings.Contains(strings.ToLower(e.Message), "client id") {
			return errors.New("Discord rejected the application ID")
		}
		if e.Message == "" {
			e.Message = "closed"
		}
		return errors.New("Discord: " + e.Message)
	case ipcOpPing:
		return writeRaw(s.t, ipcOpPong, body)
	case ipcOpFrame:
	default:
		return nil
	}
	var m rpcMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	if m.Cmd == "DISPATCH" && m.Evt == "READY" {
		s.ready = true
		var d struct {
			User struct {
				Username   string `json:"username"`
				GlobalName string `json:"global_name"`
			} `json:"user"`
		}
		_ = json.Unmarshal(m.Data, &d)
		discordMu.Lock()
		discordUser = firstNonEmpty(d.User.GlobalName, d.User.Username)
		discordMu.Unlock()
		setDiscordLink(linkConnected, "")
		return nil
	}
	if done, ok := s.pending[m.Nonce]; ok && m.Nonce != "" {
		delete(s.pending, m.Nonce)
		done(m)
		return nil
	}
	if m.Cmd == "DISPATCH" {
		s.event(m)
	}
	return nil
}

func writeRaw(t ipcTransport, op uint32, body []byte) error {
	return encodeFrame(t, op, json.RawMessage(body))
}

func (s *discordSession) tick(cfg Config) error {
	if err := s.updatePresence(cfg); err != nil {
		return err
	}
	select {
	case res := <-s.tokenCh:
		s.authBusy = false
		if res.err != nil {
			if res.refreshed {
				_ = SetSecret(SecretDiscordAccess, "")
				_ = SetSecret(SecretDiscordRefresh, "")
				setDiscordVoice(linkWaiting, "Sign-in expired. Connect voice again.")
			} else {
				setDiscordVoice(linkError, res.err.Error())
			}
			return nil
		}
		_ = SetSecret(SecretDiscordAccess, res.access)
		if res.refresh != "" {
			_ = SetSecret(SecretDiscordRefresh, res.refresh)
		}
		return s.authenticate(res.access)
	default:
	}
	if !cfg.DisplayOptions.Discord {
		setDiscordVoice(linkOff, "")
		return nil
	}
	if s.authed || s.authBusy {
		return nil
	}
	if token := GetSecret(SecretDiscordAccess); token != "" {
		return s.authenticate(token)
	}
	if GetSecret(SecretDiscordClientSecret) == "" {
		setDiscordVoice(linkWaiting, "Add your application's client secret, then connect voice")
		return nil
	}
	if !discordAuthReq.Swap(false) {
		softDiscordVoice("Not signed in. Press Connect voice.")
		return nil
	}
	s.authBusy = true
	setDiscordVoice(linkWaiting, "Approve Fern in the Discord window")
	return s.send("AUTHORIZE", map[string]any{
		"client_id": s.clientID,
		"scopes":    []string{"rpc", "rpc.voice.read"},
	}, "", func(m rpcMessage) {
		if err := m.err(); err != nil {
			s.authBusy = false
			setDiscordVoice(linkError, authorizeHint(err))
			return
		}
		var d struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(m.Data, &d)
		form := url.Values{
			"grant_type": {"authorization_code"},
			"code":       {d.Code},
		}
		if r := cfg.DiscordOptions.RedirectURI; r != "" {
			form.Set("redirect_uri", r)
		}
		go s.exchange(form, false)
	})
}

// authorizeHint explains the usual reason Discord refuses the voice scopes.
func authorizeHint(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "4002") || strings.Contains(strings.ToLower(msg), "scope") {
		return "Discord refused the voice permission. It is only granted to your own application or its testers."
	}
	if strings.Contains(msg, "5000") || strings.Contains(strings.ToLower(msg), "cancel") {
		return "Sign-in was cancelled in Discord"
	}
	return msg
}

// exchange trades an authorization code or refresh token for tokens.
func (s *discordSession) exchange(form url.Values, refreshed bool) {
	form.Set("client_id", s.clientID)
	form.Set("client_secret", GetSecret(SecretDiscordClientSecret))
	access, refresh, err := postToken(discordHTTP, discordTokenURL, form)
	s.tokenCh <- tokenResult{access: access, refresh: refresh, err: err, refreshed: refreshed}
}

// postToken posts an OAuth2 token request. Errors carry the OAuth error code
// only, never the request or response body, so tokens cannot end up in logs.
func postToken(c *http.Client, endpoint string, form url.Values) (access, refresh string, err error) {
	resp, err := c.PostForm(endpoint, form)
	if err != nil {
		return "", "", errors.New("could not reach the sign-in server")
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Error        string `json:"error"`
		Message      string `json:"message"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		code := firstNonEmpty(out.Error, out.Message, resp.Status)
		return "", "", fmt.Errorf("sign-in failed: %s", code)
	}
	return out.AccessToken, out.RefreshToken, nil
}

func (s *discordSession) authenticate(token string) error {
	s.authBusy = true
	return s.send("AUTHENTICATE", map[string]any{"access_token": token}, "", func(m rpcMessage) {
		s.authBusy = false
		if err := m.err(); err != nil {
			refresh := GetSecret(SecretDiscordRefresh)
			_ = SetSecret(SecretDiscordAccess, "")
			if refresh != "" && !s.triedRefresh {
				s.triedRefresh = true
				s.authBusy = true
				go s.exchange(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, true)
				return
			}
			setDiscordVoice(linkWaiting, "Sign-in expired. Connect voice again.")
			return
		}
		s.authed = true
		var d struct {
			User struct {
				ID string `json:"id"`
			} `json:"user"`
		}
		_ = json.Unmarshal(m.Data, &d)
		discordMu.Lock()
		dVoice.selfID = d.User.ID
		dVoice.signedIn = true
		discordMu.Unlock()
		setDiscordVoice(linkConnected, "")
		_ = s.send("SUBSCRIBE", nil, "VOICE_CHANNEL_SELECT", nil)
		_ = s.send("SUBSCRIBE", nil, "VOICE_SETTINGS_UPDATE", nil)
		_ = s.send("GET_VOICE_SETTINGS", nil, "", func(m rpcMessage) { s.voiceSettings(m.Data) })
		s.refreshChannel()
	})
}

func (s *discordSession) refreshChannel() {
	_ = s.send("GET_SELECTED_VOICE_CHANNEL", nil, "", func(m rpcMessage) {
		if m.err() != nil {
			return
		}
		s.setChannel(m.Data)
	})
}

var channelEvents = []string{"VOICE_STATE_CREATE", "VOICE_STATE_UPDATE", "VOICE_STATE_DELETE", "SPEAKING_START", "SPEAKING_STOP"}

// voiceStateData is a member entry from a channel or a VOICE_STATE event.
type voiceStateData struct {
	Nick string `json:"nick"`
	User struct {
		ID         string `json:"id"`
		Username   string `json:"username"`
		GlobalName string `json:"global_name"`
	} `json:"user"`
}

func (v voiceStateData) name() string {
	return firstNonEmpty(v.Nick, v.User.GlobalName, v.User.Username)
}

func (s *discordSession) setChannel(data json.RawMessage) {
	var ch struct {
		ID          string           `json:"id"`
		Name        string           `json:"name"`
		VoiceStates []voiceStateData `json:"voice_states"`
	}
	if len(data) == 0 || string(data) == "null" || json.Unmarshal(data, &ch) != nil || ch.ID == "" {
		s.leaveChannel()
		return
	}
	if ch.ID != s.channelID {
		s.leaveChannel()
		s.channelID = ch.ID
		for _, evt := range channelEvents {
			_ = s.send("SUBSCRIBE", map[string]string{"channel_id": ch.ID}, evt, nil)
		}
	}
	members := make(map[string]string, len(ch.VoiceStates))
	for _, vs := range ch.VoiceStates {
		if vs.User.ID != "" {
			members[vs.User.ID] = vs.name()
		}
	}
	discordMu.Lock()
	dVoice.in = true
	dVoice.channel = ch.Name
	dVoice.members = members
	if dVoice.since == nil {
		dVoice.since = map[string]time.Time{}
		dVoice.stopped = map[string]time.Time{}
	}
	discordMu.Unlock()
}

func (s *discordSession) leaveChannel() {
	if s.channelID != "" {
		for _, evt := range channelEvents {
			_ = s.send("UNSUBSCRIBE", map[string]string{"channel_id": s.channelID}, evt, nil)
		}
	}
	s.channelID = ""
	discordMu.Lock()
	dVoice.in = false
	dVoice.channel = ""
	dVoice.members = nil
	dVoice.since = nil
	dVoice.stopped = nil
	discordMu.Unlock()
}

func (s *discordSession) voiceSettings(data json.RawMessage) {
	var v struct {
		Mute bool `json:"mute"`
		Deaf bool `json:"deaf"`
	}
	if json.Unmarshal(data, &v) != nil {
		return
	}
	discordMu.Lock()
	dVoice.mute, dVoice.deaf = v.Mute, v.Deaf
	discordMu.Unlock()
}

func (s *discordSession) event(m rpcMessage) {
	switch m.Evt {
	case "VOICE_CHANNEL_SELECT":
		var d struct {
			ChannelID *string `json:"channel_id"`
		}
		_ = json.Unmarshal(m.Data, &d)
		if d.ChannelID == nil || *d.ChannelID == "" {
			s.leaveChannel()
			return
		}
		s.refreshChannel()
	case "VOICE_SETTINGS_UPDATE":
		s.voiceSettings(m.Data)
	case "VOICE_STATE_CREATE", "VOICE_STATE_UPDATE":
		var vs voiceStateData
		if json.Unmarshal(m.Data, &vs) != nil || vs.User.ID == "" {
			return
		}
		discordMu.Lock()
		if dVoice.members == nil {
			dVoice.members = map[string]string{}
		}
		dVoice.members[vs.User.ID] = vs.name()
		discordMu.Unlock()
	case "VOICE_STATE_DELETE":
		var vs voiceStateData
		if json.Unmarshal(m.Data, &vs) != nil {
			return
		}
		discordMu.Lock()
		delete(dVoice.members, vs.User.ID)
		delete(dVoice.since, vs.User.ID)
		delete(dVoice.stopped, vs.User.ID)
		discordMu.Unlock()
	case "SPEAKING_START", "SPEAKING_STOP":
		var d struct {
			UserID string `json:"user_id"`
		}
		if json.Unmarshal(m.Data, &d) != nil || d.UserID == "" {
			return
		}
		now := time.Now()
		discordMu.Lock()
		if dVoice.since == nil {
			dVoice.since = map[string]time.Time{}
			dVoice.stopped = map[string]time.Time{}
		}
		if m.Evt == "SPEAKING_START" {
			if _, talking := dVoice.since[d.UserID]; !talking {
				dVoice.since[d.UserID] = now
			} else if _, wasStopped := dVoice.stopped[d.UserID]; wasStopped && now.Sub(dVoice.stopped[d.UserID]) > 5*time.Second {
				dVoice.since[d.UserID] = now
			}
			delete(dVoice.stopped, d.UserID)
		} else {
			dVoice.stopped[d.UserID] = now
		}
		discordMu.Unlock()
		NotifyChange()
	}
}

// updatePresence sends SET_ACTIVITY when the presence changed. Discord allows
// about five updates per 20 seconds, so changes are spaced at least 4s apart.
func (s *discordSession) updatePresence(cfg Config) error {
	act := presenceActivity(cfg, s.world(), s.running(), s.players(), s.inVR())
	sig := ""
	if act != nil {
		b, _ := json.Marshal(act)
		sig = string(b)
	}
	if sig == s.lastPresence || time.Since(s.lastPresenceAt) < 4*time.Second {
		return nil
	}
	s.lastPresence = sig
	s.lastPresenceAt = time.Now()
	args := map[string]any{"pid": os.Getpid()}
	shown := ""
	if act != nil {
		args["activity"] = act
		if d, ok := act["details"].(string); ok {
			shown = d
		}
		if st, ok := act["state"].(string); ok {
			shown = strings.TrimSpace(shown + " · " + st)
		}
	}
	discordMu.Lock()
	discordPresence = strings.Trim(shown, " ·")
	discordMu.Unlock()
	return s.send("SET_ACTIVITY", args, "", func(m rpcMessage) {
		if err := m.err(); err != nil {
			setDiscordLink(linkError, "Presence refused: "+err.Error())
		}
	})
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
