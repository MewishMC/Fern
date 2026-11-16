package modules

// Controller input from SteamVR, sent to VRChat as avatar parameters.
//
// Fern connects to SteamVR as a background application. Two ways of reading
// the controllers are open to it:
//
//   - SteamVR Input (IVRInput): Fern registers an action manifest with its own
//     action set and default bindings for Index, Touch, Vive and WMR
//     controllers. This is the only way to get finger curl. SteamVR gives an
//     app's actions data only while that app may have input: for a background
//     app that is normally not while a game such as VRChat has focus. Reading
//     at overlay priority ("Read while a game has focus") makes the set global,
//     which also takes the bound inputs away from the game, so it is off by
//     default.
//   - The legacy controller state (IVRSystem::GetControllerState) gives axes
//     and buttons but no fingers. SteamVR fills it from the legacy binding of
//     whichever app it treats as Fern, which may also be empty while another
//     app has focus.
//
// "Auto" uses SteamVR Input while it gives data and the legacy state when it
// does not. The panel shows which one answered.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"Fern/modules/openvr"
)

// vrHand is one controller's reading, whatever API it came from.
type vrHand struct {
	Present    bool
	HasStick   bool
	HasPad     bool
	HasCurl    bool
	Stick      [2]float64 // -1..1
	Pad        [2]float64 // -1..1
	StickTouch bool
	PadTouch   bool
	ATouch     bool // A on the right, X on the left for Touch; A on Index
	BTouch     bool
	Trigger    float64    // 0..1
	Grip       float64    // 0..1
	Curl       [5]float64 // thumb, index, middle, ring, pinky; 0 open, 1 fist
}

// Thumb positions sent as the {Hand}Thumb int.
const (
	ThumbNone  = 0
	ThumbA     = 1
	ThumbB     = 2
	ThumbPad   = 3
	ThumbStick = 4
)

// thumbPosition says where the thumb rests, the same numbering as
// ThumbParamsOSC so avatars made for it work.
func thumbPosition(h vrHand) int {
	switch {
	case h.ATouch:
		return ThumbA
	case h.BTouch:
		return ThumbB
	case h.PadTouch:
		return ThumbPad
	case h.StickTouch:
		return ThumbStick
	}
	return ThumbNone
}

// vrDirection turns an axis into a direction: 0 in the dead zone, then 1 up,
// 2 right, 3 down, 4 left, or with eight directions 1 up to 8 up-left going
// clockwise. dead is a fraction of full tilt.
func vrDirection(x, y, dead float64, eight bool) int {
	if math.IsNaN(x) || math.IsNaN(y) || math.Hypot(x, y) < dead {
		return 0
	}
	deg := math.Atan2(x, y) * 180 / math.Pi // 0 is up, 90 is right
	if deg < 0 {
		deg += 360
	}
	if eight {
		return int(math.Round(deg/45))%8 + 1
	}
	return int(math.Round(deg/90))%4 + 1
}

// Direction names, for the panel and the rule editor.
var vrDirNames4 = []string{"centre", "up", "right", "down", "left"}
var vrDirNames8 = []string{"centre", "up", "up-right", "right", "down-right", "down", "down-left", "left", "up-left"}

func clamp1(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(lo, math.Min(hi, v))
}

// vrParam is one avatar parameter Fern sends from the controllers.
type vrParam struct {
	Name  string
	Value any // bool, int or float64
}

// vrHandParams lists the parameters for one hand ("Left" or "Right").
func vrHandParams(o ControllerInputOptions, side string, h vrHand) []vrParam {
	p := o.Prefix + side
	var out []vrParam
	f := func(name string, v float64) { out = append(out, vrParam{p + name, v}) }
	if o.Sticks {
		f("StickX", clamp1(h.Stick[0], -1, 1))
		f("StickY", clamp1(h.Stick[1], -1, 1))
	}
	if o.Pads {
		f("PadX", clamp1(h.Pad[0], -1, 1))
		f("PadY", clamp1(h.Pad[1], -1, 1))
		out = append(out, vrParam{p + "PadTouch", h.PadTouch})
	}
	if o.Triggers {
		f("Trigger", clamp1(h.Trigger, 0, 1))
		f("Grip", clamp1(h.Grip, 0, 1))
	}
	if o.Fingers {
		for i, name := range []string{"ThumbCurl", "IndexCurl", "MiddleCurl", "RingCurl", "PinkyCurl"} {
			f(name, clamp1(h.Curl[i], 0, 1))
		}
	}
	if o.Thumb {
		out = append(out, vrParam{p + "Thumb", thumbPosition(h)})
	}
	if o.Directions {
		dead := float64(o.Deadzone) / 100
		out = append(out,
			vrParam{p + "StickDir", vrDirection(h.Stick[0], h.Stick[1], dead, o.EightWay)},
			vrParam{p + "PadDir", padDirection(h, dead, o.EightWay)})
	}
	return out
}

