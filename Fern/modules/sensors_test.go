package modules

import (
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// lhmFixture is a trimmed data.json from LibreHardwareMonitor 0.9: an AMD
// CPU, a board with a Super I/O chip and an AMD card. The CPU uses the newer
// SensorId/Type fields, the rest the older text-only form, and the card's
// numbers use a decimal comma.
const lhmFixture = `{"id":0,"Text":"Sensor","Min":"Min","Value":"Value","Max":"Max","ImageURL":"","Children":[
 {"id":1,"Text":"DESKTOP","ImageURL":"images_icon/computer.png","Children":[
  {"id":2,"Text":"AMD Ryzen 7 5800X","ImageURL":"images_icon/cpu.png","Children":[
   {"id":3,"Text":"Clocks","ImageURL":"images_icon/clock.png","Children":[
    {"id":4,"Text":"Core #1","Min":"3600.0 MHz","Value":"4500.0 MHz","Max":"4850.0 MHz","SensorId":"/amdcpu/0/clock/1","Type":"Clock","ImageURL":"images/transparent.png","Children":[]},
    {"id":5,"Text":"Core #2","Min":"3600.0 MHz","Value":"4300.0 MHz","Max":"4850.0 MHz","SensorId":"/amdcpu/0/clock/2","Type":"Clock","ImageURL":"images/transparent.png","Children":[]},
    {"id":6,"Text":"Bus Speed","Min":"100.0 MHz","Value":"100.0 MHz","Max":"100.0 MHz","SensorId":"/amdcpu/0/clock/0","Type":"Clock","ImageURL":"images/transparent.png","Children":[]}]},
   {"id":7,"Text":"Temperatures","ImageURL":"images_icon/temperature.png","Children":[
    {"id":8,"Text":"Core (Tctl/Tdie)","Min":"40.1 °C","Value":"61.4 °C","Max":"88.0 °C","SensorId":"/amdcpu/0/temperature/2","Type":"Temperature","ImageURL":"images/transparent.png","Children":[]},
    {"id":9,"Text":"CCD1 (Tdie)","Min":"38.0 °C","Value":"59.0 °C","Max":"85.0 °C","SensorId":"/amdcpu/0/temperature/3","Type":"Temperature","ImageURL":"images/transparent.png","Children":[]}]},
   {"id":10,"Text":"Powers","ImageURL":"images_icon/power.png","Children":[
    {"id":11,"Text":"Package","Min":"20.0 W","Value":"72.6 W","Max":"140.2 W","SensorId":"/amdcpu/0/power/0","Type":"Power","ImageURL":"images/transparent.png","Children":[]}]}]},
  {"id":12,"Text":"ASUS ROG STRIX B550-F","ImageURL":"images_icon/mainboard.png","Children":[
   {"id":13,"Text":"Nuvoton NCT6798D","ImageURL":"images_icon/chip.png","Children":[
    {"id":14,"Text":"Fans","ImageURL":"images_icon/fan.png","Children":[
     {"id":15,"Text":"Fan #1","Min":"0 RPM","Value":"0 RPM","Max":"0 RPM","ImageURL":"images/transparent.png","Children":[]},
     {"id":16,"Text":"CPU Fan","Min":"600 RPM","Value":"1184 RPM","Max":"1500 RPM","ImageURL":"images/transparent.png","Children":[]}]}]}]},
  {"id":17,"Text":"AMD Radeon RX 7800 XT","ImageURL":"images_icon/ati.png","Children":[
   {"id":18,"Text":"Temperatures","ImageURL":"images_icon/temperature.png","Children":[
    {"id":19,"Text":"GPU Core","Min":"35,0 °C","Value":"54,0 °C","Max":"70,0 °C","ImageURL":"images/transparent.png","Children":[]},
    {"id":20,"Text":"GPU Hot Spot","Min":"36,0 °C","Value":"66,0 °C","Max":"80,0 °C","ImageURL":"images/transparent.png","Children":[]}]},
   {"id":21,"Text":"Clocks","ImageURL":"images_icon/clock.png","Children":[
    {"id":22,"Text":"GPU Core","Min":"0,0 MHz","Value":"2.430,0 MHz","Max":"2.600,0 MHz","ImageURL":"images/transparent.png","Children":[]}]},
   {"id":23,"Text":"Powers","ImageURL":"images_icon/power.png","Children":[
    {"id":24,"Text":"GPU Package","Min":"8,0 W","Value":"212,5 W","Max":"260,0 W","ImageURL":"images/transparent.png","Children":[]}]},
   {"id":25,"Text":"Fans","ImageURL":"images_icon/fan.png","Children":[
    {"id":26,"Text":"GPU Fan","Min":"0 RPM","Value":"1450 RPM","Max":"1900 RPM","ImageURL":"images/transparent.png","Children":[]}]}]}]}]}`

func lhmSensors(t *testing.T) []Sensor {
	t.Helper()
	list, err := parseLHMJSON([]byte(lhmFixture))
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func sensorNamed(t *testing.T, list []Sensor, hardware, name string) Sensor {
	t.Helper()
	for _, s := range list {
		if s.Hardware == hardware && s.Name == name {
			return s
		}
	}
	t.Fatalf("no sensor %s › %s in %+v", hardware, name, list)
	return Sensor{}
}

func TestParseLHMJSON(t *testing.T) {
	list := lhmSensors(t)
	if len(list) != 13 {
		t.Fatalf("got %d sensors, want 13", len(list))
	}
	temp := sensorNamed(t, list, "AMD Ryzen 7 5800X", "Core (Tctl/Tdie)")
	if temp.Key != "/amdcpu/0/temperature/2" || temp.Kind != HWCPU || temp.Type != SensorTemp ||
		temp.Value != 61.4 || temp.Min != 40.1 || temp.Max != 88 || temp.Unit != "°C" {
		t.Errorf("cpu temp = %+v", temp)
	}
	fan := sensorNamed(t, list, "Nuvoton NCT6798D", "CPU Fan")
	if fan.Kind != HWBoard || fan.Type != SensorFan || fan.Value != 1184 || !strings.HasPrefix(fan.Key, "lhm:") {
		t.Errorf("board fan = %+v", fan)
	}
	clock := sensorNamed(t, list, "AMD Radeon RX 7800 XT", "GPU Core")
	if clock.Type != SensorTemp {
		// Two sensors share the name; the first one is the temperature.
		t.Errorf("first GPU Core = %+v", clock)
	}
	var gpuClock Sensor
	for _, s := range list {
		if s.Kind == HWGPU && s.Type == SensorClock {
			gpuClock = s
		}
	}
	if gpuClock.Value != 2430 || gpuClock.Max != 2600 {
		t.Errorf("decimal comma with thousands mark: %+v", gpuClock)
	}
	keys := map[string]bool{}
	for _, s := range list {
		if keys[s.Key] {
			t.Errorf("duplicate key %q", s.Key)
		}
		keys[s.Key] = true
	}
}

func TestParseLHMJSONRejectsOtherJSON(t *testing.T) {
	if _, err := parseLHMJSON([]byte(`{"hello":"world"}`)); err == nil {
		t.Error("parsed a tree with no sensors")
	}
	if _, err := parseLHMJSON([]byte(`<html>`)); err == nil {
		t.Error("parsed HTML")
	}
}

func TestParseReading(t *testing.T) {
	cases := []struct {
		in   string
		v    float64
		unit string
		ok   bool
	}{
		{"45.3 °C", 45.3, "°C", true},
		{"45,3 °C", 45.3, "°C", true},
		{"1,234.5 MHz", 1234.5, "MHz", true},
		{"1.234,5 MHz", 1234.5, "MHz", true},
		{"-3 W", -3, "W", true},
		{"0 RPM", 0, "RPM", true},
		{"", 0, "", false},
		{"n/a", 0, "n/a", false},
	}
	for _, c := range cases {
		v, unit, ok := parseReading(c.in)
		if ok != c.ok || (ok && (math.Abs(v-c.v) > 1e-9 || unit != c.unit)) {
			t.Errorf("parseReading(%q) = %v %q %v", c.in, v, unit, ok)
		}
	}
}

func TestNormaliseUnits(t *testing.T) {
	s := Sensor{Type: SensorTemp, Value: 212, Min: 32, Max: 212, Unit: "°F"}
	normalise(&s)
	if s.Value != 100 || s.Min != 0 || s.Unit != "°C" {
		t.Errorf("fahrenheit = %+v", s)
	}
	s = Sensor{Type: SensorClock, Value: 4.5, Unit: "GHz"}
	normalise(&s)
	if s.Value != 4500 || s.Unit != "MHz" {
		t.Errorf("GHz = %+v", s)
	}
	s = Sensor{Type: SensorPower, Value: 1500, Unit: "mW"}
	normalise(&s)
	if s.Value != 1.5 || s.Unit != "W" {
		t.Errorf("mW = %+v", s)
	}
}

func TestFetchLHMWeb(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/data.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(lhmFixture))
	}))
	defer srv.Close()
	list, err := fetchLHMWeb(srv.URL + "/data.json")
	if err != nil || len(list) != 13 {
		t.Fatalf("fetch = %d sensors, %v", len(list), err)
	}
	if _, err := fetchLHMWeb(srv.URL + "/nope"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing page err = %v", err)
	}
	srv.Close()
	if _, err := fetchLHMWeb(srv.URL + "/data.json"); err == nil {
		t.Error("a closed server answered")
	}
}

