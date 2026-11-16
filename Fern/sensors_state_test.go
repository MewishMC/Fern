package main

import (
	"encoding/json"
	"testing"

	"Fern/modules"
)

func TestStateCarriesSensorsAndSystemLines(t *testing.T) {
	handleSensorsCommand(command{Type: "sensors", Action: "watch"})
	data, err := json.Marshal(buildState())
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Modules map[string]bool `json:"modules"`
		Sensors struct {
			Status *modules.SensorStatus `json:"status"`
			List   []modules.Sensor      `json:"list"`
		} `json:"sensors"`
		System struct {
			Disks []modules.DiskInfo  `json:"disks"`
			Ping  *modules.PingStatus `json:"ping"`
		} `json:"system"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{modules.ModuleSensors, modules.ModuleUptime, modules.ModulePing, modules.ModuleDisk, modules.ModulePCBattery} {
		if _, ok := st.Modules[id]; !ok {
			t.Errorf("state.modules has no %q", id)
		}
	}
	if st.Sensors.Status == nil || st.Sensors.List == nil || st.System.Disks == nil || st.System.Ping == nil {
		t.Errorf("sensors or system state missing: %s", data)
	}
}
