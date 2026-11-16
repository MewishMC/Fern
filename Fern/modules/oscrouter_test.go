package modules

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

func TestSanitizeForward(t *testing.T) {
	c := DefaultConfig()
	if c.OSCOptions.Forward == nil || len(c.OSCOptions.Forward) != 0 {
		t.Errorf("default forward %v", c.OSCOptions.Forward)
	}
	c.OSCOptions.Forward = nil // configs from before forwarding
	c.sanitize()
	if c.OSCOptions.Forward == nil {
		t.Error("missing forward list stayed null")
	}

	c.OSCOptions.Forward = []OSCForward{
		{Enabled: true, Host: " 10.0.0.2 ", Port: 9100, Prefix: " avatar/parameters/ "},
		{Enabled: false, Host: "", Port: 0, Prefix: "/chat box"},
		{Enabled: true, Host: "h", Port: 70000, Prefix: "/" + strings.Repeat("a", 300)},
	}
	for i := 0; i < 10; i++ {
		c.OSCOptions.Forward = append(c.OSCOptions.Forward, OSCForward{Host: "x", Port: 1})
	}
	c.sanitize()
	f := c.OSCOptions.Forward
	if len(f) != MaxOSCForwards {
		t.Fatalf("kept %d targets", len(f))
	}
	if want := (OSCForward{Enabled: true, Host: "10.0.0.2", Port: 9100, Prefix: "/avatar/parameters/"}); f[0] != want {
		t.Errorf("first %+v", f[0])
	}
	if want := (OSCForward{Host: "127.0.0.1", Port: 9010, Prefix: "/chatbox"}); f[1] != want {
		t.Errorf("second %+v", f[1])
	}
	if f[2].Port != 9010 || len([]rune(f[2].Prefix)) != 128 {
		t.Errorf("third %+v", f[2])
	}

	// Clones do not share the list.
	swapConfigForTest(t, c)
	got := GetConfig()
	got.OSCOptions.Forward[0].Host = "changed"
	if GetConfig().OSCOptions.Forward[0].Host == "changed" {
		t.Error("GetConfig shares the forward list")
	}
}

// swapConfigForTest swaps the live config for one test.
func swapConfigForTest(t *testing.T, c Config) {
	t.Helper()
	cfgMu.Lock()
	old := cfg
	cfg = c
	cfgMu.Unlock()
	t.Cleanup(func() {
		cfgMu.Lock()
		cfg = old
		cfgMu.Unlock()
	})
}

func udpSink(t *testing.T) (*net.UDPConn, int) {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, c.LocalAddr().(*net.UDPAddr).Port
}

// readAddrs collects the OSC addresses a sink receives within a moment.
func readAddrs(c *net.UDPConn) []string {
	var out []string
	buf := make([]byte, 4096)
	for {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return out
		}
		pkt, err := osc.ParsePacket(string(buf[:n]))
		if err != nil {
			out = append(out, "?")
			continue
		}
		if _, ok := pkt.(*osc.Bundle); ok {
			out = append(out, "#bundle")
		}
		for _, m := range flattenOSC(pkt) {
			out = append(out, m.Address)
		}
	}
}

func resetRouter(t *testing.T) {
	t.Helper()
	router.Store(nil)
	t.Cleanup(func() { router.Store(nil) })
}

func TestRouterForwardsAllAndByPrefix(t *testing.T) {
	resetRouter(t)
	resetAvatarCache(t)
	all, allPort := udpSink(t)
	params, paramsPort := udpSink(t)
	off, offPort := udpSink(t)
	opts := OSCOptions{Forward: []OSCForward{
		{Enabled: true, Host: "127.0.0.1", Port: allPort},
		{Enabled: true, Host: "127.0.0.1", Port: paramsPort, Prefix: AvatarParamPrefix},
		{Enabled: false, Host: "127.0.0.1", Port: offPort},
	}}
	refreshRouter(opts, 1)

	bundle := osc.NewBundle(time.Now())
	_ = bundle.Append(osc.NewMessage("/avatar/parameters/A", true))
	_ = bundle.Append(osc.NewMessage("/avatar/change", "avtr_x"))
	receiveOSC(encodeOSC(t, bundle))
	receiveOSC(encodeOSC(t, osc.NewMessage("/tracking/x", float32(1))))

	if got, want := readAddrs(all), []string{"#bundle", "/avatar/parameters/A", "/avatar/change", "/tracking/x"}; !reflect.DeepEqual(got, want) {
		t.Errorf("all target got %v, want %v", got, want)
	}
	if got, want := readAddrs(params), []string{"/avatar/parameters/A"}; !reflect.DeepEqual(got, want) {
		t.Errorf("prefix target got %v, want %v", got, want)
	}
	if got := readAddrs(off); len(got) != 0 {
		t.Errorf("disabled target got %v", got)
	}
	st := OSCForwardStatus()
	if len(st) != 3 || st[0].Sent != 2 || st[1].Sent != 1 || st[2].Sent != 0 {
		t.Errorf("status %+v", st)
	}
}

func TestRouterSkipsLoopsAndBadHosts(t *testing.T) {
	resetRouter(t)
	old := routerResolve
	routerResolve = func(host string, port int) (*net.UDPAddr, error) {
		if host == "nowhere" {
			return nil, errors.New("no such host")
		}
		return old(host, port)
	}
	t.Cleanup(func() { routerResolve = old })

	refreshRouter(OSCOptions{Forward: []OSCForward{
		{Enabled: true, Host: "127.0.0.1", Port: 4444},
		{Enabled: true, Host: "nowhere", Port: 1},
	}}, 4444)
	st := OSCForwardStatus()
	if len(st) != 2 || st[0].Error == "" || st[1].Error != "no such host" {
		t.Errorf("status %+v", st)
	}
	if n := len(router.Load().targets); n != 0 {
		t.Errorf("%d live targets", n)
	}
	// Nothing to send to is not an error.
	receiveOSC(encodeOSC(t, osc.NewMessage("/x")))
}

func TestRouterKeepsCountsUntilChanged(t *testing.T) {
	resetRouter(t)
	sink, port := udpSink(t)
	opts := OSCOptions{Forward: []OSCForward{{Enabled: true, Host: "127.0.0.1", Port: port}}}
	refreshRouter(opts, 1)
	receiveOSC(encodeOSC(t, osc.NewMessage("/x")))
	refreshRouter(opts, 1) // the same settings a second later
	if st := OSCForwardStatus(); st[0].Sent != 1 {
		t.Errorf("count reset without a change: %+v", st)
	}
	opts.Forward[0].Prefix = "/y"
	refreshRouter(opts, 1)
	if st := OSCForwardStatus(); st[0].Sent != 0 {
		t.Errorf("count kept across a change: %+v", st)
	}
	_ = readAddrs(sink)
}