func TestLHMWMIToSensors(t *testing.T) {
	hw := []lhmWMIHardware{
		{Identifier: "/intelcpu/0", Name: "Intel Core i7-12700K", HardwareType: "Cpu"},
		{Identifier: "/lpc/nct6798d", Name: "Nuvoton NCT6798D", HardwareType: "SuperIO", Parent: "/mainboard"},
		{Identifier: "/gpu-intel/0", Name: "Intel(R) Arc(TM) A770 Graphics", HardwareType: "GpuIntel"},
	}
	ws := []lhmWMISensor{
		{Identifier: "/intelcpu/0/temperature/0", Name: "CPU Package", SensorType: "Temperature", Parent: "/intelcpu/0", Value: 55, Min: 30, Max: 90},
		{Identifier: "/lpc/nct6798d/fan/1", Name: "Fan #2", SensorType: "Fan", Parent: "/lpc/nct6798d", Value: 900},
		{Identifier: "/gpu-intel/0/power/0", Name: "GPU Power", SensorType: "Power", Parent: "/gpu-intel/0", Value: 120.5},
		{Identifier: "/gpu-intel/0/smalldata/0", Name: "GPU Memory Used", SensorType: "SmallData", Parent: "/gpu-intel/0", Value: 2048},
	}
	list := lhmWMIToSensors(hw, ws)
	if len(list) != 4 {
		t.Fatalf("got %d", len(list))
	}
	if list[0].Kind != HWCPU || list[0].Unit != "°C" || list[0].Max != 90 {
		t.Errorf("cpu = %+v", list[0])
	}
	if list[1].Kind != HWBoard {
		t.Errorf("super I/O kind = %q", list[1].Kind)
	}
	if list[2].Kind != HWGPU || list[2].Type != SensorPower || list[2].Unit != "W" {
		t.Errorf("gpu power = %+v", list[2])
	}
	if list[3].Type != SensorData || list[3].Unit != "MB" {
		t.Errorf("small data = %+v", list[3])
	}
}

