package modules

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

func dnsQuery(id uint16, qu bool, qs ...dnsQuestion) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:], id)
	binary.BigEndian.PutUint16(msg[4:], uint16(len(qs)))
	for _, q := range qs {
		msg = append(msg, encodeDNSName(q.Name)...)
		msg = binary.BigEndian.AppendUint16(msg, q.Type)
		class := uint16(dnsClassIN)
		if qu {
			class |= dnsQU
		}
		msg = binary.BigEndian.AppendUint16(msg, class)
	}
	return msg
}

// parsedRR is a record read back from a response, for checking.
type parsedRR struct {
	Name  string
	Type  uint16
	Class uint16
	TTL   uint32
	Data  []byte
	Off   int // where the data starts in the message
}

func parseDNSResponse(t *testing.T, msg []byte) (id uint16, answers, extra []parsedRR) {
	t.Helper()
	if len(msg) < 12 || binary.BigEndian.Uint16(msg[2:])&0x8000 == 0 {
		t.Fatalf("not a response: %x", msg)
	}
	id = binary.BigEndian.Uint16(msg)
	qd := int(binary.BigEndian.Uint16(msg[4:]))
	an := int(binary.BigEndian.Uint16(msg[6:]))
	ar := int(binary.BigEndian.Uint16(msg[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, next, err := readDNSName(msg, off)
		if err != nil {
			t.Fatal(err)
		}
		off = next + 4
	}
	read := func() parsedRR {
		name, next, err := readDNSName(msg, off)
		if err != nil {
			t.Fatal(err)
		}
		rr := parsedRR{Name: name, Type: binary.BigEndian.Uint16(msg[next:]), Class: binary.BigEndian.Uint16(msg[next+2:]),
			TTL: binary.BigEndian.Uint32(msg[next+4:])}
		n := int(binary.BigEndian.Uint16(msg[next+8:]))
		rr.Off = next + 10
		rr.Data = msg[rr.Off : rr.Off+n]
		off = rr.Off + n
		return rr
	}
	for i := 0; i < an; i++ {
		answers = append(answers, read())
	}
	for i := 0; i < ar; i++ {
		extra = append(extra, read())
	}
	return id, answers, extra
}

func testZone() mdnsZone {
	return mdnsZone{Host: "Fern-abc.local.", IP: net.IPv4(127, 0, 0, 1), Services: []mdnsService{
		{Instance: "Fern-abc", Type: "_oscjson._tcp", Port: 4321, TXT: []string{"txtvers=1"}},
		{Instance: "Fern-abc", Type: "_osc._udp", Port: 5432, TXT: []string{"txtvers=1"}},
	}}
}

func TestReadDNSNameCompression(t *testing.T) {
	// "local." at 12, then "_osc._udp" + pointer to it.
	msg := make([]byte, 12)
	msg = append(msg, encodeDNSName("local.")...)
	start := len(msg)
	msg = append(msg, 4, '_', 'o', 's', 'c', 4, '_', 'u', 'd', 'p', 0xC0, 12)
	name, next, err := readDNSName(msg, start)
	if err != nil || name != "_osc._udp.local." || next != len(msg) {
		t.Errorf("got %q %d %v", name, next, err)
	}
	loop := append(make([]byte, 12), 0xC0, 12)
	if _, _, err := readDNSName(loop, 12); err == nil {
		t.Error("pointer loop accepted")
	}
	if _, _, err := readDNSName([]byte{5, 'a'}, 0); err == nil {
		t.Error("short label accepted")
	}
}

func TestMDNSAnswersPTRWithRecords(t *testing.T) {
	z := testZone()
	id, qs, ok := parseDNSQuery(dnsQuery(7, false, dnsQuestion{Name: "_OSCjson._tcp.local.", Type: dnsTypePTR}))
	if !ok || id != 7 || len(qs) != 1 || qs[0].Name != "_oscjson._tcp.local." {
		t.Fatalf("parsed %v %v %v", id, qs, ok)
	}
	answers, extra := z.answer(qs)
	_, an, ar := parseDNSResponse(t, encodeDNSResponse(0, nil, answers, extra, false))
	if len(an) != 1 || an[0].Type != dnsTypePTR || an[0].Name != "_oscjson._tcp.local." {
		t.Fatalf("answers %+v", an)
	}
	resp := encodeDNSResponse(0, nil, answers, extra, false)
	_, an, ar = parseDNSResponse(t, resp)
	target, _, _ := readDNSName(resp, an[0].Off)
	if target != "Fern-abc._oscjson._tcp.local." {
		t.Errorf("PTR points at %q", target)
	}
	types := map[uint16]parsedRR{}
	for _, rr := range ar {
		types[rr.Type] = rr
	}
	srv, ok := types[dnsTypeSRV]
	if !ok || binary.BigEndian.Uint16(srv.Data[4:]) != 4321 || srv.Class != dnsClassIN|dnsCacheFlush {
		t.Errorf("SRV %+v", srv)
	}
	if host, _, _ := readDNSName(resp, srv.Off+6); host != "Fern-abc.local." {
		t.Errorf("SRV target %q", host)
	}
	if a := types[dnsTypeA]; !net.IP(a.Data).Equal(net.IPv4(127, 0, 0, 1)) {
		t.Errorf("A %v", a.Data)
	}
	if txt := types[dnsTypeTXT]; string(txt.Data) != "\x09txtvers=1" {
		t.Errorf("TXT %q", txt.Data)
	}
}

func TestMDNSAnswersOtherQuestions(t *testing.T) {
	z := testZone()
	cases := []struct {
		q     dnsQuestion
		types []uint16
	}{
		{dnsQuestion{Name: "fern-abc._osc._udp.local.", Type: dnsTypeSRV, Class: dnsClassIN}, []uint16{dnsTypeSRV}},
		{dnsQuestion{Name: "fern-abc._osc._udp.local.", Type: dnsTypeTXT, Class: dnsClassIN}, []uint16{dnsTypeTXT}},
		{dnsQuestion{Name: "fern-abc._osc._udp.local.", Type: dnsTypeANY, Class: dnsClassIN}, []uint16{dnsTypeSRV, dnsTypeTXT}},
		{dnsQuestion{Name: "fern-abc.local.", Type: dnsTypeA, Class: dnsClassIN | dnsQU}, []uint16{dnsTypeA}},
		{dnsQuestion{Name: mdnsServiceEnum, Type: dnsTypePTR, Class: dnsClassIN}, []uint16{dnsTypePTR, dnsTypePTR}},
		{dnsQuestion{Name: "_http._tcp.local.", Type: dnsTypePTR, Class: dnsClassIN}, nil},
		{dnsQuestion{Name: "fern-abc.local.", Type: 28, Class: dnsClassIN}, nil}, // AAAA: Fern has none
	}
	for _, c := range cases {
		answers, _ := z.answer([]dnsQuestion{c.q})
		var got []uint16
		for _, a := range answers {
			got = append(got, a.Type)
		}
		if len(got) != len(c.types) {
			t.Errorf("%s type %d: answers %v, want %v", c.q.Name, c.q.Type, got, c.types)
			continue
		}
		for i := range got {
			if got[i] != c.types[i] {
				t.Errorf("%s type %d: answers %v, want %v", c.q.Name, c.q.Type, got, c.types)
			}
		}
	}
	if _, _, ok := parseDNSQuery(encodeDNSResponse(1, nil, nil, nil, false)); ok {
		t.Error("a response was parsed as a query")
	}
}

func TestMDNSLegacyResponseEchoesQuestion(t *testing.T) {
	z := testZone()
	qs := []dnsQuestion{{Name: "fern-abc.local.", Type: dnsTypeA, Class: dnsClassIN}}
	answers, extra := z.answer(qs)
	msg := encodeDNSResponse(99, qs, answers, extra, true)
	id, an, _ := parseDNSResponse(t, msg)
	if id != 99 || binary.BigEndian.Uint16(msg[4:]) != 1 {
		t.Errorf("id %d, questions %d", id, binary.BigEndian.Uint16(msg[4:]))
	}
	if len(an) != 1 || an[0].TTL > 10 || an[0].Class != dnsClassIN {
		t.Errorf("legacy answer %+v", an)
	}
	_, bye, _ := parseDNSResponse(t, encodeDNSResponse(0, nil, z.all(true), nil, false))
	for _, rr := range bye {
		if rr.TTL != 0 {
			t.Errorf("goodbye %s has TTL %d", rr.Name, rr.TTL)
		}
	}
}

func queryJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &out)
	}
	return resp.StatusCode, out
}

