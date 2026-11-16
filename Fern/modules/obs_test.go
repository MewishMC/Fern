package modules

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOBSAuth(t *testing.T) {
	// Worked out independently with Python's hashlib and base64.
	got := obsAuth("supersecretpassword", "lM1GncleQOaCu9lT1yeUZhFYnqhsLLP1G5lAGo3ixaI=", "+IxH4CnCiqpX1rM9scsNynZzbOe4KhDeYcTNS3PDaeY=")
	if want := "1Ct943GAT+6YQUUX47Ia/ncufilbe6+oD6lY+5kaCu4="; got != want {
		t.Errorf("obsAuth = %q, want %q", got, want)
	}
}

func TestOBSOptionsSanitize(t *testing.T) {
	def := DefaultConfig()
	if def.OBSOptions.Port != 4455 || def.OBSOptions.Host != "127.0.0.1" || def.OBSOptions.Enabled || def.DisplayOptions.OBS {
		t.Errorf("defaults: %+v", def.OBSOptions)
	}
	c := DefaultConfig()
	c.OBSOptions.Host = "  "
	c.OBSOptions.Port = 70000
	c.OBSOptions.LiveText = "  a\nvery    long live text that goes on and on  "
	c.OBSOptions.Separator = ""
	c.OBSOptions.SceneLabel = strings.Repeat("x", 40)
	c.sanitize()
	o := c.OBSOptions
	if o.Host != "127.0.0.1" || o.Port != 4455 || o.Separator != " · " {
		t.Errorf("bounds: %+v", o)
	}
	if o.LiveText != "a very long live text th" || len([]rune(o.SceneLabel)) != 16 {
		t.Errorf("texts: %q %q", o.LiveText, o.SceneLabel)
	}
	found := false
	for _, id := range c.DisplayOptions.Order {
		found = found || id == ModuleOBS
	}
	if !found || !ModuleEnabled(ModuleOBS, DisplayOptions{OBS: true}) || LineLabel(ModuleOBS) != "OBS" {
		t.Error("the OBS line is not wired into the order, toggles or labels")
	}
}

func TestOBSOutputTiming(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var o obsOutput
	if o.event(true, "OBS_WEBSOCKET_OUTPUT_STARTED", t0) != "started" {
		t.Fatal("start not reported")
	}
	if d := o.elapsed(t0.Add(65 * time.Second)); d != 65*time.Second {
		t.Errorf("running: %v", d)
	}
	o.event(true, "OBS_WEBSOCKET_OUTPUT_PAUSED", t0.Add(70*time.Second))
	if d := o.elapsed(t0.Add(500 * time.Second)); d != 70*time.Second || !o.paused {
		t.Errorf("paused: %v", d)
	}
	o.event(true, "OBS_WEBSOCKET_OUTPUT_RESUMED", t0.Add(100*time.Second))
	if d := o.elapsed(t0.Add(110 * time.Second)); d != 80*time.Second {
		t.Errorf("resumed: %v", d)
	}
	o.sync(true, false, 3_600_000, t0)
	if d := o.elapsed(t0.Add(time.Second)); d != time.Hour+time.Second {
		t.Errorf("synced: %v", d)
	}
	if o.event(false, "OBS_WEBSOCKET_OUTPUT_STOPPED", t0) != "stopped" || o.active || o.elapsed(t0) != 0 {
		t.Errorf("stopped: %+v", o)
	}
	if got := formatOBSDuration(3*time.Hour + 4*time.Minute + 5*time.Second); got != "3:04:05" {
		t.Errorf("hours: %s", got)
	}
	if got := formatOBSDuration(65 * time.Second); got != "1:05" {
		t.Errorf("minutes: %s", got)
	}
}