// hwinfoBlock builds a shared memory block like HWiNFO 7 writes, with the
// newer UTF-8 fields when utf is set.
type hwReading struct {
	typ, sensor, id uint32
	label, unit     string
	value, min, max float64
}

func hwinfoBlock(sensors []string, readings []hwReading, utf bool) []byte {
	sSize, rSize := hwinfoSensorBase, hwinfoReadingBase
	if utf {
		sSize, rSize = hwinfoSensorBase+128, hwinfoReadingBase+144
	}
	sOff := hwinfoHeaderSize
	rOff := sOff + sSize*len(sensors)
	b := make([]byte, rOff+rSize*len(readings))
	le := binary.LittleEndian
	copy(b, "HWiS")
	le.PutUint32(b[4:], 2)
	le.PutUint32(b[20:], uint32(sOff))
	le.PutUint32(b[24:], uint32(sSize))
	le.PutUint32(b[28:], uint32(len(sensors)))
	le.PutUint32(b[32:], uint32(rOff))
	le.PutUint32(b[36:], uint32(rSize))
	le.PutUint32(b[40:], uint32(len(readings)))
	for i, name := range sensors {
		e := b[sOff+i*sSize:]
		le.PutUint32(e[0:], 0xF000+uint32(i))
		copy(e[8:], name)
		if utf {
			copy(e[264:], name)
		}
	}
	for i, r := range readings {
		e := b[rOff+i*rSize:]
		le.PutUint32(e[0:], r.typ)
		le.PutUint32(e[4:], r.sensor)
		le.PutUint32(e[8:], r.id)
		copy(e[12:], r.label)
		unit := []byte(r.unit)
		if !utf && r.unit == "°C" {
			unit = []byte{0xB0, 'C'} // Windows-1252, as older versions write it
		}
		copy(e[268:], unit)
		le.PutUint64(e[284:], math.Float64bits(r.value))
		le.PutUint64(e[292:], math.Float64bits(r.min))
		le.PutUint64(e[300:], math.Float64bits(r.max))
		if utf {
			copy(e[316:], r.label)
			copy(e[444:], r.unit)
		}
	}
	return b
}

