package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"Fern/modules"
)

// Starting with SteamVR works through an application manifest: SteamVR
// auto-launches registered dashboard overlay apps whose auto-launch flag is set.
// Fern writes the manifest under %LOCALAPPDATA%\Fern and only registers it when
// the user presses the button, through SteamVR's own openvr_api.dll, which has
// to talk to a running SteamVR. Nothing here runs on its own.

const steamVRAppKey = "mewishmc.fern"

type steamVRState struct {
	Manifest string `json:"manifest"` // path of the written manifest, if any
	Busy     bool   `json:"busy"`
	Message  string `json:"message"` // result of the last button press
	Failed   bool   `json:"failed"`
}

var (
	steamVRMu sync.Mutex
	steamVR   steamVRState
)

func currentSteamVR() steamVRState {
	steamVRMu.Lock()
	defer steamVRMu.Unlock()
	st := steamVR
	if st.Manifest == "" {
		if _, err := os.Stat(manifestPath()); err == nil {
			st.Manifest = manifestPath()
		}
	}
	return st
}

func manifestPath() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base, _ = os.UserConfigDir()
	}
	return filepath.Join(base, "Fern", "fern.vrmanifest")
}

// buildManifest returns the manifest for the running executable. SteamVR
// starts Fern with --steamvr, which starts it minimised and quits it when
// SteamVR closes.
func buildManifest(exe string) ([]byte, error) {
	type strs struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	type app struct {
		AppKey     string          `json:"app_key"`
		LaunchType string          `json:"launch_type"`
		BinaryPath string          `json:"binary_path_windows"`
		Arguments  string          `json:"arguments"`
		IsOverlay  bool            `json:"is_dashboard_overlay"`
		Strings    map[string]strs `json:"strings"`
	}
	m := struct {
		Source       string `json:"source"`
		Applications []app  `json:"applications"`
	}{
		Source: "builtin",
		Applications: []app{{
			AppKey:     steamVRAppKey,
			LaunchType: "binary",
			BinaryPath: exe,
			Arguments:  "--steamvr",
			IsOverlay:  true,
			Strings:    map[string]strs{"en_us": {Name: "Fern", Description: "VRChat chatbox"}},
		}},
	}
	return json.MarshalIndent(m, "", "  ")
}

func writeManifest() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	data, err := buildManifest(exe)
	if err != nil {
		return "", err
	}
	path := manifestPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o644)
}

// steamVRAction runs a button press from the panel: "register" or "unregister".
func steamVRAction(action string) {
	steamVRMu.Lock()
	if steamVR.Busy {
		steamVRMu.Unlock()
		return
	}
	steamVR.Busy = true
	steamVRMu.Unlock()

	path, msg, err := "", "", error(nil)
	switch action {
	case "register":
		path, err = writeManifest()
		if err == nil {
			err = setSteamVRAutoLaunch(path, true)
			msg = "Fern now starts with SteamVR"
		}
	case "unregister":
		path = manifestPath()
		err = setSteamVRAutoLaunch(path, false)
		msg = "Fern no longer starts with SteamVR"
	default:
		err = fmt.Errorf("unknown action %q", action)
	}

	steamVRMu.Lock()
	defer steamVRMu.Unlock()
	steamVR.Busy = false
	if path != "" {
		if _, statErr := os.Stat(path); statErr == nil {
			steamVR.Manifest = path
		}
	}
	steamVR.Failed = err != nil
	steamVR.Message = msg
	if err != nil {
		steamVR.Message = err.Error()
	}
}

var errSteamVRNotRunning = errors.New("Start SteamVR first: Fern registers itself through the running SteamVR")

