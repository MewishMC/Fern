package modules

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// VRPerf is the SteamVR performance summary shown on the vrperf line.
type VRPerf struct {
	Valid           bool    `json:"valid"`
	FPS             float64 `json:"fps"`
	Hz              float64 `json:"hz"`
	ReprojectionPct float64 `json:"reprojectionPct"`
	DroppedPerMin   float64 `json:"droppedPerMin"` // dropped frames in the last minute
	GPUms           float64 `json:"gpuMs"`         // the game's GPU time per frame
	CPUms           float64 `json:"cpuMs"`         // the game's CPU time per frame
	BudgetPct       float64 `json:"budgetPct"`     // GPU time as a share of the frame time
	Smoothing       bool    `json:"smoothing"`     // motion smoothing ran on the latest frame
}

// compositorCounts is the part of Compositor_CumulativeStats Fern uses.
type compositorCounts struct {
	Pid         uint32
	Presents    uint32
	Dropped     uint32
	Reprojected uint32
	Submits     uint32
	SumAppGPU   float64
	SumAppCPU   float64
}

// perfDelta is what changed between two compositor readings.
type perfDelta struct {
	Seconds     float64
	Presents    float64
	Dropped     float64
	Reprojected float64
	Submits     float64
	SumAppGPU   float64
	SumAppCPU   float64
}

// diffCounts returns the change from prev to cur. ok is false when the scene
// app changed or the counters went backwards, so the pair cannot be compared.
func diffCounts(prev, cur compositorCounts, seconds float64) (perfDelta, bool) {
	if seconds <= 0 || cur.Pid != prev.Pid || cur.Presents < prev.Presents || cur.Dropped < prev.Dropped ||
		cur.Reprojected < prev.Reprojected || cur.Submits < prev.Submits {
		return perfDelta{}, false
	}
	return perfDelta{
		Seconds:     seconds,
		Presents:    float64(cur.Presents - prev.Presents),
		Dropped:     float64(cur.Dropped - prev.Dropped),
		Reprojected: float64(cur.Reprojected - prev.Reprojected),
		Submits:     float64(cur.Submits - prev.Submits),
		SumAppGPU:   math.Max(0, cur.SumAppGPU-prev.SumAppGPU),
		SumAppCPU:   math.Max(0, cur.SumAppCPU-prev.SumAppCPU),
	}, true
}

// Frame rate, reprojection and timings are averaged over the last few
// seconds; dropped frames are counted over the last minute.
const (
	perfShortWindow = 3 * time.Second
	perfLongWindow  = 60 * time.Second
)

// summarizePerf turns recent deltas (oldest first) into the numbers shown.
// fallbackGPU is the newest frame's GPU time, used when the compositor's
// running totals carry none.
func summarizePerf(deltas []perfDelta, hz, fallbackGPU float64, smoothing bool) VRPerf {
	var short, long perfDelta
	for i := len(deltas) - 1; i >= 0; i-- {
		d := deltas[i]
		if long.Seconds < perfLongWindow.Seconds() {
			long.Seconds += d.Seconds
			long.Dropped += d.Dropped
		}
		if short.Seconds < perfShortWindow.Seconds() {
			short.Seconds += d.Seconds
			short.Presents += d.Presents
			short.Reprojected += d.Reprojected
			short.Submits += d.Submits
			short.SumAppGPU += d.SumAppGPU
			short.SumAppCPU += d.SumAppCPU
		}
	}
	if short.Seconds <= 0 || short.Presents <= 0 {
		return VRPerf{Hz: hz}
	}
	p := VRPerf{Valid: true, Hz: hz, Smoothing: smoothing, DroppedPerMin: long.Dropped}
	if short.Submits > 0 {
		p.FPS = short.Submits / short.Seconds
	} else {
		p.FPS = math.Max(0, short.Presents-short.Reprojected) / short.Seconds
	}
	if hz > 0 && p.FPS > hz {
		p.FPS = hz
	}
	p.ReprojectionPct = math.Min(100, short.Reprojected/short.Presents*100)
	if short.Submits > 0 && short.SumAppGPU > 0 {
		p.GPUms = short.SumAppGPU / short.Submits
	} else {
		p.GPUms = fallbackGPU
	}
	if short.Submits > 0 {
		p.CPUms = short.SumAppCPU / short.Submits
	}
	if hz > 0 && p.GPUms > 0 {
		p.BudgetPct = p.GPUms / (1000 / hz) * 100
	}
	return p
}

