package openvr

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// Values from openvr_capi.h at Version.
const (
	MaxTrackedDeviceCount = 64

	appBackground = 3 // EVRApplicationType_VRApplication_Background

	initErrorNone                  = 0
	initErrorNoServerForBackground = 121 // SteamVR is not running
	initErrorInterfaceNotFound     = 105
)

// Tracked device classes (ETrackedDeviceClass).
const (
	ClassInvalid           = 0
	ClassHMD               = 1
	ClassController        = 2
	ClassGenericTracker    = 3
	ClassTrackingReference = 4
	ClassDisplayRedirect   = 5
)

// Controller roles (ETrackedControllerRole).
const (
	RoleInvalid   = 0
	RoleLeftHand  = 1
	RoleRightHand = 2
)

// Device properties (ETrackedDeviceProperty).
const (
	PropModelNumber             = 1001
	PropSerialNumber            = 1002
	PropManufacturerName        = 1005
	PropDeviceIsCharging        = 1011
	PropDeviceBatteryPercentage = 1012
	PropProvidesBatteryStatus   = 1026
	PropDisplayFrequency        = 2002
	PropControllerType          = 7000
)

// Event types (EVREventType).
const (
	EventTrackedDeviceActivated   = 100
	EventTrackedDeviceDeactivated = 101
	EventQuit                     = 700
	EventDriverRequestedQuit      = 704
)

// Reprojection flags in FrameTiming.ReprojectionFlags.
const (
	ReprojectionReasonCPU = 0x01
	ReprojectionReasonGPU = 0x02
	ReprojectionAsync     = 0x04
	ReprojectionMotion    = 0x08 // motion smoothing ran for this frame
)

// sdkMajor, sdkMinor and sdkBuild are k_nSteamVRVersion* in openvr.h.
const (
	sdkMajor = 2
	sdkMinor = 15
	sdkBuild = 6
)

// TrackedDevicePose mirrors TrackedDevicePose_t (80 bytes).
type TrackedDevicePose struct {
	DeviceToAbsoluteTracking [12]float32
	Velocity                 [3]float32
	AngularVelocity          [3]float32
	TrackingResult           int32
	PoseIsValid              bool
	DeviceIsConnected        bool
}

// FrameTiming mirrors Compositor_FrameTiming (192 bytes; 184 before
// IVRCompositor_028 added TransferLatencyMs).
type FrameTiming struct {
	Size                    uint32
	FrameIndex              uint32
	NumFramePresents        uint32
	NumMisPresented         uint32
	NumDroppedFrames        uint32
	ReprojectionFlags       uint32
	SystemTimeInSeconds     float64
	PreSubmitGpuMs          float32
	PostSubmitGpuMs         float32
	TotalRenderGpuMs        float32
	CompositorRenderGpuMs   float32
	CompositorRenderCpuMs   float32
	CompositorIdleCpuMs     float32
	ClientFrameIntervalMs   float32
	PresentCallCpuMs        float32
	WaitForPresentCpuMs     float32
	SubmitFrameMs           float32
	WaitGetPosesCalledMs    float32
	NewPosesReadyMs         float32
	NewFrameReadyMs         float32
	CompositorUpdateStartMs float32
	CompositorUpdateEndMs   float32
	CompositorRenderStartMs float32
	HmdPose                 TrackedDevicePose
	NumVSyncsReadyForUse    uint32
	NumVSyncsToFirstView    uint32
	TransferLatencyMs       float32
}

