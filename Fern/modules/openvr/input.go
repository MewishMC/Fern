package openvr

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Controller input, the legacy way (IVRSystem::GetControllerState) and
// through SteamVR Input (IVRInput), plus haptics for both.

// Button bits in ControllerState.Pressed and Touched (EVRButtonId).
const (
	ButtonApplicationMenu = 1
	ButtonGrip            = 2
	ButtonA               = 7
	ButtonTouchpad        = 32 // k_EButton_SteamVR_Touchpad, also Axis0
	ButtonTrigger         = 33 // k_EButton_SteamVR_Trigger, also Axis1
)

// Axis types (EVRControllerAxisType), read from PropAxis0Type + n.
const (
	AxisNone     = 0
	AxisTrackPad = 1
	AxisJoystick = 2
	AxisTrigger  = 3
)

// PropAxis0Type is Prop_Axis0Type_Int32; axes 1 to 4 follow it.
const PropAxis0Type = 3002

// ControllerAxisCount is k_unControllerStateAxisCount.
const ControllerAxisCount = 5

// ControllerAxis mirrors VRControllerAxis_t.
type ControllerAxis struct {
	X, Y float32
}

// ControllerState mirrors VRControllerState_t (64 bytes on Windows).
type ControllerState struct {
	PacketNum uint32
	Pressed   uint64
	Touched   uint64
	Axis      [ControllerAxisCount]ControllerAxis
}

// IsPressed reports whether a button bit is set in Pressed.
func (c ControllerState) IsPressed(button uint) bool { return c.Pressed&(1<<button) != 0 }

// IsTouched reports whether a button bit is set in Touched.
func (c ControllerState) IsTouched(button uint) bool { return c.Touched&(1<<button) != 0 }

// MaxHapticPulse is the longest legacy pulse SteamVR accepts, in microseconds.
const MaxHapticPulse = 3999

// ControllerState reads the legacy state of a controller. ok is false when
// the device is not a controller or SteamVR has nothing for this app.
func (s *Session) ControllerState(i uint32) (ControllerState, bool) {
	var c ControllerState
	if s.sys == nil {
		return c, false
	}
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.controllerState], uintptr(i), uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c))
	return c, byte(r) != 0
}

// HapticPulse buzzes a controller once through the legacy API. SteamVR caps
// a pulse at MaxHapticPulse microseconds; longer buzzes repeat it every few
// milliseconds.
func (s *Session) HapticPulse(i uint32, micros uint16) {
	if s.sys == nil {
		return
	}
	if micros > MaxHapticPulse {
		micros = MaxHapticPulse
	}
	syscall.SyscallN(s.sys[s.sysL.hapticPulse], uintptr(i), 0, uintptr(micros))
}

// inputLayout gives the slots used in one IVRInput FnTable version.
type inputLayout struct {
	version     string
	slots       int
	setManifest int // SetActionManifestPath
	actionSet   int // GetActionSetHandle
	action      int // GetActionHandle
	source      int // GetInputSourceHandle
	update      int // UpdateActionState
	digital     int // GetDigitalActionData
	analog      int // GetAnalogActionData
	summary     int // GetSkeletalSummaryData
	vibrate     int // TriggerHapticVibrationAction
}

// Read from openvr_capi.h at v2.15.6 (IVRInput_011) and v2.5.1
// (IVRInput_010) and checked against offsetof() with gcc.
var inputLayouts = []inputLayout{
	{version: "IVRInput_011", slots: 34, setManifest: 0, actionSet: 1, action: 2, source: 3, update: 4,
		digital: 5, analog: 6, summary: 20, vibrate: 23},
	{version: "IVRInput_010", slots: 32, setManifest: 0, actionSet: 1, action: 2, source: 3, update: 4,
		digital: 5, analog: 6, summary: 18, vibrate: 21},
}

// ActionSetOverlayGlobalPriority is k_nActionSetOverlayGlobalPriorityMin: a
// set at this priority or above is read whichever app has focus, and takes
// the inputs it binds away from that app.
const ActionSetOverlayGlobalPriority = 0x01000000

// InputError is an EVRInputError.
type InputError int32

// Input errors this package names (EVRInputError).
const (
	InputNone                = InputError(0)
	InputNameNotFound        = InputError(1)
	InputWrongType           = InputError(2)
	InputInvalidHandle       = InputError(3)
	InputNoData              = InputError(13)
	InputMismatchedManifest  = InputError(15)
	InputMissingSkeletonData = InputError(16)
	InputPermissionDenied    = InputError(19)
)

var inputErrorNames = map[InputError]string{
	1: "name not found", 2: "wrong type", 3: "invalid handle", 4: "invalid parameter", 5: "no Steam",
	6: "max capacity reached", 7: "IPC error", 8: "no active action set", 9: "invalid device",
	10: "invalid skeleton", 11: "invalid bone count", 12: "invalid compressed data", 13: "no data",
	14: "buffer too small", 15: "mismatched action manifest", 16: "missing skeleton data",
	17: "invalid bone index", 18: "invalid priority", 19: "permission denied", 20: "invalid render model",
}

