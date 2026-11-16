package modules

import (
	"net/url"
	"strings"
)

// Options for the hardware sensor line and the system lines (uptime, ping,
// disk, laptop battery). The CPU and GPU lines keep their own options in
// config.go and only gain a HWPicks for the sensors they read.

// Where sensor readings come from.
const (
	SensorSourceAuto   = "auto"   // HWiNFO, then LibreHardwareMonitor's web server, then its WMI
	SensorSourceHWiNFO = "hwinfo" // HWiNFO's shared memory
	SensorSourceLHM    = "lhm"    // LibreHardwareMonitor, web server first, then WMI
	SensorSourceOff    = "off"
)

// DefaultLHMURL is LibreHardwareMonitor's web server on its default port.
const DefaultLHMURL = "http://localhost:8085/data.json"

const (
	maxSensorPicks  = 16
	maxSensorKeyLen = 200
)

// HWPicks names the sensors a line reads, by Sensor.Key. Empty picks one
// automatically.
type HWPicks struct {
	Temp  string `json:"temp"`
	Power string `json:"power"`
	Clock string `json:"clock"`
	Fan   string `json:"fan"`
}

func (p *HWPicks) sanitize() {
	for _, s := range []*string{&p.Temp, &p.Power, &p.Clock, &p.Fan} {
		*s = RuneLimit(strings.TrimSpace(*s), maxSensorKeyLen)
	}
}

// SensorPick is one sensor on the sensors line.
type SensorPick struct {
	Key  string `json:"key"`
	Name string `json:"name"` // shown as {name}; empty uses the sensor's own name
}

// SensorOptions controls where sensors are read from and the sensors line.
type SensorOptions struct {
	Source     string       `json:"source"`
	LHMURL     string       `json:"lhm_url"`
	Label      string       `json:"label"`
	Template   string       `json:"template"` // per sensor: {name} {value} {num} {unit} {min} {max} {hardware}
	Separator  string       `json:"separator"`
	Fahrenheit bool         `json:"fahrenheit"`
	Picks      []SensorPick `json:"picks"`
}

// UptimeOptions controls the uptime line.
type UptimeOptions struct {
	Label    string `json:"label"`
	ShowPC   bool   `json:"show_pc"`
	PCText   string `json:"pc_text"`
	ShowFern bool   `json:"show_fern"`
	FernText string `json:"fern_text"`
	Style    string `json:"style"` // short "3d 4h", long "3 days 4 hours", clock "76:04"
	Units    int    `json:"units"` // how many units short and long show
}

// Uptime styles.
const (
	UptimeShort = "short"
	UptimeLong  = "long"
	UptimeClock = "clock"
)

// PingOptions controls the ping line.
type PingOptions struct {
	Label       string `json:"label"`
	Host        string `json:"host"`
	Method      string `json:"method"` // auto, icmp or tcp
	Port        int    `json:"port"`   // for tcp, and auto when ICMP gets no answer
	IntervalSec int    `json:"interval_sec"`
	ShowHost    bool   `json:"show_host"`
	ShowAvg     bool   `json:"show_avg"`
	ShowLoss    bool   `json:"show_loss"`
	TimeoutText string `json:"timeout_text"`
}

// Ping methods.
const (
	PingAuto = "auto"
	PingICMP = "icmp"
	PingTCP  = "tcp"
)

// DiskOptions controls the disk usage line.
type DiskOptions struct {
	Label       string   `json:"label"`
	Drives      []string `json:"drives"` // "C:", ...; empty is the Windows drive
	ShowLetter  bool     `json:"show_letter"`
	ShowUsed    bool     `json:"show_used"`
	ShowTotal   bool     `json:"show_total"`
	ShowFree    bool     `json:"show_free"`
	ShowPercent bool     `json:"show_percent"`
	Separator   string   `json:"separator"`
}

// PCBatteryOptions controls the laptop battery line.
type PCBatteryOptions struct {
	Label        string `json:"label"`
	Icon         string `json:"icon"`
	LowIcon      string `json:"low_icon"`
	LowThreshold int    `json:"low_threshold"`
	ChargingMark string `json:"charging_mark"`
	PluggedMark  string `json:"plugged_mark"` // plugged in, not charging
	ShowTime     bool   `json:"show_time"`    // time left on battery
	HideOnAC     bool   `json:"hide_on_ac"`
}

func defaultSensorOptions() SensorOptions {
	return SensorOptions{
		Source:    SensorSourceAuto,
		LHMURL:    DefaultLHMURL,
		Template:  "{name} {value}",
		Separator: " · ",
		Picks:     []SensorPick{},
	}
}

func defaultUptimeOptions() UptimeOptions {
	return UptimeOptions{ShowPC: true, PCText: "PC up", ShowFern: false, FernText: "Fern", Style: UptimeShort, Units: 2}
}

func defaultPingOptions() PingOptions {
	return PingOptions{Label: "ᵖⁱⁿᵍ", Host: "1.1.1.1", Method: PingAuto, Port: 443, IntervalSec: 5, TimeoutText: "timeout"}
}

func defaultDiskOptions() DiskOptions {
	return DiskOptions{Drives: []string{}, ShowLetter: true, ShowUsed: true, ShowTotal: true, ShowPercent: true, Separator: " | "}
}

