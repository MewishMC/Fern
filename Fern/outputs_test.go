package main

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"

	"Fern/modules"
)

func TestOSCDestinations(t *testing.T) {
	opts := modules.OSCOptions{Host: "127.0.0.1", Port: 9000, Extra: []modules.OSCTarget{
		{Enabled: true, Host: "127.0.0.1", Port: 9002},
		{Enabled: false, Host: "127.0.0.1", Port: 9003},
	}}
	if got, want := oscDestinations(opts), []string{"127.0.0.1:9000", "127.0.0.1:9002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	opts.Extra[1] = modules.OSCTarget{Enabled: true, Host: "127.0.0.1", Port: 9000}
	if got, want := oscDestinations(opts), []string{"127.0.0.1:9000", "127.0.0.1:9002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a repeat of the main output: got %v, want %v", got, want)
	}
}

func listenUDP(t *testing.T) (*net.UDPConn, int) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn, conn.LocalAddr().(*net.UDPAddr).Port
}

func received(t *testing.T, conn *net.UDPConn) string {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	return string(buf[:n])
}

func TestSendOSCReachesEveryOutput(t *testing.T) {
	mainOut, mainPort := listenUDP(t)
	second, secondPort := listenUDP(t)
	third, thirdPort := listenUDP(t)
	opts := modules.OSCOptions{Host: "127.0.0.1", Port: mainPort, Extra: []modules.OSCTarget{
		{Enabled: true, Host: "127.0.0.1", Port: secondPort},
		{Enabled: false, Host: "127.0.0.1", Port: thirdPort},
	}}
	if n := sendOSC(opts, osc.NewMessage("/chatbox/input", "hello", true, false)); n != 2 {
		t.Errorf("sent to %d outputs, want 2", n)
	}
	for name, conn := range map[string]*net.UDPConn{"main": mainOut, "second": second} {
		if got := received(t, conn); !strings.Contains(got, "hello") {
			t.Errorf("%s output got %q", name, got)
		}
	}
	_ = third.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _ := third.Read(make([]byte, 64)); n > 0 {
		t.Error("the disabled output got the chatbox")
	}
	if got := oscTarget(); !strings.HasSuffix(got, " +1") {
		t.Errorf("label %q should count the extra output", got)
	}
}
