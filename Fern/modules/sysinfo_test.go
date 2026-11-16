package modules

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"
	"time"
)

func TestFormatUptime(t *testing.T) {
	d := 3*24*time.Hour + 4*time.Hour + 5*time.Minute + 6*time.Second
	cases := []struct {
		d     time.Duration
		style string
		units int
		want  string
	}{
		{d, UptimeShort, 2, "3d 4h"},
		{d, UptimeShort, 3, "3d 4h 5m"},
		{d, UptimeShort, 4, "3d 4h 5m 6s"},
		{d, UptimeLong, 2, "3 days 4 hours"},
		{time.Hour + time.Minute, UptimeLong, 2, "1 hour 1 minute"},
		{d, UptimeClock, 2, "76:05"},
		{5*time.Minute + 6*time.Second, UptimeShort, 2, "5m"},
		{5*time.Minute + 6*time.Second, UptimeShort, 3, "5m 6s"},
		{42 * time.Second, UptimeShort, 2, "42s"},
		{0, UptimeShort, 2, "0s"},
		{-time.Second, UptimeShort, 2, "0s"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d, c.style, c.units); got != c.want {
			t.Errorf("formatUptime(%v, %s, %d) = %q, want %q", c.d, c.style, c.units, got, c.want)
		}
	}
}

func TestUptimeText(t *testing.T) {
	o := defaultUptimeOptions()
	o.ShowFern = true
	if got := uptimeText(o, 26*time.Hour, 90*time.Minute); got != "PC up 1d 2h · Fern 1h 30m" {
		t.Errorf("text = %q", got)
	}
	o.ShowPC, o.Label = false, "⏱"
	if got := uptimeText(o, 26*time.Hour, 90*time.Minute); got != "⏱ Fern 1h 30m" {
		t.Errorf("fern only = %q", got)
	}
	o.ShowFern = false
	if got := uptimeText(o, time.Hour, time.Hour); got != "" {
		t.Errorf("nothing on = %q", got)
	}
}

func TestDiskText(t *testing.T) {
	const gb = 1 << 30
	disks := []DiskInfo{
		{Drive: "C:", Total: 931 * gb, Free: 519 * gb, Fixed: true},
		{Drive: "D:", Total: 3726 * gb, Free: 1000 * gb, Fixed: true},
	}
	o := defaultDiskOptions()
	if got := diskText(disks, o, "C:"); got != "C: 412/931 GB · 44%" {
		t.Errorf("system drive = %q", got)
	}
	o.Drives = []string{"D:", "Z:", "C:"}
	o.ShowFree, o.ShowPercent = true, false
	if got := diskText(disks, o, "C:"); got != "D: 2.7/3.6 TB · 1 TB free | C: 412/931 GB · 519 GB free" {
		t.Errorf("two drives = %q", got)
	}
	o.ShowUsed, o.ShowTotal, o.ShowFree, o.ShowLetter = false, false, false, false
	if got := diskText(disks, o, "C:"); got != "" {
		t.Errorf("nothing on = %q", got)
	}
}

func TestPowerFromSystemStatus(t *testing.T) {
	p := powerFrom(systemPowerStatus{ACLineStatus: 0, BatteryFlag: 1, BatteryLifePercent: 76, BatteryLifeTime: 7800})
	if !p.HasBattery || p.OnAC || p.Charging || p.Percent != 76 || p.SecondsLeft != 7800 {
		t.Errorf("on battery = %+v", p)
	}
	p = powerFrom(systemPowerStatus{ACLineStatus: 1, BatteryFlag: 8, BatteryLifePercent: 50, BatteryLifeTime: 0xFFFFFFFF, SystemStatusFlag: 1})
	if !p.OnAC || !p.Charging || p.SecondsLeft != -1 || !p.Saver {
		t.Errorf("charging = %+v", p)
	}
	p = powerFrom(systemPowerStatus{ACLineStatus: 1, BatteryFlag: 128, BatteryLifePercent: 255})
	if p.HasBattery || p.Percent != -1 {
		t.Errorf("desktop = %+v", p)
	}
}

