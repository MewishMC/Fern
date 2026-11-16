package modules

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A small mDNS responder (RFC 6762 / DNS-SD RFC 6763) for OSCQuery. It only
// answers for Fern's own services: PTR for the service types, SRV and TXT for
// the instances and A for the host, all pointing at 127.0.0.1 like VRChat's
// own OSCQuery library does.

const (
	dnsTypeA   = 1
	dnsTypePTR = 12
	dnsTypeTXT = 16
	dnsTypeSRV = 33
	dnsTypeANY = 255

	dnsClassIN    = 1
	dnsCacheFlush = 0x8000
	dnsQU         = 0x8000

	mdnsPort        = 5353
	mdnsServiceEnum = "_services._dns-sd._udp.local."
)

var mdnsGroup = net.IPv4(224, 0, 0, 251)

// mdnsService is one advertised DNS-SD service.
type mdnsService struct {
	Instance string // the instance label, e.g. "Fern-1a2b3c"
	Type     string // e.g. "_oscjson._tcp"
	Port     int
	TXT      []string
}

func (s mdnsService) typeName() string     { return s.Type + ".local." }
func (s mdnsService) instanceName() string { return s.Instance + "." + s.typeName() }

// dnsRR is one resource record ready to encode.
type dnsRR struct {
	Name  string
	Type  uint16
	Flush bool
	TTL   uint32
	Data  []byte
}

type dnsQuestion struct {
	Name  string
	Type  uint16
	Class uint16
}

// mdnsZone is everything the responder answers for.
type mdnsZone struct {
	Host     string // e.g. "fern-1a2b3c.local."
	IP       net.IP
	Services []mdnsService
}

func encodeDNSName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if label == "" {
			continue
		}
		if len(label) > 63 {
			label = label[:63]
		}
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

// readDNSName reads a possibly compressed name starting at off and returns
// it with a trailing dot, plus the offset just past it.
func readDNSName(msg []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; ; {
		if off >= len(msg) {
			return "", 0, errors.New("dns: name out of range")
		}
		n := int(msg[off])
		switch {
		case n == 0:
			off++
			if end < 0 {
				end = off
			}
			return strings.Join(labels, ".") + ".", end, nil
		case n&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, errors.New("dns: bad pointer")
			}
			if jumps++; jumps > 16 {
				return "", 0, errors.New("dns: pointer loop")
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3FFF)
		case n&0xC0 != 0:
			return "", 0, errors.New("dns: bad label")
		default:
			if off+1+n > len(msg) {
				return "", 0, errors.New("dns: label out of range")
			}
			labels = append(labels, string(msg[off+1:off+1+n]))
			off += 1 + n
		}
	}
}

// parseDNSQuery reads the header and questions of a query. Responses (and
// anything malformed) give ok false.
func parseDNSQuery(msg []byte) (id uint16, qs []dnsQuestion, ok bool) {
	if len(msg) < 12 {
		return 0, nil, false
	}
	id = binary.BigEndian.Uint16(msg[0:])
	flags := binary.BigEndian.Uint16(msg[2:])
	if flags&0x8000 != 0 || flags&0x7800 != 0 { // a response, or not a standard query
		return 0, nil, false
	}
	count := int(binary.BigEndian.Uint16(msg[4:]))
	off := 12
	for i := 0; i < count; i++ {
		name, next, err := readDNSName(msg, off)
		if err != nil || next+4 > len(msg) {
			return 0, nil, false
		}
		qs = append(qs, dnsQuestion{
			Name:  strings.ToLower(name),
			Type:  binary.BigEndian.Uint16(msg[next:]),
			Class: binary.BigEndian.Uint16(msg[next+2:]),
		})
		off = next + 4
	}
	return id, qs, true
}

func (z mdnsZone) ptr(name, target string, ttl uint32) dnsRR {
	return dnsRR{Name: name, Type: dnsTypePTR, TTL: ttl, Data: encodeDNSName(target)}
}

func (z mdnsZone) srv(s mdnsService, ttl uint32) dnsRR {
	data := make([]byte, 6, 6+len(z.Host)+2)
	binary.BigEndian.PutUint16(data[4:], uint16(s.Port))
	data = append(data, encodeDNSName(z.Host)...)
	return dnsRR{Name: s.instanceName(), Type: dnsTypeSRV, Flush: true, TTL: ttl, Data: data}
}

