package main

// The local control API: HTTP and WebSocket on 127.0.0.1, on a port of its
// own, for Stream Deck, Touch Portal, Streamer.bot, scripts and the like.
//
// Every request must carry the token shown on the API page, as
// "Authorization: Bearer <token>", an "X-Fern-Token" header or "?token=".
// Requests from other machines, with a Host other than this PC (DNS
// rebinding), or from a web page (any Origin header, or a cross-site fetch)
// are refused before the token is even looked at.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"Fern/modules"

	"github.com/gorilla/websocket"
)

const (
	apiMaxBody    = 64 << 10
	apiMaxClients = 8
)

// apiStatus is what the API page shows about the server.
type apiStatus struct {
	mu        sync.Mutex
	srv       *http.Server
	opts      modules.ControlAPIOptions
	listening bool
	err       string
	retryAt   time.Time
	clients   int
}

var (
	apiSrv      apiStatus
	apiCalls    atomic.Int64
	apiLastMu   sync.Mutex
	apiLastName string
	apiLastErr  string
	apiLastAt   int64
)

// apiPanelState is the API page's part of the panel state. The token is
// shown there so it can be copied; it never appears in the API's own state.
type apiPanelState struct {
	Enabled    bool        `json:"enabled"`
	Port       int         `json:"port"`
	Listening  bool        `json:"listening"`
	Error      string      `json:"error"`
	Token      string      `json:"token"`
	Calls      int64       `json:"calls"`
	Clients    int         `json:"clients"`
	LastAction string      `json:"lastAction"`
	LastError  string      `json:"lastError"`
	LastMS     int64       `json:"lastMs"`
	Notice     noticeState `json:"notice"`
}

var apiNotice noticeState

func currentAPI() apiPanelState {
	o := modules.GetConfig().ControlAPI
	apiSrv.mu.Lock()
	st := apiPanelState{Enabled: o.Enabled, Port: o.Port, Listening: apiSrv.listening, Error: apiSrv.err, Clients: apiSrv.clients}
	apiSrv.mu.Unlock()
	st.Token = modules.GetSecret(modules.SecretAPIToken)
	st.Calls = apiCalls.Load()
	apiLastMu.Lock()
	st.LastAction, st.LastError, st.LastMS = apiLastName, apiLastErr, apiLastAt
	apiLastMu.Unlock()
	st.Notice = readNotice(&apiNotice)
	return st
}

// handleAPICommand is the API page's buttons.
func handleAPICommand(cmd command) {
	switch cmd.Action {
	case "regenerate":
		if _, err := modules.NewAPIToken(); err != nil {
			setNotice(&apiNotice, "Couldn't make a new token: "+err.Error(), true)
			return
		}
		setNotice(&apiNotice, "New token made; update it in your tools", false)
	}
}

func setupControlAPI() {
	go watchControlAPI()
}

