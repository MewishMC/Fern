package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hypebeast/go-osc/osc"
)

// Endpoints, variables so tests can point them at a local fake server.
var (
	// https://docs.pulsoid.net/heart-rate-and-sensor-data/read-heart-rate-via-websocket
	pulsoidWSURL = "wss://dev.pulsoid.net/api/v1/data/real_time"
	// https://docs.pulsoid.net/heart-rate-and-sensor-data/read-latest-heart-rate-via-http
	pulsoidRESTURL = "https://dev.pulsoid.net/api/v1/data/heart_rate/latest"
	// https://github.com/HypeRate/DevDocs (Phoenix channels)
	hypeRateWSURL = "wss://app.hyperate.io/socket/websocket"

	pulsoidRESTFor   = 2 * time.Minute  // how long to poll before trying the socket again
	hypeRateBeatTime = 10 * time.Second // Phoenix closes sockets silent for longer
	wsPingEvery      = 25 * time.Second
	wsReadTimeout    = 75 * time.Second
	maxBackoff       = 60 * time.Second
)

const maxHeartBody = 64 << 10

// hrGen identifies the current connection. A cancelled connection may still be
// finishing a read; anything it reports after a newer one started is ignored.
var hrGen atomic.Int64

type heartRun struct{ gen int64 }

func (r heartRun) current() bool { return hrGen.Load() == r.gen }

func (r heartRun) state(state, message string) {
	if r.current() {
		setHeartState(state, message)
	}
}

func (r heartRun) via(via string) {
	if r.current() {
		setHeartVia(via)
	}
}

func (r heartRun) sample(bpm float64) {
	if r.current() {
		addHeartSample(bpm, time.Now())
	}
}

func startHeartWorker() {
	go heartManager()
	go heartAvatarLoop()
}

// heartKey changes whenever the connection has to be remade. It only lives in
// memory and is never printed.
func heartKey(o HeartOptions) string {
	switch o.Source {
	case HeartPulsoid:
		return o.Source + "\x00" + o.PulsoidToken
	case HeartHypeRate:
		return o.Source + "\x00" + o.HypeRateKey + "\x00" + o.HypeRateID
	}
	return o.Source + "\x00" + o.CustomURL + "\x00" + o.CustomPath + "\x00" + strconv.Itoa(o.PollMS)
}

// heartManager keeps one connection running while the line or the avatar
// output wants it, and remakes it when the source settings change.
func heartManager() {
	var cancel context.CancelFunc
	current := ""
	for {
		cfg := GetConfig()
		o := cfg.HeartRate
		wanted := cfg.DisplayOptions.HeartRate || o.SendOSC
		key := ""
		if wanted && heartConfigured(o) {
			key = heartKey(o)
		}
		if key != current {
			if cancel != nil {
				cancel()
				cancel = nil
			}
			gen := hrGen.Add(1)
			if current != "" && key != "" {
				clearHeartSamples()
			}
			current = key
			setHeartVia("")
			if key != "" {
				var ctx context.Context
				ctx, cancel = context.WithCancel(context.Background())
				go runHeartSource(ctx, heartRun{gen: gen}, o)
			}
		}
		if key == "" {
			if wanted {
				setHeartState(HRStatusSetup, setupHint(o))
			} else {
				setHeartState(HRStatusOff, "")
			}
		}
		time.Sleep(time.Second)
	}
}

func setupHint(o HeartOptions) string {
	switch o.Source {
	case HeartPulsoid:
		return "Paste a Pulsoid token to connect"
	case HeartHypeRate:
		return "Enter a HypeRate API key and session ID to connect"
	}
	return "Enter a URL to connect"
}

// heartError is a failure with a message that is safe to show.
type heartError struct {
	msg  string
	auth bool // the credentials were refused; retrying soon will not help
}

func (e heartError) Error() string { return e.msg }