func TestOBSLine(t *testing.T) {
	now := time.Unix(5000, 0)
	obsNow = func() time.Time { return now }
	t.Cleanup(func() { obsNow = time.Now; clearOBS(); setOBSLink(linkOff, "") })
	cfg := DefaultConfig()

	setOBSLink(linkConnected, "")
	obsMu.Lock()
	obsScene = "Game"
	obsMu.Unlock()
	if got := obsLine(cfg); got != "" {
		t.Errorf("idle with only-active: %q", got)
	}
	cfg.OBSOptions.OnlyActive = false
	cfg.OBSOptions.ShowScene = true
	if got := obsLine(cfg); got != "🎬 Game" {
		t.Errorf("idle scene: %q", got)
	}

	obsMu.Lock()
	obsStream = obsOutput{active: true, base: 754 * time.Second, at: now}
	obsRecord = obsOutput{active: true, paused: true, base: 9 * time.Second, at: now}
	obsMu.Unlock()
	if got := obsLine(cfg); got != "🔴 LIVE 12:34 · ⏸️ REC paused 0:09 · 🎬 Game" {
		t.Errorf("live: %q", got)
	}
	cfg.OBSOptions.ShowDuration = false
	cfg.OBSOptions.ShowRecord = false
	if got := obsLine(cfg); got != "🔴 LIVE · 🎬 Game" {
		t.Errorf("no durations: %q", got)
	}

	setOBSLink(linkWaiting, "OBS isn't running")
	if got := obsLine(cfg); got != "" {
		t.Errorf("disconnected: %q", got)
	}
}

func TestOBSWanted(t *testing.T) {
	c := DefaultConfig()
	if obsWanted(c) {
		t.Error("wanted by default")
	}
	c.Rules.List = []Rule{{ID: "r1", Enabled: true, Trigger: RuleStep{Type: TriggerStarted}, Actions: []RuleStep{{Type: ActionOBSScene}}}}
	if !obsWanted(c) {
		t.Error("a rule with an OBS action should connect")
	}
	c.Rules.Enabled = false
	if obsWanted(c) {
		t.Error("rules are off")
	}
	c.DisplayOptions.OBS = true
	if !obsWanted(c) {
		t.Error("the line should connect")
	}
}

// fakeOBS is a minimal obs-websocket v5 server.
type fakeOBS struct {
	t        *testing.T
	password string
	reqs     chan map[string]any
	mu       sync.Mutex
	conn     *websocket.Conn
	scene    string
	items    map[int]bool
}

func (f *fakeOBS) emit(typ string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_ = f.conn.WriteJSON(map[string]any{"op": 5, "d": map[string]any{"eventType": typ, "eventIntent": 1, "eventData": data}})
}

func (f *fakeOBS) reply(typ, id string, ok bool, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := map[string]any{"requestType": typ, "requestId": id, "requestStatus": map[string]any{"result": ok, "code": map[bool]int{true: 100, false: 600}[ok]}}
	if data != nil {
		d["responseData"] = data
	}
	_ = f.conn.WriteJSON(map[string]any{"op": 7, "d": d})
}