// perfIsBad reports whether p crosses any of the configured thresholds.
func perfIsBad(p VRPerf, o VRPerfOptions) bool {
	if !p.Valid {
		return false
	}
	if o.BadReprojection > 0 && p.ReprojectionPct >= float64(o.BadReprojection) {
		return true
	}
	if o.BadDroppedPerMin > 0 && p.DroppedPerMin >= float64(o.BadDroppedPerMin) {
		return true
	}
	return o.BadFPSPercent > 0 && p.Hz > 0 && p.FPS < p.Hz*float64(o.BadFPSPercent)/100
}

// perfGate turns the raw bad/fine signal into a steady one: it goes bad at
// once, but only counts as fine again after running well for a while, so a
// single hitch does not make the line flicker in and out.
type perfGate struct {
	bad     bool
	okSince time.Time
}

func (g *perfGate) update(now time.Time, badNow bool, clear time.Duration) bool {
	switch {
	case badNow:
		g.bad = true
		g.okSince = time.Time{}
	case g.bad:
		if g.okSince.IsZero() {
			g.okSince = now
		}
		if now.Sub(g.okSince) >= clear {
			g.bad = false
			g.okSince = time.Time{}
		}
	}
	return g.bad
}

func formatMs(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
}

// vrPerfText renders the line. bad is the gated state from perfGate.
func vrPerfText(p VRPerf, bad bool, o VRPerfOptions) string {
	if !p.Valid || (o.OnlyWhenBad && !bad) {
		return ""
	}
	unit := func(small, plain string) string {
		if o.SmallUnits {
			return small
		}
		return plain
	}
	var parts []string
	if o.ShowFPS {
		parts = append(parts, strconv.Itoa(int(math.Round(p.FPS)))+unit("ᶠᵖˢ", " fps"))
	}
	if o.ShowHz && p.Hz > 0 {
		parts = append(parts, strconv.Itoa(int(math.Round(p.Hz)))+unit("ʰᶻ", " Hz"))
	}
	if o.ShowReprojection {
		parts = append(parts, strconv.Itoa(int(math.Round(p.ReprojectionPct)))+unit("%ʳᵉᵖʳᵒʲ", "% reproj"))
	}
	if o.ShowDropped {
		parts = append(parts, strconv.Itoa(int(p.DroppedPerMin))+unit("ᵈʳᵒᵖ/ᵐⁱⁿ", " dropped/min"))
	}
	if o.ShowGPU && p.GPUms > 0 {
		parts = append(parts, formatMs(p.GPUms)+unit("ᵐˢ ᵍᵖᵘ", " ms GPU"))
	}
	if o.ShowCPU && p.CPUms > 0 {
		parts = append(parts, formatMs(p.CPUms)+unit("ᵐˢ ᶜᵖᵘ", " ms CPU"))
	}
	if o.ShowBudget && p.BudgetPct > 0 {
		parts = append(parts, strconv.Itoa(int(math.Round(p.BudgetPct)))+unit("%ᵇᵘᵈᵍᵉᵗ", "% budget"))
	}
	if o.ShowSmoothing && p.Smoothing {
		parts = append(parts, unit("ˢᵐᵒᵒᵗʰⁱⁿᵍ", "smoothing"))
	}
	return withLabel(o.Label, strings.Join(parts, o.Separator))
}

// vrPerfLine is the vrperf module's chatbox line.
func vrPerfLine(cfg Config) string {
	st := SteamVRState()
	if !st.Connected {
		return ""
	}
	return vrPerfText(st.Perf, st.PerfBad, cfg.VRPerfOptions)
}
