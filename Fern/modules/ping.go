package modules

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Ping measures the round trip to a host with ICMP echo through
// iphlpapi.dll, which, unlike a raw socket, needs no admin rights. Hosts that
// drop ICMP (and IPv6-only ones) are timed with a TCP connect instead, which
// costs one extra round trip for the handshake's last step but no more.

var (
	procIcmpCreateFile  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpCloseHandle = iphlpapi.NewProc("IcmpCloseHandle")
	procIcmpSendEcho    = iphlpapi.NewProc("IcmpSendEcho")
)

const (
	pingTimeout = 2 * time.Second
	pingHistory = 20
)

// PingStatus is the ping line's state, for the panel and rules.
type PingStatus struct {
	Host    string `json:"host"`
	Address string `json:"address"`
	Method  string `json:"method"` // the one that answered: icmp or tcp
	MS      int    `json:"ms"`     // last round trip, -1 for a timeout
	AvgMS   int    `json:"avgMs"`  // over the answered pings in the window, -1 for none
	Loss    int    `json:"loss"`   // percent of the window that timed out
	Samples int    `json:"samples"`
	Error   string `json:"error"`
	Updated int64  `json:"updated"`
	Active  bool   `json:"active"` // pinging right now
}

var (
	pingMu      sync.RWMutex
	pingState   = PingStatus{MS: -1, AvgMS: -1}
	pingWindow  []int // ms, -1 timeouts
	pingKey     string
	pingWake    = make(chan struct{}, 1)
	pingPanelAt time.Time
)

// The probes, swapped in tests.
var (
	pingICMP = icmpEcho
	pingTCP  = tcpConnect
	pingDNS  = func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip", host)
	}
)

// CurrentPing reports the latest ping.
func CurrentPing() PingStatus {
	pingMu.RLock()
	defer pingMu.RUnlock()
	return pingState
}

// WatchPing keeps pinging for a few minutes for the panel, even with the
// line off, and pings now.
func WatchPing() {
	pingMu.Lock()
	pingPanelAt = time.Now().Add(5 * time.Minute)
	pingMu.Unlock()
	select {
	case pingWake <- struct{}{}:
	default:
	}
}

// icmpEcho sends one echo request to an IPv4 address.
func icmpEcho(ip net.IP, timeout time.Duration) (time.Duration, error) {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0, errors.New("ICMP needs an IPv4 address")
	}
	if procIcmpCreateFile.Find() != nil {
		return 0, errors.New("ICMP is not available")
	}
	h, _, err := procIcmpCreateFile.Call()
	if h == 0 || h == uintptr(syscall.InvalidHandle) {
		return 0, err
	}
	defer procIcmpCloseHandle.Call(h)
	payload := []byte("fern-ping-fern-ping-fern-ping-32")
	// An ICMP_ECHO_REPLY, the echoed data, 8 bytes for an ICMP error and
	// room to spare.
	reply := make([]byte, 64+len(payload)+8+64)
	start := time.Now()
	n, _, _ := procIcmpSendEcho.Call(h,
		uintptr(binary.LittleEndian.Uint32(ip4)),
		uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)),
		0,
		uintptr(unsafe.Pointer(&reply[0])), uintptr(len(reply)),
		uintptr(timeout.Milliseconds()))
	elapsed := time.Since(start)
	if n == 0 {
		return 0, errors.New("no answer")
	}
	// ICMP_ECHO_REPLY: Address, Status, RoundTripTime (u32).
	if status := binary.LittleEndian.Uint32(reply[4:]); status != 0 {
		return 0, errors.New("no answer (status " + strconv.Itoa(int(status)) + ")")
	}
	rtt := time.Duration(binary.LittleEndian.Uint32(reply[8:])) * time.Millisecond
	if rtt == 0 && elapsed < time.Millisecond {
		rtt = elapsed
	}
	return rtt, nil
}

// tcpConnect times a TCP connect to ip:port.
func tcpConnect(ip net.IP, port int, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)), timeout)
	elapsed := time.Since(start)
	if err != nil {
		// A refused connection still went there and back.
		var op *net.OpError
		if errors.As(err, &op) && strings.Contains(strings.ToLower(err.Error()), "refused") {
			return elapsed, nil
		}
		return 0, errors.New("no answer")
	}
	_ = conn.Close()
	return elapsed, nil
}

// resolvePing picks the address to ping, IPv4 first since ICMP needs it.
func resolvePing(host string) (net.IP, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return ip, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := pingDNS(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("can't find " + host)
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip, nil
		}
	}
	return ips[0], nil
}

