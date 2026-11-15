package modules

import (
	"sort"
	"strconv"
	"strings"
)

// Device kinds on the battery line.
const (
	KindHeadset    = "headset"
	KindLeft       = "left"
	KindRight      = "right"
	KindController = "controller"
	KindTracker    = "tracker"
)

// VRDevice is one SteamVR device as last read.
type VRDevice struct {
	Index       uint32 `json:"index"`
	Serial      string `json:"serial"`
	Model       string `json:"model"`
	Kind        string `json:"kind"`
	DefaultName string `json:"defaultName"` // what Fern calls it without a custom name
	HasBattery  bool   `json:"hasBattery"`
	Battery     int    `json:"battery"` // percent
	Charging    bool   `json:"charging"`
}

var kindOrder = map[string]int{KindHeadset: 0, KindLeft: 1, KindRight: 2, KindController: 3, KindTracker: 4}

// kindLabel is what {kind} becomes.
func kindLabel(kind string) string {
	switch kind {
	case KindHeadset:
		return "Headset"
	case KindTracker:
		return "Tracker"
	}
	return "Controller"
}

func batteryIcon(kind string, o BatteryOptions) string {
	switch kind {
	case KindHeadset:
		return o.HeadsetIcon
	case KindTracker:
		return o.TrackerIcon
	}
	return o.ControllerIcon
}

func kindShown(kind string, o BatteryOptions) bool {
	switch kind {
	case KindHeadset:
		return o.ShowHeadset
	case KindTracker:
		return o.ShowTrackers
	}
	return o.ShowControllers
}

// batteryDeviceName is the user's name for a device, or the detected one.
func batteryDeviceName(d VRDevice, o BatteryOptions) string {
	for _, saved := range o.Devices {
		if saved.Serial == d.Serial && saved.Name != "" {
			return saved.Name
		}
	}
	return d.DefaultName
}

func batteryDeviceHidden(d VRDevice, o BatteryOptions) bool {
	for _, saved := range o.Devices {
		if saved.Serial == d.Serial {
			return saved.Hidden
		}
	}
	return false
}

// batteryEntry fills the template for one device. Placeholders: {icon}
// {name} {batt} {charging} {low} {kind} {model} {serial}. Runs of spaces left
// by empty placeholders are squeezed out.
func batteryEntry(d VRDevice, o BatteryOptions) string {
	low := d.Battery <= o.LowThreshold
	charging, lowTag := "", ""
	if d.Charging {
		charging = o.ChargingMark
	}
	if low {
		lowTag = o.LowTag
	}
	tmpl := o.Template
	if d.Charging && charging != "" && !strings.Contains(tmpl, "{charging}") {
		tmpl += " {charging}"
	}
	text := strings.NewReplacer(
		"{icon}", batteryIcon(d.Kind, o),
		"{name}", batteryDeviceName(d, o),
		"{batt}", strconv.Itoa(d.Battery),
		"{charging}", charging,
		"{low}", lowTag,
		"{kind}", kindLabel(d.Kind),
		"{model}", d.Model,
		"{serial}", d.Serial,
	).Replace(tmpl)
	return strings.Join(strings.Fields(text), " ")
}

// batteryText renders the battery line for the given devices.
func batteryText(devices []VRDevice, o BatteryOptions) string {
	shown := make([]VRDevice, 0, len(devices))
	for _, d := range devices {
		if !d.HasBattery || !kindShown(d.Kind, o) || batteryDeviceHidden(d, o) {
			continue
		}
		if o.OnlyLow && d.Battery > o.LowThreshold {
			continue
		}
		shown = append(shown, d)
	}
	if len(shown) == 0 {
		return ""
	}
	name := func(d VRDevice) string { return strings.ToLower(batteryDeviceName(d, o)) }
	switch o.Sort {
	case BatterySortName:
		sort.SliceStable(shown, func(i, j int) bool { return name(shown[i]) < name(shown[j]) })
	case BatterySortLow:
		sort.SliceStable(shown, func(i, j int) bool { return shown[i].Battery < shown[j].Battery })
	case BatterySortHigh:
		sort.SliceStable(shown, func(i, j int) bool { return shown[i].Battery > shown[j].Battery })
	case BatterySortType:
		sort.SliceStable(shown, func(i, j int) bool {
			a, b := kindOrder[shown[i].Kind], kindOrder[shown[j].Kind]
			if a != b {
				return a < b
			}
			return name(shown[i]) < name(shown[j])
		})
	}
	if o.OnlyLowest {
		lowest := 0
		for i, d := range shown {
			if d.Battery < shown[lowest].Battery {
				lowest = i
			}
		}
		shown = shown[lowest : lowest+1]
	}
	if o.MaxDevices > 0 && len(shown) > o.MaxDevices {
		shown = shown[:o.MaxDevices]
	}
	parts := make([]string, 0, len(shown))
	for _, d := range shown {
		if entry := batteryEntry(d, o); entry != "" {
			parts = append(parts, entry)
		}
	}
	return withLabel(o.Label, strings.Join(parts, o.Separator))
}

// batteryLine is the battery module's chatbox line.
func batteryLine(cfg Config) string {
	st := SteamVRState()
	if !st.Connected {
		return ""
	}
	return batteryText(st.Devices, cfg.BatteryOptions)
}

// defaultDeviceNames fills DefaultName for devices read in index order:
// Headset, Left, Right, Controller, and Tracker 1, 2, ... in detection order.
func defaultDeviceNames(devices []VRDevice) {
	trackers := 0
	for i := range devices {
		switch devices[i].Kind {
		case KindHeadset:
			devices[i].DefaultName = "Headset"
		case KindLeft:
			devices[i].DefaultName = "Left"
		case KindRight:
			devices[i].DefaultName = "Right"
		case KindTracker:
			trackers++
			devices[i].DefaultName = "Tracker " + strconv.Itoa(trackers)
		default:
			devices[i].DefaultName = "Controller"
		}
	}
}

// rememberDevices adds devices with a battery that the config has not seen
// yet and refreshes the kind and model of known ones. It reports whether the
// list changed.
func rememberDevices(saved []BatteryDevice, devices []VRDevice) ([]BatteryDevice, bool) {
	changed := false
	index := make(map[string]int, len(saved))
	for i, s := range saved {
		index[s.Serial] = i
	}
	for _, d := range devices {
		if d.Serial == "" || !d.HasBattery {
			continue
		}
		if i, ok := index[d.Serial]; ok {
			if saved[i].Kind != d.Kind || saved[i].Model != d.Model {
				saved[i].Kind, saved[i].Model = d.Kind, d.Model
				changed = true
			}
			continue
		}
		if len(saved) >= maxBatteryDevices {
			continue
		}
		index[d.Serial] = len(saved)
		saved = append(saved, BatteryDevice{Serial: d.Serial, Kind: d.Kind, Model: d.Model})
		changed = true
	}
	return saved, changed
}