func (z mdnsZone) txt(s mdnsService, ttl uint32) dnsRR {
	var data []byte
	for _, t := range s.TXT {
		if len(t) > 255 {
			t = t[:255]
		}
		data = append(data, byte(len(t)))
		data = append(data, t...)
	}
	if len(data) == 0 {
		data = []byte{0}
	}
	return dnsRR{Name: s.instanceName(), Type: dnsTypeTXT, Flush: true, TTL: ttl, Data: data}
}

func (z mdnsZone) a(ttl uint32) dnsRR {
	return dnsRR{Name: z.Host, Type: dnsTypeA, Flush: true, TTL: ttl, Data: append([]byte{}, z.IP.To4()...)}
}

// all is every record, for announcements, or with goodbye set for saying
// the records are gone (TTL 0).
func (z mdnsZone) all(goodbye bool) []dnsRR {
	host, other := uint32(120), uint32(4500)
	if goodbye {
		host, other = 0, 0
	}
	var out []dnsRR
	for _, s := range z.Services {
		out = append(out, z.ptr(s.typeName(), s.instanceName(), other), z.srv(s, host), z.txt(s, other))
	}
	return append(out, z.a(host))
}

// answer lists the answers and additional records for some questions.
func (z mdnsZone) answer(qs []dnsQuestion) (answers, extra []dnsRR) {
	const ttlHost, ttlOther = 120, 4500
	seen := map[string]bool{}
	add := func(list *[]dnsRR, rr dnsRR) {
		key := strings.ToLower(rr.Name) + "/" + string(rune(rr.Type)) + "/" + string(rr.Data)
		if !seen[key] {
			seen[key] = true
			*list = append(*list, rr)
		}
	}
	host := strings.ToLower(z.Host)
	is := func(q dnsQuestion, t uint16) bool { return q.Type == t || q.Type == dnsTypeANY }
	for _, q := range qs {
		if q.Class&^dnsQU != dnsClassIN && q.Class&^dnsQU != dnsTypeANY {
			continue
		}
		for _, s := range z.Services {
			switch q.Name {
			case strings.ToLower(s.typeName()):
				if is(q, dnsTypePTR) {
					add(&answers, z.ptr(s.typeName(), s.instanceName(), ttlOther))
					add(&extra, z.srv(s, ttlHost))
					add(&extra, z.txt(s, ttlOther))
					add(&extra, z.a(ttlHost))
				}
			case strings.ToLower(s.instanceName()):
				if is(q, dnsTypeSRV) {
					add(&answers, z.srv(s, ttlHost))
					add(&extra, z.a(ttlHost))
				}
				if is(q, dnsTypeTXT) {
					add(&answers, z.txt(s, ttlOther))
				}
			case mdnsServiceEnum:
				if is(q, dnsTypePTR) {
					add(&answers, z.ptr(mdnsServiceEnum, s.typeName(), ttlOther))
				}
			}
		}
		if q.Name == host && is(q, dnsTypeA) {
			add(&answers, z.a(ttlHost))
		}
	}
	// Records already in the answers are not repeated as additional ones.
	kept := extra[:0]
	for _, rr := range extra {
		dup := false
		for _, a := range answers {
			if a.Name == rr.Name && a.Type == rr.Type {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, rr)
		}
	}
	return answers, kept
}

// encodeDNSResponse builds an authoritative response. Questions are echoed
// for legacy unicast replies only.
func encodeDNSResponse(id uint16, qs []dnsQuestion, answers, extra []dnsRR, legacy bool) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:], id)
	binary.BigEndian.PutUint16(msg[2:], 0x8400) // response, authoritative
	if legacy {
		binary.BigEndian.PutUint16(msg[4:], uint16(len(qs)))
		for _, q := range qs {
			msg = append(msg, encodeDNSName(q.Name)...)
			msg = binary.BigEndian.AppendUint16(msg, q.Type)
			msg = binary.BigEndian.AppendUint16(msg, q.Class&^dnsQU)
		}
	}
	binary.BigEndian.PutUint16(msg[6:], uint16(len(answers)))
	binary.BigEndian.PutUint16(msg[10:], uint16(len(extra)))
	for _, list := range [][]dnsRR{answers, extra} {
		for _, rr := range list {
			msg = append(msg, encodeDNSName(rr.Name)...)
			msg = binary.BigEndian.AppendUint16(msg, rr.Type)
			class := uint16(dnsClassIN)
			if rr.Flush && !legacy {
				class |= dnsCacheFlush
			}
			ttl := rr.TTL
			if legacy && ttl > 10 {
				ttl = 10 // RFC 6762 section 6.7
			}
			msg = binary.BigEndian.AppendUint16(msg, class)
			msg = binary.BigEndian.AppendUint32(msg, ttl)
			msg = binary.BigEndian.AppendUint16(msg, uint16(len(rr.Data)))
			msg = append(msg, rr.Data...)
		}
	}
	return msg
}