// padDirection only counts a trackpad direction while the thumb is on it:
// an untouched pad reports where the thumb last was on some drivers.
func padDirection(h vrHand, dead float64, eight bool) int {
	if !h.PadTouch {
		return 0
	}
	return vrDirection(h.Pad[0], h.Pad[1], dead, eight)
}

// vrInputParams lists every parameter for both hands.
func vrInputParams(o ControllerInputOptions, left, right vrHand) []vrParam {
	return append(vrHandParams(o, "Left", left), vrHandParams(o, "Right", right)...)
}

// vrParamDiff remembers what was last sent and picks out what changed.
type vrParamDiff struct {
	last map[string]any
}

// floatStep is the smallest float change worth sending; VRChat syncs floats
// at about 1/127 anyway.
const floatStep = 0.01

// changed returns the parameters that differ from the last ones sent and
// remembers them. After reset everything is sent again.
func (d *vrParamDiff) changed(params []vrParam) []vrParam {
	if d.last == nil {
		d.last = map[string]any{}
	}
	var out []vrParam
	for _, p := range params {
		prev, ok := d.last[p.Name]
		if ok {
			if pf, isF := prev.(float64); isF {
				if nf, _ := p.Value.(float64); math.Abs(nf-pf) < floatStep && !(nf == 0 && pf != 0) {
					continue
				}
			} else if prev == p.Value {
				continue
			}
		}
		d.last[p.Name] = p.Value
		out = append(out, p)
	}
	return out
}

func (d *vrParamDiff) reset() { d.last = nil }

/* ---------------- action manifest ---------------- */

const vrActionSet = "/actions/fern"

// Action names, without the set; "left" and "right" are put in front.
var vrInputActions = []struct{ name, kind string }{
	{"stick", "vector2"}, {"pad", "vector2"}, {"trigger", "vector1"}, {"grip", "vector1"},
	{"sticktouch", "boolean"}, {"padtouch", "boolean"}, {"atouch", "boolean"}, {"btouch", "boolean"},
	{"skeleton", "skeleton"},
}

const vrHapticAction = vrActionSet + "/out/haptic"

func vrActionName(side, name string) string { return vrActionSet + "/in/" + side + name }

type vrManifestAction struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Skeleton    string `json:"skeleton,omitempty"`
	Requirement string `json:"requirement"`
}

// vrManifest builds the action manifest, naming the binding files next to it.
func vrManifest() map[string]any {
	var actions []vrManifestAction
	loc := map[string]string{"language_tag": "en_US", vrActionSet: "Fern", vrHapticAction: "Haptics"}
	for _, side := range []string{"left", "right"} {
		for _, a := range vrInputActions {
			act := vrManifestAction{Name: vrActionName(side, a.name), Type: a.kind, Requirement: "optional"}
			if a.kind == "skeleton" {
				act.Skeleton = "/skeleton/hand/" + side
			}
			actions = append(actions, act)
			loc[act.Name] = side + " " + a.name
		}
	}
	actions = append(actions, vrManifestAction{Name: vrHapticAction, Type: "vibration", Requirement: "optional"})
	var defaults []map[string]string
	for _, t := range vrBindingTypes {
		defaults = append(defaults, map[string]string{"controller_type": t, "binding_url": "bindings_" + t + ".json"})
	}
	return map[string]any{
		"default_bindings": defaults,
		"actions":          actions,
		"action_sets":      []map[string]string{{"name": vrActionSet, "usage": "single"}},
		"localization":     []map[string]string{loc},
	}
}

// Controller types Fern ships default bindings for.
var vrBindingTypes = []string{"knuckles", "oculus_touch", "vive_controller", "holographic_controller"}

type vrBindingSource struct {
	Path   string                       `json:"path"`
	Mode   string                       `json:"mode"`
	Inputs map[string]map[string]string `json:"inputs"`
}

