package modules

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hypebeast/go-osc/osc"
)

// The inbound router passes what VRChat sends Fern on to other OSC apps, so
// an app that only knows port 9001 keeps working while Fern listens.

// OSCForward is one place received OSC is passed on to.
type OSCForward struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	// Prefix limits forwarding to addresses that start with it, e.g.
	// "/avatar/parameters/". Empty forwards everything as it arrived.
	Prefix string `json:"prefix"`
}

// MaxOSCForwards is how many forward targets can be set up.
const MaxOSCForwards = 8

func defaultOSCForward() OSCForward { return OSCForward{Host: "127.0.0.1", Port: 9010} }

// sanitizeForward keeps the forward targets usable: at most MaxOSCForwards,
// a host and port each, and a prefix that is an OSC address.
func (o *OSCOptions) sanitizeForward() {
	if len(o.Forward) > MaxOSCForwards {
		o.Forward = o.Forward[:MaxOSCForwards]
	}
	out := make([]OSCForward, 0, len(o.Forward))
	for _, f := range o.Forward {
		d := defaultOSCForward()
		d.Enabled = f.Enabled
		if host := RuneLimit(strings.TrimSpace(f.Host), 253); host != "" {
			d.Host = host
		}
		if f.Port >= 1 && f.Port <= 65535 {
			d.Port = f.Port
		}
		prefix := strings.Join(strings.Fields(f.Prefix), "")
		if prefix != "" && !strings.HasPrefix(prefix, "/") {
			prefix = "/" + prefix
		}
		d.Prefix = RuneLimit(prefix, 128)
		out = append(out, d)
	}
	o.Forward = out
}

// OSCForwardState is how one forward target is doing, for the panel.
type OSCForwardState struct {
	Sent  int64  `json:"sent"`  // packets passed on since the target last changed
	Error string `json:"error"` // the last failure, "" while it works
}

// forwardTarget is a resolved, enabled target.
type forwardTarget struct {
	index  int
	addr   *net.UDPAddr
	prefix string
	sent   atomic.Int64
	err    atomic.Value // string
}

type routerState struct {
	key     string
	targets []*forwardTarget
	all     []OSCForwardState // per configured target, for those not live
}

var (
	routerMu   sync.Mutex
	routerConn *net.UDPConn
	router     atomic.Pointer[routerState]
	// routerResolve looks a target up; a variable so tests stay offline.
	routerResolve = func(host string, port int) (*net.UDPAddr, error) {
		return net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprint(port)))
	}
)

// refreshRouter rebuilds the target list when the settings or Fern's own
// port change. listenPort guards against forwarding to Fern itself, which
// would loop every packet forever.
func refreshRouter(opts OSCOptions, listenPort int) {
	key := fmt.Sprintf("%d|%+v", listenPort, opts.Forward)
	if cur := router.Load(); cur != nil && cur.key == key {
		return
	}
	next := &routerState{key: key, all: make([]OSCForwardState, len(opts.Forward))}
	for i, f := range opts.Forward {
		if !f.Enabled {
			continue
		}
		addr, err := routerResolve(f.Host, f.Port)
		if err != nil {
			next.all[i].Error = err.Error()
			continue
		}
		if addr.Port == listenPort && (addr.IP.IsLoopback() || addr.IP.IsUnspecified()) {
			next.all[i].Error = "this is Fern's own port"
			continue
		}
		t := &forwardTarget{index: i, addr: addr, prefix: f.Prefix}
		t.err.Store("")
		next.targets = append(next.targets, t)
	}
	if len(next.targets) > 0 {
		routerMu.Lock()
		if routerConn == nil {
			if c, err := net.ListenUDP("udp", nil); err == nil {
				routerConn = c
			} else {
				for _, t := range next.targets {
					next.all[t.index].Error = err.Error()
				}
				next.targets = nil
			}
		}
		routerMu.Unlock()
	}
	router.Store(next)
}

// routeOSC passes one received datagram on. A target without a prefix gets
// the datagram exactly as it came; one with a prefix gets each matching
// message on its own.
func routeOSC(data []byte, msgs []*osc.Message) {
	st := router.Load()
	if st == nil || len(st.targets) == 0 {
		return
	}
	routerMu.Lock()
	conn := routerConn
	routerMu.Unlock()
	if conn == nil {
		return
	}
	var encoded map[*osc.Message][]byte
	for _, t := range st.targets {
		if t.prefix == "" {
			t.send(conn, data)
			continue
		}
		for _, m := range msgs {
			if !strings.HasPrefix(m.Address, t.prefix) {
				continue
			}
			if encoded == nil {
				encoded = map[*osc.Message][]byte{}
			}
			b, ok := encoded[m]
			if !ok {
				var err error
				if b, err = m.MarshalBinary(); err != nil {
					continue
				}
				encoded[m] = b
			}
			t.send(conn, b)
		}
	}
}

func (t *forwardTarget) send(conn *net.UDPConn, b []byte) {
	if _, err := conn.WriteToUDP(b, t.addr); err != nil {
		t.err.Store(err.Error())
		return
	}
	t.sent.Add(1)
	if s, _ := t.err.Load().(string); s != "" {
		t.err.Store("")
	}
}

// OSCForwardStatus reports every configured forward target, in order.
func OSCForwardStatus() []OSCForwardState {
	st := router.Load()
	if st == nil {
		return []OSCForwardState{}
	}
	out := append([]OSCForwardState{}, st.all...)
	for _, t := range st.targets {
		e, _ := t.err.Load().(string)
		out[t.index] = OSCForwardState{Sent: t.sent.Load(), Error: e}
	}
	return out
}
