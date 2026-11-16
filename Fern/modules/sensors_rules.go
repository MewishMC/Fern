package modules

import (
	"sort"
	"strings"
)

// Rule triggers for hardware sensors and the system lines.
const (
	TriggerSensor    = "pc.sensor"      // a picked sensor is above or below a value
	TriggerHot       = "pc.temperature" // the CPU or GPU temperature is above or below a value
	TriggerOnBattery = "pc.on_battery"  // the laptop runs on its battery
	TriggerPing      = "pc.ping"        // the ping is slow or timing out

	SourceSensors = "sensors"
)

// sensorTriggerIDs are the triggers that need sensor readings.
var sensorTriggerIDs = []string{TriggerSensor, TriggerHot}

// rulesUse reports whether an enabled rule uses one of the triggers, as its
// trigger or a condition.
func rulesUse(c Config, ids ...string) bool {
	if !c.Rules.Enabled {
		return false
	}
	for _, r := range c.Rules.List {
		if !r.Enabled {
			continue
		}
		for _, id := range ids {
			if r.Trigger.Type == id {
				return true
			}
			for _, cond := range r.Conditions {
				if cond.Type == id {
					return true
				}
			}
		}
	}
	return false
}

var compareOptions = []RuleOption{{Value: "gt", Label: "is above"}, {Value: "lt", Label: "is below"}}

func init() {
	lineLabels[ModuleSensors] = "Sensors"
	lineLabels[ModuleUptime] = "Uptime"
	lineLabels[ModulePing] = "Ping"
	lineLabels[ModuleDisk] = "Disk usage"
	lineLabels[ModulePCBattery] = "PC battery"

	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerHot, Label: "CPU or GPU temperature", Category: "PC",
		Description: "While the processor or graphics card is hotter or cooler than a value. Needs HWiNFO or LibreHardwareMonitor running.",
		Fields: []RuleField{
			{Key: "part", Label: "Part", Type: FieldSelect, Options: []RuleOption{{Value: "cpu", Label: "CPU"}, {Value: "gpu", Label: "GPU"}, {Value: "either", Label: "CPU or GPU"}}},
			{Key: "op", Label: "Test", Type: FieldSelect, Options: compareOptions},
			{Key: "value", Label: "Temperature (°C)", Type: FieldNumber, Default: 80.0, Min: -50, Max: 150, Step: 1},
		},
		Check: func(p RuleParams) bool {
			c := GetConfig()
			list := Sensors()
			test := func(s *Sensor) bool {
				return s != nil && compareParam(p.String("op"), s.Value, p.Number("value"))
			}
			cpu := func() bool { return test(cpuReadings(list, c.CPUOptions.Sensors).Temp) }
			gpu := func() bool {
				name := ""
				if g, ok := pickGPU(GPUs(), c.GPUOptions.Adapter); ok {
					name = g.Name
					if g.HasTemp && c.GPUOptions.Sensors.Temp == "" {
						return compareParam(p.String("op"), g.TempC, p.Number("value"))
					}
				}
				return test(gpuReadings(list, name, c.GPUOptions.Sensors).Temp)
			}
			switch p.String("part") {
			case "gpu":
				return gpu()
			case "either":
				return cpu() || gpu()
			}
			return cpu()
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSensor, Label: "Hardware sensor", Category: "PC",
		Description: "While any sensor HWiNFO or LibreHardwareMonitor reports is above or below a value. Temperatures are in °C.",
		Fields: []RuleField{
			{Key: "sensor", Label: "Sensor", Type: FieldSelect, Source: SourceSensors, Placeholder: "Pick a sensor"},
			{Key: "op", Label: "Test", Type: FieldSelect, Options: compareOptions},
			{Key: "value", Label: "Value", Type: FieldNumber, Min: -100000, Max: 100000, Step: 0.1},
		},
		Check: func(p RuleParams) bool {
			s, ok := findSensor(Sensors(), p.String("sensor"))
			return ok && compareParam(p.String("op"), s.Value, p.Number("value"))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerOnBattery, Label: "On battery", Category: "PC",
		Description: "While the PC runs on its own battery, optionally only once it is low",
		Fields: []RuleField{
			{Key: "below", Label: "Only below (%)", Type: FieldNumber, Hint: "0 is any charge", Min: 0, Max: 100, Step: 1},
		},
		Check: func(p RuleParams) bool {
			pw := CurrentPower()
			if !pw.HasBattery || pw.OnAC {
				return false
			}
			below := p.Number("below")
			return below <= 0 || (pw.Percent >= 0 && float64(pw.Percent) < below)
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerPing, Label: "Slow ping", Category: "PC",
		Description: "While the ping line's host answers slower than a value, or not at all. Pings run while a rule uses this, even with the line off.",
		Fields: []RuleField{
			{Key: "ms", Label: "Slower than (ms)", Type: FieldNumber, Default: 150.0, Min: 1, Max: 10000, Step: 1},
			{Key: "timeouts", Label: "Count timeouts", Type: FieldBool, Default: true, Hint: "A ping that gets no answer counts as slow"},
		},
		Check: func(p RuleParams) bool {
			st := CurrentPing()
			if !st.Active || st.Updated == 0 || st.Error != "" {
				return false
			}
			if st.MS < 0 {
				return p.Bool("timeouts")
			}
			return float64(st.MS) > p.Number("ms")
		},
	}))
	RegisterRuleSource(SourceSensors, func() []RuleOption {
		list := Sensors()
		out := make([]RuleOption, 0, len(list))
		for _, s := range list {
			out = append(out, RuleOption{Value: s.Key, Label: sensorLabel(s)})
		}
		sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label) })
		return out
	})
}

// sensorLabel names a sensor for pickers: "Ryzen 7 5800X › CPU Package (temperature)".
func sensorLabel(s Sensor) string {
	return s.Hardware + " › " + s.Name + " (" + s.Type + ")"
}