// vrBinding builds the default binding for one controller type.
func vrBinding(controller string) map[string]any {
	var sources []vrBindingSource
	var skeleton, haptics []map[string]string
	out := func(side, name string) map[string]string {
		return map[string]string{"output": vrActionName(side, name)}
	}
	for _, side := range []string{"left", "right"} {
		hand := "/user/hand/" + side + "/input/"
		stick := vrBindingSource{Path: hand + "thumbstick", Mode: "joystick",
			Inputs: map[string]map[string]string{"position": out(side, "stick"), "touch": out(side, "sticktouch")}}
		pad := vrBindingSource{Path: hand + "trackpad", Mode: "trackpad",
			Inputs: map[string]map[string]string{"position": out(side, "pad"), "touch": out(side, "padtouch")}}
		trigger := vrBindingSource{Path: hand + "trigger", Mode: "trigger",
			Inputs: map[string]map[string]string{"pull": out(side, "trigger")}}
		grip := vrBindingSource{Path: hand + "grip", Mode: "trigger",
			Inputs: map[string]map[string]string{"pull": out(side, "grip")}}
		button := func(name, action string) vrBindingSource {
			return vrBindingSource{Path: hand + name, Mode: "button",
				Inputs: map[string]map[string]string{"touch": out(side, action)}}
		}
		switch controller {
		case "knuckles":
			sources = append(sources, stick, pad, trigger, grip, button("a", "atouch"), button("b", "btouch"))
		case "oculus_touch":
			a, b := "a", "b"
			if side == "left" {
				a, b = "x", "y"
			}
			sources = append(sources, stick, trigger, grip, button(a, "atouch"), button(b, "btouch"))
		case "vive_controller":
			sources = append(sources, pad, trigger)
		case "holographic_controller":
			sources = append(sources, stick, pad, trigger)
		}
		skeleton = append(skeleton, map[string]string{"output": vrActionName(side, "skeleton"), "path": "/user/hand/" + side + "/input/skeleton/" + side})
		haptics = append(haptics, map[string]string{"output": vrHapticAction, "path": "/user/hand/" + side + "/output/haptic"})
	}
	return map[string]any{
		"controller_type": controller,
		"name":            "Fern defaults",
		"description":     "Reads the sticks, trackpads, triggers and fingers for avatar parameters",
		"bindings": map[string]any{
			vrActionSet: map[string]any{"sources": sources, "skeleton": skeleton, "haptics": haptics},
		},
	}
}

