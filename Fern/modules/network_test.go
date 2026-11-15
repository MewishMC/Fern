package modules

import (
	"testing"
	"time"
	"unsafe"
)

func TestMibIfRow2Layout(t *testing.T) {
	var r mibIfRow2
	if got := unsafe.Sizeof(r); got != 1352 {
		t.Errorf("sizeof MIB_IF_ROW2 = %d, want 1352", got)
	}
	checks := map[string][2]uintptr{
		"Description":       {unsafe.Offsetof(r.Description), 542},
		"Flags":             {unsafe.Offsetof(r.Flags), 1152},
		"OperStatus":        {unsafe.Offsetof(r.OperStatus), 1156},
		"TransmitLinkSpeed": {unsafe.Offsetof(r.TransmitLinkSpeed), 1192},
		"ReceiveLinkSpeed":  {unsafe.Offsetof(r.ReceiveLinkSpeed), 1200},
		"InOctets":          {unsafe.Offsetof(r.InOctets), 1208},
		"OutOctets":         {unsafe.Offsetof(r.OutOctets), 1280},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("offset of %s = %d, want %d", name, c[0], c[1])
		}
	}
}

func TestReadInterfaces(t *testing.T) {
	list, err := readInterfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		if c.Name == "" {
			t.Errorf("interface without a name: %+v", c)
		}
	}
}

func TestNetTrackerRatesPeaksAndTotals(t *testing.T) {
	tr := newNetTracker()
	t0 := time.Unix(1000, 0)
	eth := func(in, out uint64) []ifCounters {
		return []ifCounters{
			{Name: "Ethernet", In: in, Out: out, RxLink: 1e9, TxLink: 1e9, Hardware: true},
			{Name: "vEthernet", In: 5e9, Out: 5e9, Hardware: false},
		}
	}
	tr.update(t0, eth(1_000_000, 500_000))
	tr.update(t0.Add(2*time.Second), eth(3_000_000, 700_000))
	a, ok := tr.pick("")
	if !ok || a.Name != "Ethernet" {
		t.Fatalf("auto pick = %+v, %v", a.Name, ok)
	}
	if a.Down != 1_000_000 || a.Up != 100_000 {
		t.Errorf("rates = %v/%v", a.Down, a.Up)
	}
	tr.update(t0.Add(3*time.Second), eth(3_500_000, 700_000))
	a, _ = tr.pick("")
	if a.PeakDown != 1_000_000 || a.Down != 500_000 {
		t.Errorf("peak %v, now %v", a.PeakDown, a.Down)
	}
	if a.TotalDown != 2_500_000 || a.TotalUp != 200_000 {
		t.Errorf("totals %v/%v", a.TotalDown, a.TotalUp)
	}
	// Counter reset: no huge wrap-around delta.
	tr.update(t0.Add(4*time.Second), eth(100, 100))
	a, _ = tr.pick("")
	if a.TotalDown != 2_500_000 || a.Down != 0 {
		t.Errorf("after reset: total %v, rate %v", a.TotalDown, a.Down)
	}
	if v, ok := tr.pick("vEthernet"); !ok || v.Name != "vEthernet" {
		t.Errorf("named pick = %+v", v)
	}
	if v, _ := tr.pick("missing"); v.Name != "Ethernet" {
		t.Errorf("missing adapter should fall back to auto, got %q", v.Name)
	}
}

func TestFormatNetwork(t *testing.T) {
	a := NetAdapter{Down: 1_550_000, Up: 12_000, PeakDown: 3_100_000, TotalDown: 2_400_000_000, RxLink: 100_000_000, TxLink: 100_000_000}
	o := NetworkOptions{ShowDown: true, ShowUp: true, ShowUtil: true}
	if got, want := formatNetwork(o, a), "↓ 12 Mbps ↑ 96 kbps · 12% used"; got != want {
		t.Errorf("plain = %q, want %q", got, want)
	}
	o.SmallLabels = true
	if got, want := formatNetwork(o, a), "↓12ᴹᵇᵖˢ ↑96ᵏᵇᵖˢ · 12%"; got != want {
		t.Errorf("small = %q, want %q", got, want)
	}
	o = NetworkOptions{ShowPeakDown: true, ShowTotalDown: true, Bytes: true, UtilVsPeak: true, ShowUtil: true, Label: "net"}
	if got, want := formatNetwork(o, a), "net peak ↓ 3.1 MB/s · total ↓ 2.4 GB · 50% used"; got != want {
		t.Errorf("bytes = %q, want %q", got, want)
	}
	if got := formatNetwork(NetworkOptions{}, a); got != "" {
		t.Errorf("nothing enabled = %q", got)
	}
	if got := formatRate(0, false, false); got != "0 bps" {
		t.Errorf("zero rate = %q", got)
	}
}