// CumulativeStats mirrors Compositor_CumulativeStats (112 bytes). The counts
// are totals since the scene application (Pid) connected.
type CumulativeStats struct {
	Pid                           uint32
	NumFramePresents              uint32
	NumDroppedFrames              uint32
	NumReprojectedFrames          uint32
	NumFramePresentsOnStartup     uint32
	NumDroppedFramesOnStartup     uint32
	NumReprojectedFramesOnStartup uint32
	NumLoading                    uint32
	NumFramePresentsLoading       uint32
	NumDroppedFramesLoading       uint32
	NumReprojectedFramesLoading   uint32
	NumTimedOut                   uint32
	NumFramePresentsTimedOut      uint32
	NumDroppedFramesTimedOut      uint32
	NumReprojectedFramesTimedOut  uint32
	NumFrameSubmits               uint32
	SumCompositorCPUTimeMS        float64
	SumCompositorGPUTimeMS        float64
	SumTargetFrameTimes           float64
	SumApplicationCPUTimeMS       float64
	SumApplicationGPUTimeMS       float64
	NumFramesWithDepth            uint32
	_                             uint32
}

// eventSize is sizeof(VREvent_t) on 64-bit Windows.
const eventSize = 64

// systemLayout gives the slots this package uses in one IVRSystem FnTable
// version. Slots are zero-based indexes into the table of function pointers.
type systemLayout struct {
	version        string
	slots          int // table length, so only that many pointers are read
	controllerRole int // GetControllerRoleForTrackedDeviceIndex
	deviceClass    int // GetTrackedDeviceClass
	connected      int // IsTrackedDeviceConnected
	boolProp       int // GetBoolTrackedDeviceProperty
	floatProp      int // GetFloatTrackedDeviceProperty
	int32Prop      int // GetInt32TrackedDeviceProperty
	stringProp     int // GetStringTrackedDeviceProperty
	pollEvent      int // PollNextEvent
	ackQuit        int // AcknowledgeQuit_Exiting
	setSDK         int // SetSDKVersion, -1 when the version has none
}

// The slots were read from openvr_capi.h at v2.15.6 (IVRSystem_026) and
// v2.5.1 (IVRSystem_022), and checked against offsetof() with gcc.
var systemLayouts = []systemLayout{
	{version: "IVRSystem_026", slots: 51, controllerRole: 19, deviceClass: 20, connected: 21, boolProp: 22,
		floatProp: 23, int32Prop: 24, stringProp: 28, pollEvent: 30, ackQuit: 47, setSDK: 50},
	{version: "IVRSystem_022", slots: 46, controllerRole: 18, deviceClass: 19, connected: 20, boolProp: 21,
		floatProp: 22, int32Prop: 23, stringProp: 27, pollEvent: 29, ackQuit: 43, setSDK: -1},
}

// compositorLayout gives the slots used in one IVRCompositor FnTable version.
type compositorLayout struct {
	version         string
	slots           int
	frameTiming     int // GetFrameTiming
	cumulativeStats int // GetCumulativeStats
	timingSize      uint32
}

var compositorLayouts = []compositorLayout{
	{version: "IVRCompositor_029", slots: 14, frameTiming: 10, cumulativeStats: 13, timingSize: 192},
	{version: "IVRCompositor_028", slots: 13, frameTiming: 9, cumulativeStats: 12, timingSize: 192},
	{version: "IVRCompositor_027", slots: 12, frameTiming: 8, cumulativeStats: 11, timingSize: 184},
}

// Error is an EVRInitError with Valve's description.
type Error struct {
	Code int32
	Text string
}

func (e *Error) Error() string {
	if e.Text != "" {
		// Valve's descriptions already end in the code, e.g. "... (121)".
		if strings.HasSuffix(e.Text, fmt.Sprintf("(%d)", e.Code)) {
			return e.Text
		}
		return fmt.Sprintf("%s (%d)", e.Text, e.Code)
	}
	return fmt.Sprintf("OpenVR error %d", e.Code)
}

// NotRunning reports whether the error means SteamVR is simply not running.
func (e *Error) NotRunning() bool { return e.Code == initErrorNoServerForBackground }

// cString reads a NUL-terminated string the DLL owns.
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	buf := unsafe.Slice((*byte)(ptr), 1024)
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func initError(code int32) error {
	if code == initErrorNone {
		return nil
	}
	text := ""
	if procInitErrorEnglish != nil {
		r, _, _ := syscall.SyscallN(procInitErrorEnglish.Addr(), uintptr(code))
		text = cString(r)
	}
	return &Error{Code: code, Text: text}
}

