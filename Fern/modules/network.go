package modules

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Network traffic comes from GetIfTable2, which returns every interface's byte
// counters in one call. A sample every second costs one syscall and no
// dependency, and the rates are worked out from the counter deltas.
var (
	iphlpapi        = syscall.NewLazyDLL("iphlpapi.dll")
	procGetIfTable2 = iphlpapi.NewProc("GetIfTable2")
	procFreeMibTbl  = iphlpapi.NewProc("FreeMibTable")
)

// mibIfRow2 mirrors MIB_IF_ROW2 (1352 bytes on 64-bit Windows).
type mibIfRow2 struct {
	InterfaceLuid            uint64
	InterfaceIndex           uint32
	InterfaceGuid            [16]byte
	Alias                    [257]uint16
	Description              [257]uint16
	PhysicalAddressLength    uint32
	PhysicalAddress          [32]byte
	PermanentPhysicalAddress [32]byte
	Mtu                      uint32
	Type                     uint32
	TunnelType               uint32
	MediaType                uint32
	PhysicalMediumType       uint32
	AccessType               uint32
	DirectionType            uint32
	Flags                    uint8 // HardwareInterface, FilterInterface, ...
	OperStatus               uint32
	AdminStatus              uint32
	MediaConnectState        uint32
	NetworkGuid              [16]byte
	ConnectionType           uint32
	TransmitLinkSpeed        uint64 // bits per second
	ReceiveLinkSpeed         uint64
	InOctets                 uint64
	InUcastPkts              uint64
	InNUcastPkts             uint64
	InDiscards               uint64
	InErrors                 uint64
	InUnknownProtos          uint64
	InUcastOctets            uint64
	InMulticastOctets        uint64
	InBroadcastOctets        uint64
	OutOctets                uint64
	OutUcastPkts             uint64
	OutNUcastPkts            uint64
	OutDiscards              uint64
	OutErrors                uint64
	OutUcastOctets           uint64
	OutMulticastOctets       uint64
	OutBroadcastOctets       uint64
	OutQLen                  uint64
}

const (
	ifFlagHardware   = 0x1
	ifFlagFilter     = 0x2
	ifOperStatusUp   = 1
	ifTypeLoopback   = 24
	ifTypeTunnel     = 131
	netSampleSpacing = time.Second
)

// ifCounters is one interface as read from the table.
type ifCounters struct {
	Name     string // alias, e.g. "Ethernet" or "Wi-Fi"
	Desc     string
	In, Out  uint64 // bytes since the adapter came up
	RxLink   uint64 // bits per second
	TxLink   uint64
	Hardware bool
}

func readInterfaces() ([]ifCounters, error) {
	if err := procGetIfTable2.Find(); err != nil {
		return nil, err
	}
	var table unsafe.Pointer // allocated by Windows, freed with FreeMibTable
	if r, _, _ := procGetIfTable2.Call(uintptr(unsafe.Pointer(&table))); r != 0 {
		return nil, syscall.Errno(r)
	}
	defer procFreeMibTbl.Call(uintptr(table))
	count := *(*uint32)(table)
	// The rows start after NumEntries, aligned to 8 bytes.
	rows := unsafe.Slice((*mibIfRow2)(unsafe.Add(table, 8)), count)
	out := make([]ifCounters, 0, 4)
	for i := range rows {
		r := &rows[i]
		if r.OperStatus != ifOperStatusUp || r.Type == ifTypeLoopback || r.Type == ifTypeTunnel {
			continue
		}
		// Filter interfaces (QoS, WFP, VPN lightweight filters) repeat the
		// traffic of the adapter below them.
		if r.Flags&ifFlagFilter != 0 {
			continue
		}
		out = append(out, ifCounters{
			Name:     syscall.UTF16ToString(r.Alias[:]),
			Desc:     syscall.UTF16ToString(r.Description[:]),
			In:       r.InOctets,
			Out:      r.OutOctets,
			RxLink:   r.ReceiveLinkSpeed,
			TxLink:   r.TransmitLinkSpeed,
			Hardware: r.Flags&ifFlagHardware != 0,
		})
	}
	return out, nil
}

// NetAdapter is one adapter's session numbers, as shown in the panel.
type NetAdapter struct {
	Name      string  `json:"name"`
	Desc      string  `json:"desc"`
	Hardware  bool    `json:"hardware"`
	Down      float64 `json:"down"` // bytes per second
	Up        float64 `json:"up"`
	PeakDown  float64 `json:"peakDown"`
	PeakUp    float64 `json:"peakUp"`
	TotalDown uint64  `json:"totalDown"` // bytes since Fern started
	TotalUp   uint64  `json:"totalUp"`
	RxLink    uint64  `json:"rxLink"` // bits per second
	TxLink    uint64  `json:"txLink"`

	lastIn, lastOut uint64
	seenAt          time.Time
}

