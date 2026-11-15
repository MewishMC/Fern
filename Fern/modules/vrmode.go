package modules

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	createToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	process32FirstW          = kernel32.NewProc("Process32FirstW")
	process32NextW           = kernel32.NewProc("Process32NextW")

	vrMu    sync.Mutex
	vrProcs map[string]bool
	vrAt    time.Time
)

const th32csSnapProcess = 0x2

type processEntry32 struct {
	Size            uint32
	Usage           uint32
	ProcessID       uint32
	DefaultHeapID   uintptr
	ModuleID        uint32
	Threads         uint32
	ParentProcessID uint32
	PriClassBase    int32
	Flags           uint32
	ExeFile         [260]uint16
}

// runningProcesses returns the lower-case names of running executables.
func runningProcesses() map[string]bool {
	out := map[string]bool{}
	snap, _, _ := createToolhelp32Snapshot.Call(th32csSnapProcess, 0)
	if snap == uintptr(syscall.InvalidHandle) {
		return out
	}
	defer syscall.CloseHandle(syscall.Handle(snap))
	var e processEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for ok, _, _ := process32FirstW.Call(snap, uintptr(unsafe.Pointer(&e))); ok != 0; ok, _, _ = process32NextW.Call(snap, uintptr(unsafe.Pointer(&e))) {
		out[strings.ToLower(syscall.UTF16ToString(e.ExeFile[:]))] = true
	}
	return out
}

// inVR reports whether a VR runtime is running: SteamVR, or the Oculus
// runtime when countOculus is set (it also runs without a headset on).
// The process list is cached for a few seconds.
func inVR(countOculus bool) bool {
	vrMu.Lock()
	if vrProcs == nil || time.Since(vrAt) > 5*time.Second {
		vrProcs, vrAt = runningProcesses(), time.Now()
	}
	procs := vrProcs
	vrMu.Unlock()
	return vrRuntimeRunning(procs, countOculus)
}

func vrRuntimeRunning(procs map[string]bool, countOculus bool) bool {
	if procs["vrserver.exe"] || procs["vrcompositor.exe"] {
		return true
	}
	return countOculus && (procs["ovrserver_x64.exe"] || procs["oculusclient.exe"])
}

// InVR reports whether a VR runtime is running, using the window settings
// for whether the Oculus runtime counts.
func InVR() bool {
	return inVR(GetConfig().WindowOptions.CountOculus)
}
