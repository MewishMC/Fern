package modules

// The fast half of the SteamVR worker: haptic pulses and controller reads.
// Everything here runs on the SteamVR worker's locked OS thread.

import (
	"math"
	"sync/atomic"
	"time"

	"Fern/modules/openvr"
)

// hapticRequest asks the SteamVR worker to play a pattern.
type hapticRequest struct {
	Pattern HapticPattern
	Reason  string // for the log and the panel, e.g. "heartbeat"
}

var (
	hapticQueue = make(chan hapticRequest, 32)
	vrWake      = make(chan struct{}, 1)
	vrResend    atomic.Bool // set on avatar change: send every parameter again
)

// wakeVR makes the SteamVR worker look at its queue now.
func wakeVR() {
	select {
	case vrWake <- struct{}{}:
	default:
	}
}

// QueueHaptic asks for a controller buzz. It never blocks and reports false
// when the queue is full. The pattern's On flag is ignored.
func QueueHaptic(p HapticPattern, reason string) bool {
	p.sanitize(defaultVRInputOptions().Haptics.Param)
	select {
	case hapticQueue <- hapticRequest{Pattern: p, Reason: reason}:
		wakeVR()
		return true
	default:
		return false
	}
}

// vrPulse is one scheduled buzz of one pattern.
type vrPulse struct {
	at       time.Time
	hands    [2]bool // left, right
	strength float64 // 0..1
	dur      time.Duration
}

// hapticGap is the pause between the pulses of one pattern.
func hapticGap(dur time.Duration) time.Duration {
	return min(max(dur/2, 60*time.Millisecond), 250*time.Millisecond)
}

// schedulePulses expands a pattern into its pulses, starting at now.
func schedulePulses(p HapticPattern, now time.Time) []vrPulse {
	dur := time.Duration(p.DurationMS) * time.Millisecond
	hands := [2]bool{p.Hand != HandRight, p.Hand != HandLeft}
	out := make([]vrPulse, 0, p.Pulses)
	at := now
	for i := 0; i < p.Pulses; i++ {
		out = append(out, vrPulse{at: at, hands: hands, strength: float64(p.Strength) / 100, dur: dur})
		at = at.Add(dur + hapticGap(dur))
	}
	return out
}

// legacyTrain repeats legacy pulses until a time, since one legacy pulse is
// at most 4ms long.
type legacyTrain struct {
	until  time.Time
	micros uint16
}

// legacyPulseEvery is how often a legacy pulse is repeated.
const legacyPulseEvery = 5 * time.Millisecond

// maxPendingPulses bounds the haptic backlog.
const maxPendingPulses = 64

// hapticFrequency is the vibration frequency asked of SteamVR Input.
const hapticFrequency = 160

// vrInputRun is the per-connection state of haptics and input.
type vrInputRun struct {
	actions  vrActions
	tried    bool
	pending  []vrPulse
	trains   [2]legacyTrain
	nextRead time.Time
	lastLive time.Time
	mode     string
	diff     vrParamDiff
	axes     map[uint32]vrAxisMap
	emit     func([]vrParam) // where changed parameters go; nil sends over OSC
	dir      string          // manifest folder; "" uses vrManifestDir
}

// vrHands finds the left and right controller slots from the device list.
func vrHands(devices []VRDevice) (idx [2]uint32, ok [2]bool) {
	for _, d := range devices {
		switch d.Kind {
		case KindLeft:
			if !ok[0] {
				idx[0], ok[0] = d.Index, true
			}
		case KindRight:
			if !ok[1] {
				idx[1], ok[1] = d.Index, true
			}
		}
	}
	return idx, ok
}

// tick plays due haptics and reads the controllers when a read is due. It
// returns how long the worker may sleep before calling it again.
func (r *vrInputRun) tick(io vrIO, now time.Time, o VRInputOptions, devices []VRDevice) time.Duration {
	if !r.tried && (o.Haptics.Enabled || o.Input.Enabled) && o.Input.Source != VRInputLegacy {
		r.tried = true
		dir := r.dir
		if dir == "" {
			dir = vrManifestDir()
		}
		r.actions = setupActions(io, dir)
		updateVRInput(func(s *VRInputStatus) { s.ActionsErr = r.actions.err })
	}
	idx, have := vrHands(devices)
	r.takeHaptics(now)
	wait := r.playHaptics(io, now, o, idx, have)

	if !o.Input.Enabled {
		if r.mode != "" || r.diff.last != nil {
			r.mode, r.diff = "", vrParamDiff{}
			updateVRInput(func(s *VRInputStatus) {
				s.Mode, s.Live, s.Left, s.Right, s.Curl, s.Params = "", false, false, false, false, []VRInputParam{}
			})
		}
		return wait
	}
	if now.Before(r.nextRead) {
		return min(wait, r.nextRead.Sub(now))
	}
	r.nextRead = now.Add(time.Second / time.Duration(o.Input.RateHz))
	r.read(io, now, o.Input, idx, have)
	return min(wait, r.nextRead.Sub(now))
}

// takeHaptics moves queued requests into the schedule.
func (r *vrInputRun) takeHaptics(now time.Time) {
	for {
		select {
		case req := <-hapticQueue:
			for _, p := range schedulePulses(req.Pattern, now) {
				if len(r.pending) < maxPendingPulses {
					r.pending = append(r.pending, p)
				}
			}
		default:
			return
		}
	}
}