func defaultPCBatteryOptions() PCBatteryOptions {
	return PCBatteryOptions{Icon: "🔋", LowIcon: "🪫", LowThreshold: 20, ChargingMark: "⚡", PluggedMark: "🔌", ShowTime: true}
}

// oneLineSep keeps a separator's spaces but not its line breaks.
func oneLineSep(s, def string, n int) string {
	s = RuneLimit(vrOneLine(s), n)
	if s == "" {
		return def
	}
	return s
}

// cleanLHMURL accepts an http(s) URL and gives the default for anything else.
func cleanLHMURL(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 300 {
		return DefaultLHMURL
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return DefaultLHMURL
	}
	return u.String()
}

// cleanDrive turns "c", "C:", "c:\" into "C:", or "" when it is no drive.
func cleanDrive(s string) string {
	s = strings.ToUpper(strings.TrimRight(strings.TrimSpace(s), `\/`))
	s = strings.TrimSuffix(s, ":")
	if len(s) != 1 || s[0] < 'A' || s[0] > 'Z' {
		return ""
	}
	return s + ":"
}

// sanitizeSystemLines bounds the sensor and system line options.
func (c *Config) sanitizeSystemLines() {
	c.CPUOptions.Sensors.sanitize()
	c.GPUOptions.Sensors.sanitize()

	s := &c.SensorOptions
	switch s.Source {
	case SensorSourceAuto, SensorSourceHWiNFO, SensorSourceLHM, SensorSourceOff:
	default:
		s.Source = SensorSourceAuto
	}
	s.LHMURL = cleanLHMURL(s.LHMURL)
	s.Label = RuneLimit(strings.TrimSpace(s.Label), 16)
	s.Template = RuneLimit(strings.TrimSpace(vrOneLine(s.Template)), 80)
	if s.Template == "" {
		s.Template = "{name} {value}"
	}
	s.Separator = oneLineSep(s.Separator, " · ", 8)
	picks := make([]SensorPick, 0, len(s.Picks))
	seen := map[string]bool{}
	for _, p := range s.Picks {
		p.Key = RuneLimit(strings.TrimSpace(p.Key), maxSensorKeyLen)
		if p.Key == "" || seen[p.Key] || len(picks) >= maxSensorPicks {
			continue
		}
		seen[p.Key] = true
		p.Name = RuneLimit(strings.TrimSpace(vrOneLine(p.Name)), 24)
		picks = append(picks, p)
	}
	s.Picks = picks

	u := &c.UptimeOptions
	u.Label = RuneLimit(strings.TrimSpace(u.Label), 16)
	u.PCText = RuneLimit(strings.TrimSpace(vrOneLine(u.PCText)), 16)
	u.FernText = RuneLimit(strings.TrimSpace(vrOneLine(u.FernText)), 16)
	switch u.Style {
	case UptimeShort, UptimeLong, UptimeClock:
	default:
		u.Style = UptimeShort
	}
	if u.Units == 0 {
		u.Units = 2
	}
	u.Units = clampInt(u.Units, 1, 4)

	p := &c.PingOptions
	p.Label = RuneLimit(strings.TrimSpace(p.Label), 16)
	p.Host = RuneLimit(strings.TrimSpace(p.Host), 253)
	if strings.ContainsAny(p.Host, " /\\\t\r\n") {
		p.Host = ""
	}
	switch p.Method {
	case PingAuto, PingICMP, PingTCP:
	default:
		p.Method = PingAuto
	}
	if p.Port < 1 || p.Port > 65535 {
		p.Port = 443
	}
	if p.IntervalSec == 0 {
		p.IntervalSec = 5
	}
	p.IntervalSec = clampInt(p.IntervalSec, 2, 60)
	p.TimeoutText = RuneLimit(strings.TrimSpace(vrOneLine(p.TimeoutText)), 16)

	d := &c.DiskOptions
	d.Label = RuneLimit(strings.TrimSpace(d.Label), 16)
	drives := make([]string, 0, len(d.Drives))
	seenDrive := map[string]bool{}
	for _, v := range d.Drives {
		if v = cleanDrive(v); v != "" && !seenDrive[v] {
			seenDrive[v] = true
			drives = append(drives, v)
		}
	}
	d.Drives = drives
	d.Separator = oneLineSep(d.Separator, " | ", 8)

	b := &c.PCBatteryOptions
	b.Label = RuneLimit(strings.TrimSpace(b.Label), 16)
	b.Icon = RuneLimit(strings.TrimSpace(b.Icon), 4)
	b.LowIcon = RuneLimit(strings.TrimSpace(b.LowIcon), 4)
	b.ChargingMark = RuneLimit(strings.TrimSpace(b.ChargingMark), 4)
	b.PluggedMark = RuneLimit(strings.TrimSpace(b.PluggedMark), 4)
	b.LowThreshold = clampInt(b.LowThreshold, 0, 100)
}

// keepSensorMachineSettings copies the sensor source, which belongs to the PC,
// not to a profile.
func keepSensorMachineSettings(dst *Config, cur Config) {
	dst.SensorOptions.Source = cur.SensorOptions.Source
	dst.SensorOptions.LHMURL = cur.SensorOptions.LHMURL
}