// mdnsInterfaces lists the IPv4 addresses to join the group on and send
// from: loopback first, then every interface that is up. A variable so tests
// stay on loopback.
var mdnsInterfaces = func() [][4]byte {
	out := [][4]byte{{127, 0, 0, 1}}
	list, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifc := range list {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLoopback() {
					out = append(out, [4]byte(ip4))
				}
			}
		}
	}
	return out
}

// mdnsResponder answers queries on 224.0.0.251:5353 until closed.
type mdnsResponder struct {
	zone   mdnsZone
	conn   *net.UDPConn
	port   int
	ifaces [][4]byte // interface addresses joined, loopback first
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// startMDNS joins the mDNS group on loopback and every IPv4 interface, then
// announces the zone and answers queries for it.
// port is mdnsPort except in tests.
func startMDNS(zone mdnsZone, port int) (*mdnsResponder, error) {
	conn, err := net.ListenMulticastUDP("udp4", nil, &net.UDPAddr{IP: mdnsGroup, Port: port})
	if err != nil {
		return nil, err
	}
	r := &mdnsResponder{zone: zone, conn: conn, port: port, done: make(chan struct{})}
	r.ifaces = mdnsInterfaces()
	if raw, err := conn.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			for _, ifc := range r.ifaces {
				mreq := &syscall.IPMreq{Multiaddr: [4]byte(mdnsGroup.To4()), Interface: ifc}
				// Joining twice (the default interface) fails harmlessly.
				_ = syscall.SetsockoptIPMreq(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
			}
		})
	}
	go r.serve()
	go r.announce()
	return r, nil
}

func (r *mdnsResponder) serve() {
	defer close(r.done)
	buf := make([]byte, 9000)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		id, qs, ok := parseDNSQuery(buf[:n])
		if !ok || len(qs) == 0 {
			continue
		}
		answers, extra := r.zone.answer(qs)
		if len(answers) == 0 {
			continue
		}
		switch {
		case from.Port != r.port: // a plain DNS resolver asking: reply to it alone
			_, _ = r.conn.WriteToUDP(encodeDNSResponse(id, qs, answers, extra, true), from)
		case wantsUnicast(qs):
			_, _ = r.conn.WriteToUDP(encodeDNSResponse(0, nil, answers, extra, false), from)
		default:
			r.multicast(encodeDNSResponse(0, nil, answers, extra, false))
		}
	}
}

func wantsUnicast(qs []dnsQuestion) bool {
	for _, q := range qs {
		if q.Class&dnsQU == 0 {
			return false
		}
	}
	return true
}

// multicast sends msg to the group out of every joined interface, so a
// listener bound to any of them hears it.
func (r *mdnsResponder) multicast(msg []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	group := &net.UDPAddr{IP: mdnsGroup, Port: r.port}
	raw, err := r.conn.SyscallConn()
	if err != nil {
		_, _ = r.conn.WriteToUDP(msg, group)
		return
	}
	for _, ifc := range r.ifaces {
		_ = raw.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInet4Addr(syscall.Handle(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, ifc)
		})
		_, _ = r.conn.WriteToUDP(msg, group)
	}
}

// announce sends the records unprompted a few times after starting, so
// listeners notice Fern without asking (RFC 6762 section 8.3).
func (r *mdnsResponder) announce() {
	for _, wait := range []time.Duration{0, time.Second, 2 * time.Second} {
		select {
		case <-r.done:
			return
		case <-time.After(wait):
		}
		r.multicast(encodeDNSResponse(0, nil, r.zone.all(false), nil, false))
	}
}

// close says goodbye (TTL 0, section 10.1) and stops answering.
func (r *mdnsResponder) close() {
	r.multicast(encodeDNSResponse(0, nil, r.zone.all(true), nil, false))
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.conn.Close()
	<-r.done
}