func (e InputError) Error() string {
	if name, ok := inputErrorNames[e]; ok {
		return fmt.Sprintf("SteamVR input: %s (%d)", name, int32(e))
	}
	return fmt.Sprintf("SteamVR input error %d", int32(e))
}

func inputErr(r uintptr) error {
	if code := InputError(int32(uint32(r))); code != InputNone {
		return code
	}
	return nil
}

// ActiveActionSet mirrors VRActiveActionSet_t (32 bytes).
type ActiveActionSet struct {
	ActionSet          uint64
	RestrictedToDevice uint64
	SecondaryActionSet uint64
	_                  uint32
	Priority           int32
}

// DigitalActionData mirrors InputDigitalActionData_t (24 bytes).
type DigitalActionData struct {
	Active       bool
	ActiveOrigin uint64
	State        bool
	Changed      bool
	UpdateTime   float32
}

// AnalogActionData mirrors InputAnalogActionData_t (48 bytes).
type AnalogActionData struct {
	Active                 bool
	ActiveOrigin           uint64
	X, Y, Z                float32
	DeltaX, DeltaY, DeltaZ float32
	UpdateTime             float32
}

// SkeletalSummary mirrors VRSkeletalSummaryData_t (36 bytes): curl is 0 for
// an open finger and 1 for a fist, thumb first.
type SkeletalSummary struct {
	Curl  [5]float32
	Splay [4]float32
}

// Summary types (EVRSummaryType).
const (
	SummaryFromAnimation = 0
	SummaryFromDevice    = 1
)

// HasInput reports whether the SteamVR Input interface is available.
func (s *Session) HasInput() bool { return s.inp != nil }

// InputVersion is the IVRInput interface in use, or "".
func (s *Session) InputVersion() string {
	if s.inp == nil {
		return ""
	}
	return s.inL.version
}

func (s *Session) inputCall(slot int, args ...uintptr) error {
	if s.inp == nil {
		return InputInvalidHandle
	}
	r, _, _ := syscall.SyscallN(s.inp[slot], args...)
	return inputErr(r)
}

// SetActionManifestPath registers the app's action manifest. It must be an
// absolute path and is set once per connection.
func (s *Session) SetActionManifestPath(path string) error {
	p, err := syscall.BytePtrFromString(path)
	if err != nil {
		return err
	}
	return s.inputCall(s.inL.setManifest, uintptr(unsafe.Pointer(p)))
}

func (s *Session) handle(slot int, name string) (uint64, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	var h uint64
	if err := s.inputCall(slot, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&h))); err != nil {
		return 0, err
	}
	return h, nil
}

// ActionSetHandle looks up an action set, e.g. "/actions/fern".
func (s *Session) ActionSetHandle(name string) (uint64, error) {
	return s.handle(s.inL.actionSet, name)
}

// ActionHandle looks up an action, e.g. "/actions/fern/in/lefttrigger".
func (s *Session) ActionHandle(name string) (uint64, error) { return s.handle(s.inL.action, name) }

// InputSourceHandle looks up a device path, e.g. "/user/hand/left".
func (s *Session) InputSourceHandle(path string) (uint64, error) { return s.handle(s.inL.source, path) }

// UpdateActionState refreshes the state of the actions in one set. Call it
// before reading them.
func (s *Session) UpdateActionState(set uint64, priority int32) error {
	a := ActiveActionSet{ActionSet: set, Priority: priority}
	return s.inputCall(s.inL.update, uintptr(unsafe.Pointer(&a)), unsafe.Sizeof(a), 1)
}

// DigitalAction reads a boolean action; restrict is an input source handle
// or 0 for any device.
func (s *Session) DigitalAction(action, restrict uint64) (DigitalActionData, error) {
	var d DigitalActionData
	err := s.inputCall(s.inL.digital, uintptr(action), uintptr(unsafe.Pointer(&d)), unsafe.Sizeof(d), uintptr(restrict))
	return d, err
}

// AnalogAction reads a vector1, vector2 or vector3 action.
func (s *Session) AnalogAction(action, restrict uint64) (AnalogActionData, error) {
	var d AnalogActionData
	err := s.inputCall(s.inL.analog, uintptr(action), uintptr(unsafe.Pointer(&d)), unsafe.Sizeof(d), uintptr(restrict))
	return d, err
}

// SkeletalSummary reads finger curl and splay from a skeleton action.
func (s *Session) SkeletalSummary(action uint64, summaryType int32) (SkeletalSummary, error) {
	var d SkeletalSummary
	err := s.inputCall(s.inL.summary, uintptr(action), uintptr(summaryType), uintptr(unsafe.Pointer(&d)))
	return d, err
}

// Vibrate plays a haptic action: duration in seconds, frequency in Hz,
// amplitude 0 to 1; restrict picks the hand (an input source handle).
func (s *Session) Vibrate(action uint64, duration, frequency, amplitude float32, restrict uint64) error {
	if s.inp == nil {
		return InputInvalidHandle
	}
	if code := InputError(callVibrate(s.inp[s.inL.vibrate], action, 0, duration, frequency, amplitude, restrict)); code != InputNone {
		return code
	}
	return nil
}