// netTracker keeps per-adapter session numbers. It is fed counter readings and
// does no I/O itself, so it can be tested.
type netTracker struct {
	adapters map[string]*NetAdapter
	busiest  string
	at       time.Time
}

func newNetTracker() *netTracker { return &netTracker{adapters: map[string]*NetAdapter{}} }

func (t *netTracker) update(now time.Time, list []ifCounters) {
	elapsed := now.Sub(t.at).Seconds()
	first := t.at.IsZero()
	t.at = now
	bestScore := -1.0
	for _, c := range list {
		a, ok := t.adapters[c.Name]
		if !ok {
			a = &NetAdapter{Name: c.Name, lastIn: c.In, lastOut: c.Out}
			t.adapters[c.Name] = a
		}
		a.Desc, a.Hardware, a.RxLink, a.TxLink = c.Desc, c.Hardware, c.RxLink, c.TxLink
		// A counter going backwards means the adapter was reset: start again
		// from the new reading instead of adding a huge wrapped delta.
		var dIn, dOut uint64
		if c.In >= a.lastIn {
			dIn = c.In - a.lastIn
		}
		if c.Out >= a.lastOut {
			dOut = c.Out - a.lastOut
		}
		a.lastIn, a.lastOut = c.In, c.Out
		a.TotalDown += dIn
		a.TotalUp += dOut
		if ok && !first && elapsed > 0 {
			a.Down = float64(dIn) / elapsed
			a.Up = float64(dOut) / elapsed
			a.PeakDown = math.Max(a.PeakDown, a.Down)
			a.PeakUp = math.Max(a.PeakUp, a.Up)
		}
		a.seenAt = now
		// Busiest physical adapter right now; ties go to the one that has moved
		// the most data this session, so an idle moment does not flip it.
		score := a.Down + a.Up + float64(a.TotalDown+a.TotalUp)/1e12
		if !c.Hardware {
			score -= 1e15 // virtual adapters only when nothing physical is up
		}
		if score > bestScore {
			bestScore, t.busiest = score, c.Name
		}
	}
	for name, a := range t.adapters {
		if a.seenAt != now {
			// Gone (unplugged, disconnected): keep its totals but no live rate.
			a.Down, a.Up = 0, 0
			if now.Sub(a.seenAt) > time.Hour {
				delete(t.adapters, name)
			}
		}
	}
}

// pick returns the adapter named by the user, or the busiest one.
func (t *netTracker) pick(name string) (NetAdapter, bool) {
	if name != "" {
		if a, ok := t.adapters[name]; ok {
			return *a, true
		}
	}
	if a, ok := t.adapters[t.busiest]; ok {
		return *a, true
	}
	return NetAdapter{}, false
}

var (
	netMu    sync.Mutex
	netState = newNetTracker()
	netErr   string
)

func sampleNetwork() {
	list, err := readInterfaces()
	netMu.Lock()
	defer netMu.Unlock()
	if err != nil {
		netErr = err.Error()
		return
	}
	netErr = ""
	netState.update(time.Now(), list)
}

func startNetworkWorker() {
	go func() {
		sampleNetwork()
		ticker := time.NewTicker(netSampleSpacing)
		defer ticker.Stop()
		for range ticker.C {
			sampleNetwork()
		}
	}()
}

