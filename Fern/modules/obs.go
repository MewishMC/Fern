package modules

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// OBS Studio talks obs-websocket v5, built into OBS 28 and later. Fern
// connects as a client: the server greets with Hello (op 0), Fern answers
// with Identify (op 1), carrying a SHA-256 answer to the server's challenge
// when a password is set, and the server confirms with Identified (op 2).
// After that OBS sends events (op 5), and requests (op 6) get responses
// (op 7) matched by requestId. The password lives in the secrets store.

// SecretOBSPassword is the obs-websocket server password.
const SecretOBSPassword = "obs_password"

// ModuleOBS is the OBS line: live and recording with their duration, and the
// current scene.
const ModuleOBS = "obs"

const (
	obsOpHello      = 0
	obsOpIdentify   = 1
	obsOpIdentified = 2
	obsOpEvent      = 5
	obsOpRequest    = 6
	obsOpResponse   = 7

	// General, Scenes, Outputs and SceneItems events.
	obsEventSubs = 1<<0 | 1<<2 | 1<<6 | 1<<7

	obsDefaultPort = 4455
	obsMaxScenes   = 200
	obsMaxItems    = 200
)

// OBSOptions controls the OBS connection and its line. Enabled, Host and
// Port are per PC; the rest belongs to profiles.
type OBSOptions struct {
	Enabled      bool   `json:"enabled"` // connect with the line off, for rules and the panel
	Host         string `json:"host"`
	Port         int    `json:"port"`
	ShowStream   bool   `json:"show_stream"`
	ShowRecord   bool   `json:"show_record"`
	ShowDuration bool   `json:"show_duration"`
	ShowScene    bool   `json:"show_scene"`
	OnlyActive   bool   `json:"only_active"` // nothing while neither live nor recording
	LiveText     string `json:"live_text"`
	RecText      string `json:"rec_text"`
	PausedText   string `json:"paused_text"`
	SceneLabel   string `json:"scene_label"`
	Separator    string `json:"separator"`
}

func defaultOBSOptions() OBSOptions {
	return OBSOptions{
		Host:         "127.0.0.1",
		Port:         obsDefaultPort,
		ShowStream:   true,
		ShowRecord:   true,
		ShowDuration: true,
		ShowScene:    false,
		OnlyActive:   true,
		LiveText:     "🔴 LIVE",
		RecText:      "⏺️ REC",
		PausedText:   "⏸️ REC paused",
		SceneLabel:   "🎬",
		Separator:    " · ",
	}
}

func (o *OBSOptions) sanitize() {
	def := defaultOBSOptions()
	o.Host = RuneLimit(strings.TrimSpace(o.Host), 253)
	if o.Host == "" {
		o.Host = def.Host
	}
	if o.Port < 1 || o.Port > 65535 {
		o.Port = def.Port
	}
	for _, s := range []*string{&o.LiveText, &o.RecText, &o.PausedText} {
		*s = RuneLimit(strings.Join(strings.Fields(*s), " "), 24)
	}
	o.SceneLabel = RuneLimit(strings.TrimSpace(o.SceneLabel), 16)
	o.Separator = RuneLimit(strings.ReplaceAll(o.Separator, "\n", " "), 8)
	if o.Separator == "" {
		o.Separator = def.Separator
	}
}

// OBSSceneItem is a source in the current scene.
type OBSSceneItem struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// OBSStatus is what the OBS page shows.
type OBSStatus struct {
	Link         LinkStatus     `json:"link"`
	Version      string         `json:"version"`
	Streaming    bool           `json:"streaming"`
	StreamMS     int64          `json:"streamMs"`
	Recording    bool           `json:"recording"`
	RecordPaused bool           `json:"recordPaused"`
	RecordMS     int64          `json:"recordMs"`
	Scene        string         `json:"scene"`
	Scenes       []string       `json:"scenes"`
	Items        []OBSSceneItem `json:"items"`
}

// obsOutput follows one output's running time between status polls.
type obsOutput struct {
	active bool
	paused bool
	base   time.Duration // time counted up to at
	at     time.Time
}