// pingOnce runs one probe with the configured method. It returns the round
// trip in ms (-1 for a timeout) and the method that answered.
func pingOnce(o PingOptions, ip net.IP) (int, string) {
	toMS := func(d time.Duration) int {
		ms := int(math.Round(float64(d) / float64(time.Millisecond)))
		if ms < 1 {
			ms = 1
		}
		return ms
	}
	if o.Method != PingTCP && ip.To4() != nil {
		if d, err := pingICMP(ip, pingTimeout); err == nil {
			return toMS(d), PingICMP
		}
		if o.Method == PingICMP {
			return -1, PingICMP
		}
	}
	if o.Method == PingICMP {
		return -1, PingICMP
	}
	if d, err := pingTCP(ip, o.Port, pingTimeout); err == nil {
		return toMS(d), PingTCP
	}
	return -1, PingTCP
}

// summarise fills the average and loss from the window.
func summarise(st *PingStatus, window []int) {
	st.Samples = len(window)
	st.AvgMS, st.Loss = -1, 0
	if len(window) == 0 {
		return
	}
	sum, ok := 0, 0
	for _, v := range window {
		if v >= 0 {
			sum += v
			ok++
		}
	}
	if ok > 0 {
		st.AvgMS = int(math.Round(float64(sum) / float64(ok)))
	}
	st.Loss = int(math.Round(float64(len(window)-ok) * 100 / float64(len(window))))
}

// recordPing adds one result for host.
func recordPing(host, addr, method string, ms int, errText string) {
	pingMu.Lock()
	defer pingMu.Unlock()
	key := host + "|" + addr
	if key != pingKey {
		pingKey, pingWindow = key, nil
	}
	if errText == "" {
		pingWindow = append(pingWindow, ms)
		if len(pingWindow) > pingHistory {
			pingWindow = slices.Delete(pingWindow, 0, len(pingWindow)-pingHistory)
		}
	}
	st := PingStatus{Host: host, Address: addr, Method: method, MS: ms, Error: errText, Updated: time.Now().UnixMilli(), Active: true}
	summarise(&st, pingWindow)
	pingState = st
}

// pingWanted reports whether anything needs pings.
func pingWanted(c Config, panelUntil time.Time) bool {
	if strings.TrimSpace(c.PingOptions.Host) == "" {
		return false
	}
	return c.DisplayOptions.Ping || time.Now().Before(panelUntil) || rulesUse(c, TriggerPing)
}

func startPingWorker() {
	go func() {
		for {
			c := GetConfig()
			o := c.PingOptions
			pingMu.RLock()
			until := pingPanelAt
			pingMu.RUnlock()
			wait := time.Duration(o.IntervalSec) * time.Second
			if wait < 2*time.Second {
				wait = 2 * time.Second
			}
			if pingWanted(c, until) {
				if ip, err := resolvePing(o.Host); err != nil {
					recordPing(o.Host, "", "", -1, err.Error())
				} else {
					ms, method := pingOnce(o, ip)
					recordPing(o.Host, ip.String(), method, ms, "")
				}
				NotifyChange()
				NudgeRules()
			} else {
				pingMu.Lock()
				pingState.Active = false
				pingMu.Unlock()
				wait = 2 * time.Second
			}
			select {
			case <-time.After(wait):
			case <-pingWake:
			}
		}
	}()
}

func pingText(st PingStatus, o PingOptions) string {
	if st.Updated == 0 || !st.Active {
		return ""
	}
	var parts []string
	switch {
	case st.Error != "":
		return ""
	case st.MS < 0:
		if o.TimeoutText == "" {
			return ""
		}
		parts = append(parts, o.TimeoutText)
	default:
		parts = append(parts, strconv.Itoa(st.MS)+" ms")
	}
	if o.ShowAvg && st.AvgMS >= 0 && st.Samples > 1 {
		parts = append(parts, "avg "+strconv.Itoa(st.AvgMS)+" ms")
	}
	if o.ShowLoss && st.Samples > 1 {
		parts = append(parts, strconv.Itoa(st.Loss)+"% loss")
	}
	text := strings.Join(parts, " · ")
	if o.ShowHost {
		text = o.Host + " " + text
	}
	return withLabel(o.Label, text)
}

func pingLine(cfg Config) string {
	st := CurrentPing()
	if !strings.EqualFold(st.Host, cfg.PingOptions.Host) {
		return ""
	}
	return pingText(st, cfg.PingOptions)
}