var hwinfoSample = []hwReading{
	{1, 0, 1, "CPU (Tctl/Tdie)", "°C", 63.5, 40, 90},
	{5, 0, 2, "CPU Package Power", "W", 88.2, 20, 140},
	{6, 0, 3, "Core 0 Clock (perf #1/1)", "MHz", 4600, 3000, 4850},
	{6, 0, 4, "Core 1 Clock (perf #2/2)", "MHz", 4400, 3000, 4850},
	{6, 0, 5, "Bus Clock", "MHz", 100, 100, 100},
	{1, 1, 1, "GPU Temperature", "°C", 58, 30, 75},
	{5, 1, 2, "GPU ASIC Power", "W", 180, 10, 250},
	{3, 1, 3, "GPU Fan1", "RPM", 1300, 0, 2000},
	{7, 1, 4, "GPU Core Load", "%", 97, 0, 100},
	{7, 1, 5, "GPU D3D Usage Yes/No", "Yes/No", 1, 0, 1},
	{3, 2, 1, "CPU", "RPM", 1000, 600, 1600},
	{99, 2, 2, "Unknown type", "", 1, 1, 1},
}

var hwinfoSensorNames = []string{
	"CPU [#0]: AMD Ryzen 7 5800X: Enhanced",
	"GPU [#0]: AMD Radeon RX 6800 XT: ",
	"ASUS ROG STRIX B550-F GAMING (Nuvoton NCT6798D)",
}

func TestParseHWiNFO(t *testing.T) {
	for _, utf := range []bool{false, true} {
		list, err := parseHWiNFO(hwinfoBlock(hwinfoSensorNames, hwinfoSample, utf))
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 11 {
			t.Fatalf("utf=%v: got %d readings, want 11", utf, len(list))
		}
		temp := list[0]
		if temp.Kind != HWCPU || temp.Type != SensorTemp || temp.Value != 63.5 || temp.Unit != "°C" || temp.Key != "hwi:f000.0/1" {
			t.Errorf("utf=%v: cpu temp = %+v", utf, temp)
		}
		if list[5].Kind != HWGPU || list[5].Hardware != "GPU [#0]: AMD Radeon RX 6800 XT:" {
			t.Errorf("utf=%v: gpu = %+v", utf, list[5])
		}
		if list[9].Type != SensorOther {
			t.Errorf("a yes/no usage reading counts as a load: %+v", list[9])
		}
		if list[10].Kind != HWBoard {
			t.Errorf("board fan kind = %q", list[10].Kind)
		}
	}
}