func TestPCBatteryText(t *testing.T) {
	o := defaultPCBatteryOptions()
	cases := []struct {
		p    PowerStatus
		want string
	}{
		{PowerStatus{HasBattery: true, Percent: 76, SecondsLeft: 7800}, "🔋 76% · 2h 10m left"},
		{PowerStatus{HasBattery: true, Percent: 12, SecondsLeft: -1}, "🪫 12%"},
		{PowerStatus{HasBattery: true, OnAC: true, Charging: true, Percent: 12, SecondsLeft: 600}, "🔋 12%⚡"},
		{PowerStatus{HasBattery: true, OnAC: true, Percent: 100, SecondsLeft: -1}, "🔋 100%🔌"},
		{PowerStatus{HasBattery: false, Percent: -1}, ""},
	}
	for _, c := range cases {
		if got := pcBatteryText(c.p, o); got != c.want {
			t.Errorf("pcBatteryText(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
	o.HideOnAC = true
	if got := pcBatteryText(PowerStatus{HasBattery: true, OnAC: true, Percent: 90}, o); got != "" {
		t.Errorf("hidden on AC = %q", got)
	}
}

func TestPingOnceFallsBackToTCP(t *testing.T) {
	oldICMP, oldTCP := pingICMP, pingTCP
	t.Cleanup(func() { pingICMP, pingTCP = oldICMP, oldTCP })
	var used []string
	pingICMP = func(net.IP, time.Duration) (time.Duration, error) {
		used = append(used, "icmp")
		return 0, errors.New("no answer")
	}
	pingTCP = func(_ net.IP, port int, _ time.Duration) (time.Duration, error) {
		used = append(used, "tcp")
		if port != 443 {
			t.Errorf("port = %d", port)
		}
		return 23 * time.Millisecond, nil
	}
	o := defaultPingOptions()
	ip := net.ParseIP("192.0.2.1")
	if ms, method := pingOnce(o, ip); ms != 23 || method != PingTCP || !slices.Equal(used, []string{"icmp", "tcp"}) {
		t.Errorf("auto = %d %s %v", ms, method, used)
	}
	used = nil
	o.Method = PingICMP
	if ms, _ := pingOnce(o, ip); ms != -1 || !slices.Equal(used, []string{"icmp"}) {
		t.Errorf("icmp only = %d %v", ms, used)
	}
	used = nil
	o.Method = PingAuto
	if ms, method := pingOnce(o, net.ParseIP("2001:db8::1")); ms != 23 || method != PingTCP || !slices.Equal(used, []string{"tcp"}) {
		t.Errorf("ipv6 = %d %s %v", ms, method, used)
	}
	pingICMP = func(net.IP, time.Duration) (time.Duration, error) { return 300 * time.Microsecond, nil }
	if ms, method := pingOnce(o, ip); ms != 1 || method != PingICMP {
		t.Errorf("sub-millisecond = %d %s", ms, method)
	}
}

func TestPingWindowAndText(t *testing.T) {
	t.Cleanup(func() {
		pingMu.Lock()
		pingKey, pingWindow, pingState = "", nil, PingStatus{MS: -1, AvgMS: -1}
		pingMu.Unlock()
	})
	o := defaultPingOptions()
	o.ShowAvg, o.ShowLoss = true, true
	recordPing("1.1.1.1", "1.1.1.1", PingICMP, 10, "")
	if got := pingText(CurrentPing(), o); got != "ᵖⁱⁿᵍ 10 ms" {
		t.Errorf("first = %q", got)
	}
	recordPing("1.1.1.1", "1.1.1.1", PingICMP, 20, "")
	recordPing("1.1.1.1", "1.1.1.1", PingICMP, -1, "")
	recordPing("1.1.1.1", "1.1.1.1", PingICMP, 30, "")
	st := CurrentPing()
	if st.AvgMS != 20 || st.Loss != 25 || st.Samples != 4 {
		t.Errorf("window = %+v", st)
	}
	if got := pingText(st, o); got != "ᵖⁱⁿᵍ 30 ms · avg 20 ms · 25% loss" {
		t.Errorf("text = %q", got)
	}
	recordPing("1.1.1.1", "1.1.1.1", PingICMP, -1, "")
	if got := pingText(CurrentPing(), defaultPingOptions()); got != "ᵖⁱⁿᵍ timeout" {
		t.Errorf("timeout = %q", got)
	}
	// A new host starts a new window.
	recordPing("example.test", "192.0.2.9", PingTCP, 40, "")
	if st := CurrentPing(); st.Samples != 1 || st.AvgMS != 40 {
		t.Errorf("new host = %+v", st)
	}
	recordPing("nope.test", "", "", -1, "can't find nope.test")
	if got := pingText(CurrentPing(), o); got != "" {
		t.Errorf("lookup error = %q", got)
	}
	for i := 0; i < 30; i++ {
		recordPing("1.1.1.1", "1.1.1.1", PingICMP, i, "")
	}
	if st := CurrentPing(); st.Samples != pingHistory {
		t.Errorf("window grew to %d", st.Samples)
	}
}

func TestResolvePingPrefersIPv4(t *testing.T) {
	old := pingDNS
	t.Cleanup(func() { pingDNS = old })
	pingDNS = func(_ context.Context, host string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("2001:db8::5"), net.ParseIP("192.0.2.5")}, nil
	}
	if ip, err := resolvePing("example.test"); err != nil || ip.String() != "192.0.2.5" {
		t.Errorf("resolve = %v %v", ip, err)
	}
	if ip, err := resolvePing("[2001:db8::7]"); err != nil || ip.String() != "2001:db8::7" {
		t.Errorf("literal = %v %v", ip, err)
	}
	pingDNS = func(context.Context, string) ([]net.IP, error) { return nil, errors.New("nxdomain") }
	if _, err := resolvePing("nope.test"); err == nil {
		t.Error("resolved a missing host")
	}
}

func TestSystemLinesSanitize(t *testing.T) {
	c := DefaultConfig()
	c.sanitize()
	def := DefaultConfig()
	if c.SensorOptions.LHMURL != DefaultLHMURL || c.SensorOptions.Template != def.SensorOptions.Template ||
		c.PingOptions.Host != "1.1.1.1" || c.PingOptions.IntervalSec != 5 || c.UptimeOptions.Units != 2 ||
		c.DiskOptions.Separator != " | " || c.PCBatteryOptions.LowThreshold != 20 {
		t.Errorf("defaults changed by sanitize: %+v %+v %+v", c.SensorOptions, c.PingOptions, c.UptimeOptions)
	}

	c.SensorOptions = SensorOptions{Source: "magic", LHMURL: "file:///etc/passwd", Template: "  ", Separator: ""}
	for i := 0; i < 20; i++ {
		c.SensorOptions.Picks = append(c.SensorOptions.Picks, SensorPick{Key: string(rune('a' + i))})
	}
	c.SensorOptions.Picks = append(c.SensorOptions.Picks, SensorPick{Key: "a"}, SensorPick{Key: " "})
	c.PingOptions = PingOptions{Host: "bad host/x", Method: "smoke", Port: 70000, IntervalSec: 1}
	c.UptimeOptions = UptimeOptions{Style: "weird", Units: 9}
	c.DiskOptions = DiskOptions{Drives: []string{"c", "C:\\", "d:", "nope", "1"}}
	c.PCBatteryOptions = PCBatteryOptions{LowThreshold: 300}
	c.sanitize()

	s := c.SensorOptions
	if s.Source != SensorSourceAuto || s.LHMURL != DefaultLHMURL || s.Template != "{name} {value}" || s.Separator != " · " || len(s.Picks) != maxSensorPicks {
		t.Errorf("sensor options = %+v", s)
	}
	p := c.PingOptions
	if p.Host != "" || p.Method != PingAuto || p.Port != 443 || p.IntervalSec != 2 {
		t.Errorf("ping options = %+v", p)
	}
	if u := c.UptimeOptions; u.Style != UptimeShort || u.Units != 4 {
		t.Errorf("uptime options = %+v", u)
	}
	if d := c.DiskOptions; !slices.Equal(d.Drives, []string{"C:", "D:"}) || d.Separator != " | " {
		t.Errorf("disk options = %+v", d)
	}
	if c.PCBatteryOptions.LowThreshold != 100 {
		t.Errorf("battery threshold = %d", c.PCBatteryOptions.LowThreshold)
	}
	c.SensorOptions.LHMURL = "http://192.168.1.20:8085/data.json"
	c.sanitize()
	if c.SensorOptions.LHMURL != "http://192.168.1.20:8085/data.json" {
		t.Errorf("a LAN address was dropped: %q", c.SensorOptions.LHMURL)
	}
}

func TestNewLinesAreKnownEverywhere(t *testing.T) {
	for _, id := range []string{ModuleSensors, ModuleUptime, ModulePing, ModuleDisk, ModulePCBattery} {
		if !knownModules[id] {
			t.Errorf("%s is not in DefaultOrder", id)
		}
		if !slices.Contains(DefaultDropOrder, id) {
			t.Errorf("%s is not in DefaultDropOrder", id)
		}
		var d DisplayOptions
		if moduleFlag(&d, id) == nil {
			t.Errorf("%s has no switch", id)
		}
		if LineLabel(id) == id {
			t.Errorf("%s has no label", id)
		}
	}
}

func TestSensorSourceStaysWithThePC(t *testing.T) {
	cur := DefaultConfig()
	cur.SensorOptions.Source = SensorSourceHWiNFO
	cur.SensorOptions.LHMURL = "http://10.0.0.2:8085/data.json"
	cur.SensorOptions.Picks = []SensorPick{{Key: "x"}}
	snap, err := profileSettings(cur)
	if err != nil {
		t.Fatal(err)
	}
	other := DefaultConfig()
	next, err := applyProfileSettings(other, snap)
	if err != nil {
		t.Fatal(err)
	}
	if next.SensorOptions.Source != SensorSourceAuto || next.SensorOptions.LHMURL != DefaultLHMURL {
		t.Errorf("the source came along with the profile: %+v", next.SensorOptions)
	}
	if len(next.SensorOptions.Picks) != 1 {
		t.Errorf("picks did not come along: %+v", next.SensorOptions.Picks)
	}
}

func TestSystemRuleTriggers(t *testing.T) {
	oldPower := readPower
	t.Cleanup(func() { readPower = oldPower })
	trig := lookupTrigger(TriggerOnBattery)
	if trig == nil || trig.Check == nil {
		t.Fatal("on battery trigger missing")
	}
	readPower = func() PowerStatus { return PowerStatus{HasBattery: true, Percent: 30} }
	if !trig.Check(RuleParams{"below": 0.0}) || trig.Check(RuleParams{"below": 20.0}) || !trig.Check(RuleParams{"below": 40.0}) {
		t.Error("on battery checks wrong")
	}
	readPower = func() PowerStatus { return PowerStatus{HasBattery: true, OnAC: true, Percent: 30} }
	if trig.Check(RuleParams{"below": 0.0}) {
		t.Error("on battery while plugged in")
	}

	t.Cleanup(func() {
		pingMu.Lock()
		pingKey, pingWindow, pingState = "", nil, PingStatus{MS: -1, AvgMS: -1}
		pingMu.Unlock()
	})
	ping := lookupTrigger(TriggerPing)
	recordPing("h", "192.0.2.1", PingICMP, 200, "")
	if !ping.Check(RuleParams{"ms": 150.0, "timeouts": true}) || ping.Check(RuleParams{"ms": 250.0}) {
		t.Error("slow ping checks wrong")
	}
	recordPing("h", "192.0.2.1", PingICMP, -1, "")
	if !ping.Check(RuleParams{"ms": 150.0, "timeouts": true}) || ping.Check(RuleParams{"ms": 150.0, "timeouts": false}) {
		t.Error("timeout checks wrong")
	}

	sensorMu.Lock()
	oldList := sensorList
	sensorList = []Sensor{{Key: "k", Kind: HWCPU, Hardware: "CPU", Name: "CPU Package", Type: SensorTemp, Value: 85}}
	sensorMu.Unlock()
	t.Cleanup(func() { sensorMu.Lock(); sensorList = oldList; sensorMu.Unlock() })
	if s := lookupTrigger(TriggerSensor); !s.Check(RuleParams{"sensor": "k", "op": "gt", "value": 80.0}) || s.Check(RuleParams{"sensor": "k", "op": "lt", "value": 80.0}) {
		t.Error("sensor trigger checks wrong")
	}
	if hot := lookupTrigger(TriggerHot); !hot.Check(RuleParams{"part": "cpu", "op": "gt", "value": 80.0}) {
		t.Error("cpu temperature trigger checks wrong")
	}
	if opts := RuleSources()[SourceSensors]; len(opts) != 1 || opts[0].Value != "k" {
		t.Errorf("sensor source = %+v", opts)
	}
}