// watchControlAPI starts, stops and moves the server as its settings change,
// and keeps trying a port that was busy.
func watchControlAPI() {
	for {
		o := modules.GetConfig().ControlAPI
		apiSrv.mu.Lock()
		changed := o != apiSrv.opts
		retry := o.Enabled && !apiSrv.listening && !apiSrv.retryAt.IsZero() && time.Now().After(apiSrv.retryAt)
		apiSrv.mu.Unlock()
		if changed || retry {
			applyControlAPI(o)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func applyControlAPI(o modules.ControlAPIOptions) {
	apiSrv.mu.Lock()
	old := apiSrv.srv
	apiSrv.srv, apiSrv.opts, apiSrv.listening, apiSrv.err, apiSrv.retryAt = nil, o, false, "", time.Time{}
	apiSrv.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if !o.Enabled {
		return
	}
	if modules.APIToken() == "" {
		apiSrv.mu.Lock()
		apiSrv.err = "couldn't make a token"
		apiSrv.mu.Unlock()
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", o.Port))
	if err != nil {
		apiSrv.mu.Lock()
		apiSrv.err = fmt.Sprintf("port %d is in use by another app; pick another or close it", o.Port)
		apiSrv.retryAt = time.Now().Add(5 * time.Second)
		apiSrv.mu.Unlock()
		return
	}
	srv := &http.Server{Handler: newAPIHandler(o.Port), ReadHeaderTimeout: 5 * time.Second}
	apiSrv.mu.Lock()
	apiSrv.srv, apiSrv.listening = srv, true
	apiSrv.mu.Unlock()
	fmt.Printf("control API listening on http://127.0.0.1:%d/api\n", o.Port)
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			apiSrv.mu.Lock()
			if apiSrv.srv == srv {
				apiSrv.listening, apiSrv.err = false, err.Error()
			}
			apiSrv.mu.Unlock()
		}
	}()
}

// apiError is an error with the HTTP status it answers with.
type apiError struct {
	status int
	msg    string
}

func (e apiError) Error() string { return e.msg }

func badRequest(format string, a ...any) error {
	return apiError{http.StatusBadRequest, fmt.Sprintf(format, a...)}
}

// apiGuard refuses requests that don't come from a program on this PC with
// the token.
func apiGuard(r *http.Request, port int) error {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return apiError{http.StatusForbidden, "only programs on this PC may use the API"}
	}
	switch strings.ToLower(r.Host) {
	case fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("localhost:%d", port):
	default:
		return apiError{http.StatusForbidden, "use http://127.0.0.1:" + strconv.Itoa(port)}
	}
	// Browsers name the page that makes a request; tools don't. A web page
	// must never drive the chatbox.
	if r.Header.Get("Origin") != "" {
		return apiError{http.StatusForbidden, "requests from web pages are not allowed"}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
		return apiError{http.StatusForbidden, "requests from web pages are not allowed"}
	}
	if !modules.CheckAPIToken(requestToken(r)) {
		return apiError{http.StatusUnauthorized, "missing or wrong token; copy it from Fern's API page"}
	}
	return nil
}

func requestToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if t, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
	}
	if t := r.Header.Get("X-Fern-Token"); t != "" {
		return strings.TrimSpace(t)
	}
	return r.URL.Query().Get("token")
}

func newAPIHandler(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		name, ok := strings.CutPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api")
		if !ok || (name != "" && !strings.HasPrefix(name, "/")) {
			writeAPI(w, nil, apiError{http.StatusNotFound, "not found; the API is under /api"})
			return
		}
		name = strings.TrimPrefix(name, "/")
		if err := apiGuard(r, port); err != nil {
			writeAPI(w, nil, err)
			return
		}
		if name == "ws" {
			serveAPISocket(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeAPI(w, nil, apiError{http.StatusMethodNotAllowed, "use GET or POST"})
			return
		}
		args, err := requestArgs(r)
		if err != nil {
			writeAPI(w, nil, err)
			return
		}
		if name == "" {
			name = "help"
		}
		result, err := runAPI(name, args)
		writeAPI(w, result, err)
	})
}

func writeAPI(w http.ResponseWriter, result any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status, body := http.StatusOK, map[string]any{"ok": true}
	if err != nil {
		status = http.StatusBadRequest
		var ae apiError
		if errors.As(err, &ae) {
			status = ae.status
		}
		body = map[string]any{"ok": false, "error": err.Error()}
	} else if result != nil {
		body["result"] = result
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// requestArgs merges the query, a form body and a JSON object body into one
// set of text arguments. The token is never one of them.
func requestArgs(r *http.Request) (map[string]string, error) {
	args := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}
	if r.Method == http.MethodPost && r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, apiMaxBody+1))
		if err != nil {
			return nil, badRequest("couldn't read the request")
		}
		if len(data) > apiMaxBody {
			return nil, apiError{http.StatusRequestEntityTooLarge, "the request is too large"}
		}
		body := strings.TrimSpace(string(data))
		switch {
		case body == "":
		case strings.HasPrefix(body, "{"):
			if err := mergeJSONArgs(args, []byte(body)); err != nil {
				return nil, err
			}
		case strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded"):
			form, err := url.ParseQuery(body)
			if err != nil {
				return nil, badRequest("couldn't read the form")
			}
			for k, v := range form {
				if len(v) > 0 {
					args[k] = v[0]
				}
			}
		default:
			// A plain body is the message, for tools that can only post text.
			if _, ok := args["text"]; !ok {
				args["text"] = body
			}
		}
	}
	delete(args, "token")
	return args, nil
}

// mergeJSONArgs adds a JSON object's fields to args as text: strings as
// they are, numbers and booleans spelled out, objects and arrays as JSON.
func mergeJSONArgs(args map[string]string, data []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return badRequest("the body is not a JSON object")
	}
	for k, raw := range obj {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			args[k] = s
			continue
		}
		args[k] = strings.TrimSpace(string(raw))
	}
	return nil
}

