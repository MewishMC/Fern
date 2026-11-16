package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"Fern/UI"
	"Fern/modules"

	"github.com/gorilla/websocket"
)

// apiToken keeps the secrets store in a temporary folder for the test and
// returns a fresh token.
func apiToken(t *testing.T) string {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	tok, err := modules.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, id := range modules.CustomLineIDs {
			_ = modules.SetCustomLineText(id, "", 0)
		}
	})
	return tok
}

func TestAPIGuard(t *testing.T) {
	tok := apiToken(t)
	const port = 1931
	req := func(mutate func(r *http.Request)) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1931/api/state", nil)
		r.RemoteAddr = "127.0.0.1:50000"
		r.Header.Set("Authorization", "Bearer "+tok)
		if mutate != nil {
			mutate(r)
		}
		return r
	}
	cases := []struct {
		name   string
		mutate func(r *http.Request)
		status int
	}{
		{"loopback with token", nil, 0},
		{"localhost host", func(r *http.Request) { r.Host = "localhost:1931" }, 0},
		{"header token", func(r *http.Request) { r.Header.Del("Authorization"); r.Header.Set("X-Fern-Token", tok) }, 0},
		{"query token", func(r *http.Request) { r.Header.Del("Authorization"); r.URL.RawQuery = "token=" + url.QueryEscape(tok) }, 0},
		{"other machine", func(r *http.Request) { r.RemoteAddr = "192.168.1.20:50000" }, http.StatusForbidden},
		{"rebound host", func(r *http.Request) { r.Host = "evil.example:1931" }, http.StatusForbidden},
		{"other port", func(r *http.Request) { r.Host = "127.0.0.1:1932" }, http.StatusForbidden},
		{"web page", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }, http.StatusForbidden},
		{"cross-site fetch", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok+"x") }, http.StatusUnauthorized},
	}
	for _, c := range cases {
		err := apiGuard(req(c.mutate), port)
		status := 0
		if ae, ok := err.(apiError); ok {
			status = ae.status
		} else if err != nil {
			status = -1
		}
		if status != c.status {
			t.Errorf("%s: status %d (%v), want %d", c.name, status, err, c.status)
		}
	}
}

// apiTestServer serves the API on a random loopback port.
func apiTestServer(t *testing.T) (base string, tok string) {
	t.Helper()
	tok = apiToken(t)
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = newAPIHandler(srv.Listener.Addr().(*net.TCPAddr).Port)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, tok
}

type apiReply struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Result json.RawMessage `json:"result"`
}

func callAPI(t *testing.T, method, target, contentType, body string, header map[string]string) (int, apiReply) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out apiReply
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("%s %s: bad JSON: %v", method, target, err)
	}
	return resp.StatusCode, out
}

func TestAPIEndpoints(t *testing.T) {
	base, tok := apiTestServer(t)
	auth := map[string]string{"Authorization": "Bearer " + tok}

	status, r := callAPI(t, http.MethodGet, base+"/api/state?token="+url.QueryEscape(tok), "", "", nil)
	if status != 200 || !r.OK {
		t.Fatalf("state: %d %+v", status, r)
	}
	var st apiStateOut
	if err := json.Unmarshal(r.Result, &st); err != nil || st.App != "fern" || st.Lines == nil || len(st.Custom) != modules.MaxCustomLines {
		t.Errorf("state %s (%v)", r.Result, err)
	}
	if strings.Contains(string(r.Result), tok) {
		t.Error("the API state leaked the token")
	}

	// JSON body.
	status, r = callAPI(t, http.MethodPost, base+"/api/custom", "application/json", `{"id":"custom2","text":"from json","seconds":0}`, auth)
	if status != 200 || !r.OK || modules.CustomLineText(modules.ModuleCustom2) != "from json" {
		t.Errorf("custom json: %d %+v", status, r)
	}
	// Form body, with the line found by its name.
	status, r = callAPI(t, http.MethodPost, base+"/api/custom", "application/x-www-form-urlencoded", "id=Custom+3&text=from+form", auth)
	if status != 200 || !r.OK || modules.CustomLineText(modules.ModuleCustom3) != "from form" {
		t.Errorf("custom form: %d %+v", status, r)
	}
	// A plain body is the text.
	status, r = callAPI(t, http.MethodPost, base+"/api/custom?id=custom1", "text/plain", "plain words", auth)
	if status != 200 || !r.OK || modules.CustomLineText(modules.ModuleCustom1) != "plain words" {
		t.Errorf("custom plain: %d %+v", status, r)
	}
	// Empty text clears it.
	if status, r = callAPI(t, http.MethodGet, base+"/api/custom?id=custom1&text=", "", "", auth); !r.OK || modules.CustomLineText(modules.ModuleCustom1) != "" {
		t.Errorf("custom clear: %d %+v", status, r)
	}

	for _, c := range []struct {
		path   string
		status int
	}{
		{"/api/nope", http.StatusNotFound},
		{"/apix", http.StatusNotFound},
		{"/api/send", http.StatusBadRequest},
		{"/api/custom?id=custom9&text=x", http.StatusBadRequest},
		{"/api/line?id=nope", http.StatusBadRequest},
		{"/api/line?id=time&on=maybe", http.StatusBadRequest},
		{"/api/rule?id=missing", http.StatusBadRequest},
		{"/api/action", http.StatusBadRequest},
		{"/api/action?type=no.such", http.StatusBadRequest},
		{"/api/event", http.StatusBadRequest},
		{"/api/event?name=brb&scene=BRB", http.StatusOK},
		{"/api", http.StatusOK},
		{"/api/actions", http.StatusOK},
		{"/api/send?seconds=-1&text=x", http.StatusBadRequest},
	} {
		if status, r := callAPI(t, http.MethodGet, base+c.path, "", "", auth); status != c.status || r.OK != (c.status == 200) {
			t.Errorf("%s: %d %+v, want %d", c.path, status, r, c.status)
		}
	}
	if status, _ := callAPI(t, http.MethodDelete, base+"/api/state", "", "", auth); status != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d", status)
	}
	if status, _ := callAPI(t, http.MethodGet, base+"/api/state", "", "", map[string]string{"Origin": base}); status != http.StatusForbidden {
		t.Errorf("a page served from the API's own origin still must not get in: %d", status)
	}
}