func TestOSCQueryHTTP(t *testing.T) {
	srv := httptest.NewServer(oscQueryHandler("Fern-test", func() int { return 4567 }))
	defer srv.Close()

	code, info := queryJSON(t, srv.URL+"/?HOST_INFO")
	if code != 200 || info["NAME"] != "Fern-test" || info["OSC_PORT"] != float64(4567) ||
		info["OSC_IP"] != "127.0.0.1" || info["OSC_TRANSPORT"] != "UDP" {
		t.Errorf("HOST_INFO %d %v", code, info)
	}

	code, root := queryJSON(t, srv.URL+"/")
	if code != 200 || root["FULL_PATH"] != "/" {
		t.Fatalf("root %d %v", code, root)
	}
	avatar := root["CONTENTS"].(map[string]any)["avatar"].(map[string]any)
	change := avatar["CONTENTS"].(map[string]any)["change"].(map[string]any)
	if change["FULL_PATH"] != "/avatar/change" || change["TYPE"] != "s" || change["ACCESS"] != float64(2) {
		t.Errorf("change node %v", change)
	}

	if code, node := queryJSON(t, srv.URL+"/avatar/change"); code != 200 || node["TYPE"] != "s" {
		t.Errorf("node %d %v", code, node)
	}
	if code, attr := queryJSON(t, srv.URL+"/avatar/change?TYPE"); code != 200 || attr["TYPE"] != "s" || len(attr) != 1 {
		t.Errorf("attribute %d %v", code, attr)
	}
	if code, _ := queryJSON(t, srv.URL+"/avatar/change?VALUE"); code != http.StatusNoContent {
		t.Errorf("missing attribute gave %d", code)
	}
	if code, _ := queryJSON(t, srv.URL+"/nope"); code != 404 {
		t.Errorf("unknown path gave %d", code)
	}
	resp, err := http.Post(srv.URL+"/", "text/plain", strings.NewReader("x"))
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST gave %d", resp.StatusCode)
		}
	}
}