func TestParseHWiNFORejectsBadBlocks(t *testing.T) {
	good := hwinfoBlock(hwinfoSensorNames, hwinfoSample, false)
	dead := append([]byte(nil), good...)
	copy(dead, "DEAD")
	if _, err := parseHWiNFO(dead); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("dead block err = %v", err)
	}
	if _, err := parseHWiNFO(good[:100]); err == nil {
		t.Error("parsed a cut block")
	}
	other := append([]byte(nil), good...)
	copy(other, "NOPE")
	if _, err := parseHWiNFO(other); err == nil {
		t.Error("parsed a block with another signature")
	}
	if _, err := parseHWiNFO(nil); err == nil {
		t.Error("parsed nothing")
	}
}

func swapReaders(t *testing.T, hw, web, wmi func() ([]Sensor, error)) *[]string {
	t.Helper()
	calls := &[]string{}
	oldHW, oldWeb, oldWMI := readHWiNFO, readLHMWeb, readLHMWMI
	readHWiNFO = func() ([]Sensor, error) { *calls = append(*calls, SourceHWiNFO); return hw() }
	readLHMWeb = func(string) ([]Sensor, error) { *calls = append(*calls, SourceLHMWeb); return web() }
	readLHMWMI = func() ([]Sensor, error) { *calls = append(*calls, SourceLHMWMI); return wmi() }
	t.Cleanup(func() { readHWiNFO, readLHMWeb, readLHMWMI = oldHW, oldWeb, oldWMI })
	return calls
}

func TestPollSensorsFallsBackInOrder(t *testing.T) {
	none := func() ([]Sensor, error) { return nil, errors.New("not running") }
	some := func() ([]Sensor, error) { return []Sensor{{Key: "a", Type: SensorTemp}}, nil }
	calls := swapReaders(t, none, none, some)

	list, st := pollSensors(SensorSourceAuto, "", "")
	if len(list) != 1 || st.Source != SourceLHMWMI || len(st.Tried) != 2 || st.Error != "" {
		t.Errorf("auto: %v %+v", list, st)
	}
	if strings.Join(*calls, ",") != "hwinfo,lhm-web,lhm-wmi" {
		t.Errorf("order = %v", *calls)
	}
	// The source that worked last time goes first.
	*calls = nil
	pollSensors(SensorSourceAuto, "", SourceLHMWMI)
	if strings.Join(*calls, ",") != "lhm-wmi" {
		t.Errorf("last-first order = %v", *calls)
	}
	*calls = nil
	if _, st := pollSensors(SensorSourceHWiNFO, "", ""); st.Source != "" || st.Error == "" || strings.Join(*calls, ",") != "hwinfo" {
		t.Errorf("hwinfo only: %+v %v", st, *calls)
	}
	*calls = nil
	if _, st := pollSensors(SensorSourceOff, "", ""); st.Source != "" || len(*calls) != 0 {
		t.Errorf("off: %+v %v", st, *calls)
	}
}

func TestPollSensorsTreatsEmptyAsMissing(t *testing.T) {
	empty := func() ([]Sensor, error) { return nil, nil }
	swapReaders(t, empty, empty, empty)
	if _, st := pollSensors(SensorSourceAuto, "", ""); st.Source != "" || len(st.Tried) != 3 {
		t.Errorf("empty sources: %+v", st)
	}
}