// apiOnOff reads an on/off/toggle argument; empty means toggle.
func apiOnOff(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "toggle":
		return "toggle", nil
	case "on", "true", "1", "yes":
		return "on", nil
	case "off", "false", "0", "no":
		return "off", nil
	}
	return "", badRequest(`on must be "true", "false" or "toggle"`)
}

func apiSeconds(args map[string]string, def int) (time.Duration, error) {
	v := strings.TrimSpace(args["seconds"])
	if v == "" {
		return time.Duration(def) * time.Second, nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n < 0 || n > 86400 {
		return 0, badRequest("seconds must be a number from 0 to 86400")
	}
	return time.Duration(n * float64(time.Second)), nil
}

// apiActions are the calls the API answers, by name. Each takes the
// request's arguments.
var apiActions map[string]func(args map[string]string) (any, error)

func init() {
	apiActions = map[string]func(map[string]string) (any, error){
		"help":    func(map[string]string) (any, error) { return apiHelp(), nil },
		"state":   func(map[string]string) (any, error) { return apiState(), nil },
		"send":    apiSend,
		"clear":   func(map[string]string) (any, error) { modules.ClearHold(); return nil, nil },
		"typing":  apiTyping,
		"line":    apiLine,
		"custom":  apiCustom,
		"profile": apiProfile,
		"dnd":     apiDND,
		"display": apiDisplay,
		"rule":    apiRule,
		"action":  apiAction,
		"actions": func(map[string]string) (any, error) { return modules.RuleActions(), nil },
		"event":   apiEvent,
		"skip":    func(map[string]string) (any, error) { modules.SkipMessage(); return nil, nil },
		"clipboard": func(map[string]string) (any, error) {
			text, err := modules.SendClipboard()
			if err != nil {
				return nil, badRequest("%s", err.Error())
			}
			return map[string]string{"sent": text}, nil
		},
	}
}

// runAPI runs one call and notes it for the API page.
func runAPI(name string, args map[string]string) (any, error) {
	fn, ok := apiActions[name]
	if !ok {
		return nil, apiError{http.StatusNotFound, fmt.Sprintf("unknown call %q; GET /api lists them", name)}
	}
	result, err := fn(args)
	if name != "state" && name != "help" && name != "actions" {
		apiCalls.Add(1)
		apiLastMu.Lock()
		apiLastName, apiLastErr, apiLastAt = name, "", time.Now().UnixMilli()
		if err != nil {
			apiLastErr = err.Error()
		}
		apiLastMu.Unlock()
	}
	return result, err
}

func apiHelp() map[string]string {
	return map[string]string{
		"state":     "GET: what the chatbox shows, the lines, profiles, rules and switches",
		"send":      "text, seconds (default 10, 0 pins): send a chatbox message",
		"clear":     "remove the sent message",
		"typing":    "on: show or hide the typing bubble",
		"line":      "id (or name), on (true, false or toggle): switch a chatbox line",
		"custom":    "id (custom1-3), text, seconds (0 keeps it): set a custom line; empty text clears it",
		"profile":   "name: switch profile; empty name stops using one",
		"dnd":       "on (true, false or toggle): do not disturb",
		"display":   "on (true, false or toggle): the whole chatbox",
		"rule":      "id (or name): run a rule's actions now",
		"action":    "type (an action id from /api/actions), then its settings, or params as a JSON object: run one rule action",
		"actions":   "the rule actions and their settings",
		"event":     "name, plus anything else: fire the Control API call trigger for rules",
		"skip":      "show the next custom message",
		"clipboard": "send the clipboard's text, like the clipboard shortcut",
		"ws":        "WebSocket: state pushes, and {\"action\":\"send\",\"text\":\"hi\"} style calls",
	}
}

func apiSend(args map[string]string) (any, error) {
	text := strings.TrimSpace(args["text"])
	if text == "" {
		return nil, badRequest("text is empty")
	}
	hold, err := apiSeconds(args, 10)
	if err != nil {
		return nil, err
	}
	modules.SetHold(text, hold)
	modules.SpeakSent(text)
	modules.AddChatHistory(text)
	held, _, _ := modules.HoldState()
	return map[string]string{"sent": held}, nil
}

func apiTyping(args map[string]string) (any, error) {
	state, err := apiOnOff(args["on"])
	if err != nil || state == "toggle" {
		return nil, badRequest(`on must be "true" or "false"`)
	}
	go sendTyping(state == "on")
	return nil, nil
}

func apiLine(args map[string]string) (any, error) {
	id, ok := modules.FindLine(firstArg(args, "id", "line", "name"))
	if !ok {
		return nil, badRequest("unknown line; GET /api/state lists them")
	}
	state, err := apiOnOff(firstArg(args, "on", "state"))
	if err != nil {
		return nil, err
	}
	on, err := modules.SetLine(id, state)
	if err != nil {
		return nil, badRequest("%s", err.Error())
	}
	return map[string]any{"id": id, "on": on}, nil
}

func apiCustom(args map[string]string) (any, error) {
	id := firstArg(args, "id", "line")
	if id == "" {
		id = modules.ModuleCustom1
	}
	if resolved, ok := modules.FindLine(id); ok && modules.IsCustomLine(resolved) {
		id = resolved
	}
	hold, err := apiSeconds(args, 0)
	if err != nil {
		return nil, err
	}
	if err := modules.SetCustomLineText(id, args["text"], hold); err != nil {
		return nil, badRequest("%s", err.Error())
	}
	return map[string]string{"id": id, "text": modules.CustomLineText(id)}, nil
}

func apiProfile(args map[string]string) (any, error) {
	name := strings.TrimSpace(args["name"])
	var err error
	if name == "" {
		err = modules.DeactivateProfile()
	} else {
		err = modules.SwitchProfile(name)
	}
	if err != nil {
		return nil, badRequest("%s", err.Error())
	}
	return map[string]string{"active": modules.ActiveProfile()}, nil
}

func apiDND(args map[string]string) (any, error) {
	state, err := apiOnOff(args["on"])
	if err != nil {
		return nil, err
	}
	on := false
	if state == "toggle" {
		on = modules.ToggleDND()
	} else {
		on = state == "on"
		modules.SetDND(on)
	}
	return map[string]bool{"on": on}, nil
}

func apiDisplay(args map[string]string) (any, error) {
	state, err := apiOnOff(args["on"])
	if err != nil {
		return nil, err
	}
	on := false
	modules.UpdateConfig(func(c *modules.Config) {
		switch state {
		case "toggle":
			c.DisplayOptions.Show = !c.DisplayOptions.Show
		default:
			c.DisplayOptions.Show = state == "on"
		}
		on = c.DisplayOptions.Show
	})
	return map[string]bool{"on": on}, nil
}

func apiRule(args map[string]string) (any, error) {
	r, ok := modules.FindRule(firstArg(args, "id", "name"))
	if !ok {
		return nil, badRequest("no such rule")
	}
	if err := modules.TestRule(r.ID); err != nil {
		return nil, badRequest("%s", err.Error())
	}
	return map[string]string{"id": r.ID, "name": r.Name}, nil
}

func apiAction(args map[string]string) (any, error) {
	id := firstArg(args, "type", "id")
	if id == "" {
		return nil, badRequest("type is empty; GET /api/actions lists them")
	}
	params := modules.RuleParams{}
	if raw := strings.TrimSpace(args["params"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return nil, badRequest("params must be a JSON object")
		}
	} else {
		for k, v := range args {
			if k != "type" && k != "id" {
				params[k] = v
			}
		}
	}
	if err := modules.RunRuleAction(id, params, args); err != nil {
		return nil, badRequest("%s", err.Error())
	}
	return nil, nil
}

func apiEvent(args map[string]string) (any, error) {
	name := strings.TrimSpace(args["name"])
	if name == "" {
		return nil, badRequest("name is empty")
	}
	data := make(map[string]string, len(args))
	for k, v := range args {
		if len(data) < 32 {
			data[k] = modules.RuneLimit(v, 500)
		}
	}
	modules.FireTrigger(modules.TriggerAPI, data)
	return nil, nil
}

func firstArg(args map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(args[k]); v != "" {
			return v
		}
	}
	return ""
}