func (f *fakeOBS) serve(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	salt, challenge := "c2FsdA==", "Y2hhbGxlbmdl"
	hello := map[string]any{"obsWebSocketVersion": "5.5.0", "rpcVersion": 1}
	if f.password != "" {
		hello["authentication"] = map[string]any{"salt": salt, "challenge": challenge}
	}
	_ = conn.WriteJSON(map[string]any{"op": 0, "d": hello})
	var id struct {
		Op int `json:"op"`
		D  struct {
			RPC  int    `json:"rpcVersion"`
			Auth string `json:"authentication"`
			Subs int    `json:"eventSubscriptions"`
		} `json:"d"`
	}
	if conn.ReadJSON(&id) != nil || id.Op != 1 {
		return
	}
	if f.password != "" && id.D.Auth != obsAuth(f.password, salt, challenge) {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4009, "Authentication failed."))
		return
	}
	if id.D.Subs&(1<<6) == 0 || id.D.Subs&(1<<2) == 0 {
		f.t.Errorf("not subscribed to outputs and scenes: %d", id.D.Subs)
	}
	f.mu.Lock()
	_ = conn.WriteJSON(map[string]any{"op": 2, "d": map[string]any{"negotiatedRpcVersion": 1}})
	f.mu.Unlock()
	for {
		var m struct {
			Op int `json:"op"`
			D  struct {
				Type string         `json:"requestType"`
				ID   string         `json:"requestId"`
				Data map[string]any `json:"requestData"`
			} `json:"d"`
		}
		if conn.ReadJSON(&m) != nil {
			return
		}
		f.reqs <- map[string]any{"type": m.D.Type, "data": m.D.Data}
		switch m.D.Type {
		case "GetStreamStatus", "GetRecordStatus":
			f.reply(m.D.Type, m.D.ID, true, map[string]any{"outputActive": false, "outputDuration": 0})
		case "GetSceneList":
			f.reply(m.D.Type, m.D.ID, true, map[string]any{"currentProgramSceneName": f.scene, "scenes": []map[string]any{
				{"sceneName": "BRB", "sceneIndex": 0}, {"sceneName": "Game", "sceneIndex": 1},
			}})
		case "GetSceneItemList":
			f.mu.Lock()
			items := []map[string]any{{"sceneItemId": 7, "sourceName": "Webcam", "sceneItemEnabled": f.items[7], "sceneItemIndex": 1}}
			f.mu.Unlock()
			f.reply(m.D.Type, m.D.ID, true, map[string]any{"sceneItems": items})
		case "GetSceneItemEnabled":
			f.mu.Lock()
			on := f.items[7]
			f.mu.Unlock()
			f.reply(m.D.Type, m.D.ID, true, map[string]any{"sceneItemEnabled": on})
		case "SetSceneItemEnabled":
			on, _ := m.D.Data["sceneItemEnabled"].(bool)
			f.mu.Lock()
			f.items[7] = on
			f.mu.Unlock()
			f.reply(m.D.Type, m.D.ID, true, nil)
			f.emit("SceneItemEnableStateChanged", map[string]any{"sceneName": f.scene, "sceneItemId": 7, "sceneItemEnabled": on})
		case "SetCurrentProgramScene":
			f.scene, _ = m.D.Data["sceneName"].(string)
			f.reply(m.D.Type, m.D.ID, true, nil)
			f.emit("CurrentProgramSceneChanged", map[string]any{"sceneName": f.scene})
		case "StartStream":
			f.reply(m.D.Type, m.D.ID, true, nil)
			f.emit("StreamStateChanged", map[string]any{"outputActive": true, "outputState": "OBS_WEBSOCKET_OUTPUT_STARTED"})
		default:
			f.reply(m.D.Type, m.D.ID, false, nil)
		}
	}
}

func (f *fakeOBS) waitReq(t *testing.T, typ string) map[string]any {
	t.Helper()
	deadline := time.After(4 * time.Second)
	for {
		select {
		case r := <-f.reqs:
			if r["type"] == typ {
				d, _ := r["data"].(map[string]any)
				return d
			}
		case <-deadline:
			t.Fatalf("no %s request", typ)
			return nil
		}
	}
}