func (o obsOutput) elapsed(now time.Time) time.Duration {
	if !o.active {
		return 0
	}
	if o.paused || o.at.IsZero() {
		return o.base
	}
	return o.base + now.Sub(o.at)
}

// sync sets the output from a status reply.
func (o *obsOutput) sync(active, paused bool, durMS int64, now time.Time) {
	o.active, o.paused = active, active && paused
	o.base, o.at = 0, now
	if active && durMS > 0 {
		o.base = time.Duration(durMS) * time.Millisecond
	}
}

// event applies an OutputStateChanged event and reports "started",
// "stopped" or "".
func (o *obsOutput) event(active bool, state string, now time.Time) string {
	switch strings.TrimPrefix(state, "OBS_WEBSOCKET_OUTPUT_") {
	case "STARTED":
		*o = obsOutput{active: true, at: now}
		return "started"
	case "STOPPED":
		*o = obsOutput{}
		return "stopped"
	case "PAUSED":
		o.base, o.at, o.paused = o.elapsed(now), now, true
	case "RESUMED":
		o.at, o.paused = now, false
	default:
		if !active && o.active {
			*o = obsOutput{}
			return "stopped"
		}
	}
	return ""
}

var (
	obsMu      sync.Mutex
	obsLink    = LinkStatus{State: linkOff}
	obsVersion string
	obsStream  obsOutput
	obsRecord  obsOutput
	obsScene   string
	obsScenes  []string
	obsItems   []OBSSceneItem
	obsCur     *obsConn
	obsKickCh  = make(chan struct{}, 1)
	obsNow     = time.Now
	// obsFire hands rule events on; swapped in tests.
	obsFire = FireTrigger
)

func obsKick() { poke(obsKickCh) }

func setOBSLink(state, detail string) {
	obsMu.Lock()
	obsLink = LinkStatus{State: state, Detail: detail}
	obsMu.Unlock()
	NotifyChange()
}

func clearOBS() {
	obsMu.Lock()
	obsVersion, obsScene = "", ""
	obsStream, obsRecord = obsOutput{}, obsOutput{}
	obsScenes, obsItems = nil, nil
	obsMu.Unlock()
}

// CurrentOBS returns the OBS snapshot for the panel.
func CurrentOBS() OBSStatus {
	now := obsNow()
	obsMu.Lock()
	defer obsMu.Unlock()
	return OBSStatus{
		Link:         obsLink,
		Version:      obsVersion,
		Streaming:    obsStream.active,
		StreamMS:     obsStream.elapsed(now).Milliseconds(),
		Recording:    obsRecord.active,
		RecordPaused: obsRecord.paused,
		RecordMS:     obsRecord.elapsed(now).Milliseconds(),
		Scene:        obsScene,
		Scenes:       append([]string{}, obsScenes...),
		Items:        append([]OBSSceneItem{}, obsItems...),
	}
}

// formatOBSDuration writes a running time as 4:05 or 1:02:03.
func formatOBSDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	h, m, sec := s/3600, s/60%60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// obsLine renders the OBS line from the snapshot.
func obsLine(cfg Config) string {
	o := cfg.OBSOptions
	st := CurrentOBS()
	if st.Link.State != linkConnected {
		return ""
	}
	active := st.Streaming || st.Recording
	if o.OnlyActive && !active {
		return ""
	}
	var parts []string
	add := func(label string, ms int64) {
		if o.ShowDuration {
			label = strings.TrimSpace(label + " " + formatOBSDuration(time.Duration(ms)*time.Millisecond))
		}
		if label != "" {
			parts = append(parts, label)
		}
	}
	if o.ShowStream && st.Streaming {
		add(o.LiveText, st.StreamMS)
	}
	if o.ShowRecord && st.Recording {
		label := o.RecText
		if st.RecordPaused {
			label = o.PausedText
		}
		add(label, st.RecordMS)
	}
	if o.ShowScene && st.Scene != "" {
		parts = append(parts, strings.TrimSpace(o.SceneLabel+" "+st.Scene))
	}
	return strings.Join(parts, o.Separator)
}