// apiStateOut is GET /api/state.
type apiStateOut struct {
	App      string          `json:"app"`
	Version  string          `json:"version"`
	Chatbox  string          `json:"chatbox"`
	Hold     *holdState      `json:"hold"`
	Display  bool            `json:"display"`
	DND      bool            `json:"dnd"`
	AFK      bool            `json:"afk"`
	InVR     bool            `json:"inVR"`
	Profile  string          `json:"profile"`
	Profiles []string        `json:"profiles"`
	Lines    []apiLineState  `json:"lines"`
	Custom   []apiCustomInfo `json:"custom"`
	Rules    []apiRuleInfo   `json:"rules"`
}

type apiLineState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	On   bool   `json:"on"`
}

type apiCustomInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

type apiRuleInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

func apiState() apiStateOut {
	cfg := modules.GetConfig()
	out := apiStateOut{
		App: "fern", Version: appVersion,
		Chatbox:  modules.ComposeChatbox().Text,
		Display:  cfg.DisplayOptions.Show,
		DND:      cfg.DND.On,
		InVR:     modules.InVR(),
		Profile:  modules.ActiveProfile(),
		Profiles: []string{},
		Lines:    []apiLineState{},
		Custom:   []apiCustomInfo{},
		Rules:    []apiRuleInfo{},
	}
	out.AFK, _ = modules.AfkInfo()
	if text, until, pinned := modules.HoldState(); text != "" {
		out.Hold = &holdState{Text: text, Until: until, Pinned: pinned}
	}
	for _, p := range modules.ListProfiles() {
		out.Profiles = append(out.Profiles, p.Name)
	}
	for _, id := range cfg.DisplayOptions.Order {
		out.Lines = append(out.Lines, apiLineState{ID: id, Name: modules.LineLabel(id), On: modules.ModuleEnabled(id, cfg.DisplayOptions)})
	}
	for i, s := range modules.CustomLineStatuses() {
		l := cfg.CustomLines.Line(i)
		out.Custom = append(out.Custom, apiCustomInfo{ID: s.ID, Name: l.Name, Source: l.Source, Text: s.Text})
	}
	for _, r := range cfg.Rules.List {
		out.Rules = append(out.Rules, apiRuleInfo{ID: r.ID, Name: r.Name, Enabled: r.Enabled})
	}
	return out
}

