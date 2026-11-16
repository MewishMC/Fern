package modules

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// OSCFixedPort is where VRChat sends OSC when nobody asks it otherwise.
const OSCFixedPort = 9001

// How the listener recovers: a busy 9001 is tried again every few seconds,
// and when OSCQuery failed and 9001 stands in, OSCQuery is tried again now
// and then.
var (
	oscFixedRetry = 5 * time.Second
	oscQueryRetry = time.Minute
	// oscFindWait is how long VRChat may run without finding Fern over
	// OSCQuery (no query, no OSC) before Fern falls back to 9001. A firewall
	// that drops multicast does that silently.
	oscFindWait = time.Minute
	// oscMDNSPort is the mDNS port, changed by tests.
	oscMDNSPort = mdnsPort
)

// OSCInStatus is how Fern hears VRChat, as the panel shows it.
type OSCInStatus struct {
	OSCQuery  bool   `json:"oscquery"`  // on a free port, advertised over OSCQuery
	Port      int    `json:"port"`      // UDP port Fern listens on, 0 while it cannot
	QueryPort int    `json:"queryPort"` // the OSCQuery HTTP port
	Service   string `json:"service"`   // the advertised name
	Fallback  bool   `json:"fallback"`  // OSCQuery is on but failed, so 9001 is used
	Error     string `json:"error"`     // why OSCQuery or the port failed
	// QueriedMS is when an app (normally VRChat) last read Fern's OSCQuery
	// tree, unix ms, 0 if none has since the listener opened.
	QueriedMS int64 `json:"queriedMs"`
}

var (
	startOSCOnce sync.Once
	oscInMu      sync.Mutex
	oscInState   OSCInStatus
	oscService   = "Fern-" + randomHex(3)
	oscQueriedAt atomic.Int64 // unix ms of the last OSCQuery request
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// CurrentOSCIn reports the inbound OSC listener.
func CurrentOSCIn() OSCInStatus {
	oscInMu.Lock()
	st := oscInState
	oscInMu.Unlock()
	if st.OSCQuery {
		st.QueriedMS = oscQueriedAt.Load()
	}
	return st
}

func setOSCIn(st OSCInStatus) {
	oscInMu.Lock()
	oscInState = st
	oscInMu.Unlock()
}

// startOSCIn opens the inbound OSC listener that feeds the bus. Safe to call
// more than once.
func startOSCIn() {
	startOSCOnce.Do(func() { go runOSCIn() })
}

// runOSCIn keeps one listener open the way the settings ask for, reopening
// it when the OSCQuery switch changes or the listener fails.
func runOSCIn() {
	var (
		cur      *oscInbound
		hold     string    // why OSCQuery is not tried again until things change
		vrcSince time.Time // when VRChat was first seen running
	)
	for {
		now := time.Now()
		opts := GetConfig().OSCOptions
		vr := CurrentVRChatStatus()
		if !vr.Running {
			if !vrcSince.IsZero() {
				hold = "" // a new VRChat session gets a new chance
			}
			vrcSince = time.Time{}
		} else if vrcSince.IsZero() {
			vrcSince = now
		}
		if cur != nil && cur.query != opts.OSCQuery {
			hold = ""
		}
		if cur != nil && cur.unfound(now, vrcSince, vr.Linked) {
			hold = "VRChat did not find Fern over OSCQuery; a firewall may be blocking multicast"
			fmt.Println("osc:", hold+", listening on 9001")
		}
		if cur != nil && (cur.query != opts.OSCQuery || cur.stale(now) ||
			(hold != "" && cur.query && !cur.fallback) || (hold == "" && cur.held)) {
			cur.close()
			cur = nil
		}
		if cur == nil {
			cur = openOSCIn(opts.OSCQuery, hold)
		}
		refreshRouter(opts, cur.port())
		time.Sleep(time.Second)
	}
}

// unfound says VRChat has been running for a while with OSCQuery advertised
// and has neither asked about Fern nor sent it anything.
func (in *oscInbound) unfound(now, vrcSince time.Time, linked bool) bool {
	if !in.query || in.fallback || in.conn == nil || vrcSince.IsZero() || linked || oscQueriedAt.Load() != 0 {
		return false
	}
	start := vrcSince
	if in.opened.After(start) {
		start = in.opened
	}
	return now.Sub(start) >= oscFindWait
}

// oscInbound is one open listener: the UDP socket and, in OSCQuery mode,
// the HTTP server and mDNS responder that advertise it.
type oscInbound struct {
	query    bool // what the settings asked for
	fallback bool // OSCQuery failed, so this is 9001
	held     bool // and is not tried again on a timer
	conn     *net.UDPConn
	http     *queryServer
	mdns     *mdnsResponder
	readDone chan struct{}
	opened   time.Time
}

func (in *oscInbound) port() int {
	if in == nil || in.conn == nil {
		return 0
	}
	return in.conn.LocalAddr().(*net.UDPAddr).Port
}

// stale says it is time to open the listener again.
func (in *oscInbound) stale(now time.Time) bool {
	if in.conn == nil {
		return now.Sub(in.opened) >= oscFixedRetry
	}
	select {
	case <-in.readDone:
		return now.Sub(in.opened) >= time.Second // the socket broke
	default:
	}
	return in.fallback && !in.held && now.Sub(in.opened) >= oscQueryRetry
}

func (in *oscInbound) close() {
	if in.mdns != nil {
		in.mdns.close()
	}
	if in.http != nil {
		_ = in.http.Close()
	}
	if in.conn != nil {
		in.conn.Close()
		<-in.readDone
	}
	setOSCListener(false, nil)
}

// openOSCIn opens the listener: with query on, a free port advertised over
// OSCQuery, falling back to 9001 if that fails. A non-empty hold skips
// OSCQuery for that reason.
func openOSCIn(query bool, hold string) *oscInbound {
	in := &oscInbound{query: query, opened: time.Now(), readDone: make(chan struct{})}
	st := OSCInStatus{}
	oscQueriedAt.Store(0)
	if query && hold != "" {
		in.fallback, in.held = true, true
		st.Fallback = true
		st.Error = hold
	} else if query {
		err := in.openQuery()
		if err == nil {
			st = OSCInStatus{OSCQuery: true, Port: in.port(), QueryPort: in.http.port, Service: oscService}
			setOSCIn(st)
			setOSCListener(true, nil)
			go in.read()
			return in
		}
		fmt.Println("osc: OSCQuery failed, listening on 9001:", err)
		in.fallback = true
		st.Fallback = true
		st.Error = "OSCQuery: " + err.Error()
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: OSCFixedPort})
	if err != nil {
		setOSCListener(false, err)
		if st.Error == "" {
			st.Error = err.Error()
		} else {
			st.Error += "; " + err.Error()
		}
		setOSCIn(st)
		close(in.readDone)
		return in
	}
	in.conn = conn
	st.Port = OSCFixedPort
	setOSCIn(st)
	setOSCListener(true, nil)
	go in.read()
	return in
}