// obsWanted reports whether anything needs the connection: the line, the
// switch on the OBS page, or an enabled rule using an OBS trigger or action.
func obsWanted(c Config) bool {
	if c.OBSOptions.Enabled || c.DisplayOptions.OBS {
		return true
	}
	if !c.Rules.Enabled {
		return false
	}
	for _, r := range c.Rules.List {
		if !r.Enabled {
			continue
		}
		if strings.HasPrefix(r.Trigger.Type, "obs.") {
			return true
		}
		for _, s := range r.Conditions {
			if strings.HasPrefix(s.Type, "obs.") {
				return true
			}
		}
		for _, s := range r.Actions {
			if strings.HasPrefix(s.Type, "obs.") {
				return true
			}
		}
	}
	return false
}

// obsAuth answers the Hello challenge: base64(sha256(secret + challenge)),
// where secret is base64(sha256(password + salt)).
func obsAuth(password, salt, challenge string) string {
	sum := sha256.Sum256([]byte(password + salt))
	secret := base64.StdEncoding.EncodeToString(sum[:])
	sum = sha256.Sum256([]byte(secret + challenge))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// obsMessage is any obs-websocket frame.
type obsMessage struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
}

type obsReply struct {
	ok      bool
	code    int
	comment string
	data    json.RawMessage
}

// obsConn is one identified connection. Writes are serialised; replies to
// call reach the waiting goroutine by requestId.
type obsConn struct {
	conn    *websocket.Conn
	wmu     sync.Mutex
	seq     atomic.Uint64
	pmu     sync.Mutex
	pending map[string]chan obsReply
	gone    chan struct{} // closed when the session ends
	once    sync.Once
}

func newOBSConn(c *websocket.Conn) *obsConn {
	return &obsConn{conn: c, pending: map[string]chan obsReply{}, gone: make(chan struct{})}
}

func (c *obsConn) write(op int, d any) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return c.conn.WriteJSON(map[string]any{"op": op, "d": d})
}

func (c *obsConn) nextID() string {
	return "fern-" + strconv.FormatUint(c.seq.Add(1), 10)
}

// send makes a request without waiting for its reply.
func (c *obsConn) send(typ string, data any) error {
	d := map[string]any{"requestType": typ, "requestId": c.nextID()}
	if data != nil {
		d["requestData"] = data
	}
	return c.write(obsOpRequest, d)
}

var errOBSOffline = errors.New("OBS isn't connected")

// call makes a request and waits for its reply.
func (c *obsConn) call(typ string, data any) (json.RawMessage, error) {
	id := c.nextID()
	ch := make(chan obsReply, 1)
	select {
	case <-c.gone:
		return nil, errOBSOffline
	default:
	}
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
	}()
	d := map[string]any{"requestType": typ, "requestId": id}
	if data != nil {
		d["requestData"] = data
	}
	if err := c.write(obsOpRequest, d); err != nil {
		return nil, errOBSOffline
	}
	select {
	case <-c.gone:
		return nil, errOBSOffline
	case r := <-ch:
		if !r.ok {
			if r.comment != "" {
				return nil, fmt.Errorf("OBS: %s", r.comment)
			}
			return nil, fmt.Errorf("OBS refused %s (%d)", typ, r.code)
		}
		return r.data, nil
	case <-time.After(4 * time.Second):
		return nil, fmt.Errorf("OBS did not answer %s", typ)
	}
}

func (c *obsConn) deliver(id string, r obsReply) {
	c.pmu.Lock()
	ch := c.pending[id]
	c.pmu.Unlock()
	if ch != nil {
		select {
		case ch <- r:
		default:
		}
	}
}

// shut fails every waiting call. Reply channels are never closed, so a
// reply arriving late cannot panic.
func (c *obsConn) shut() {
	c.once.Do(func() { close(c.gone) })
}

func currentOBSConn() *obsConn {
	obsMu.Lock()
	defer obsMu.Unlock()
	return obsCur
}

