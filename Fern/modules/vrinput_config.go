package modules

import "strings"

// Settings for SteamVR controller haptics and controller input; the worker
// side is in vrinput.go and haptics.go.

// Hands a haptic pattern can buzz.
const (
	HandBoth  = "both"
	HandLeft  = "left"
	HandRight = "right"
)

// Ways of reading the controllers.
const (
	VRInputAuto    = "auto"    // SteamVR Input, falling back to the legacy state
	VRInputActions = "actions" // SteamVR Input only
	VRInputLegacy  = "legacy"  // IVRSystem::GetControllerState only
)

// When the parameter haptic fires.
const (
	HapticParamChange = "change" // any new value
	HapticParamOn     = "on"     // the value turns on (true, or above zero)
)

// Bounds of a haptic pattern.
const (
	hapticMinMS     = 10
	hapticMaxMS     = 1000
	hapticMaxPulses = 5
)

// HapticPattern is one buzz: which controller, how strong, how long and how
// many times.
type HapticPattern struct {
	On         bool   `json:"on"`
	Hand       string `json:"hand"`        // both, left or right
	Strength   int    `json:"strength"`    // 1-100 %
	DurationMS int    `json:"duration_ms"` // 10-1000 per pulse
	Pulses     int    `json:"pulses"`      // 1-5, with a short gap between them
}

// HapticOptions are the controller vibrations Fern plays.
type HapticOptions struct {
	Enabled   bool          `json:"enabled"`
	Heartbeat HapticPattern `json:"heartbeat"` // every beat from the heart rate source
	Joined    HapticPattern `json:"joined"`    // someone joins your instance
	Param     HapticPattern `json:"param"`     // an avatar parameter changes
	ParamName string        `json:"param_name"`
	ParamMode string        `json:"param_mode"` // change or on
}

// ControllerInputOptions turn controller axes and buttons into avatar
// parameters.
type ControllerInputOptions struct {
	Enabled    bool   `json:"enabled"`
	Source     string `json:"source"`     // auto, actions or legacy
	Global     bool   `json:"global"`     // read SteamVR Input at overlay priority
	Prefix     string `json:"prefix"`     // put before every parameter name
	RateHz     int    `json:"rate_hz"`    // reads per second, 5-30
	Sticks     bool   `json:"sticks"`     // thumbstick X/Y
	Pads       bool   `json:"pads"`       // trackpad X/Y and touch
	Triggers   bool   `json:"triggers"`   // trigger and grip pull
	Fingers    bool   `json:"fingers"`    // finger curl, SteamVR Input only
	Thumb      bool   `json:"thumb"`      // where the thumb rests, as an int
	Directions bool   `json:"directions"` // stick and trackpad direction ints
	EightWay   bool   `json:"eight_way"`  // eight directions instead of four
	Deadzone   int    `json:"deadzone"`   // % of full tilt before a direction counts, 10-90
}

// VRInputOptions is the "SteamVRInput" section of config.json.
type VRInputOptions struct {
	Haptics HapticOptions          `json:"haptics"`
	Input   ControllerInputOptions `json:"input"`
}

func defaultVRInputOptions() VRInputOptions {
	return VRInputOptions{
		Haptics: HapticOptions{
			Heartbeat: HapticPattern{On: true, Hand: HandLeft, Strength: 30, DurationMS: 40, Pulses: 1},
			Joined:    HapticPattern{On: true, Hand: HandBoth, Strength: 60, DurationMS: 120, Pulses: 2},
			Param:     HapticPattern{On: false, Hand: HandBoth, Strength: 50, DurationMS: 80, Pulses: 1},
			ParamMode: HapticParamOn,
		},
		Input: ControllerInputOptions{
			Source:     VRInputAuto,
			RateHz:     10,
			Sticks:     true,
			Pads:       true,
			Triggers:   true,
			Fingers:    false,
			Thumb:      true,
			Directions: true,
			Deadzone:   50,
		},
	}
}

func (p *HapticPattern) sanitize(def HapticPattern) {
	switch p.Hand {
	case HandBoth, HandLeft, HandRight:
	default:
		p.Hand = def.Hand
	}
	if p.Strength == 0 {
		p.Strength = def.Strength
	}
	p.Strength = clampInt(p.Strength, 1, 100)
	if p.DurationMS == 0 {
		p.DurationMS = def.DurationMS
	}
	p.DurationMS = clampInt(p.DurationMS, hapticMinMS, hapticMaxMS)
	if p.Pulses == 0 {
		p.Pulses = def.Pulses
	}
	p.Pulses = clampInt(p.Pulses, 1, hapticMaxPulses)
}

// vrParamName keeps an avatar parameter name (or prefix) to the characters
// VRChat uses: letters, digits, _ - . and /, without a leading slash.
func vrParamName(s string, n int) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, AvatarParamPrefix)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '/':
			b.WriteRune(r)
		}
	}
	return RuneLimit(strings.TrimLeft(b.String(), "/"), n)
}

func (o *VRInputOptions) sanitize() {
	def := defaultVRInputOptions()
	h := &o.Haptics
	h.Heartbeat.sanitize(def.Haptics.Heartbeat)
	h.Joined.sanitize(def.Haptics.Joined)
	h.Param.sanitize(def.Haptics.Param)
	h.ParamName = vrParamName(h.ParamName, 64)
	switch h.ParamMode {
	case HapticParamChange, HapticParamOn:
	default:
		h.ParamMode = def.Haptics.ParamMode
	}

	in := &o.Input
	switch in.Source {
	case VRInputAuto, VRInputActions, VRInputLegacy:
	default:
		in.Source = def.Input.Source
	}
	in.Prefix = vrParamName(in.Prefix, 24)
	if in.RateHz == 0 {
		in.RateHz = def.Input.RateHz
	}
	in.RateHz = clampInt(in.RateHz, 5, 30)
	if in.Deadzone == 0 {
		in.Deadzone = def.Input.Deadzone
	}
	in.Deadzone = clampInt(in.Deadzone, 10, 90)
}

// vrInputWanted reports whether haptics or controller input need SteamVR.
func vrInputWanted(c Config) bool {
	return c.VRInput.Haptics.Enabled || c.VRInput.Input.Enabled
}

// heartHapticsWanted reports whether the heartbeat haptic needs the heart
// rate source running.
func heartHapticsWanted(c Config) bool {
	return c.VRInput.Haptics.Enabled && c.VRInput.Haptics.Heartbeat.On
}