func TestOBSSession(t *testing.T) {
	fake := &fakeOBS{t: t, password: "hunter2", reqs: make(chan map[string]any, 256), scene: "Game", items: map[int]bool{7: true}}
	srv := httptest.NewServer(http.HandlerFunc(fake.serve))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	var fired sync.Map
	obsFire = func(id string, data map[string]string) { fired.Store(id+":"+data["scene"]+data["state"], true) }
	t.Cleanup(func() { obsFire = FireTrigger; clearOBS(); setOBSLink(linkOff, "") })

	// A wrong password is reported, not retried silently.
	conn := dialOBS(url)
	if conn == nil {
		t.Fatal("could not dial the fake")
	}
	err := obsSession(conn, "wrong", func() bool { return true })
	conn.Close()
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("wrong password: %v", err)
	}
	conn = dialOBS(url)
	err = obsSession(conn, "", func() bool { return true })
	conn.Close()
	if err == nil || !strings.Contains(err.Error(), "asks for a password") {
		t.Fatalf("no password: %v", err)
	}

	conn = dialOBS(url)
	var wanted atomic.Bool
	wanted.Store(true)
	done := make(chan error, 1)
	go func() { done <- obsSession(conn, "hunter2", wanted.Load) }()
	waitFor(t, "scene list", func() bool {
		s := CurrentOBS()
		return s.Link.State == linkConnected && len(s.Scenes) == 2 && len(s.Items) == 1
	})
	if s := CurrentOBS(); s.Scenes[0] != "Game" || s.Scene != "Game" || s.Version != "5.5.0" || s.Items[0].Name != "Webcam" {
		t.Errorf("after connect: %+v", s)
	}

	if err := OBSOutput("stream", "start"); err != nil {
		t.Fatal(err)
	}
	fake.waitReq(t, "StartStream")
	waitFor(t, "streaming", func() bool { return CurrentOBS().Streaming })
	if _, ok := fired.Load(TriggerOBSOutput + ":started"); !ok {
		t.Error("no output trigger fired")
	}

	if err := OBSSetScene("BRB"); err != nil {
		t.Fatal(err)
	}
	if d := fake.waitReq(t, "SetCurrentProgramScene"); d["sceneName"] != "BRB" {
		t.Errorf("scene request: %v", d)
	}
	waitFor(t, "scene", func() bool { return CurrentOBSScene() == "BRB" })
	if _, ok := fired.Load(TriggerOBSSceneChanged + ":BRB"); !ok {
		t.Error("no scene trigger fired")
	}

	res, err := OBSSetSource("", "webcam", "toggle")
	if err != nil {
		t.Fatal(err)
	}
	if d := fake.waitReq(t, "SetSceneItemEnabled"); d["sceneItemEnabled"] != false || d["sceneName"] != "BRB" || d["sceneItemId"] != float64(7) {
		t.Errorf("source request: %v", d)
	}
	waitFor(t, "source result", func() bool { res.mu.Lock(); defer res.mu.Unlock(); return res.set })
	res.undo()
	if d := fake.waitReq(t, "SetSceneItemEnabled"); d["sceneItemEnabled"] != true {
		t.Errorf("undo request: %v", d)
	}

	if _, err := OBSSetSource("", "", "on"); err == nil {
		t.Error("an empty source was accepted")
	}
	if err := OBSOutput("stream", "pause"); err == nil {
		t.Error("the stream was paused")
	}

	wanted.Store(false)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("session end: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("session did not stop when switched off")
	}
	conn.Close()
	if err := OBSSetScene("Game"); err != errOBSOffline {
		t.Errorf("after the session: %v", err)
	}
}

func TestOBSRulesRegistered(t *testing.T) {
	want := map[string]bool{ActionOBSStream: true, ActionOBSRecord: true, ActionOBSScene: true, ActionOBSSource: true}
	for _, a := range RuleActions() {
		delete(want, a.ID)
	}
	for _, tr := range RuleTriggers() {
		if tr.ID == TriggerOBSOutput {
			ev := RuleEvent{Type: tr.ID, Data: map[string]string{"output": "record", "state": "started"}}
			p := SanitizeRuleParams(tr.Fields, RuleParams{"output": "either", "state": "started"})
			if !tr.Match(p, ev) {
				t.Error("either did not match a recording")
			}
			p = SanitizeRuleParams(tr.Fields, RuleParams{"output": "stream"})
			if tr.Match(p, ev) {
				t.Error("stream matched a recording")
			}
		}
	}
	if len(want) > 0 {
		t.Errorf("missing actions: %v", want)
	}
	if _, err := json.Marshal(RuleSources()); err != nil {
		t.Error(err)
	}
}