// writeIfChanged writes a file unless it already holds data.
func writeIfChanged(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// writeVRManifest writes the action manifest and bindings into dir and
// returns the manifest's absolute path.
func writeVRManifest(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, t := range vrBindingTypes {
		data, err := json.MarshalIndent(vrBinding(t), "", "  ")
		if err != nil {
			return "", err
		}
		if err := writeIfChanged(filepath.Join(dir, "bindings_"+t+".json"), data); err != nil {
			return "", err
		}
	}
	data, err := json.MarshalIndent(vrManifest(), "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "actions.json")
	if err := writeIfChanged(path, data); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// vrManifestDir is where the manifest lives: %LOCALAPPDATA%\Fern\steamvr-input.
var vrManifestDir = func() string {
	if dir := DataDir(); dir != "" {
		return filepath.Join(dir, "steamvr-input")
	}
	return ""
}

/* ---------------- reading, on the SteamVR thread ---------------- */

// vrIO is the part of an OpenVR session the input code uses, so tests can
// stand in for SteamVR.
type vrIO interface {
	HasInput() bool
	SetActionManifestPath(path string) error
	ActionSetHandle(name string) (uint64, error)
	ActionHandle(name string) (uint64, error)
	InputSourceHandle(path string) (uint64, error)
	UpdateActionState(set uint64, priority int32) error
	DigitalAction(action, restrict uint64) (openvr.DigitalActionData, error)
	AnalogAction(action, restrict uint64) (openvr.AnalogActionData, error)
	SkeletalSummary(action uint64, summaryType int32) (openvr.SkeletalSummary, error)
	Vibrate(action uint64, duration, frequency, amplitude float32, restrict uint64) error
	ControllerState(i uint32) (openvr.ControllerState, bool)
	HapticPulse(i uint32, micros uint16)
	Int32Prop(i uint32, prop int32) (int32, bool)
}

// vrActions holds the SteamVR Input handles once the manifest is set.
type vrActions struct {
	ready   bool
	err     string
	set     uint64
	haptic  uint64
	hands   [2]uint64            // /user/hand/left, /user/hand/right
	handles [2]map[string]uint64 // per hand, by action name
}

// setupActions registers the manifest and looks up every handle. It runs
// once per connection.
func setupActions(io vrIO, dir string) vrActions {
	var a vrActions
	fail := func(err error) vrActions {
		a.err = err.Error()
		return a
	}
	if !io.HasInput() {
		return fail(errors.New("SteamVR Input is not available"))
	}
	if dir == "" {
		return fail(errors.New("no folder for the action manifest"))
	}
	path, err := writeVRManifest(dir)
	if err != nil {
		return fail(err)
	}
	if err := io.SetActionManifestPath(path); err != nil {
		return fail(err)
	}
	if a.set, err = io.ActionSetHandle(vrActionSet); err != nil {
		return fail(err)
	}
	if a.haptic, err = io.ActionHandle(vrHapticAction); err != nil {
		return fail(err)
	}
	for i, side := range []string{"left", "right"} {
		if a.hands[i], err = io.InputSourceHandle("/user/hand/" + side); err != nil {
			return fail(err)
		}
		a.handles[i] = map[string]uint64{}
		for _, act := range vrInputActions {
			h, err := io.ActionHandle(vrActionName(side, act.name))
			if err != nil {
				return fail(fmt.Errorf("%s: %w", act.name, err))
			}
			a.handles[i][act.name] = h
		}
	}
	a.ready = true
	return a
}

// readActions reads both hands through SteamVR Input. live is false when
// SteamVR gave no active action at all, which means Fern has no input.
func readActions(io vrIO, a vrActions, priority int32) (hands [2]vrHand, live bool, err error) {
	if err := io.UpdateActionState(a.set, priority); err != nil {
		return hands, false, err
	}
	for i := range hands {
		h := &hands[i]
		get := func(name string) openvr.AnalogActionData {
			d, err := io.AnalogAction(a.handles[i][name], 0)
			if err != nil || !d.Active {
				return openvr.AnalogActionData{}
			}
			h.Present, live = true, true
			return d
		}
		touch := func(name string) bool {
			d, err := io.DigitalAction(a.handles[i][name], 0)
			if err != nil || !d.Active {
				return false
			}
			h.Present, live = true, true
			return d.State
		}
		if d := get("stick"); d.Active {
			h.HasStick, h.Stick = true, [2]float64{float64(d.X), float64(d.Y)}
		}
		if d := get("pad"); d.Active {
			h.HasPad, h.Pad = true, [2]float64{float64(d.X), float64(d.Y)}
		}
		h.Trigger = float64(get("trigger").X)
		h.Grip = float64(get("grip").X)
		h.StickTouch, h.PadTouch = touch("sticktouch"), touch("padtouch")
		h.ATouch, h.BTouch = touch("atouch"), touch("btouch")
		if s, err := io.SkeletalSummary(a.handles[i]["skeleton"], openvr.SummaryFromDevice); err == nil {
			h.HasCurl, h.Present = true, true
			for f := range h.Curl {
				h.Curl[f] = float64(s.Curl[f])
			}
		}
	}
	return hands, live, nil
}

// vrAxisMap says which legacy axis holds what, from Prop_Axis{n}Type.
type vrAxisMap struct {
	stick, pad, trigger, grip int // axis index, -1 when absent
}

func legacyAxes(types [openvr.ControllerAxisCount]int32) vrAxisMap {
	m := vrAxisMap{-1, -1, -1, -1}
	for i, t := range types {
		switch t {
		case openvr.AxisJoystick:
			if m.stick < 0 {
				m.stick = i
			}
		case openvr.AxisTrackPad:
			if m.pad < 0 {
				m.pad = i
			}
		case openvr.AxisTrigger:
			if m.trigger < 0 {
				m.trigger = i
			} else if m.grip < 0 {
				m.grip = i
			}
		}
	}
	return m
}

// legacyHand turns a legacy controller state into a reading. Axis n's touch
// and press bits are button 32+n.
func legacyHand(c openvr.ControllerState, m vrAxisMap) vrHand {
	h := vrHand{Present: true}
	axis := func(i int) [2]float64 { return [2]float64{float64(c.Axis[i].X), float64(c.Axis[i].Y)} }
	if m.stick >= 0 {
		h.HasStick, h.Stick = true, axis(m.stick)
		h.StickTouch = c.IsTouched(uint(openvr.ButtonTouchpad + m.stick))
	}
	if m.pad >= 0 {
		h.HasPad, h.Pad = true, axis(m.pad)
		h.PadTouch = c.IsTouched(uint(openvr.ButtonTouchpad + m.pad))
	}
	if m.trigger >= 0 {
		h.Trigger = float64(c.Axis[m.trigger].X)
	} else if c.IsPressed(openvr.ButtonTrigger) {
		h.Trigger = 1
	}
	if m.grip >= 0 {
		h.Grip = float64(c.Axis[m.grip].X)
	} else if c.IsPressed(openvr.ButtonGrip) {
		h.Grip = 1
	}
	h.ATouch = c.IsTouched(openvr.ButtonA)
	h.BTouch = c.IsTouched(openvr.ButtonApplicationMenu)
	return h
}

/* ---------------- status ---------------- */

// VRInputParam is one parameter as last sent, for the panel.
type VRInputParam struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// VRInputStatus is the Controllers page's view of haptics and input.
type VRInputStatus struct {
	Connected   bool           `json:"connected"`   // Fern is connected to SteamVR
	Mode        string         `json:"mode"`        // "actions", "legacy" or "" while nothing answers
	ActionsErr  string         `json:"actionsErr"`  // why SteamVR Input is not used
	Interface   string         `json:"interface"`   // IVRInput version
	Live        bool           `json:"live"`        // readings arrived in the last two seconds
	Left        bool           `json:"left"`        // a left controller answered
	Right       bool           `json:"right"`       // a right controller answered
	Curl        bool           `json:"curl"`        // finger curl is available
	Params      []VRInputParam `json:"params"`      // what was last sent, sorted
	Sent        uint64         `json:"sent"`        // parameters sent since startup
	Haptics     uint64         `json:"haptics"`     // buzzes played since startup
	HapticMode  string         `json:"hapticMode"`  // how the last buzz was played
	HapticError string         `json:"hapticError"` // why the last buzz could not play
	Notice      string         `json:"notice"`      // result of the last panel test
	NoticeSeq   int            `json:"noticeSeq"`
	NoticeBad   bool           `json:"noticeBad"`
	Dirs        [2][2]int      `json:"dirs"`   // [hand][stick, pad] direction, as sent
	Thumbs      [2]int         `json:"thumbs"` // [hand] thumb position
}

var (
	vrInMu    sync.Mutex
	vrInState = VRInputStatus{Params: []VRInputParam{}}
)

// CurrentVRInput returns a copy of the haptics and input status.
func CurrentVRInput() VRInputStatus {
	vrInMu.Lock()
	defer vrInMu.Unlock()
	out := vrInState
	out.Params = append([]VRInputParam{}, vrInState.Params...)
	return out
}

func updateVRInput(fn func(s *VRInputStatus)) {
	vrInMu.Lock()
	fn(&vrInState)
	vrInMu.Unlock()
}

func setVRInputNotice(text string, bad bool) {
	updateVRInput(func(s *VRInputStatus) {
		s.Notice, s.NoticeBad = text, bad
		s.NoticeSeq++
	})
}

func formatVRParam(v any) string {
	switch x := v.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', 2, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(x)
	}
	return fmt.Sprint(v)
}

func vrParamList(params []vrParam) []VRInputParam {
	out := make([]VRInputParam, 0, len(params))
	for _, p := range params {
		out = append(out, VRInputParam{Name: p.Name, Value: formatVRParam(p.Value)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// vrDerived is the latest derived state, for the rules trigger.
var vrDerived struct {
	sync.Mutex
	dirs   [2][2]int // [hand][stick, pad], always eight-way
	thumbs [2]int
	at     time.Time
}

func noteVRDerived(hands [2]vrHand, dead float64) {
	var dirs [2][2]int
	var thumbs [2]int
	for i, h := range hands {
		dirs[i] = [2]int{vrDirection(h.Stick[0], h.Stick[1], dead, true), padDirection(h, dead, true)}
		thumbs[i] = thumbPosition(h)
	}
	vrDerived.Lock()
	changed := dirs != vrDerived.dirs || thumbs != vrDerived.thumbs
	vrDerived.dirs, vrDerived.thumbs, vrDerived.at = dirs, thumbs, time.Now()
	vrDerived.Unlock()
	if changed {
		NudgeRules()
	}
}