// runHeartSource reconnects with exponential backoff until ctx is cancelled.
func runHeartSource(ctx context.Context, run heartRun, o HeartOptions) {
	backoff := time.Second
	for ctx.Err() == nil {
		run.state(HRStatusConnecting, "")
		start := time.Now()
		err := heartOnce(ctx, run, o)
		if ctx.Err() != nil {
			return
		}
		// A connection that lasted a while was healthy; start the backoff over.
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		wait := backoff
		var he heartError
		if errors.As(err, &he) && he.auth && wait < 30*time.Second {
			wait = 30 * time.Second
		}
		msg := cleanHeartError(err, heartSecrets(o))
		run.state(HRStatusError, fmt.Sprintf("%s · retrying in %ds", msg, int(wait.Seconds())))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func heartSecrets(o HeartOptions) []string {
	return []string{o.PulsoidToken, o.HypeRateKey, o.CustomURL}
}

func heartOnce(ctx context.Context, run heartRun, o HeartOptions) error {
	switch o.Source {
	case HeartPulsoid:
		return pulsoidOnce(ctx, run, o)
	case HeartHypeRate:
		return hypeRateOnce(ctx, run, o)
	}
	return customOnce(ctx, run, o)
}

func pulsoidOnce(ctx context.Context, run heartRun, o HeartOptions) error {
	header := http.Header{"Authorization": {"Bearer " + o.PulsoidToken}}
	parse := func(data []byte) (float64, bool, error) {
		v, err := extractBPM(data, "data.heart_rate")
		return v, err == nil, nil
	}
	err := wsSession(ctx, run, pulsoidWSURL, header, nil, nil, parse)
	var he heartError
	if ctx.Err() != nil || (errors.As(err, &he) && he.auth) {
		return err
	}
	var dial wsDialError
	if !errors.As(err, &dial) {
		return err // the socket worked and then dropped; reconnect it
	}
	// The socket could not be opened: read the REST endpoint for a while.
	pollCtx, cancel := context.WithTimeout(ctx, pulsoidRESTFor)
	defer cancel()
	perr := pollSession(pollCtx, run, pulsoidRESTURL, header, time.Second, func(status int, body []byte) (float64, bool, error) {
		if status == http.StatusPreconditionFailed {
			return 0, false, nil // no heart rate recorded yet
		}
		if status != http.StatusOK {
			return 0, false, statusError(status, "Pulsoid")
		}
		v, err := extractBPM(body, "data.heart_rate")
		return v, err == nil, nil
	})
	if ctx.Err() == nil && pollCtx.Err() != nil {
		return heartError{msg: "trying the Pulsoid socket again"}
	}
	return perr
}

type phxMessage struct {
	Topic   string          `json:"topic"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
	Ref     any             `json:"ref"`
}

func hypeRateOnce(ctx context.Context, run heartRun, o HeartOptions) error {
	topic := "hr:" + o.HypeRateID
	target := hypeRateWSURL + "?token=" + url.QueryEscape(o.HypeRateKey)
	ref := 0
	phx := func(topic, event string) []byte {
		ref++
		data, _ := json.Marshal(phxMessage{Topic: topic, Event: event, Payload: json.RawMessage("{}"), Ref: ref})
		return data
	}
	open := func(write func([]byte) error) error { return write(phx(topic, "phx_join")) }
	keep := func(write func([]byte) error) error { return write(phx("phoenix", "heartbeat")) }
	parse := func(data []byte) (float64, bool, error) {
		var m phxMessage
		if json.Unmarshal(data, &m) != nil {
			return 0, false, nil
		}
		switch m.Event {
		case "hr_update":
			v, err := extractBPM(m.Payload, "hr")
			return v, err == nil, nil
		case "phx_reply":
			var reply struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(m.Payload, &reply)
			if m.Topic == topic && reply.Status == "error" {
				return 0, false, heartError{msg: "HypeRate refused that session ID", auth: true}
			}
		case "phx_error", "phx_close":
			if m.Topic == topic {
				return 0, false, heartError{msg: "HypeRate closed the channel"}
			}
		}
		return 0, false, nil
	}
	return wsSession(ctx, run, target, nil, open, keep, parse)
}

func customOnce(ctx context.Context, run heartRun, o HeartOptions) error {
	u, err := url.Parse(o.CustomURL)
	if err != nil || u.Host == "" {
		return heartError{msg: "that URL is not valid", auth: true}
	}
	switch strings.ToLower(u.Scheme) {
	case "ws", "wss":
		return wsSession(ctx, run, o.CustomURL, nil, nil, nil, func(data []byte) (float64, bool, error) {
			v, err := extractBPM(data, o.CustomPath)
			return v, err == nil, nil
		})
	case "http", "https":
		interval := time.Duration(o.PollMS) * time.Millisecond
		return pollSession(ctx, run, o.CustomURL, nil, interval, func(status int, body []byte) (float64, bool, error) {
			if status != http.StatusOK {
				return 0, false, statusError(status, "the server")
			}
			v, err := extractBPM(body, o.CustomPath)
			if err != nil {
				return 0, false, heartError{msg: err.Error()}
			}
			return v, true, nil
		})
	}
	return heartError{msg: "the URL must start with ws://, wss://, http:// or https://", auth: true}
}

func statusError(status int, who string) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return heartError{msg: fmt.Sprintf("%s refused the credentials (HTTP %d)", who, status), auth: true}
	}
	return heartError{msg: fmt.Sprintf("%s answered HTTP %d", who, status)}
}

// wsDialError marks a socket that never opened, as opposed to one that dropped.
type wsDialError struct{ err error }

func (e wsDialError) Error() string { return e.err.Error() }
func (e wsDialError) Unwrap() error { return e.err }

// wsSession reads one websocket until it fails or ctx is cancelled. open runs
// once after connecting, keep every hypeRateBeatTime; parse turns a message
// into a reading. Reads and writes never block the chatbox: they run on this
// goroutine, and the panel only sees the shared sample history.
func wsSession(ctx context.Context, run heartRun, target string, header http.Header,
	open, keep func(write func([]byte) error) error,
	parse func([]byte) (float64, bool, error)) error {

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}
	conn, resp, err := dialer.DialContext(ctx, target, header)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
			return wsDialError{statusError(resp.StatusCode, "the server")}
		}
		return wsDialError{err}
	}
	defer conn.Close()
	conn.SetReadLimit(maxHeartBody)
	run.via("websocket")
	run.state(HRStatusConnected, "")

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	// gorilla allows one writer at a time, so every write goes through this
	// goroutine; a full queue fails the write instead of blocking the reader.
	writes := make(chan []byte, 4)
	write := func(data []byte) error {
		select {
		case writes <- data:
			return nil
		default:
			return heartError{msg: "the connection is not keeping up"}
		}
	}
	go func() {
		ping := time.NewTicker(wsPingEvery)
		defer ping.Stop()
		var beat <-chan time.Time
		if keep != nil {
			t := time.NewTicker(hypeRateBeatTime)
			defer t.Stop()
			beat = t.C
		}
		for {
			select {
			case <-stop:
				return
			case data := <-writes:
				_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if conn.WriteMessage(websocket.TextMessage, data) != nil {
					_ = conn.Close()
					return
				}
			case <-beat:
				_ = keep(write)
			case <-ping.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()
	if open != nil {
		if err := open(write); err != nil {
			return err
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				if ce.Code == 4001 || ce.Code == 4003 || ce.Code == 1008 {
					return heartError{msg: "the server refused the credentials", auth: true}
				}
				return heartError{msg: fmt.Sprintf("the server closed the connection (%d)", ce.Code)}
			}
			return err
		}
		_ = conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		bpm, ok, err := parse(data)
		if err != nil {
			return err
		}
		if ok {
			run.sample(bpm)
		}
	}
}

// pollSession reads url every interval until a request fails or ctx ends.
func pollSession(ctx context.Context, run heartRun, target string, header http.Header, interval time.Duration,
	parse func(status int, body []byte) (float64, bool, error)) error {

	client := &http.Client{Timeout: 10 * time.Second}
	first := true
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return heartError{msg: "that URL is not valid", auth: true}
		}
		for k, v := range header {
			req.Header[k] = v
		}
		req.Header.Set("Accept", "application/json, text/plain")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHeartBody))
		_ = resp.Body.Close()
		bpm, ok, err := parse(resp.StatusCode, body)
		if err != nil {
			return err
		}
		if first {
			first = false
			run.via("http")
			run.state(HRStatusConnected, "")
		}
		if ok {
			run.sample(bpm)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

var (
	queryPart  = regexp.MustCompile(`\?[^\s"']*`)
	bearerPart = regexp.MustCompile(`(?i)(bearer|token|key)[=: ]+[^\s"'&]+`)
)

// cleanHeartError turns an error into a short message for the panel without
// the token, the key, or a URL's query string.
func cleanHeartError(err error, secrets []string) string {
	if err == nil {
		return "the connection ended"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var he heartError
	var dnsErr *net.DNSError
	var opErr *net.OpError
	msg := err.Error()
	switch {
	case errors.As(err, &he):
		msg = he.msg
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "Client.Timeout"):
		msg = "timed out"
	case errors.Is(err, websocket.ErrBadHandshake):
		msg = "the server did not accept a websocket"
	case errors.As(err, &dnsErr):
		msg = "could not find " + dnsErr.Name
	case errors.As(err, &opErr) && opErr.Op == "dial" && opErr.Addr != nil:
		msg = "could not reach " + opErr.Addr.String()
	}
	for _, s := range secrets {
		if len(s) >= 4 {
			msg = strings.ReplaceAll(msg, s, "•••")
			if esc := url.QueryEscape(s); esc != s {
				msg = strings.ReplaceAll(msg, esc, "•••")
			}
		}
	}
	msg = queryPart.ReplaceAllString(msg, "")
	msg = bearerPart.ReplaceAllString(msg, "$1 •••")
	return oneLine(msg, 120)
}

var bracketIndex = regexp.MustCompile(`\[(\d+)\]`)

// guessKeys are tried, at the top level and under "data", when no JSON path is set.
var guessKeys = []string{"heart_rate", "heartRate", "heartrate", "hr", "bpm", "HeartRate", "value"}

// extractBPM reads a heart rate out of a message: a bare number, or a JSON
// document with the number at path (dots between keys, [n] or .n for array
// items). An empty path tries the usual field names.
func extractBPM(data []byte, path string) (float64, error) {
	text := strings.TrimSpace(string(data))
	if v, err := strconv.ParseFloat(text, 64); err == nil {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return 0, errors.New("the reply is not JSON or a number")
	}
	if path == "" {
		for _, base := range []string{"", "data."} {
			for _, k := range guessKeys {
				if v, err := jsonPathNumber(doc, base+k); err == nil {
					return v, nil
				}
			}
		}
		if v, ok := asNumber(doc); ok {
			return v, nil
		}
		return 0, errors.New("no heart rate field found; set the JSON field")
	}
	return jsonPathNumber(doc, path)
}

func jsonPathNumber(doc any, path string) (float64, error) {
	path = bracketIndex.ReplaceAllString(strings.TrimPrefix(path, "$."), ".$1")
	cur := doc
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			continue
		}
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				for k, v := range node {
					if strings.EqualFold(k, part) {
						next, ok = v, true
						break
					}
				}
			}
			if !ok {
				return 0, fmt.Errorf("no field %q in the reply", part)
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return 0, fmt.Errorf("no item %q in the reply", part)
			}
			cur = node[i]
		default:
			return 0, fmt.Errorf("no field %q in the reply", part)
		}
	}
	if v, ok := asNumber(cur); ok {
		return v, nil
	}
	return 0, fmt.Errorf("%q is not a number", path)
}