func TestCPUReadingsFromLHM(t *testing.T) {
	list := lhmSensors(t)
	r := cpuReadings(list, HWPicks{})
	if r.Temp == nil || r.Temp.Name != "Core (Tctl/Tdie)" {
		t.Errorf("temp = %+v", r.Temp)
	}
	if r.Power == nil || r.Power.Value != 72.6 {
		t.Errorf("power = %+v", r.Power)
	}
	if r.Clock == nil || r.Clock.Value != 4400 {
		t.Errorf("clock (core average, no bus) = %+v", r.Clock)
	}
	if r.Fan == nil || r.Fan.Name != "CPU Fan" {
		t.Errorf("fan = %+v", r.Fan)
	}
	// A pick wins, and a pick that no longer exists shows nothing.
	r = cpuReadings(list, HWPicks{Temp: "/amdcpu/0/temperature/3", Fan: "gone"})
	if r.Temp == nil || r.Temp.Name != "CCD1 (Tdie)" || r.Fan != nil {
		t.Errorf("picked: temp %+v fan %+v", r.Temp, r.Fan)
	}
}

func TestCPUReadingsFromHWiNFO(t *testing.T) {
	list, err := parseHWiNFO(hwinfoBlock(hwinfoSensorNames, hwinfoSample, true))
	if err != nil {
		t.Fatal(err)
	}
	r := cpuReadings(list, HWPicks{})
	if r.Temp == nil || r.Temp.Value != 63.5 || r.Power == nil || r.Power.Value != 88.2 {
		t.Errorf("temp %+v power %+v", r.Temp, r.Power)
	}
	if r.Clock == nil || r.Clock.Value != 4500 {
		t.Errorf("clock = %+v", r.Clock)
	}
	if r.Fan == nil || r.Fan.Value != 1000 {
		t.Errorf("fan = %+v", r.Fan)
	}
}

func TestGPUReadingsMatchTheCard(t *testing.T) {
	list := lhmSensors(t)
	r := gpuReadings(list, "AMD Radeon(TM) RX 7800 XT", HWPicks{})
	if r.Hardware != "AMD Radeon RX 7800 XT" {
		t.Fatalf("hardware = %q", r.Hardware)
	}
	if r.Temp == nil || r.Temp.Value != 54 || r.Power == nil || r.Power.Value != 212.5 ||
		r.Clock == nil || r.Clock.Value != 2430 || r.Fan == nil || r.Fan.Value != 1450 {
		t.Errorf("readings = %+v %+v %+v %+v", r.Temp, r.Power, r.Clock, r.Fan)
	}
	hw, _ := parseHWiNFO(hwinfoBlock(hwinfoSensorNames, hwinfoSample, false))
	r = gpuReadings(hw, "AMD Radeon RX 6800 XT", HWPicks{})
	if r.Temp == nil || r.Temp.Value != 58 || r.Power == nil || r.Power.Value != 180 || r.Fan == nil {
		t.Errorf("hwinfo readings = %+v %+v %+v", r.Temp, r.Power, r.Fan)
	}
	if gpuNameKey("GPU [#0]: AMD Radeon RX 6800 XT: ") != "rx 6800 xt" {
		t.Errorf("name key = %q", gpuNameKey("GPU [#0]: AMD Radeon RX 6800 XT: "))
	}
}

func TestFormatSensorValues(t *testing.T) {
	cases := []struct {
		s    Sensor
		f    bool
		want string
	}{
		{Sensor{Type: SensorTemp, Value: 61.4}, false, "61°C"},
		{Sensor{Type: SensorTemp, Value: 100}, true, "212°F"},
		{Sensor{Type: SensorPower, Value: 72.6}, false, "73 W"},
		{Sensor{Type: SensorPower, Value: 4.26}, false, "4.3 W"},
		{Sensor{Type: SensorClock, Value: 4400}, false, "4.40 GHz"},
		{Sensor{Type: SensorClock, Value: 800}, false, "800 MHz"},
		{Sensor{Type: SensorFan, Value: 1184.4}, false, "1184 rpm"},
		{Sensor{Type: SensorLoad, Value: 97.4}, false, "97%"},
		{Sensor{Type: SensorVoltage, Value: 1.2}, false, "1.2 V"},
		{Sensor{Type: SensorData, Value: 12.34, Unit: "GB"}, false, "12.3 GB"},
	}
	for _, c := range cases {
		if got := FormatSensor(c.s, c.f); got != c.want {
			t.Errorf("FormatSensor(%+v) = %q, want %q", c.s, got, c.want)
		}
	}
}