// IsRuntimeInstalled reports whether SteamVR is installed. It never starts it.
func IsRuntimeInstalled() (bool, error) {
	if err := load(); err != nil {
		return false, err
	}
	r, _, _ := syscall.SyscallN(procIsRuntimeInstalled.Addr())
	return byte(r) != 0, nil
}

// IsHmdPresent reports whether a headset is plugged in. It never starts SteamVR.
func IsHmdPresent() (bool, error) {
	if err := load(); err != nil {
		return false, err
	}
	r, _, _ := syscall.SyscallN(procIsHmdPresent.Addr())
	return byte(r) != 0, nil
}

// Session is a connection to a running SteamVR as a background application.
// It is not safe for concurrent use: call it from one locked OS thread.
type Session struct {
	sys     []uintptr
	comp    []uintptr
	sysL    systemLayout
	compL   compositorLayout
	stopped bool
}

// table copies the function pointers of a FnTable into Go memory.
func table(p uintptr, n int) []uintptr {
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p))
	return append([]uintptr(nil), unsafe.Slice((*uintptr)(ptr), n)...)
}

func genericInterface(name string) (uintptr, int32) {
	cname, err := syscall.BytePtrFromString("FnTable:" + name)
	if err != nil {
		return 0, initErrorInterfaceNotFound
	}
	var code int32
	r, _, _ := syscall.SyscallN(procGetGenericIface.Addr(), uintptr(unsafe.Pointer(cname)), uintptr(unsafe.Pointer(&code)))
	if r == 0 && code == initErrorNone {
		code = initErrorInterfaceNotFound
	}
	return r, code
}

// Init connects to SteamVR as a background application. A background
// application never launches SteamVR: when it is not running Init returns an
// *Error whose NotRunning is true.
func Init() (*Session, error) {
	if err := load(); err != nil {
		return nil, err
	}
	var code int32
	syscall.SyscallN(procInitInternal2.Addr(), uintptr(unsafe.Pointer(&code)), appBackground, 0)
	if code != initErrorNone {
		return nil, initError(code)
	}
	s := &Session{}
	var lastCode int32 = initErrorInterfaceNotFound
	for _, l := range systemLayouts {
		if p, c := genericInterface(l.version); p != 0 {
			s.sys, s.sysL = table(p, l.slots), l
			break
		} else {
			lastCode = c
		}
	}
	if s.sys == nil {
		s.Shutdown()
		return nil, initError(lastCode)
	}
	for _, l := range compositorLayouts {
		if p, _ := genericInterface(l.version); p != 0 {
			s.comp, s.compL = table(p, l.slots), l
			break
		}
	}
	if s.sysL.setSDK >= 0 {
		// The C++ VR_Init does this too; it tells the runtime which SDK built us.
		syscall.SyscallN(s.sys[s.sysL.setSDK], sdkMajor, sdkMinor, sdkBuild)
	}
	return s, nil
}

// Versions returns the interface versions in use.
func (s *Session) Versions() (system, compositor string) {
	return s.sysL.version, s.compL.version
}

// HasCompositor reports whether the compositor interface is available.
func (s *Session) HasCompositor() bool { return s.comp != nil }

// Shutdown disconnects from SteamVR. Safe to call more than once.
func (s *Session) Shutdown() {
	if s == nil || s.stopped {
		return
	}
	s.stopped = true
	s.sys, s.comp = nil, nil
	syscall.SyscallN(procShutdownInternal.Addr())
}

// AcknowledgeQuit tells SteamVR Fern got its quit event and is letting go.
func (s *Session) AcknowledgeQuit() {
	if s.sys != nil {
		syscall.SyscallN(s.sys[s.sysL.ackQuit])
	}
}

