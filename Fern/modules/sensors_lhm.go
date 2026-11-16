package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yusufpapurcu/wmi"
)

// LibreHardwareMonitor publishes its readings two ways: a web server
// (Options > Remote Web Server, default port 8085) whose data.json is the
// sensor tree, and WMI classes in root\LibreHardwareMonitor while it runs.
// OpenHardwareMonitor, which it forked from, uses the same WMI classes in
// root\OpenHardwareMonitor and the same data.json.

// lhmNode is one node of data.json: the computer, a piece of hardware, a
// sensor group ("Temperatures") or a sensor.
type lhmNode struct {
	Text     string    `json:"Text"`
	Min      string    `json:"Min"`
	Value    string    `json:"Value"`
	Max      string    `json:"Max"`
	SensorID string    `json:"SensorId"` // newer versions
	Type     string    `json:"Type"`     // newer versions
	ImageURL string    `json:"ImageURL"`
	Children []lhmNode `json:"Children"`
}

var lhmClient = &http.Client{Timeout: 1500 * time.Millisecond}

// fetchLHMWeb reads LibreHardwareMonitor's data.json.
func fetchLHMWeb(url string) ([]Sensor, error) {
	if url == "" {
		url = DefaultLHMURL
	}
	resp, err := lhmClient.Get(url)
	if err != nil {
		if strings.Contains(err.Error(), "refused") {
			return nil, errors.New("web server not running")
		}
		return nil, errors.New("web server not reachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("web server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return parseLHMJSON(data)
}

// lhmGroupTypes maps data.json's group names to sensor types.
var lhmGroupTypes = map[string]string{
	"temperatures": SensorTemp, "powers": SensorPower, "clocks": SensorClock, "fans": SensorFan,
	"load": SensorLoad, "voltages": SensorVoltage, "currents": SensorCurrent, "controls": SensorControl,
	"data": SensorData, "small data": SensorData, "smalldata": SensorData, "throughput": SensorData,
	"levels": SensorLoad, "factors": SensorOther, "frequencies": SensorOther, "energies": SensorOther,
	"noise": SensorOther, "flows": SensorOther, "humidity": SensorOther, "timings": SensorOther,
	"conductivities": SensorOther, "times": SensorOther,
}

// lhmTypes maps LibreHardwareMonitor's SensorType names to sensor types.
func lhmType(t string) string {
	switch strings.ToLower(t) {
	case "temperature":
		return SensorTemp
	case "power":
		return SensorPower
	case "clock":
		return SensorClock
	case "fan":
		return SensorFan
	case "load", "level":
		return SensorLoad
	case "voltage":
		return SensorVoltage
	case "current":
		return SensorCurrent
	case "control":
		return SensorControl
	case "data", "smalldata", "throughput":
		return SensorData
	case "":
		return ""
	}
	return SensorOther
}

// lhmKindFromID reads the hardware kind from an identifier like
// "/amdcpu/0/temperature/2" or "/gpu-nvidia/0".
func lhmKindFromID(id string) string {
	first := strings.ToLower(strings.SplitN(strings.TrimPrefix(id, "/"), "/", 2)[0])
	switch {
	case first == "":
		return ""
	case strings.Contains(first, "cpu"):
		return HWCPU
	case strings.Contains(first, "gpu"):
		return HWGPU
	case first == "lpc" || first == "mainboard" || first == "motherboard" || first == "ec":
		return HWBoard
	case first == "ram" || first == "memory" || strings.HasPrefix(first, "dimm"):
		return HWMemory
	case first == "hdd" || first == "nvme" || first == "ssd" || strings.Contains(first, "storage"):
		return HWStorage
	}
	return HWOther
}

// lhmKindFromImage reads the hardware kind from data.json's icon.
func lhmKindFromImage(img string) string {
	img = strings.ToLower(img)
	switch {
	case strings.HasSuffix(img, "cpu.png"):
		return HWCPU
	case strings.HasSuffix(img, "nvidia.png"), strings.HasSuffix(img, "ati.png"), strings.HasSuffix(img, "amd.png"),
		strings.HasSuffix(img, "intel.png"), strings.HasSuffix(img, "gpu.png"):
		return HWGPU
	case strings.HasSuffix(img, "mainboard.png"), strings.HasSuffix(img, "chip.png"):
		return HWBoard
	case strings.HasSuffix(img, "ram.png"):
		return HWMemory
	case strings.HasSuffix(img, "hdd.png"), strings.HasSuffix(img, "nvme.png"):
		return HWStorage
	}
	return ""
}

// guessKind falls back on the hardware's name.
func guessKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "geforce"), strings.Contains(n, "radeon"), strings.Contains(n, "arc "),
		strings.Contains(n, "graphics"), strings.HasPrefix(n, "gpu"), strings.Contains(n, "nvidia"):
		return HWGPU
	case strings.Contains(n, "ryzen"), strings.Contains(n, "core i"), strings.Contains(n, "core ultra"),
		strings.Contains(n, "xeon"), strings.Contains(n, "threadripper"), strings.Contains(n, "epyc"),
		strings.Contains(n, "pentium"), strings.Contains(n, "celeron"), strings.HasPrefix(n, "cpu"):
		return HWCPU
	}
	return HWOther
}

