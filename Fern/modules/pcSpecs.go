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

// cpuLine renders the CPU module line using the configured options.
func cpuLine(cfg Config) string {
	o := cfg.CPUOptions
	specsMu.RLock()
	usage := cpuUsage
	primed := specsPrimed
	model := cpuModel
	specsMu.RUnlock()
	if !primed {
		sampleSpecs()
		specsMu.RLock()
		usage = cpuUsage
		model = cpuModel
		specsMu.RUnlock()
	}

	label := strings.TrimSpace(o.Label)
	var parts []string
	if o.ShowName {
		if model == "" {
			model = "CPU"
		}
		parts = append(parts, withLabel(label, model))
	} else if label != "" {
		parts = append(parts, label)
	}
	if o.ShowCores {
		parts = append(parts, fmt.Sprintf("cores %d", runtime.NumCPU()))
	}
	if o.ShowUsage {
		parts = append(parts, fmt.Sprintf("%.*f%%", o.Decimals, usage))
	}
	return strings.Join(parts, " · ")
}

// CpuUsage returns the last sampled CPU load in percent.
func CpuUsage() float64 {
	specsMu.RLock()
	defer specsMu.RUnlock()
	return cpuUsage
}

// memoryLine renders the memory module line using the configured options.
func memoryLine(cfg Config) string {
	o := cfg.MemoryOptions
	used, total, pct := MemoryStats()
	if total == 0 {
		sampleSpecs()
		used, total, pct = MemoryStats()
	}

	var parts []string
	switch {
	case o.ShowUsed && o.ShowTotal:
		parts = append(parts, fmt.Sprintf("%.*f/%.*f GB", o.Decimals, used, o.Decimals, total))
	case o.ShowUsed:
		parts = append(parts, fmt.Sprintf("%.*f GB", o.Decimals, used))
	case o.ShowTotal:
		parts = append(parts, fmt.Sprintf("%.*f GB", o.Decimals, total))
	}
	if o.ShowPercent {
		parts = append(parts, fmt.Sprintf("%.0f%%", pct))
	}
	if len(parts) == 0 {
		return ""
	}
	return withLabel(o.Label, strings.Join(parts, " · "))
}

// MemoryStats exposes the sampled memory numbers to the UI.
func MemoryStats() (usedGB, totalGB, percent float64) {
	specsMu.RLock()
	defer specsMu.RUnlock()
	return memUsedGB, memTotalGB, memPercent
}
