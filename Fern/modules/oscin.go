package modules

import (
	"net"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// OSCFixedPort is where VRChat sends OSC when nobody asks it otherwise.
const OSCFixedPort = 9001

var startOSCOnce sync.Once

// startOSCIn opens the inbound OSC listener that feeds the bus. Safe to call
// more than once.
func startOSCIn() {
	startOSCOnce.Do(func() { go listenFixedOSC() })
}

// listenFixedOSC listens on VRChat's default output port. A busy port is not
// fatal (another OSC app may own it), but the panel shows it, and the port is
// retried in case it frees up.
func listenFixedOSC() {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: OSCFixedPort}
	for {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			setOSCListener(false, err)
			time.Sleep(5 * time.Second)
			continue
		}
		setOSCListener(true, nil)
		readOSC(conn)
		conn.Close()
		setOSCListener(false, nil)
		time.Sleep(time.Second)
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

func routeOSC(data []byte, msgs []*osc.Message) {}