func startOBSWorker() {
	go func() {
		for {
			cfg := GetConfig()
			if !obsWanted(cfg) {
				setOBSLinkQuiet(linkOff, "")
				clearOBS()
				sleepOrKick(2*time.Second, obsKickCh)
				continue
			}
			addr := net.JoinHostPort(cfg.OBSOptions.Host, strconv.Itoa(cfg.OBSOptions.Port))
			conn := dialOBS("ws://" + addr)
			if conn == nil {
				setOBSLinkQuiet(linkWaiting, "OBS isn't running, or its WebSocket server is off (Tools › WebSocket Server Settings)")
				clearOBS()
				sleepOrKick(5*time.Second, obsKickCh)
				continue
			}
			err := obsSession(conn, GetSecret(SecretOBSPassword), func() bool {
				c := GetConfig()
				return obsWanted(c) && net.JoinHostPort(c.OBSOptions.Host, strconv.Itoa(c.OBSOptions.Port)) == addr
			})
			_ = conn.Close()
			clearOBS()
			if err != nil {
				setOBSLink(linkError, err.Error())
				sleepOrKick(5*time.Second, obsKickCh)
			}
		}
	}()
}

// setOBSLinkQuiet changes the link without waking the panel when nothing
// changed, since the worker sets it on every idle pass.
func setOBSLinkQuiet(state, detail string) {
	obsMu.Lock()
	same := obsLink.State == state && obsLink.Detail == detail
	obsMu.Unlock()
	if !same {
		setOBSLink(state, detail)
	}
}

func dialOBS(url string) *websocket.Conn {
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, resp, err := dialer.Dial(url, http.Header{})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil
	}
	conn.SetReadLimit(8 << 20)
	return conn
}

// obsHandshake reads Hello, identifies and waits for Identified. It returns
// the obs-websocket version.
func obsHandshake(conn *websocket.Conn, password string) (string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello obsMessage
	if err := conn.ReadJSON(&hello); err != nil || hello.Op != obsOpHello {
		return "", errors.New("that port isn't an OBS WebSocket server")
	}
	var h struct {
		Version string `json:"obsWebSocketVersion"`
		RPC     int    `json:"rpcVersion"`
		Auth    *struct {
			Challenge string `json:"challenge"`
			Salt      string `json:"salt"`
		} `json:"authentication"`
	}
	_ = json.Unmarshal(hello.D, &h)
	id := map[string]any{"rpcVersion": 1, "eventSubscriptions": obsEventSubs}
	if h.Auth != nil {
		if password == "" {
			return "", errors.New("OBS asks for a password: add it below")
		}
		id["authentication"] = obsAuth(password, h.Auth.Salt, h.Auth.Challenge)
	}
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteJSON(map[string]any{"op": obsOpIdentify, "d": id}); err != nil {
		return "", errors.New("OBS closed the connection")
	}
	for {
		var m obsMessage
		if err := conn.ReadJSON(&m); err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				switch ce.Code {
				case 4009:
					return "", errors.New("OBS refused the password")
				case 4010:
					return "", errors.New("this OBS speaks a different WebSocket version: update OBS")
				}
			}
			return "", errors.New("OBS closed the connection while signing in")
		}
		if m.Op == obsOpIdentified {
			return h.Version, nil
		}
	}
}

// obsSession signs in and services the socket until it closes, the
// settings no longer want it, or it is kicked. A nil error means it ended on
// purpose or OBS went away; an error is shown on the page.
func obsSession(conn *websocket.Conn, password string, wanted func() bool) error {
	setOBSLink(linkWaiting, "Signing in to OBS")
	version, err := obsHandshake(conn, password)
	if err != nil {
		return err
	}
	c := newOBSConn(conn)
	obsMu.Lock()
	obsVersion, obsCur = version, c
	obsMu.Unlock()
	defer func() {
		obsMu.Lock()
		if obsCur == c {
			obsCur = nil
		}
		obsMu.Unlock()
		c.shut()
	}()
	setOBSLink(linkConnected, "")

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))
			var m obsMessage
			if err := conn.ReadJSON(&m); err != nil {
				return
			}
			switch m.Op {
			case obsOpEvent:
				applyOBSEvent(c, m.D)
			case obsOpResponse:
				applyOBSResponse(c, m.D)
			}
		}
	}()
	obsPoll(c)
	go obsFetchScenes(c)

	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	check := time.NewTicker(2 * time.Second)
	defer check.Stop()
	for {
		select {
		case <-done:
			setOBSLink(linkWaiting, "OBS closed the connection")
			return nil
		case <-obsKickCh:
			return nil
		case <-check.C:
			if !wanted() {
				return nil
			}
		case <-poll.C:
			obsPoll(c)
		}
	}
}