// openVRLibrary finds SteamVR's openvr_api.dll from the runtime path SteamVR
// records in %LOCALAPPDATA%\openvr\openvrpaths.vrpath.
func openVRLibrary() (string, error) {
	data, err := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "openvr", "openvrpaths.vrpath"))
	if err != nil {
		return "", errors.New("SteamVR does not seem to be installed")
	}
	var paths struct {
		Runtime []string `json:"runtime"`
	}
	if err := json.Unmarshal(data, &paths); err != nil {
		return "", fmt.Errorf("could not read openvrpaths.vrpath: %w", err)
	}
	for _, rt := range paths.Runtime {
		dll := filepath.Join(rt, "bin", "win64", "openvr_api.dll")
		if _, err := os.Stat(dll); err == nil {
			return dll, nil
		}
	}
	return "", errors.New("could not find SteamVR's openvr_api.dll")
}

// Indices in OpenVR's IVRApplications function table (openvr_capi.h).
const (
	vrAppAddManifest    = 0
	vrAppRemoveManifest = 1
	vrAppSetAutoLaunch  = 17
	vrApplicationUtil   = 4 // EVRApplicationType_VRApplication_Utility
)

// setSteamVRAutoLaunch registers the manifest and turns auto-launch on, or
// turns it off and removes the manifest from SteamVR.
func setSteamVRAutoLaunch(manifest string, on bool) error {
	if !steamVRRunning() {
		return errSteamVRNotRunning
	}
	dllPath, err := openVRLibrary()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	dll := syscall.NewLazyDLL(dllPath)
	initProc := dll.NewProc("VR_InitInternal2")
	if initProc.Find() != nil {
		initProc = dll.NewProc("VR_InitInternal")
	}
	getInterface := dll.NewProc("VR_GetGenericInterface")
	shutdown := dll.NewProc("VR_ShutdownInternal")
	for _, p := range []*syscall.LazyProc{initProc, getInterface, shutdown} {
		if err := p.Find(); err != nil {
			return fmt.Errorf("openvr_api.dll: %w", err)
		}
	}
	var vrErr int32
	_, _, _ = initProc.Call(uintptr(unsafe.Pointer(&vrErr)), vrApplicationUtil, 0)
	if vrErr != 0 {
		return fmt.Errorf("SteamVR refused the connection (error %d)", vrErr)
	}
	defer shutdown.Call()

	var table unsafe.Pointer
	for _, version := range []string{"FnTable:IVRApplications_008", "FnTable:IVRApplications_007"} {
		vrErr = 0
		name, _ := syscall.BytePtrFromString(version)
		r, _, _ := getInterface.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&vrErr)))
		if r != 0 && vrErr == 0 {
			table = *(*unsafe.Pointer)(unsafe.Pointer(&r))
			break
		}
	}
	if table == nil {
		return errors.New("this SteamVR version has no application interface Fern knows")
	}
	call := func(index int, args ...uintptr) int32 {
		fn := *(*uintptr)(unsafe.Add(table, index*int(unsafe.Sizeof(uintptr(0)))))
		r, _, _ := syscall.SyscallN(fn, args...)
		return int32(r)
	}
	pathPtr, err := syscall.BytePtrFromString(manifest)
	if err != nil {
		return err
	}
	keyPtr, _ := syscall.BytePtrFromString(steamVRAppKey)
	// The strings are only referenced through uintptrs below, so keep them alive.
	defer runtime.KeepAlive(pathPtr)
	defer runtime.KeepAlive(keyPtr)
	path, key := uintptr(unsafe.Pointer(pathPtr)), uintptr(unsafe.Pointer(keyPtr))
	if on {
		if e := call(vrAppAddManifest, path, 0); e != 0 {
			return fmt.Errorf("SteamVR did not accept the manifest (error %d)", e)
		}
		if e := call(vrAppSetAutoLaunch, key, 1); e != 0 {
			return fmt.Errorf("SteamVR did not turn on auto-launch (error %d)", e)
		}
		return nil
	}
	_ = call(vrAppSetAutoLaunch, key, 0)
	if e := call(vrAppRemoveManifest, path); e != 0 {
		return fmt.Errorf("SteamVR could not remove the manifest (error %d)", e)
	}
	return nil
}

func steamVRRunning() bool { return modules.ProcessRunning("vrserver.exe") }
