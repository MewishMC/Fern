package modules

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"

	"Fern/modules/openvr"
)

// maxBatteryDevices caps how many devices the config remembers.
const maxBatteryDevices = 64

// SteamVRStatus is what Fern currently knows from SteamVR, for the battery and
// performance lines and their panel pages.
type SteamVRStatus struct {
	Running    bool       `json:"running"`   // vrserver.exe is up
	Connected  bool       `json:"connected"` // Fern is reading from it
	Idle       bool       `json:"idle"`      // running, but neither line is on, so Fern stays away
	Error      string     `json:"error"`
	Interfaces string     `json:"interfaces"`
	Devices    []VRDevice `json:"devices"`
	Perf       VRPerf     `json:"perf"`
	PerfBad    bool       `json:"perfBad"` // after hysteresis
}

var (
	steamVRMu    sync.RWMutex
	steamVRState SteamVRStatus
)

// SteamVRState returns a copy of the latest SteamVR readings. It never waits
// on SteamVR: the worker updates it in the background.
func SteamVRState() SteamVRStatus {
	steamVRMu.RLock()
	defer steamVRMu.RUnlock()
	out := steamVRState
	out.Devices = append([]VRDevice{}, steamVRState.Devices...)
	return out
}

func publishSteamVR(st SteamVRStatus) {
	steamVRMu.Lock()
	changed := steamVRState.Connected != st.Connected
	steamVRState = st
	steamVRMu.Unlock()
	if changed {
		NotifyChange()
	}
}

// startSteamVRWorker polls SteamVR once a second on its own OS thread, so a
// slow call into OpenVR can never hold up the chatbox sender. Haptics and
// controller input (vrinput_worker.go) run on the same thread in between,
// as often as they need.
func startSteamVRWorker() {
	go func() {
		// OpenVR is not thread safe; keep every call on one thread.
		runtime.LockOSThread()
		w := &vrWorker{}
		next := time.Now().Add(time.Second)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			now := time.Now()
			if !now.Before(next) {
				w.step(now)
				next = now.Add(time.Second)
			}
			wait := time.Until(next)
			if w.s != nil {
				wait = min(wait, w.in.tick(w.s, time.Now(), w.inOpts, w.devices))
			} else {
				dropHaptics()
			}
			timer.Reset(max(wait, time.Millisecond))
			select {
			case <-timer.C:
			case <-vrWake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}
	}()
}

type vrWorker struct {
	s        *openvr.Session
	lastErr  string
	nextTry  time.Time
	backoff  time.Duration
	quitSeen bool // SteamVR asked its apps to quit; wait for it to go away

	in     vrInputRun     // haptics and controller input
	inOpts VRInputOptions // as of the last step

	devices        []VRDevice
	devicesAt      time.Time
	refreshDevices bool

	hz       float64
	hzAt     time.Time
	prev     compositorCounts
	prevAt   time.Time
	havePrev bool
	deltas   []perfDelta
	gate     perfGate
	perf     VRPerf
}

func (w *vrWorker) disconnect() {
	if w.s != nil {
		w.s.Shutdown()
		w.s = nil
	}
	w.devices, w.havePrev, w.deltas, w.perf, w.gate, w.hz = nil, false, nil, VRPerf{}, perfGate{}, 0
	w.in = vrInputRun{}
	updateVRInput(func(s *VRInputStatus) {
		s.Connected, s.Mode, s.Live, s.Left, s.Right, s.Curl, s.Interface = false, "", false, false, false, false, ""
		s.Params = []VRInputParam{}
	})
}

func (w *vrWorker) connect(now time.Time) {
	s, err := openvr.Init()
	if err != nil {
		var vrErr *openvr.Error
		if errors.As(err, &vrErr) && vrErr.NotRunning() {
			// vrserver.exe is up but not ready for apps yet; try again soon.
			w.lastErr = "SteamVR is still starting"
			w.nextTry = now.Add(5 * time.Second)
			return
		}
		w.lastErr = err.Error()
		fmt.Println("steamvr:", err)
		// Back off gently: 5s, 10s, 20s ... up to a minute.
		if w.backoff == 0 {
			w.backoff = 5 * time.Second
		} else if w.backoff < time.Minute {
			w.backoff *= 2
		}
		w.nextTry = now.Add(w.backoff)
		return
	}
	sys, comp := s.Versions()
	fmt.Println("steamvr: connected using", sys, comp)
	w.s, w.lastErr, w.backoff, w.refreshDevices = s, "", 0, true
	updateVRInput(func(st *VRInputStatus) { st.Connected, st.Interface = true, s.InputVersion() })
}

