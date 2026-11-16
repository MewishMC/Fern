package modules

import (
	"net"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

func encodeOSC(t *testing.T, p osc.Packet) []byte {
	t.Helper()
	data, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func resetAvatarCache(t *testing.T) {
	t.Helper()
	reset := func() {
		paramMu.Lock()
		clear(paramCache)
		avatarID = ""
		paramMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestSubscribeExactAndPrefix(t *testing.T) {
	resetAvatarCache(t)
	var exact, prefix, all []string
	c1 := SubscribeOSC("/avatar/parameters/Voice", func(m *osc.Message) { exact = append(exact, m.Address) })
	c2 := SubscribeOSCPrefix(AvatarParamPrefix, func(m *osc.Message) { prefix = append(prefix, m.Address) })
	c3 := SubscribeOSCPrefix("", func(m *osc.Message) { all = append(all, m.Address) })
	defer c3()

	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/Voice", float32(0.5))))
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/VoiceLevel", float32(0.5))))
	receiveOSC(encodeOSC(t, osc.NewMessage("/input/Jump", int32(1))))
	if len(exact) != 1 || exact[0] != "/avatar/parameters/Voice" {
		t.Errorf("exact got %v", exact)
	}
	if len(prefix) != 2 {
		t.Errorf("prefix got %v", prefix)
	}
	if len(all) != 3 {
		t.Errorf("catch-all got %v", all)
	}

	c1()
	c1() // cancelling twice is harmless
	c2()
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/Voice", float32(1))))
	if len(exact) != 1 || len(prefix) != 2 {
		t.Errorf("cancelled subscribers still called: %v %v", exact, prefix)
	}
}

func TestBundlesReachSubscribers(t *testing.T) {
	resetAvatarCache(t)
	inner := osc.NewBundle(time.Now())
	_ = inner.Append(osc.NewMessage("/avatar/parameters/B", true))
	outer := osc.NewBundle(time.Now())
	_ = outer.Append(osc.NewMessage("/avatar/parameters/A", int32(3)))
	_ = outer.Append(inner)

	var got []string
	cancel := SubscribeOSCPrefix(AvatarParamPrefix, func(m *osc.Message) { got = append(got, m.Address) })
	defer cancel()
	receiveOSC(encodeOSC(t, outer))
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if v, ok := AvatarParam("B"); !ok || v != true {
		t.Errorf("B = %v %v", v, ok)
	}
}

func TestParamCacheAndAvatarChange(t *testing.T) {
	resetAvatarCache(t)
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/GestureLeft", int32(4))))
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/Upright", float32(0.25))))
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/Seated", true)))
	if v, ok := AvatarParamFloat("GestureLeft"); !ok || v != 4 {
		t.Errorf("GestureLeft = %v %v", v, ok)
	}
	if v, ok := AvatarParamFloat("Seated"); !ok || v != 1 {
		t.Errorf("Seated = %v %v", v, ok)
	}
	if _, ok := AvatarParam("Missing"); ok {
		t.Error("unknown parameter reported")
	}
	if n := len(AvatarParams()); n != 3 {
		t.Errorf("cached %d parameters", n)
	}

	var changes []string
	var cachedAtChange int
	cancel := OnAvatarChange(func(id string) {
		changes = append(changes, id)
		cachedAtChange = len(AvatarParams())
	})
	receiveOSC(encodeOSC(t, osc.NewMessage(AvatarChangeAddr, "avtr_1234")))
	if CurrentAvatarID() != "avtr_1234" || len(changes) != 1 || changes[0] != "avtr_1234" {
		t.Errorf("avatar %q, changes %v", CurrentAvatarID(), changes)
	}
	if cachedAtChange != 0 {
		t.Errorf("the old avatar's %d parameters were still cached", cachedAtChange)
	}
	cancel()
	receiveOSC(encodeOSC(t, osc.NewMessage(AvatarChangeAddr, "avtr_5678")))
	if len(changes) != 1 || CurrentAvatarID() != "avtr_5678" {
		t.Errorf("after cancel: %v, %q", changes, CurrentAvatarID())
	}
}

func TestSubscriberPanicDoesNotStopOthers(t *testing.T) {
	resetAvatarCache(t)
	c1 := SubscribeOSC("/x", func(*osc.Message) { panic("boom") })
	defer c1()
	called := false
	c2 := SubscribeOSC("/x", func(*osc.Message) { called = true })
	defer c2()
	receiveOSC(encodeOSC(t, osc.NewMessage("/x")))
	if !called {
		t.Error("second subscriber not called")
	}
}

func TestBusDrivesAfkAndMute(t *testing.T) {
	resetAvatarCache(t)
	c1 := SubscribeOSCPrefix("/", func(m *osc.Message) { handleAddress(m.Address) })
	defer c1()
	c2 := SubscribeOSC(AvatarParamPrefix+"MuteSelf", noteMuteSelf)
	defer c2()

	activityMu.Lock()
	activityAt[AfkSourceMovement] = time.Time{}
	activityMu.Unlock()
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/VelocityX", float32(1))))
	activityMu.RLock()
	moved := activityAt[AfkSourceMovement]
	activityMu.RUnlock()
	if time.Since(moved) > time.Second {
		t.Error("movement over the bus did not count as activity")
	}

	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/MuteSelf", true)))
	if muted, known := knownMute(); !muted || !known {
		t.Errorf("mute %v known %v", muted, known)
	}
	receiveOSC(encodeOSC(t, osc.NewMessage("/avatar/parameters/MuteSelf", false)))
	if muted, _ := knownMute(); muted {
		t.Error("unmute not seen")
	}
}

func TestReadOSCFromSocket(t *testing.T) {
	resetAvatarCache(t)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	cancel := SubscribeOSC("/test/read", func(m *osc.Message) { got <- m.Arguments[0].(string) })
	defer cancel()
	done := make(chan struct{})
	go func() { readOSC(conn); close(done) }()

	port := conn.LocalAddr().(*net.UDPAddr).Port
	if err := osc.NewClient("127.0.0.1", port).Send(osc.NewMessage("/test/read", "hello")); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-got:
		if v != "hello" {
			t.Errorf("got %q", v)
		}
	case <-time.After(2 * time.Second):
		t.Error("message never arrived")
	}
	conn.Close()
	<-done
}
