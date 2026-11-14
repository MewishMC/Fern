package modules

import (
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// VRChatStatus is what the panel knows about the link to VRChat.
type VRChatStatus struct {
	Running   bool   `json:"running"`   // VRChat.exe is running
	Listening bool   `json:"listening"` // Fern holds the OSC input port
	ListenErr string `json:"listenErr"` // why the port could not be opened
	Linked    bool   `json:"linked"`    // VRChat has sent OSC since it started
	LastOSCMS int64  `json:"lastOscMs"` // unix ms of the last packet, 0 if none
}

var (
	vrcMu       sync.Mutex
	vrcRunning  bool
	oscBound    bool
	oscBindErr  string
	lastOSCAt   time.Time
	oscSinceRun bool // a packet arrived during the current VRChat session
)

// CurrentVRChatStatus reports the link state shown in the panel.
func CurrentVRChatStatus() VRChatStatus {
	vrcMu.Lock()
	defer vrcMu.Unlock()
	st := VRChatStatus{
		Running:   vrcRunning,
		Listening: oscBound,
		ListenErr: oscBindErr,
		Linked:    vrcRunning && oscSinceRun,
	}
	if !lastOSCAt.IsZero() {
		st.LastOSCMS = lastOSCAt.UnixMilli()
	}
	return st
}

func noteOSCPacket() {
	vrcMu.Lock()
	lastOSCAt = time.Now()
	oscSinceRun = true
	vrcMu.Unlock()
}

func setOSCListener(bound bool, err error) {
	vrcMu.Lock()
	defer vrcMu.Unlock()
	oscBound = bound
	oscBindErr = ""
	if err != nil {
		oscBindErr = err.Error()
	}
}

// watchVRChatProcess polls the process list. VRChat's log can stay quiet for
// minutes, so "wrote to the log recently" kept flipping the status while the
// game was plainly running; the process list does not.
func watchVRChatProcess() {
	for {
		running := processRunning("vrchat.exe")
		vrcMu.Lock()
		if running != vrcRunning {
			vrcRunning = running
			// OSC from an earlier session says nothing about this one.
			oscSinceRun = false
			if running && time.Since(lastOSCAt) < 5*time.Second {
				oscSinceRun = true
			}
		}
		vrcMu.Unlock()
		time.Sleep(3 * time.Second)
	}
}

// processRunning reports whether a process with this executable name exists.
func processRunning(exe string) bool {
	const th32csSnapProcess = 0x2
	snap, err := syscall.CreateToolhelp32Snapshot(th32csSnapProcess, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snap)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = syscall.Process32First(snap, &entry); err == nil; err = syscall.Process32Next(snap, &entry) {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), exe) {
			return true
		}
	}
	return false
}
