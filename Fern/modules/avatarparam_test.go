package modules

import (
	"math"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

func captureAvatarSend(t *testing.T) *[]*osc.Message {
	t.Helper()
	var got []*osc.Message
	old := avatarSend
	avatarSend = func(_ OSCOptions, m *osc.Message) error { got = append(got, m); return nil }
	t.Cleanup(func() { avatarSend = old })
	return &got
}

func TestAvatarParamValues(t *testing.T) {
	got := captureAvatarSend(t)
	o := OSCOptions{}
	cases := []struct {
		name string
		in   any
		want any
	}{
		{"B", true, true},
		{"I", 70, int32(70)},
		{"I", int32(300), int32(255)},
		{"I", int64(-4), int32(0)},
		{"I", uint8(9), int32(9)},
		{"F", 0.5, float32(0.5)},
		{"F", float32(2), float32(1)},
		{"F", -3.0, float32(-1)},
		{"F", math.NaN(), float32(0)},
	}
	for _, c := range cases {
		if err := sendAvatarParamTo(o, c.name, c.in); err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
	}
	if len(*got) != len(cases) {
		t.Fatalf("sent %d", len(*got))
	}
	for i, c := range cases {
		m := (*got)[i]
		if m.Address != AvatarParamPrefix+c.name || !reflect.DeepEqual(m.Arguments, []any{c.want}) {
			t.Errorf("%v sent %s %v, want %v", c.in, m.Address, m.Arguments, c.want)
		}
	}
}

func TestAvatarParamNames(t *testing.T) {
	got := captureAvatarSend(t)
	if err := sendAvatarParamTo(OSCOptions{}, " /avatar/parameters/HR ", 1); err != nil {
		t.Fatal(err)
	}
	if (*got)[0].Address != "/avatar/parameters/HR" {
		t.Errorf("address %q", (*got)[0].Address)
	}
	if err := sendAvatarParamTo(OSCOptions{}, "  ", 1); err == nil {
		t.Error("empty name accepted")
	}
	if err := sendAvatarParamTo(OSCOptions{}, "S", "text"); err == nil {
		t.Error("string value accepted")
	}
	if len(*got) != 1 {
		t.Errorf("bad sends went out: %v", *got)
	}
}

// The real sender reaches the main output and enabled extras.
func TestAvatarParamGoesToEveryOutput(t *testing.T) {
	listen := func() (*net.UDPConn, int) {
		c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c, c.LocalAddr().(*net.UDPAddr).Port
	}
	main, mainPort := listen()
	extra, extraPort := listen()
	off, offPort := listen()
	o := OSCOptions{Host: "127.0.0.1", Port: mainPort, Extra: []OSCTarget{
		{Enabled: true, Host: "127.0.0.1", Port: extraPort},
		{Enabled: false, Host: "127.0.0.1", Port: offPort},
	}}
	if err := sendAvatarParamTo(o, "Seen", true); err != nil {
		t.Fatal(err)
	}
	read := func(c *net.UDPConn) string {
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return ""
		}
		pkt, err := osc.ParsePacket(string(buf[:n]))
		if err != nil {
			return ""
		}
		return pkt.(*osc.Message).Address
	}
	if a := read(main); a != "/avatar/parameters/Seen" {
		t.Errorf("main got %q", a)
	}
	if a := read(extra); a != "/avatar/parameters/Seen" {
		t.Errorf("extra got %q", a)
	}
	if a := read(off); a != "" {
		t.Errorf("disabled output got %q", a)
	}
}