// parseReading splits "45.3 °C" or "1,5 W" into a number and its unit.
func parseReading(s string) (float64, string, bool) {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && strings.IndexByte("+-0123456789.,", s[end]) >= 0 {
		end++
	}
	num, unit := s[:end], strings.TrimSpace(s[end:])
	if num == "" || num == "-" {
		return 0, unit, false
	}
	switch {
	case strings.Contains(num, ".") && strings.Contains(num, ","):
		// "1,234.5" or "1.234,5": the last mark is the decimal point.
		if strings.LastIndex(num, ",") > strings.LastIndex(num, ".") {
			num = strings.ReplaceAll(num, ".", "")
			num = strings.ReplaceAll(num, ",", ".")
		} else {
			num = strings.ReplaceAll(num, ",", "")
		}
	case strings.Contains(num, ","):
		num = strings.ReplaceAll(num, ",", ".")
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, unit, false
	}
	return v, unit, true
}

// normalise converts a reading to the unit Fern uses for its type.
func normalise(s *Sensor) {
	u := strings.ToLower(strings.TrimSpace(s.Unit))
	conv := func(f func(float64) float64) {
		s.Value, s.Min, s.Max = f(s.Value), f(s.Min), f(s.Max)
	}
	switch s.Type {
	case SensorTemp:
		if u == "°f" || u == "f" {
			conv(func(v float64) float64 { return (v - 32) * 5 / 9 })
		}
		s.Unit = "°C"
	case SensorClock:
		switch u {
		case "ghz":
			conv(func(v float64) float64 { return v * 1000 })
		case "khz":
			conv(func(v float64) float64 { return v / 1000 })
		case "hz":
			conv(func(v float64) float64 { return v / 1e6 })
		}
		s.Unit = "MHz"
	case SensorPower:
		switch u {
		case "mw":
			conv(func(v float64) float64 { return v / 1000 })
		case "kw":
			conv(func(v float64) float64 { return v * 1000 })
		}
		s.Unit = "W"
	case SensorFan:
		s.Unit = "RPM"
	case SensorLoad, SensorControl:
		s.Unit = "%"
	case SensorVoltage:
		if u == "mv" {
			conv(func(v float64) float64 { return v / 1000 })
		}
		s.Unit = "V"
	case SensorCurrent:
		if u == "ma" {
			conv(func(v float64) float64 { return v / 1000 })
		}
		s.Unit = "A"
	}
}

// parseLHMJSON flattens data.json into sensors.
func parseLHMJSON(data []byte) ([]Sensor, error) {
	var root lhmNode
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, errors.New("data.json is not LibreHardwareMonitor's sensor tree")
	}
	var out []Sensor
	seen := map[string]int{}
	var walk func(n lhmNode, depth int, hardware, kind, group string)
	walk = func(n lhmNode, depth int, hardware, kind, group string) {
		if len(n.Children) == 0 {
			if depth < 3 || hardware == "" {
				return
			}
			v, unit, ok := parseReading(n.Value)
			if !ok {
				return
			}
			s := Sensor{Hardware: hardware, Kind: kind, Name: strings.TrimSpace(n.Text), Value: v, Unit: unit}
			s.Min, _, _ = parseReading(n.Min)
			s.Max, _, _ = parseReading(n.Max)
			s.Type = lhmType(n.Type)
			if s.Type == "" {
				s.Type = lhmGroupTypes[strings.ToLower(group)]
			}
			if s.Type == "" {
				s.Type = SensorOther
			}
			if k := lhmKindFromID(n.SensorID); k != "" && k != HWOther {
				s.Kind = k
			}
			s.Key = n.SensorID
			if s.Key == "" {
				s.Key = "lhm:" + hardware + "/" + s.Type + "/" + s.Name
			}
			if i := seen[s.Key]; i > 0 {
				s.Key += "#" + strconv.Itoa(i)
			}
			seen[s.Key]++
			normalise(&s)
			out = append(out, s)
			return
		}
		text := strings.TrimSpace(n.Text)
		if _, isGroup := lhmGroupTypes[strings.ToLower(text)]; isGroup && depth >= 3 {
			for _, c := range n.Children {
				walk(c, depth+1, hardware, kind, text)
			}
			return
		}
		if depth >= 2 {
			// A piece of hardware, or a chip under one (the board's
			// Super I/O). A chip keeps its parent's kind if its own is unclear.
			hardware = text
			if k := lhmKindFromImage(n.ImageURL); k != "" {
				kind = k
			} else if kind == "" {
				kind = guessKind(text)
			}
		}
		for _, c := range n.Children {
			walk(c, depth+1, hardware, kind, group)
		}
	}
	walk(root, 0, "", "", "")
	if len(out) == 0 {
		return nil, errors.New("no sensors in data.json")
	}
	return out, nil
}