// The whole OSCQuery listener on this machine: HOST_INFO names the UDP port,
// OSC sent there reaches the bus, and an mDNS query is answered.
func TestOSCQueryListener(t *testing.T) {
	resetAvatarCache(t)
	oldPort := oscMDNSPort
	oscMDNSPort = quietMDNS(t)
	t.Cleanup(func() { oscMDNSPort = oldPort })
	in := openOSCIn(true, "")
	t.Cleanup(in.close)
	st := CurrentOSCIn()
	if !st.OSCQuery {
		t.Fatalf("OSCQuery did not start: %+v", st)
	}
	if st.Port == 0 || st.Port == OSCFixedPort || st.QueryPort == 0 || st.Service != oscService {
		t.Fatalf("status %+v", st)
	}

	_, info := queryJSON(t, "http://127.0.0.1:"+strconv.Itoa(st.QueryPort)+"/?HOST_INFO")
	if info["OSC_PORT"] != float64(st.Port) {
		t.Errorf("HOST_INFO port %v, listening on %d", info["OSC_PORT"], st.Port)
	}

	got := make(chan bool, 1)
	cancel := SubscribeOSC("/avatar/parameters/QueryTest", func(m *osc.Message) { got <- true })
	defer cancel()
	if err := osc.NewClient("127.0.0.1", st.Port).Send(osc.NewMessage("/avatar/parameters/QueryTest", true)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Error("OSC on the OSCQuery port never reached the bus")
	}

	if CurrentOSCIn().QueriedMS == 0 {
		t.Error("the HOST_INFO request was not noted")
	}
}