func TestAPIArgsDropTheToken(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/send?token=abc&text=q", strings.NewReader(`{"token":"def","n":5,"b":true,"o":{"a":1}}`))
	args, err := requestArgs(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := args["token"]; ok {
		t.Error("token kept as an argument")
	}
	if args["text"] != "q" || args["n"] != "5" || args["b"] != "true" || args["o"] != `{"a":1}` {
		t.Errorf("args %v", args)
	}
	big := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(strings.Repeat("x", apiMaxBody+10)))
	if _, err := requestArgs(big); err == nil {
		t.Error("oversized body accepted")
	}
}

func TestAPISocket(t *testing.T) {
	base, tok := apiTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/api/ws?token=" + url.QueryEscape(tok)

	if conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		conn.Close()
		t.Error("a web page opened the API socket")
	}
	if conn, _, err := websocket.DefaultDialer.Dial(strings.Split(wsURL, "?")[0], nil); err == nil {
		conn.Close()
		t.Error("opened the API socket without a token")
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var first map[string]json.RawMessage
	if err := conn.ReadJSON(&first); err != nil || string(first["type"]) != `"state"` {
		t.Fatalf("first message %v, %v", first, err)
	}
	if err := conn.WriteJSON(map[string]any{"action": "custom", "id": "custom3", "text": "over ws", "requestId": 7}); err != nil {
		t.Fatal(err)
	}
	for {
		var msg struct {
			Type      string          `json:"type"`
			RequestID json.RawMessage `json:"requestId"`
			OK        bool            `json:"ok"`
			Error     string          `json:"error"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "result" {
			continue
		}
		if !msg.OK || string(msg.RequestID) != "7" || modules.CustomLineText(modules.ModuleCustom3) != "over ws" {
			t.Errorf("reply %+v", msg)
		}
		break
	}
}

func TestPanelStateCarriesTheToken(t *testing.T) {
	tok := apiToken(t)
	if st := currentAPI(); st.Token != tok {
		t.Error("the API page can't show the token")
	}
	before := readNotice(&apiNotice).Seq
	handleAPICommand(command{Type: "api", Action: "regenerate"})
	if st := currentAPI(); st.Token == tok || st.Token == "" || st.Notice.Seq != before+1 || st.Notice.Bad {
		t.Errorf("regenerate: %+v", st.Notice)
	}
}

func TestClipboardShortcutPassesOthersOn(t *testing.T) {
	oldKey := UI.OnExtraHotkey
	t.Cleanup(func() { UI.OnExtraHotkey = oldKey })
	var got []string
	UI.OnExtraHotkey = func(id string) { got = append(got, id) }
	setupClipboard()
	UI.OnExtraHotkey(dndKeyID)
	if strings.Join(got, ",") != dndKeyID {
		t.Errorf("passed on %v", got)
	}
}
