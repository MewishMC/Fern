package modules

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

var (
	specsOnce   sync.Once
	cpuModel    string
	specsMu     sync.RWMutex
	cpuUsage    float64
	memUsedGB   float64
	memTotalGB  float64
	memPercent  float64
	specsPrimed bool
)

func loadStaticSpecs() {
	specsOnce.Do(func() {
		info, err := cpu.Info()
		if err != nil || len(info) == 0 {
			cpuModel = "CPU"
			return
		}
		fields := strings.Fields(info[0].ModelName)
		if len(fields) > 4 {
			fields = fields[:4]
		}
		cpuModel = strings.Join(fields, " ")
	})
}

func sampleSpecs() {
	loadStaticSpecs()

	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		specsMu.Lock()
		cpuUsage = pct[0]
		specsMu.Unlock()
	}
	if v, err := mem.VirtualMemory(); err == nil && v != nil {
		specsMu.Lock()
		memTotalGB = float64(v.Total) / (1024 * 1024 * 1024)
		memUsedGB = float64(v.Used) / (1024 * 1024 * 1024)
		memPercent = v.UsedPercent
		specsMu.Unlock()
	}
	specsMu.Lock()
	specsPrimed = true
	specsMu.Unlock()
}

func startSpecsWorker() {
	go func() {
		// Prime cpu.Percent so the first real sample has a baseline.
		_, _ = cpu.Percent(0, false)
		sampleSpecs()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			sampleSpecs()
		}
	}()
}

// GetCpuInfo renders the CPU module line.
func GetCpuInfo(cpuName bool, cores bool) string {
	specsMu.RLock()
	usage := cpuUsage
	primed := specsPrimed
	specsMu.RUnlock()
	if !primed {
		sampleSpecs()
		specsMu.RLock()
		usage = cpuUsage
		specsMu.RUnlock()
	}

	var result []string
	if cpuName {
		result = append(result, fmt.Sprintf("ᶜᵖᵘ %s", cpuModel))
	}
	if cores {
		result = append(result, fmt.Sprintf("| ᶜᵒʳᵉˢ %d", runtime.NumCPU()))
	}
	result = append(result, fmt.Sprintf("· %.0f%%", usage))
	return strings.Join(result, " ")
}

// CpuUsage returns the last sampled CPU load in percent.
func CpuUsage() float64 {
	specsMu.RLock()
	defer specsMu.RUnlock()
	return cpuUsage
}

// GetMem renders the memory module line.
func GetMem() string {
	specsMu.RLock()
	used, total, pct := memUsedGB, memTotalGB, memPercent
	specsMu.RUnlock()
	if total == 0 {
		sampleSpecs()
		specsMu.RLock()
		used, total, pct = memUsedGB, memTotalGB, memPercent
		specsMu.RUnlock()
	}
	return fmt.Sprintf("ᵐᵉᵐ %.1f/%.1f GB · %.0f%%", used, total, pct)
}

// MemoryStats exposes the sampled memory numbers to the UI.
func MemoryStats() (usedGB, totalGB, percent float64) {
	specsMu.RLock()
	defer specsMu.RUnlock()
	return memUsedGB, memTotalGB, memPercent
}
