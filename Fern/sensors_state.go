package main

import (
	"Fern/modules"
)

// sensorsState is the Sensors page and the sensor parts of the CPU and GPU
// pages: where readings come from, every sensor for the pickers, and what
// the CPU and GPU lines read right now.
type sensorsState struct {
	Status modules.SensorStatus `json:"status"`
	List   []modules.Sensor     `json:"list"`
	CPU    modules.HWReadings   `json:"cpu"`
	GPU    modules.HWReadings   `json:"gpu"`
}

// systemState is the uptime, ping, disk and PC battery pages.
type systemState struct {
	PCUptimeSec   int64               `json:"pcUptimeSec"`
	FernUptimeSec int64               `json:"fernUptimeSec"`
	Ping          modules.PingStatus  `json:"ping"`
	Disks         []modules.DiskInfo  `json:"disks"`
	Power         modules.PowerStatus `json:"power"`
}

func currentSensors(cfg modules.Config) sensorsState {
	return sensorsState{
		Status: modules.CurrentSensorStatus(),
		List:   orEmpty(modules.Sensors()),
		CPU:    modules.CPUReadings(cfg),
		GPU:    modules.GPUReadings(cfg),
	}
}

func currentSystem() systemState {
	return systemState{
		PCUptimeSec:   int64(modules.PCUptime().Seconds()),
		FernUptimeSec: int64(modules.FernUptime().Seconds()),
		Ping:          modules.CurrentPing(),
		Disks:         orEmpty(modules.Disks()),
		Power:         modules.CurrentPower(),
	}
}

// handleSensorsCommand: {type:"sensors", action: "watch" | "refresh"} keeps
// sensors and pings running for the open page, or looks for a sensor app
// again right away.
func handleSensorsCommand(cmd command) {
	switch cmd.Action {
	case "watch":
		modules.WatchSensors()
		modules.WatchPing()
	case "refresh":
		modules.WatchSensors()
	case "ping":
		modules.WatchPing()
	}
}