// obsPoll asks for the output states, which also corrects the running times.
func obsPoll(c *obsConn) {
	_ = c.send("GetStreamStatus", nil)
	_ = c.send("GetRecordStatus", nil)
}

// obsFetchScenes reads the scene list, then the current scene's sources.
func obsFetchScenes(c *obsConn) {
	data, err := c.call("GetSceneList", nil)
	if err != nil {
		return
	}
	var r struct {
		Current string `json:"currentProgramSceneName"`
		Scenes  []struct {
			Name  string `json:"sceneName"`
			Index int    `json:"sceneIndex"`
		} `json:"scenes"`
	}
	if json.Unmarshal(data, &r) != nil {
		return
	}
	// OBS lists the bottom scene first; its window shows the top one first.
	sort.SliceStable(r.Scenes, func(i, j int) bool { return r.Scenes[i].Index > r.Scenes[j].Index })
	names := make([]string, 0, len(r.Scenes))
	for _, s := range r.Scenes {
		if len(names) < obsMaxScenes {
			names = append(names, s.Name)
		}
	}
	obsMu.Lock()
	obsScenes = names
	if r.Current != "" {
		obsScene = r.Current
	}
	scene := obsScene
	obsMu.Unlock()
	NotifyChange()
	NudgeRules()
	obsFetchItems(c, scene)
}