func (w *vrWorker) step(now time.Time) {
	cfg := GetConfig()
	wanted := cfg.DisplayOptions.Battery || cfg.DisplayOptions.VRPerf || vrInputWanted(cfg)
	w.inOpts = cfg.VRInput
	running := inVR(false)
	if !running {
		w.quitSeen, w.backoff, w.nextTry, w.lastErr = false, 0, time.Time{}, ""
	}
	if w.s != nil && (!running || !wanted) {
		w.disconnect()
	}
	if w.s == nil && running && wanted && !w.quitSeen && !now.Before(w.nextTry) {
		w.connect(now)
	}
	if w.s == nil {
		publishSteamVR(SteamVRStatus{Running: running, Idle: running && !wanted, Error: w.lastErr})
		return
	}

	for {
		kind, _, ok := w.s.PollEvent()
		if !ok {
			break
		}
		switch kind {
		case openvr.EventQuit, openvr.EventDriverRequestedQuit:
			// Let SteamVR close without waiting on Fern.
			w.s.AcknowledgeQuit()
			w.disconnect()
			w.quitSeen = true
			fmt.Println("steamvr: SteamVR is quitting, disconnected")
			publishSteamVR(SteamVRStatus{Running: running})
			return
		case openvr.EventTrackedDeviceActivated, openvr.EventTrackedDeviceDeactivated:
			w.refreshDevices = true
		}
	}

	if w.refreshDevices || now.Sub(w.devicesAt) >= 5*time.Second {
		w.devices = readVRDevices(w.s)
		w.devicesAt, w.refreshDevices = now, false
		w.in.axes = nil
		if cfg.DisplayOptions.Battery {
			if next, changed := rememberDevices(append([]BatteryDevice{}, cfg.BatteryOptions.Devices...), w.devices); changed {
				UpdateConfig(func(c *Config) { c.BatteryOptions.Devices = next })
			}
		}
	}
	if cfg.DisplayOptions.VRPerf {
		w.samplePerf(now, cfg.VRPerfOptions)
	}

	sys, comp := w.s.Versions()
	publishSteamVR(SteamVRStatus{
		Running:    true,
		Connected:  true,
		Interfaces: strings.TrimSpace(sys + " " + comp),
		Devices:    append([]VRDevice{}, w.devices...),
		Perf:       w.perf,
		PerfBad:    w.gate.bad,
	})
}

func (w *vrWorker) samplePerf(now time.Time, o VRPerfOptions) {
	if w.hz == 0 || now.Sub(w.hzAt) >= 10*time.Second {
		if hz, ok := w.s.FloatProp(0, openvr.PropDisplayFrequency); ok && hz > 0 && hz < 1000 {
			w.hz = float64(hz)
		}
		w.hzAt = now
	}
	stats, ok := w.s.CumulativeStats()
	if !ok {
		w.perf = VRPerf{}
		return
	}
	cur := compositorCounts{
		Pid:         stats.Pid,
		Presents:    stats.NumFramePresents,
		Dropped:     stats.NumDroppedFrames,
		Reprojected: stats.NumReprojectedFrames,
		Submits:     stats.NumFrameSubmits,
		SumAppGPU:   stats.SumApplicationGPUTimeMS,
		SumAppCPU:   stats.SumApplicationCPUTimeMS,
	}
	if w.havePrev {
		if d, ok := diffCounts(w.prev, cur, now.Sub(w.prevAt).Seconds()); ok {
			w.deltas = trimDeltas(append(w.deltas, d))
		} else {
			w.deltas = nil
		}
	}
	w.prev, w.prevAt, w.havePrev = cur, now, true

	fallbackGPU, smoothing := 0.0, false
	if t, ok := w.s.FrameTiming(0); ok {
		fallbackGPU = float64(t.PreSubmitGpuMs + t.PostSubmitGpuMs)
		smoothing = t.ReprojectionFlags&openvr.ReprojectionMotion != 0
	}
	w.perf = summarizePerf(w.deltas, w.hz, fallbackGPU, smoothing)
	w.gate.update(now, perfIsBad(w.perf, o), time.Duration(o.ClearSeconds)*time.Second)
}

// trimDeltas keeps about a minute of samples.
func trimDeltas(deltas []perfDelta) []perfDelta {
	total := 0.0
	for i := len(deltas) - 1; i >= 0; i-- {
		total += deltas[i].Seconds
		if total > perfLongWindow.Seconds() {
			return append([]perfDelta{}, deltas[i:]...)
		}
	}
	return deltas
}