// NetAdapters lists the adapters seen this session for the panel, busiest first.
func NetAdapters() (list []NetAdapter, busiest string) {
	netMu.Lock()
	defer netMu.Unlock()
	for _, a := range netState.adapters {
		list = append(list, *a)
	}
	busiest = netState.busiest
	slices.SortFunc(list, func(a, b NetAdapter) int {
		if (a.Name == busiest) != (b.Name == busiest) {
			if a.Name == busiest {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return list, busiest
}

// CurrentNetAdapter is the adapter the network line reports on.
func CurrentNetAdapter(name string) (NetAdapter, bool) {
	netMu.Lock()
	defer netMu.Unlock()
	return netState.pick(name)
}

// networkLine renders the network module line.
func networkLine(cfg Config) string {
	o := cfg.NetworkOptions
	a, ok := CurrentNetAdapter(o.Adapter)
	if !ok {
		return ""
	}
	return formatNetwork(o, a)
}

func formatNetwork(o NetworkOptions, a NetAdapter) string {
	small := o.SmallLabels
	rate := func(bytesPerSec float64) string { return formatRate(bytesPerSec, o.Bytes, small) }
	size := func(bytes uint64) string { return formatSize(float64(bytes), small) }
	tag := func(word, arrow string) string {
		if small {
			return Superscript(word) + arrow
		}
		return word + " " + arrow
	}
	var parts []string
	var now []string
	if o.ShowDown {
		now = append(now, "↓"+spaceUnless(small)+rate(a.Down))
	}
	if o.ShowUp {
		now = append(now, "↑"+spaceUnless(small)+rate(a.Up))
	}
	if len(now) > 0 {
		parts = append(parts, strings.Join(now, " "))
	}
	if o.ShowPeakDown {
		parts = append(parts, tag("peak", "↓")+spaceUnless(small)+rate(a.PeakDown))
	}
	if o.ShowPeakUp {
		parts = append(parts, tag("peak", "↑")+spaceUnless(small)+rate(a.PeakUp))
	}
	if o.ShowTotalDown {
		parts = append(parts, tag("total", "↓")+spaceUnless(small)+size(a.TotalDown))
	}
	if o.ShowTotalUp {
		parts = append(parts, tag("total", "↑")+spaceUnless(small)+size(a.TotalUp))
	}
	if o.ShowUtil {
		if pct, ok := utilisation(a, o.UtilVsPeak); ok {
			text := fmt.Sprintf("%.0f%%", pct)
			if !small {
				text += " used"
			}
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return withLabel(o.Label, strings.Join(parts, " · "))
}

func spaceUnless(small bool) string {
	if small {
		return ""
	}
	return " "
}

// utilisation is the busier direction as a share of the link speed, or of the
// fastest rate seen this session when vsPeak is set. A gigabit port reports
// 1000 Mbps whatever the internet line behind it does, so the peak can be the
// more honest yardstick.
func utilisation(a NetAdapter, vsPeak bool) (float64, bool) {
	var down, up float64
	if vsPeak {
		if a.PeakDown > 0 {
			down = a.Down / a.PeakDown
		}
		if a.PeakUp > 0 {
			up = a.Up / a.PeakUp
		}
		if a.PeakDown == 0 && a.PeakUp == 0 {
			return 0, false
		}
	} else {
		if a.RxLink == 0 && a.TxLink == 0 {
			return 0, false
		}
		if a.RxLink > 0 {
			down = a.Down * 8 / float64(a.RxLink)
		}
		if a.TxLink > 0 {
			up = a.Up * 8 / float64(a.TxLink)
		}
	}
	return math.Min(100, math.Max(down, up)*100), true
}

// formatRate picks the unit so the number stays short: 840 kbps, 12.4 Mbps,
// 1.2 Gbps, or the byte equivalents.
func formatRate(bytesPerSec float64, bytes, small bool) string {
	v := bytesPerSec * 8
	units := []string{"bps", "kbps", "Mbps", "Gbps"}
	if bytes {
		v = bytesPerSec
		units = []string{"B/s", "KB/s", "MB/s", "GB/s"}
	}
	return scaled(v, units, small)
}

// formatSize shows a byte count as KB, MB, GB or TB.
func formatSize(bytes float64, small bool) string {
	return scaled(bytes, []string{"B", "KB", "MB", "GB", "TB"}, small)
}

func scaled(v float64, units []string, small bool) string {
	i := 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	num := fmt.Sprintf("%.1f", v)
	if i == 0 || v >= 10 {
		num = fmt.Sprintf("%.0f", v)
	}
	if small {
		return num + Superscript(units[i])
	}
	return num + " " + units[i]
}

var superscripts = map[rune]rune{
	'a': 'ᵃ', 'b': 'ᵇ', 'c': 'ᶜ', 'd': 'ᵈ', 'e': 'ᵉ', 'f': 'ᶠ', 'g': 'ᵍ', 'h': 'ʰ',
	'i': 'ⁱ', 'j': 'ʲ', 'k': 'ᵏ', 'l': 'ˡ', 'm': 'ᵐ', 'n': 'ⁿ', 'o': 'ᵒ', 'p': 'ᵖ',
	'r': 'ʳ', 's': 'ˢ', 't': 'ᵗ', 'u': 'ᵘ', 'v': 'ᵛ', 'w': 'ʷ', 'x': 'ˣ', 'y': 'ʸ',
	'z': 'ᶻ', 'A': 'ᴬ', 'B': 'ᴮ', 'D': 'ᴰ', 'E': 'ᴱ', 'G': 'ᴳ', 'H': 'ᴴ', 'I': 'ᴵ',
	'J': 'ᴶ', 'K': 'ᴷ', 'L': 'ᴸ', 'M': 'ᴹ', 'N': 'ᴺ', 'O': 'ᴼ', 'P': 'ᴾ', 'R': 'ᴿ',
	'T': 'ᵀ', 'U': 'ᵁ', 'V': 'ⱽ', 'W': 'ᵂ', '0': '⁰', '1': '¹', '2': '²', '3': '³',
	'4': '⁴', '5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹', '+': '⁺', '-': '⁻',
}

// Superscript raises the letters that have a small form; others stay as they are.
func Superscript(s string) string {
	var b strings.Builder
	for _, r := range s {
		if up, ok := superscripts[r]; ok {
			b.WriteRune(up)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