// obsSceneItems reads a scene's sources.
func obsSceneItems(c *obsConn, scene string) ([]OBSSceneItem, error) {
	data, err := c.call("GetSceneItemList", map[string]any{"sceneName": scene})
	if err != nil {
		return nil, err
	}
	var r struct {
		Items []struct {
			ID      int    `json:"sceneItemId"`
			Name    string `json:"sourceName"`
			Enabled bool   `json:"sceneItemEnabled"`
			Index   int    `json:"sceneItemIndex"`
		} `json:"sceneItems"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].Index > r.Items[j].Index })
	items := make([]OBSSceneItem, 0, len(r.Items))
	for _, it := range r.Items {
		if len(items) < obsMaxItems {
			items = append(items, OBSSceneItem{ID: it.ID, Name: it.Name, Enabled: it.Enabled})
		}
	}
	return items, nil
}

// obsFetchItems refreshes the cached sources when scene is still current.
func obsFetchItems(c *obsConn, scene string) {
	if scene == "" {
		return
	}
	items, err := obsSceneItems(c, scene)
	if err != nil {
		return
	}
	obsMu.Lock()
	if obsScene == scene {
		obsItems = items
	}
	obsMu.Unlock()
	NotifyChange()
}

func applyOBSResponse(c *obsConn, d json.RawMessage) {
	var r struct {
		Type   string `json:"requestType"`
		ID     string `json:"requestId"`
		Status struct {
			Result  bool   `json:"result"`
			Code    int    `json:"code"`
			Comment string `json:"comment"`
		} `json:"requestStatus"`
		Data json.RawMessage `json:"responseData"`
	}
	if json.Unmarshal(d, &r) != nil {
		return
	}
	c.deliver(r.ID, obsReply{ok: r.Status.Result, code: r.Status.Code, comment: r.Status.Comment, data: r.Data})
	if !r.Status.Result || len(r.Data) == 0 {
		return
	}
	var st struct {
		Active   bool  `json:"outputActive"`
		Paused   bool  `json:"outputPaused"`
		Duration int64 `json:"outputDuration"`
	}
	now := obsNow()
	switch r.Type {
	case "GetStreamStatus":
		if json.Unmarshal(r.Data, &st) == nil {
			obsMu.Lock()
			changed := obsStream.active != st.Active
			obsStream.sync(st.Active, false, st.Duration, now)
			obsMu.Unlock()
			obsChanged(changed)
		}
	case "GetRecordStatus":
		if json.Unmarshal(r.Data, &st) == nil {
			obsMu.Lock()
			changed := obsRecord.active != st.Active || obsRecord.paused != (st.Active && st.Paused)
			obsRecord.sync(st.Active, st.Paused, st.Duration, now)
			obsMu.Unlock()
			obsChanged(changed)
		}
	}
}

// obsChanged wakes the panel and the rules after a state change.
func obsChanged(changed bool) {
	if changed {
		NotifyChange()
		NudgeRules()
	}
}

func applyOBSEvent(c *obsConn, d json.RawMessage) {
	var ev struct {
		Type string          `json:"eventType"`
		Data json.RawMessage `json:"eventData"`
	}
	if json.Unmarshal(d, &ev) != nil {
		return
	}
	var data struct {
		Active    bool   `json:"outputActive"`
		State     string `json:"outputState"`
		Scene     string `json:"sceneName"`
		OldScene  string `json:"oldSceneName"`
		ItemID    int    `json:"sceneItemId"`
		Enabled   bool   `json:"sceneItemEnabled"`
		SceneList []struct {
			Name  string `json:"sceneName"`
			Index int    `json:"sceneIndex"`
		} `json:"scenes"`
	}
	_ = json.Unmarshal(ev.Data, &data)
	now := obsNow()
	switch ev.Type {
	case "StreamStateChanged", "RecordStateChanged":
		output := "stream"
		obsMu.Lock()
		o := &obsStream
		if ev.Type == "RecordStateChanged" {
			output, o = "record", &obsRecord
		}
		what := o.event(data.Active, data.State, now)
		obsMu.Unlock()
		NotifyChange()
		NudgeRules()
		if what != "" {
			obsFire(TriggerOBSOutput, map[string]string{"output": output, "state": what})
		}
	case "CurrentProgramSceneChanged":
		obsMu.Lock()
		prev := obsScene
		obsScene, obsItems = data.Scene, nil
		obsMu.Unlock()
		NotifyChange()
		NudgeRules()
		if prev != data.Scene {
			obsFire(TriggerOBSSceneChanged, map[string]string{"scene": data.Scene, "previous": prev})
		}
		go obsFetchItems(c, data.Scene)
	case "SceneListChanged", "SceneCreated", "SceneRemoved":
		go obsFetchScenes(c)
	case "SceneNameChanged":
		obsMu.Lock()
		if obsScene == data.OldScene {
			obsScene = data.Scene
		}
		obsMu.Unlock()
		go obsFetchScenes(c)
	case "SceneItemEnableStateChanged":
		obsMu.Lock()
		if data.Scene == obsScene {
			for i := range obsItems {
				if obsItems[i].ID == data.ItemID {
					obsItems[i].Enabled = data.Enabled
				}
			}
		}
		obsMu.Unlock()
		NotifyChange()
	case "SceneItemCreated", "SceneItemRemoved", "SceneItemListReindexed", "InputNameChanged":
		obsMu.Lock()
		scene := obsScene
		obsMu.Unlock()
		if data.Scene == "" || data.Scene == scene {
			go obsFetchItems(c, scene)
		}
	case "ExitStarted":
		_ = c.conn.Close()
	}
}

// OBS actions, shared by the panel and the rules.

// OBSOutput starts, stops or toggles the stream ("stream") or the
// recording ("record"), or pauses or resumes the recording.
func OBSOutput(output, want string) error {
	c := currentOBSConn()
	if c == nil {
		return errOBSOffline
	}
	name := "Stream"
	if output == "record" {
		name = "Record"
	} else if output != "stream" {
		return errors.New("unknown output")
	}
	var typ string
	switch want {
	case "start":
		typ = "Start" + name
	case "stop":
		typ = "Stop" + name
	case "toggle":
		typ = "Toggle" + name
	case "pause", "resume":
		if output != "record" {
			return errors.New("only the recording can be paused")
		}
		typ = strings.ToUpper(want[:1]) + want[1:] + "Record"
	default:
		return errors.New("unknown action")
	}
	go func() {
		if _, err := c.call(typ, nil); err != nil {
			fmt.Println("obs:", err)
		}
	}()
	return nil
}

// obsOutputActive reports whether an output is running, as last seen.
func obsOutputActive(output string) bool {
	obsMu.Lock()
	defer obsMu.Unlock()
	if output == "record" {
		return obsRecord.active
	}
	return obsStream.active
}

// OBSSetScene switches the program scene.
func OBSSetScene(scene string) error {
	c := currentOBSConn()
	if c == nil {
		return errOBSOffline
	}
	if strings.TrimSpace(scene) == "" {
		return errors.New("pick a scene")
	}
	go func() {
		if _, err := c.call("SetCurrentProgramScene", map[string]any{"sceneName": scene}); err != nil {
			fmt.Println("obs:", err)
		}
	}()
	return nil
}

// CurrentOBSScene is the program scene, or "".
func CurrentOBSScene() string {
	obsMu.Lock()
	defer obsMu.Unlock()
	return obsScene
}

// obsSourceResult is filled in by OBSSetSource's goroutine: the visibility
// it set, once it has.
type obsSourceResult struct {
	mu    sync.Mutex
	set   bool
	scene string
	id    int
	value bool
}

// OBSSetSource shows ("on"), hides ("off") or flips ("toggle") a source in a
// scene; an empty scene is the current one. The work happens in the
// background; the result says what was set once it is done.
func OBSSetSource(scene, source, state string) (*obsSourceResult, error) {
	c := currentOBSConn()
	if c == nil {
		return nil, errOBSOffline
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, errors.New("pick a source")
	}
	if scene == "" {
		scene = CurrentOBSScene()
		if scene == "" {
			return nil, errors.New("OBS hasn't said which scene is on yet")
		}
	}
	res := &obsSourceResult{}
	go func() {
		id, enabled, err := obsFindItem(c, scene, source)
		if err != nil {
			fmt.Println("obs:", err)
			return
		}
		value := true
		switch state {
		case "off":
			value = false
		case "toggle":
			value = !enabled
		}
		if _, err := c.call("SetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemEnabled": value}); err != nil {
			fmt.Println("obs:", err)
			return
		}
		res.mu.Lock()
		res.set, res.scene, res.id, res.value = true, scene, id, value
		res.mu.Unlock()
	}()
	return res, nil
}

// OBSSetSourceID shows or hides a source of the current scene by its item id,
// for the panel's switches.
func OBSSetSourceID(id int, on bool) error {
	c := currentOBSConn()
	if c == nil {
		return errOBSOffline
	}
	scene := CurrentOBSScene()
	go func() {
		if _, err := c.call("SetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemEnabled": on}); err != nil {
			fmt.Println("obs:", err)
		}
	}()
	return nil
}

// obsFindItem finds a source in a scene by name, ignoring case, and its
// visibility.
func obsFindItem(c *obsConn, scene, source string) (int, bool, error) {
	items, err := obsSceneItems(c, scene)
	if err != nil {
		return 0, false, err
	}
	for _, it := range items {
		if strings.EqualFold(it.Name, source) {
			return it.ID, it.Enabled, nil
		}
	}
	return 0, false, fmt.Errorf("no source %q in scene %q", source, scene)
}

// undoSource puts a source back when it still shows what res set.
func (res *obsSourceResult) undo() {
	res.mu.Lock()
	set, scene, id, value := res.set, res.scene, res.id, res.value
	res.mu.Unlock()
	c := currentOBSConn()
	if !set || c == nil {
		return
	}
	go func() {
		data, err := c.call("GetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id})
		if err != nil {
			return
		}
		var r struct {
			Enabled bool `json:"sceneItemEnabled"`
		}
		if json.Unmarshal(data, &r) != nil || r.Enabled != value {
			return
		}
		_, _ = c.call("SetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id, "sceneItemEnabled": !value})
	}()
}

// OBSCommand is a panel action: stream or record with start, stop, toggle,
// pause or resume; scene with a scene name; source with an item id and on;
// reconnect.
func OBSCommand(action, want, text string, on bool) error {
	switch action {
	case "stream", "record":
		return OBSOutput(action, want)
	case "scene":
		return OBSSetScene(text)
	case "source":
		id, err := strconv.Atoi(text)
		if err != nil {
			return errors.New("unknown source")
		}
		return OBSSetSourceID(id, on)
	case "reconnect":
		obsKick()
		return nil
	}
	return errors.New("unknown OBS action")
}
