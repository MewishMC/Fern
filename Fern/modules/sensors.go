package modules

import (
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hardware sensors: temperatures, power, clocks and fans. Windows has no
// vendor-neutral way to read them without a kernel driver, and Fern ships
// none, so it reads them from a monitoring app the user already runs:
// HWiNFO's shared memory, or LibreHardwareMonitor's web server or WMI
// classes. Every source is turned into the same flat list of Sensor.

// Sensor types, normalised across sources.
const (
	SensorTemp    = "temperature" // °C
	SensorPower   = "power"       // W
	SensorClock   = "clock"       // MHz
	SensorFan     = "fan"         // RPM
	SensorLoad    = "load"        // %
	SensorVoltage = "voltage"     // V
	SensorCurrent = "current"     // A
	SensorData    = "data"
	SensorControl = "control" // %
	SensorOther   = "other"
)

// Hardware kinds.
const (
	HWCPU     = "cpu"
	HWGPU     = "gpu"
	HWBoard   = "board"
	HWMemory  = "memory"
	HWStorage = "storage"
	HWOther   = "other"
)

// Sensor is one reading.
type Sensor struct {
	Key      string  `json:"key"` // stable id, used to pick it
	Hardware string  `json:"hardware"`
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Value    float64 `json:"value"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Unit     string  `json:"unit"`
}

// Sources a reading can come from, as SensorStatus.Source reports them.
const (
	SourceHWiNFO = "hwinfo"
	SourceLHMWeb = "lhm-web"
	SourceLHMWMI = "lhm-wmi"
)

// SensorProbe is one source Fern tried and why it did not answer.
type SensorProbe struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

// SensorStatus is what the panel shows about the sensor source.
type SensorStatus struct {
	Mode    string        `json:"mode"`    // the configured source
	Source  string        `json:"source"`  // the one being read, "" for none
	Count   int           `json:"count"`   // sensors read
	Updated int64         `json:"updated"` // unix ms of the last good read
	Error   string        `json:"error"`
	Tried   []SensorProbe `json:"tried"`
}

// maxSensors caps the list; a big HWiNFO setup can report more than a
// thousand readings, most of them residency counters nobody puts in a chatbox.
const maxSensors = 600

// The readers, swapped in tests.
var (
	readHWiNFO = readHWiNFOShared
	readLHMWeb = fetchLHMWeb
	readLHMWMI = queryLHMWMI
)

var (
	sensorMu     sync.RWMutex
	sensorList   []Sensor
	sensorStatus = SensorStatus{Mode: SensorSourceAuto}
	sensorWake   = make(chan struct{}, 1)
)

// Sensors returns the latest readings.
func Sensors() []Sensor {
	sensorMu.RLock()
	defer sensorMu.RUnlock()
	return slices.Clone(sensorList)
}

// CurrentSensorStatus reports the sensor source.
func CurrentSensorStatus() SensorStatus {
	sensorMu.RLock()
	defer sensorMu.RUnlock()
	st := sensorStatus
	st.Tried = slices.Clone(st.Tried)
	return st
}

// RefreshSensors asks the worker to look for a source again now.
func RefreshSensors() {
	select {
	case sensorWake <- struct{}{}:
	default:
	}
}

// sensorOrder lists the sources to try for a mode.
func sensorOrder(mode string) []string {
	switch mode {
	case SensorSourceHWiNFO:
		return []string{SourceHWiNFO}
	case SensorSourceLHM:
		return []string{SourceLHMWeb, SourceLHMWMI}
	case SensorSourceOff:
		return nil
	}
	return []string{SourceHWiNFO, SourceLHMWeb, SourceLHMWMI}
}

// pollSensors reads the first source that answers. last, the source that
// worked the time before, is tried first so a working source is not slowed
// down by the ones in front of it failing.
func pollSensors(mode, lhmURL, last string) ([]Sensor, SensorStatus) {
	st := SensorStatus{Mode: mode}
	order := sensorOrder(mode)
	if i := slices.Index(order, last); i > 0 {
		order = append([]string{last}, slices.Delete(slices.Clone(order), i, i+1)...)
	}
	for _, src := range order {
		var list []Sensor
		var err error
		switch src {
		case SourceHWiNFO:
			list, err = readHWiNFO()
		case SourceLHMWeb:
			list, err = readLHMWeb(lhmURL)
		case SourceLHMWMI:
			list, err = readLHMWMI()
		}
		if err == nil && len(list) == 0 {
			err = errors.New("no sensors reported")
		}
		if err != nil {
			st.Tried = append(st.Tried, SensorProbe{Source: src, Error: err.Error()})
			continue
		}
		if len(list) > maxSensors {
			list = list[:maxSensors]
		}
		st.Source, st.Count, st.Updated = src, len(list), time.Now().UnixMilli()
		return list, st
	}
	if mode == SensorSourceOff {
		st.Error = "Sensors are off"
	} else {
		st.Error = "No sensor app found"
	}
	return nil, st
}

// sensorsWanted reports whether anything uses sensor readings, so Fern does
// not poll a monitoring app for nothing. The panel's Sensors page counts: it
// asks by sending a refresh, which keeps polling on for a while.
func sensorsWanted(c Config, panelUntil time.Time) bool {
	if c.SensorOptions.Source == SensorSourceOff {
		return false
	}
	if time.Now().Before(panelUntil) {
		return true
	}
	d := c.DisplayOptions
	if d.Sensors && len(c.SensorOptions.Picks) > 0 {
		return true
	}
	if d.CPU && (c.CPUOptions.ShowTemp || c.CPUOptions.ShowPower || c.CPUOptions.ShowClock || c.CPUOptions.ShowFan) {
		return true
	}
	if d.GPU && (c.GPUOptions.ShowTemp || c.GPUOptions.ShowPower || c.GPUOptions.ShowClock || c.GPUOptions.ShowFan) {
		return true
	}
	return rulesUse(c, sensorTriggerIDs...)
}

var (
	sensorPanelMu    sync.Mutex
	sensorPanelUntil time.Time
)

// WatchSensors keeps sensors polled for the next few minutes, for the panel.
func WatchSensors() {
	sensorPanelMu.Lock()
	sensorPanelUntil = time.Now().Add(5 * time.Minute)
	sensorPanelMu.Unlock()
	RefreshSensors()
}

func startSensorWorker() {
	go func() {
		last := ""
		for {
			c := GetConfig()
			sensorPanelMu.Lock()
			until := sensorPanelUntil
			sensorPanelMu.Unlock()
			wait := 2 * time.Second
			if sensorsWanted(c, until) {
				list, st := pollSensors(c.SensorOptions.Source, c.SensorOptions.LHMURL, last)
				last = st.Source
				if st.Source == "" {
					// Nothing is running: look again now and then, not every tick.
					wait = 10 * time.Second
				}
				sensorMu.Lock()
				sensorList, sensorStatus = list, st
				sensorMu.Unlock()
				NudgeRules()
			} else {
				sensorMu.Lock()
				sensorList = nil
				sensorStatus = SensorStatus{Mode: c.SensorOptions.Source, Error: "Not in use"}
				sensorMu.Unlock()
			}
			select {
			case <-time.After(wait):
			case <-sensorWake:
			}
		}
	}()
}

// findSensor returns the sensor with key.
func findSensor(list []Sensor, key string) (Sensor, bool) {
	if key == "" {
		return Sensor{}, false
	}
	for _, s := range list {
		if s.Key == key {
			return s, true
		}
	}
	return Sensor{}, false
}

// HWReadings is what the CPU or GPU line can show from sensors. A nil field
// was not found.
type HWReadings struct {
	Hardware string  `json:"hardware"`
	Temp     *Sensor `json:"temp"`
	Power    *Sensor `json:"power"`
	Clock    *Sensor `json:"clock"`
	Fan      *Sensor `json:"fan"`
}

func sensorPtr(s Sensor, ok bool) *Sensor {
	if !ok {
		return nil
	}
	return &s
}

// pickBy returns the first sensor of type t, preferring names that equal one
// of exact (in order), then names containing one of parts (in order), then any.
func pickBy(list []Sensor, t string, exact, parts []string, anyOK bool) (Sensor, bool) {
	var typed []Sensor
	for _, s := range list {
		if s.Type == t {
			typed = append(typed, s)
		}
	}
	for _, want := range exact {
		for _, s := range typed {
			if strings.EqualFold(s.Name, want) {
				return s, true
			}
		}
	}
	for _, part := range parts {
		for _, s := range typed {
			if strings.Contains(strings.ToLower(s.Name), part) {
				return s, true
			}
		}
	}
	if anyOK && len(typed) > 0 {
		return typed[0], true
	}
	return Sensor{}, false
}

var (
	coreClockName = regexp.MustCompile(`(?i)^(cpu )?core\s*#?\d+(\s+clock)?(\s*\(perf.*\))?$`)
	clockNoise    = []string{"bus", "effective", "memory", "uncore", "ring", "fabric", "llc", "soc", "l3"}
)

// averageCoreClock averages the per-core clocks into one reading.
func averageCoreClock(list []Sensor) (Sensor, bool) {
	var sum, n float64
	var first Sensor
	for _, s := range list {
		if s.Type != SensorClock || !coreClockName.MatchString(strings.TrimSpace(s.Name)) {
			continue
		}
		if n == 0 {
			first = s
		}
		sum += s.Value
		n++
	}
	if n == 0 {
		return Sensor{}, false
	}
	first.Name, first.Value, first.Key = "Core average", sum/n, first.Key+"#avg"
	return first, true
}

func ofKind(list []Sensor, kind string) []Sensor {
	var out []Sensor
	for _, s := range list {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// cpuReadings finds the CPU's temperature, package power, clock and fan,
// honouring the user's picks.
func cpuReadings(list []Sensor, picks HWPicks) HWReadings {
	cpu := ofKind(list, HWCPU)
	var r HWReadings
	if len(cpu) > 0 {
		r.Hardware = cpu[0].Hardware
	}
	r.Temp = pickOr(list, picks.Temp, func() (Sensor, bool) {
		return pickBy(cpu, SensorTemp,
			[]string{"CPU Package", "Core (Tctl/Tdie)", "CPU (Tctl/Tdie)", "Package", "CPU Die (average)", "Core (Tctl)", "CPU (Tctl)", "Tdie", "Tctl"},
			[]string{"package", "tctl", "tdie", "core max", "cpu"}, true)
	})
	r.Power = pickOr(list, picks.Power, func() (Sensor, bool) {
		return pickBy(cpu, SensorPower, []string{"CPU Package", "Package", "CPU Package Power", "CPU PPT", "Package Power"},
			[]string{"package", "ppt", "cpu"}, true)
	})
	r.Clock = pickOr(list, picks.Clock, func() (Sensor, bool) {
		if s, ok := pickBy(cpu, SensorClock, []string{"Cores (Average)", "Core Clocks (avg)", "Core Clocks", "Cores Average"}, []string{"average", "avg"}, false); ok {
			return s, true
		}
		if s, ok := averageCoreClock(cpu); ok {
			return s, true
		}
		var cores []Sensor
		for _, s := range cpu {
			name := strings.ToLower(s.Name)
			if s.Type == SensorClock && !slices.ContainsFunc(clockNoise, func(w string) bool { return strings.Contains(name, w) }) {
				cores = append(cores, s)
			}
		}
		return pickBy(cores, SensorClock, nil, nil, true)
	})
	r.Fan = pickOr(list, picks.Fan, func() (Sensor, bool) {
		if s, ok := pickBy(list, SensorFan, []string{"CPU", "CPU Fan", "CPU_FAN"}, []string{"cpu"}, false); ok && s.Kind != HWGPU {
			return s, true
		}
		if s, ok := pickBy(cpu, SensorFan, nil, nil, true); ok {
			return s, true
		}
		// The first spinning board fan, which is the CPU header on most boards.
		for _, s := range list {
			if s.Type == SensorFan && s.Kind == HWBoard && s.Value > 0 {
				return s, true
			}
		}
		return Sensor{}, false
	})
	return r
}

// gpuNameKey reduces a card name to words that match between DXGI and the
// sensor apps: "AMD Radeon(TM) RX 7800 XT" and "GPU [#0]: AMD Radeon RX 7800
// XT: " both become "rx 7800 xt".
func gpuNameKey(name string) string {
	name = gpuPrefix.ReplaceAllString(name, "")
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[:i]
	}
	return strings.ToLower(shortGPUName(name))
}

var gpuPrefix = regexp.MustCompile(`(?i)^\s*gpu\s*\[#\d+\]:\s*`)

// gpuHardware returns the sensors of the card named adapter, or of the first
// card when none matches by name.
func gpuHardware(list []Sensor, adapter string) []Sensor {
	gpus := ofKind(list, HWGPU)
	if len(gpus) == 0 {
		return nil
	}
	want := gpuNameKey(adapter)
	pick := ""
	if want != "" {
		for _, s := range gpus {
			have := gpuNameKey(s.Hardware)
			if have != "" && (have == want || strings.Contains(have, want) || strings.Contains(want, have)) {
				pick = s.Hardware
				break
			}
		}
	}
	if pick == "" {
		pick = gpus[0].Hardware
	}
	var out []Sensor
	for _, s := range gpus {
		if s.Hardware == pick {
			out = append(out, s)
		}
	}
	return out
}

// gpuReadings finds the card's temperature, power, core clock and fan.
func gpuReadings(list []Sensor, adapter string, picks HWPicks) HWReadings {
	gpu := gpuHardware(list, adapter)
	var r HWReadings
	if len(gpu) > 0 {
		r.Hardware = gpu[0].Hardware
	}
	r.Temp = pickOr(list, picks.Temp, func() (Sensor, bool) {
		return pickBy(gpu, SensorTemp, []string{"GPU Core", "GPU Temperature", "GPU", "GPU Edge", "Edge"},
			[]string{"core", "edge", "gpu temp"}, true)
	})
	r.Power = pickOr(list, picks.Power, func() (Sensor, bool) {
		return pickBy(gpu, SensorPower,
			[]string{"GPU Package", "GPU Power", "Total Board Power", "Total Graphics Power", "GPU ASIC Power", "GPU Core"},
			[]string{"package", "board", "total", "asic", "power"}, true)
	})
	r.Clock = pickOr(list, picks.Clock, func() (Sensor, bool) {
		return pickBy(gpu, SensorClock, []string{"GPU Core", "GPU Clock", "Core", "GPU Shader", "Graphics"},
			[]string{"core", "gpu clock", "shader", "graphics"}, true)
	})
	r.Fan = pickOr(list, picks.Fan, func() (Sensor, bool) {
		return pickBy(gpu, SensorFan, []string{"GPU Fan", "GPU Fan1", "GPU Fan 1", "Fan", "Fan #1"}, []string{"fan"}, true)
	})
	return r
}

// pickOr is the sensor the user picked, if it still exists, else auto().
func pickOr(list []Sensor, key string, auto func() (Sensor, bool)) *Sensor {
	if key != "" {
		return sensorPtr(findSensor(list, key))
	}
	return sensorPtr(auto())
}

// CPUReadings and GPUReadings are the panel's view of what those lines read.
func CPUReadings(c Config) HWReadings { return cpuReadings(Sensors(), c.CPUOptions.Sensors) }

func GPUReadings(c Config) HWReadings {
	name := ""
	if g, ok := pickGPU(GPUs(), c.GPUOptions.Adapter); ok {
		name = g.Name
	}
	return gpuReadings(Sensors(), name, c.GPUOptions.Sensors)
}

// formatTemp renders °C as a whole number in °C or °F.
func formatTemp(c float64, fahrenheit bool) string {
	if fahrenheit {
		return strconv.Itoa(int(math.Round(c*9/5+32))) + "°F"
	}
	return strconv.Itoa(int(math.Round(c))) + "°C"
}

// trimNum prints v with at most d decimals and no trailing zeros.
func trimNum(v float64, d int) string {
	s := strconv.FormatFloat(v, 'f', d, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

// sensorNumber is the number and unit for a value of a sensor's type.
func sensorNumber(s Sensor, v float64, fahrenheit bool) (string, string) {
	switch s.Type {
	case SensorTemp:
		if fahrenheit {
			return strconv.Itoa(int(math.Round(v*9/5 + 32))), "°F"
		}
		return strconv.Itoa(int(math.Round(v))), "°C"
	case SensorPower:
		if math.Abs(v) < 10 {
			return trimNum(v, 1), "W"
		}
		return strconv.Itoa(int(math.Round(v))), "W"
	case SensorClock:
		if v >= 1000 {
			return strconv.FormatFloat(v/1000, 'f', 2, 64), "GHz"
		}
		return strconv.Itoa(int(math.Round(v))), "MHz"
	case SensorFan:
		return strconv.Itoa(int(math.Round(v))), "rpm"
	case SensorLoad, SensorControl:
		return strconv.Itoa(int(math.Round(v))), "%"
	case SensorVoltage:
		return trimNum(v, 3), "V"
	case SensorCurrent:
		return trimNum(v, 2), "A"
	}
	return trimNum(v, 1), s.Unit
}

// joinUnit glues a number to its unit the way the chatbox lines do: "65°C",
// "4.52 GHz", "45%".
func joinUnit(num, unit string) string {
	switch unit {
	case "", "°C", "°F", "%":
		return num + unit
	}
	return num + " " + unit
}

// FormatSensor renders a reading with its unit.
func FormatSensor(s Sensor, fahrenheit bool) string {
	return joinUnit(sensorNumber(s, s.Value, fahrenheit))
}

// hwParts renders the sensor readings the CPU or GPU line has switched on.
func hwParts(r HWReadings, temp, power, clock, fan, fahrenheit bool) []string {
	var parts []string
	add := func(on bool, s *Sensor) {
		if on && s != nil {
			parts = append(parts, FormatSensor(*s, fahrenheit))
		}
	}
	add(temp, r.Temp)
	add(power, r.Power)
	add(clock, r.Clock)
	add(fan, r.Fan)
	return parts
}

// sensorEntry fills the template for one picked sensor.
func sensorEntry(s Sensor, name string, o SensorOptions) string {
	if name == "" {
		name = s.Name
	}
	num, unit := sensorNumber(s, s.Value, o.Fahrenheit)
	minNum, _ := sensorNumber(s, s.Min, o.Fahrenheit)
	maxNum, _ := sensorNumber(s, s.Max, o.Fahrenheit)
	text := strings.NewReplacer(
		"{name}", name,
		"{value}", joinUnit(num, unit),
		"{num}", num,
		"{unit}", unit,
		"{min}", joinUnit(minNum, unit),
		"{max}", joinUnit(maxNum, unit),
		"{hardware}", s.Hardware,
	).Replace(o.Template)
	return strings.Join(strings.Fields(text), " ")
}

// sensorsText renders the sensors line from a list of readings.
func sensorsText(list []Sensor, o SensorOptions) string {
	var parts []string
	for _, p := range o.Picks {
		s, ok := findSensor(list, p.Key)
		if !ok {
			continue
		}
		if entry := sensorEntry(s, p.Name, o); entry != "" {
			parts = append(parts, entry)
		}
	}
	return withLabel(o.Label, strings.Join(parts, o.Separator))
}

// sensorsLine is the sensors module's chatbox line.
func sensorsLine(cfg Config) string {
	return sensorsText(Sensors(), cfg.SensorOptions)
}