func (in *oscInbound) read() {
	defer close(in.readDone)
	readOSC(in.conn)
}

// openQuery binds a free UDP port, serves OSCQuery over HTTP and advertises
// both over mDNS. On failure everything it opened is closed again.
func (in *oscInbound) openQuery() (err error) {
	defer func() {
		if err != nil {
			if in.mdns != nil {
				in.mdns.close()
				in.mdns = nil
			}
			if in.http != nil {
				_ = in.http.Close()
				in.http = nil
			}
			if in.conn != nil {
				in.conn.Close()
				in.conn = nil
			}
		}
	}()
	in.conn, err = net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	udpPort := in.port()
	in.http = &queryServer{Server: http.Server{
		Handler:           noteQueries(oscQueryHandler(oscService, func() int { return udpPort })),
		ReadHeaderTimeout: 5 * time.Second,
	}, port: ln.Addr().(*net.TCPAddr).Port}
	go func(s *queryServer) { _ = s.Serve(ln) }(in.http)

	in.mdns, err = startMDNS(oscZone(udpPort, in.http.port), oscMDNSPort)
	return err
}

// noteQueries records when the OSCQuery server was last asked anything.
func noteQueries(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oscQueriedAt.Store(time.Now().UnixMilli())
		next.ServeHTTP(w, r)
	})
}

// queryServer is the OSCQuery HTTP server and the port it got.
type queryServer struct {
	http.Server
	port int
}

// oscZone is what Fern advertises: the OSCQuery HTTP server and the OSC
// port itself, both on 127.0.0.1.
func oscZone(udpPort, httpPort int) mdnsZone {
	txt := []string{"txtvers=1"}
	return mdnsZone{
		Host: oscService + ".local.",
		IP:   net.IPv4(127, 0, 0, 1),
		Services: []mdnsService{
			{Instance: oscService, Type: "_oscjson._tcp", Port: httpPort, TXT: txt},
			{Instance: oscService, Type: "_osc._udp", Port: udpPort, TXT: txt},
		},
	}
}

// readOSC feeds every datagram from conn to the bus until conn fails. The
// bus is done with a datagram before the next one is read, so the buffer is
// reused.
func readOSC(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		receiveOSC(buf[:n])
	}
}