func TestSensorsText(t *testing.T) {
	list := lhmSensors(t)
	o := defaultSensorOptions()
	o.Picks = []SensorPick{{Key: "/amdcpu/0/temperature/2", Name: "CPU"}, {Key: "missing"}, {Key: "/amdcpu/0/power/0"}}
	if got := sensorsText(list, o); got != "CPU 61°C · Package 73 W" {
		t.Errorf("text = %q", got)
	}
	o.Template = "{name}: {num}{unit} (max {max})"
	o.Separator = " | "
	o.Label = "🌡"
	o.Fahrenheit = true
	o.Picks = o.Picks[:1]
	if got := sensorsText(list, o); got != "🌡 CPU: 143°F (max 190°F)" {
		t.Errorf("template = %q", got)
	}
	if got := sensorsText(nil, o); got != "" {
		t.Errorf("no readings = %q", got)
	}
}

func TestGPULineUsesSensorsWhenTheDriverHasNoTemperature(t *testing.T) {
	list := lhmSensors(t)
	o := GPUOptions{ShowName: true, ShowUsage: true, ShowTemp: true, ShowPower: true, ShowClock: true, ShowFan: true}
	g := GPUInfo{Name: "AMD Radeon RX 7800 XT", Usage: 88}
	if got := formatGPUWithSensors(o, g, true, list); got != "RX 7800 XT · 88% · 54°C · 213 W · 2.43 GHz · 1450 rpm" {
		t.Errorf("amd = %q", got)
	}
	// NVIDIA's own reading stays when nothing was picked.
	nv := GPUInfo{Name: "AMD Radeon RX 7800 XT", Usage: 88, HasTemp: true, TempC: 70}
	o.ShowPower, o.ShowClock, o.ShowFan = false, false, false
	if got := formatGPUWithSensors(o, nv, true, list); got != "RX 7800 XT · 88% · 70°C" {
		t.Errorf("driver temp = %q", got)
	}
	// No sensor app: the line is what it always was.
	if got := formatGPUWithSensors(o, g, true, nil); got != "RX 7800 XT · 88%" {
		t.Errorf("no sensors = %q", got)
	}
}

func TestHWParts(t *testing.T) {
	r := cpuReadings(lhmSensors(t), HWPicks{})
	got := strings.Join(hwParts(r, true, true, true, true, false), " · ")
	if got != "61°C · 73 W · 4.40 GHz · 1184 rpm" {
		t.Errorf("parts = %q", got)
	}
	if got := hwParts(HWReadings{}, true, true, true, true, false); len(got) != 0 {
		t.Errorf("no readings = %v", got)
	}
}

func TestSensorsWanted(t *testing.T) {
	c := DefaultConfig()
	c.sanitize()
	if sensorsWanted(c, time.Time{}) {
		t.Error("wanted with nothing using sensors")
	}
	c.DisplayOptions.CPU, c.CPUOptions.ShowTemp = true, true
	if !sensorsWanted(c, time.Time{}) {
		t.Error("not wanted with the CPU temperature on")
	}
	c.SensorOptions.Source = SensorSourceOff
	if sensorsWanted(c, time.Time{}) {
		t.Error("wanted with sensors off")
	}
	c = DefaultConfig()
	c.Rules.Enabled = true
	c.Rules.List = []Rule{{ID: "r", Enabled: true, Trigger: RuleStep{Type: TriggerAfk}, Conditions: []RuleStep{{Type: TriggerHot}}}}
	if !sensorsWanted(c, time.Time{}) {
		t.Error("not wanted with a rule condition on the temperature")
	}
	c.Rules.List[0].Enabled = false
	if sensorsWanted(c, time.Time{}) {
		t.Error("wanted for a disabled rule")
	}
}