// apiUpgrader accepts only clients without an Origin, which apiGuard has
// already made sure of.
var apiUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}

// apiSocketReply answers one call over the WebSocket. A call is a JSON
// object with "action" and the call's arguments; an optional "requestId"
// comes back in the reply.
type apiSocketReply struct {
	Type      string          `json:"type"`
	RequestID json.RawMessage `json:"requestId,omitempty"`
	OK        bool            `json:"ok"`
	Error     string          `json:"error,omitempty"`
	Result    any             `json:"result,omitempty"`
}

func serveAPISocket(w http.ResponseWriter, r *http.Request) {
	apiSrv.mu.Lock()
	if apiSrv.clients >= apiMaxClients {
		apiSrv.mu.Unlock()
		writeAPI(w, nil, apiError{http.StatusServiceUnavailable, "too many WebSocket clients"})
		return
	}
	apiSrv.clients++
	apiSrv.mu.Unlock()
	defer func() {
		apiSrv.mu.Lock()
		apiSrv.clients--
		apiSrv.mu.Unlock()
	}()

	conn, err := apiUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(apiMaxBody)

	out := make(chan any, 16)
	done := make(chan struct{})
	defer close(done)
	go func() {
		var last string
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			state := apiState()
			if data, err := json.Marshal(state); err == nil && string(data) != last {
				last = string(data)
				select {
				case out <- map[string]any{"type": "state", "state": state}:
				default:
				}
			}
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	go func() {
		for {
			select {
			case <-done:
				return
			case msg := <-out:
				_ = conn.SetWriteDeadline(time.Now().Add(4 * time.Second))
				if conn.WriteJSON(msg) != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		args := map[string]string{}
		reply := apiSocketReply{Type: "result"}
		var head struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(data, &head)
		reply.RequestID = head.RequestID
		if err := mergeJSONArgs(args, data); err != nil {
			reply.Error = err.Error()
		} else {
			name := args["action"]
			delete(args, "action")
			delete(args, "requestId")
			delete(args, "token")
			if name == "" {
				reply.Error = "action is empty"
			} else if result, err := runAPI(name, args); err != nil {
				reply.Error = err.Error()
			} else {
				reply.OK, reply.Result = true, result
			}
		}
		select {
		case out <- reply:
		case <-time.After(2 * time.Second):
		}
	}
}