func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// avatarSend delivers one avatar parameter with the same OSC library and
// target the chatbox uses. A variable so tests can capture the messages.
var avatarSend = func(o OSCOptions, msg *osc.Message) error {
	return osc.NewClient(o.Host, o.Port).Send(msg)
}

// heartFloat maps 0-255 bpm onto -1..1 (signed, the HeartRateFloat convention
// of the common VRChat heart rate prefabs) or 0..1 (unsigned, HRPercent style).
func heartFloat(bpm int, signed bool) float32 {
	v := float64(bpm) / maxBPM
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	if signed {
		v = v*2 - 1
	}
	return float32(v)
}

func sendParam(o OSCOptions, name string, value any) {
	if name == "" {
		return
	}
	_ = avatarSend(o, osc.NewMessage("/avatar/parameters/"+name, value))
}

// heartAvatarLoop drives the avatar parameters on its own goroutine: the
// value once a second, the connected flag on change and every five seconds,
// and the beat parameter at the heart's own pace.
func heartAvatarLoop() {
	var (
		nextValue, nextConn, nextBeat, beatOff time.Time
		toggle, connSent, connKnown            bool
	)
	for {
		now := time.Now()
		cfg := GetConfig()
		o := cfg.HeartRate
		if !o.SendOSC {
			if connKnown && connSent && o.ParamConnected != "" {
				sendParam(cfg.OSCOptions, o.ParamConnected, false)
			}
			connKnown = false
			time.Sleep(500 * time.Millisecond)
			continue
		}
		samples, last := heartSnapshot()
		r := readHeart(o, samples, last, now)
		if !now.Before(nextValue) {
			nextValue = now.Add(time.Second)
			if r.ok {
				sendParam(cfg.OSCOptions, o.ParamInt, int32(r.bpm))
				sendParam(cfg.OSCOptions, o.ParamFloat, heartFloat(r.bpm, o.FloatRange == "signed"))
			}
		}
		if !now.Before(nextConn) || !connKnown || connSent != r.ok {
			nextConn = now.Add(5 * time.Second)
			connSent, connKnown = r.ok, true
			sendParam(cfg.OSCOptions, o.ParamConnected, r.ok)
		}
		if !beatOff.IsZero() && !now.Before(beatOff) {
			beatOff = time.Time{}
			sendParam(cfg.OSCOptions, o.ParamBeat, false)
		}
		if r.ok && r.bpm >= 30 && o.ParamBeat != "" {
			period := time.Minute / time.Duration(r.bpm)
			if nextBeat.IsZero() || nextBeat.Sub(now) > period {
				nextBeat = now
			}
			if !now.Before(nextBeat) {
				nextBeat = now.Add(period)
				if o.BeatMode == "pulse" {
					sendParam(cfg.OSCOptions, o.ParamBeat, true)
					beatOff = now.Add(min(150*time.Millisecond, period/3))
				} else {
					toggle = !toggle
					sendParam(cfg.OSCOptions, o.ParamBeat, toggle)
				}
			}
		} else {
			nextBeat = time.Time{}
		}
		wake := nextValue
		for _, t := range []time.Time{nextConn, nextBeat, beatOff} {
			if !t.IsZero() && t.Before(wake) {
				wake = t
			}
		}
		wait := time.Until(wake)
		if wait < 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		if wait > 250*time.Millisecond {
			wait = 250 * time.Millisecond // notice settings changes quickly
		}
		time.Sleep(wait)
	}
}