// PollEvent returns the next queued event type and its device, if any.
func (s *Session) PollEvent() (eventType, device uint32, ok bool) {
	if s.sys == nil {
		return 0, 0, false
	}
	var ev [eventSize]byte
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.pollEvent], uintptr(unsafe.Pointer(&ev[0])), eventSize)
	if byte(r) == 0 {
		return 0, 0, false
	}
	eventType = *(*uint32)(unsafe.Pointer(&ev[0]))
	device = *(*uint32)(unsafe.Pointer(&ev[4]))
	return eventType, device, true
}

// DeviceClass returns the ETrackedDeviceClass of a device slot.
func (s *Session) DeviceClass(i uint32) int32 {
	if s.sys == nil {
		return ClassInvalid
	}
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.deviceClass], uintptr(i))
	return int32(uint32(r))
}

// Connected reports whether a device slot has a connected device.
func (s *Session) Connected(i uint32) bool {
	if s.sys == nil {
		return false
	}
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.connected], uintptr(i))
	return byte(r) != 0
}

// ControllerRole returns the hand a controller is assigned to.
func (s *Session) ControllerRole(i uint32) int32 {
	if s.sys == nil {
		return RoleInvalid
	}
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.controllerRole], uintptr(i))
	return int32(uint32(r))
}

// BoolProp reads a bool property; ok is false when the device lacks it.
func (s *Session) BoolProp(i uint32, prop int32) (value, ok bool) {
	if s.sys == nil {
		return false, false
	}
	var perr int32
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.boolProp], uintptr(i), uintptr(prop), uintptr(unsafe.Pointer(&perr)))
	return byte(r) != 0, perr == 0
}

// FloatProp reads a float property; ok is false when the device lacks it.
// A float comes back in XMM0, which syscall cannot read, so this one call
// goes through a tiny cgo trampoline.
func (s *Session) FloatProp(i uint32, prop int32) (value float32, ok bool) {
	if s.sys == nil {
		return 0, false
	}
	v, perr := callFloatProp(s.sys[s.sysL.floatProp], i, prop)
	return v, perr == 0
}

// Int32Prop reads an int32 property; ok is false when the device lacks it.
func (s *Session) Int32Prop(i uint32, prop int32) (value int32, ok bool) {
	if s.sys == nil {
		return 0, false
	}
	var perr int32
	r, _, _ := syscall.SyscallN(s.sys[s.sysL.int32Prop], uintptr(i), uintptr(prop), uintptr(unsafe.Pointer(&perr)))
	return int32(uint32(r)), perr == 0
}

// StringProp reads a string property, or "" when the device lacks it.
func (s *Session) StringProp(i uint32, prop int32) string {
	if s.sys == nil {
		return ""
	}
	var buf [256]byte
	var perr int32
	n, _, _ := syscall.SyscallN(s.sys[s.sysL.stringProp], uintptr(i), uintptr(prop),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&perr)))
	if perr != 0 || n == 0 {
		return ""
	}
	end := int(uint32(n)) - 1 // n counts the terminating NUL
	if end < 0 || end > len(buf) {
		end = len(buf)
	}
	for j := 0; j < end; j++ {
		if buf[j] == 0 {
			end = j
			break
		}
	}
	return string(buf[:end])
}

// FrameTiming returns the compositor's timing for a frame, 0 being the newest.
func (s *Session) FrameTiming(framesAgo uint32) (FrameTiming, bool) {
	var t FrameTiming
	if s.comp == nil {
		return t, false
	}
	t.Size = s.compL.timingSize
	r, _, _ := syscall.SyscallN(s.comp[s.compL.frameTiming], uintptr(unsafe.Pointer(&t)), uintptr(framesAgo))
	return t, byte(r) != 0
}

// CumulativeStats returns the compositor's running totals for the scene app.
func (s *Session) CumulativeStats() (CumulativeStats, bool) {
	var c CumulativeStats
	if s.comp == nil {
		return c, false
	}
	syscall.SyscallN(s.comp[s.compL.cumulativeStats], uintptr(unsafe.Pointer(&c)), unsafe.Sizeof(c))
	return c, true
}