// quietMDNS keeps a test's mDNS traffic on loopback and off port 5353, so
// tests never advertise on the real network. It returns a spare port.
func quietMDNS(t *testing.T) int {
	t.Helper()
	old := mdnsInterfaces
	mdnsInterfaces = func() [][4]byte { return [][4]byte{{127, 0, 0, 1}} }
	t.Cleanup(func() { mdnsInterfaces = old })
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.LocalAddr().(*net.UDPAddr).Port
}

// The responder answers over a real socket. A spare port stands in for 5353,
// which the system's own mDNS service may hold, and the query is sent
// straight to it, the way a plain DNS resolver asks (multicast delivery
// depends on the machine's firewall).
func TestMDNSResponderAnswers(t *testing.T) {
	port := quietMDNS(t)
	r, err := startMDNS(testZone(), port)
	if err != nil {
		t.Skipf("no multicast socket: %v", err)
	}
	defer r.close()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := dnsQuery(42, false, dnsQuestion{Name: "Fern-abc._oscjson._tcp.local.", Type: dnsTypeSRV})
	if _, err := conn.WriteToUDP(q, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	id, an, ar := parseDNSResponse(t, buf[:n])
	if id != 42 || len(an) != 1 || an[0].Type != dnsTypeSRV || binary.BigEndian.Uint16(an[0].Data[4:]) != 4321 {
		t.Errorf("id %d answers %+v", id, an)
	}
	if len(ar) != 1 || ar[0].Type != dnsTypeA {
		t.Errorf("additional %+v", ar)
	}
}

// With 9001 taken, the fixed listener says so and asks to be retried.
func TestFixedListenerBusyPort(t *testing.T) {
	hold, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: OSCFixedPort})
	if err == nil {
		defer hold.Close()
	} // else another app already holds it, which is just as busy
	in := openOSCIn(false, "")
	t.Cleanup(in.close)
	st := CurrentOSCIn()
	if st.Port != 0 || st.OSCQuery || st.Error == "" {
		t.Errorf("status %+v", st)
	}
	if CurrentVRChatStatus().Listening {
		t.Error("panel told the port is open")
	}
	if in.stale(time.Now()) || !in.stale(time.Now().Add(oscFixedRetry)) {
		t.Error("busy port not retried on schedule")
	}
}

func TestOSCQueryDefaultsOn(t *testing.T) {
	c := DefaultConfig()
	if !c.OSCOptions.OSCQuery {
		t.Error("OSCQuery off by default")
	}
	// A config from before the setting keeps the default.
	if err := json.Unmarshal([]byte(`{"OSC":{"host":"127.0.0.1","port":9000}}`), &c); err != nil || !c.OSCOptions.OSCQuery {
		t.Errorf("old config turned OSCQuery off: %v", err)
	}
}

func TestUnfoundFallsBack(t *testing.T) {
	now := time.Now()
	oscQueriedAt.Store(0)
	t.Cleanup(func() { oscQueriedAt.Store(0) })
	in := &oscInbound{query: true, conn: &net.UDPConn{}, opened: now.Add(-time.Hour)}
	if in.unfound(now, time.Time{}, false) {
		t.Error("fell back while VRChat is not running")
	}
	if in.unfound(now, now.Add(-oscFindWait/2), false) {
		t.Error("fell back too soon")
	}
	if !in.unfound(now, now.Add(-oscFindWait), false) {
		t.Error("never fell back")
	}
	if in.unfound(now, now.Add(-oscFindWait), true) {
		t.Error("fell back although VRChat sends OSC")
	}
	oscQueriedAt.Store(now.UnixMilli())
	if in.unfound(now, now.Add(-oscFindWait), false) {
		t.Error("fell back although VRChat asked about Fern")
	}
	oscQueriedAt.Store(0)
	in.opened = now.Add(-time.Second) // reopened just now: wait again
	if in.unfound(now, now.Add(-time.Hour), false) {
		t.Error("a fresh listener fell back at once")
	}
	in.fallback = true
	in.opened = now.Add(-time.Hour)
	if in.unfound(now, now.Add(-time.Hour), false) {
		t.Error("the 9001 fallback itself was judged")
	}
}