// WMI classes, as LibreHardwareMonitor defines them.
type lhmWMIHardware struct {
	Identifier   string
	Name         string
	HardwareType string
	Parent       string
}

type lhmWMISensor struct {
	Identifier string
	Name       string
	SensorType string
	Parent     string
	Value      float32
	Min        float32
	Max        float32
}

var lhmNamespaces = []string{`root\LibreHardwareMonitor`, `root\OpenHardwareMonitor`}

// queryLHMWMI reads the sensors through WMI.
func queryLHMWMI() ([]Sensor, error) {
	var lastErr error
	for _, ns := range lhmNamespaces {
		var hw []lhmWMIHardware
		if err := wmi.QueryNamespace("SELECT Identifier, Name, HardwareType, Parent FROM Hardware", &hw, ns); err != nil {
			lastErr = err
			continue
		}
		var sensors []lhmWMISensor
		if err := wmi.QueryNamespace("SELECT Identifier, Name, SensorType, Parent, Value, Min, Max FROM Sensor", &sensors, ns); err != nil {
			lastErr = err
			continue
		}
		if list := lhmWMIToSensors(hw, sensors); len(list) > 0 {
			return list, nil
		}
		lastErr = errors.New("no sensors in WMI")
	}
	if lastErr != nil && strings.Contains(strings.ToLower(lastErr.Error()), "invalid namespace") {
		return nil, errors.New("WMI classes not found (LibreHardwareMonitor not running)")
	}
	if lastErr == nil {
		lastErr = errors.New("no sensors in WMI")
	}
	return nil, lastErr
}

// lhmHardwareKind maps the WMI HardwareType.
func lhmHardwareKind(t string) string {
	t = strings.ToLower(t)
	switch {
	case t == "cpu":
		return HWCPU
	case strings.HasPrefix(t, "gpu"):
		return HWGPU
	case t == "motherboard" || t == "mainboard" || t == "superio" || t == "embeddedcontroller":
		return HWBoard
	case t == "memory" || t == "ram":
		return HWMemory
	case t == "storage" || t == "hdd":
		return HWStorage
	}
	return HWOther
}

// wmiUnits are the units LibreHardwareMonitor's sensor types are in.
var wmiUnits = map[string]string{
	SensorTemp: "°C", SensorPower: "W", SensorClock: "MHz", SensorFan: "RPM", SensorLoad: "%",
	SensorControl: "%", SensorVoltage: "V", SensorCurrent: "A",
}

func lhmWMIToSensors(hw []lhmWMIHardware, sensors []lhmWMISensor) []Sensor {
	byID := make(map[string]lhmWMIHardware, len(hw))
	for _, h := range hw {
		byID[h.Identifier] = h
	}
	out := make([]Sensor, 0, len(sensors))
	for _, w := range sensors {
		h, ok := byID[w.Parent]
		name := h.Name
		kind := lhmHardwareKind(h.HardwareType)
		// A Super I/O chip's parent is the motherboard; keep the chip's name
		// but the board's kind when the chip's is unknown.
		if ok && kind == HWOther {
			if p, ok := byID[h.Parent]; ok {
				kind = lhmHardwareKind(p.HardwareType)
			}
		}
		if !ok {
			name, kind = w.Parent, lhmKindFromID(w.Parent)
		}
		t := lhmType(w.SensorType)
		if t == "" {
			t = SensorOther
		}
		s := Sensor{
			Key: w.Identifier, Hardware: name, Kind: kind, Name: w.Name, Type: t,
			Value: float64(w.Value), Min: float64(w.Min), Max: float64(w.Max), Unit: wmiUnits[t],
		}
		if strings.EqualFold(w.SensorType, "SmallData") {
			s.Unit = "MB"
		} else if strings.EqualFold(w.SensorType, "Data") {
			s.Unit = "GB"
		}
		out = append(out, s)
	}
	return out
}