// playHaptics starts every pulse that is due and keeps legacy trains going.
func (r *vrInputRun) playHaptics(io vrIO, now time.Time, o VRInputOptions, idx [2]uint32, have [2]bool) time.Duration {
	wait := time.Second
	useActions := r.actions.ready && o.Input.Source != VRInputLegacy
	keep := r.pending[:0]
	for _, p := range r.pending {
		if p.at.After(now) {
			keep = append(keep, p)
			wait = min(wait, p.at.Sub(now))
			continue
		}
		played, errText := false, ""
		for h := 0; h < 2; h++ {
			if !p.hands[h] {
				continue
			}
			if useActions {
				if err := io.Vibrate(r.actions.haptic, float32(p.dur.Seconds()), hapticFrequency, float32(p.strength), r.actions.hands[h]); err != nil {
					errText = err.Error()
					continue
				}
				played = true
				continue
			}
			if !have[h] {
				errText = "no controller in that hand"
				continue
			}
			micros := uint16(math.Round(p.strength * openvr.MaxHapticPulse))
			r.trains[h] = legacyTrain{until: now.Add(p.dur), micros: max(micros, 100)}
			played = true
		}
		mode := "legacy"
		if useActions {
			mode = "SteamVR Input"
		}
		updateVRInput(func(s *VRInputStatus) {
			if played {
				s.Haptics++
				s.HapticMode, s.HapticError = mode, ""
			} else if errText != "" {
				s.HapticError = errText
			}
		})
	}
	r.pending = keep
	for h := range r.trains {
		t := &r.trains[h]
		if t.until.IsZero() {
			continue
		}
		if !now.Before(t.until) || !have[h] {
			*t = legacyTrain{}
			continue
		}
		io.HapticPulse(idx[h], t.micros)
		wait = min(wait, legacyPulseEvery)
	}
	return wait
}

// read takes one reading of both controllers and sends what changed.
func (r *vrInputRun) read(io vrIO, now time.Time, o ControllerInputOptions, idx [2]uint32, have [2]bool) {
	var hands [2]vrHand
	mode := ""
	if o.Source != VRInputLegacy && r.actions.ready {
		priority := int32(0)
		if o.Global {
			priority = openvr.ActionSetOverlayGlobalPriority
		}
		if h, live, err := readActions(io, r.actions, priority); err == nil && live {
			hands, mode, r.lastLive = h, VRInputActions, now
		}
	}
	if mode == "" && o.Source != VRInputActions {
		for i := range hands {
			if !have[i] {
				continue
			}
			c, ok := io.ControllerState(idx[i])
			if !ok || c.PacketNum == 0 {
				continue
			}
			hands[i] = legacyHand(c, r.axisMap(io, idx[i]))
			mode = VRInputLegacy
		}
	}
	if mode == "" {
		hands = [2]vrHand{}
	}
	if vrResend.Swap(false) {
		r.diff.reset()
	}
	params := vrInputParams(o, hands[0], hands[1])
	if changed := r.diff.changed(params); len(changed) > 0 {
		r.send(changed)
	}
	dead := float64(o.Deadzone) / 100
	noteVRDerived(hands, dead)
	r.mode = mode
	list := vrParamList(params)
	dirs := [2][2]int{}
	thumbs := [2]int{}
	for i, h := range hands {
		dirs[i] = [2]int{vrDirection(h.Stick[0], h.Stick[1], dead, o.EightWay), padDirection(h, dead, o.EightWay)}
		thumbs[i] = thumbPosition(h)
	}
	updateVRInput(func(s *VRInputStatus) {
		s.Mode = mode
		s.Live = mode != ""
		s.Left, s.Right = hands[0].Present, hands[1].Present
		s.Curl = hands[0].HasCurl || hands[1].HasCurl
		s.Params = list
		s.Dirs, s.Thumbs = dirs, thumbs
	})
}

// axisMap reads and caches which legacy axis is which for a device.
func (r *vrInputRun) axisMap(io vrIO, i uint32) vrAxisMap {
	if m, ok := r.axes[i]; ok {
		return m
	}
	var types [openvr.ControllerAxisCount]int32
	for n := range types {
		types[n], _ = io.Int32Prop(i, int32(openvr.PropAxis0Type+n))
	}
	m := legacyAxes(types)
	if r.axes == nil {
		r.axes = map[uint32]vrAxisMap{}
	}
	r.axes[i] = m
	return m
}

func (r *vrInputRun) send(params []vrParam) {
	if r.emit != nil {
		r.emit(params)
		return
	}
	select {
	case vrSendCh <- params:
	default:
		// The sender is behind; the next change brings it up to date.
		r.diff.reset()
	}
}

var vrSendCh = make(chan []vrParam, 8)

// vrParamSender sends controller parameters off the SteamVR thread.
func vrParamSender() {
	for batch := range vrSendCh {
		o := GetConfig().OSCOptions
		n := 0
		for _, p := range batch {
			if err := sendAvatarParamTo(o, p.Name, p.Value); err == nil {
				n++
			}
		}
		updateVRInput(func(s *VRInputStatus) { s.Sent += uint64(n) })
	}
}

// dropHaptics empties the queue while SteamVR is not connected.
func dropHaptics() {
	for {
		select {
		case <-hapticQueue:
			updateVRInput(func(s *VRInputStatus) { s.HapticError = "SteamVR is not connected" })
		default:
			return
		}
	}
}