// readVRDevices lists the connected headset, controllers and trackers.
func readVRDevices(s *openvr.Session) []VRDevice {
	var out []VRDevice
	for i := uint32(0); i < openvr.MaxTrackedDeviceCount; i++ {
		var kind string
		switch s.DeviceClass(i) {
		case openvr.ClassHMD:
			kind = KindHeadset
		case openvr.ClassController:
			switch s.ControllerRole(i) {
			case openvr.RoleLeftHand:
				kind = KindLeft
			case openvr.RoleRightHand:
				kind = KindRight
			default:
				kind = KindController
			}
		case openvr.ClassGenericTracker:
			kind = KindTracker
		default:
			continue // empty slots, base stations, display redirects
		}
		if !s.Connected(i) {
			continue
		}
		d := VRDevice{
			Index:  i,
			Kind:   kind,
			Serial: strings.TrimSpace(s.StringProp(i, openvr.PropSerialNumber)),
			Model:  strings.TrimSpace(s.StringProp(i, openvr.PropModelNumber)),
		}
		// Some drivers report a level without setting the "provides" flag, so
		// only an explicit false rules the battery out.
		if provides, ok := s.BoolProp(i, openvr.PropProvidesBatteryStatus); provides || !ok {
			if pct, ok := s.FloatProp(i, openvr.PropDeviceBatteryPercentage); ok {
				d.HasBattery = true
				d.Battery = clampInt(int(math.Round(float64(pct)*100)), 0, 100)
				d.Charging, _ = s.BoolProp(i, openvr.PropDeviceIsCharging)
			}
		}
		out = append(out, d)
	}
	defaultDeviceNames(out)
	return out
}

// vrOneLine keeps chatbox settings on a single line.
func vrOneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)
}

// sanitizeSteamVR bounds the battery and performance settings.
func (c *Config) sanitizeSteamVR() {
	def := DefaultConfig()
	b := &c.BatteryOptions
	b.Label = RuneLimit(strings.TrimSpace(b.Label), 16)
	b.Template = RuneLimit(strings.TrimSpace(vrOneLine(b.Template)), 80)
	if b.Template == "" {
		b.Template = def.BatteryOptions.Template
	}
	b.Separator = RuneLimit(vrOneLine(b.Separator), 8)
	if b.Separator == "" {
		b.Separator = def.BatteryOptions.Separator
	}
	for _, s := range []*string{&b.HeadsetIcon, &b.ControllerIcon, &b.TrackerIcon, &b.ChargingMark} {
		*s = RuneLimit(strings.TrimSpace(vrOneLine(*s)), 4)
	}
	b.LowTag = RuneLimit(strings.TrimSpace(vrOneLine(b.LowTag)), 12)
	b.LowThreshold = clampInt(b.LowThreshold, 1, 100)
	switch b.Sort {
	case BatterySortDetected, BatterySortName, BatterySortLow, BatterySortHigh, BatterySortType:
	default:
		b.Sort = BatterySortDetected
	}
	b.MaxDevices = clampInt(b.MaxDevices, 0, 16)
	devices := make([]BatteryDevice, 0, len(b.Devices))
	seen := map[string]bool{}
	for _, d := range b.Devices {
		d.Serial = RuneLimit(strings.TrimSpace(d.Serial), 64)
		if d.Serial == "" || seen[d.Serial] || len(devices) >= maxBatteryDevices {
			continue
		}
		seen[d.Serial] = true
		d.Name = RuneLimit(strings.TrimSpace(vrOneLine(d.Name)), 32)
		d.Model = RuneLimit(strings.TrimSpace(vrOneLine(d.Model)), 64)
		if _, ok := kindOrder[d.Kind]; !ok {
			d.Kind = KindController
		}
		devices = append(devices, d)
	}
	b.Devices = devices

	p := &c.VRPerfOptions
	p.Label = RuneLimit(strings.TrimSpace(p.Label), 16)
	p.Separator = RuneLimit(vrOneLine(p.Separator), 8)
	if p.Separator == "" {
		p.Separator = def.VRPerfOptions.Separator
	}
	p.BadReprojection = clampInt(p.BadReprojection, 0, 100)
	p.BadDroppedPerMin = clampInt(p.BadDroppedPerMin, 0, 600)
	p.BadFPSPercent = clampInt(p.BadFPSPercent, 0, 100)
	p.ClearSeconds = clampInt(p.ClearSeconds, 0, 120)
}
