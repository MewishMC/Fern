package modules

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessRunning(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !processRunning(filepath.Base(self)) {
		t.Errorf("own process %s not found", filepath.Base(self))
	}
	if processRunning("fern-no-such-process.exe") {
		t.Error("found a process that does not exist")
	}
}

func TestVRChatLinkFollowsSession(t *testing.T) {
	reset := func(running bool) {
		vrcMu.Lock()
		vrcRunning, oscSinceRun, lastOSCAt = running, false, time.Time{}
		vrcMu.Unlock()
	}
	t.Cleanup(func() { reset(false); setOSCListener(false, nil) })

	reset(true)
	setOSCListener(true, nil)
	if st := CurrentVRChatStatus(); !st.Running || !st.Listening || st.Linked {
		t.Fatalf("before any OSC: %+v", st)
	}
	noteOSCPacket()
	if st := CurrentVRChatStatus(); !st.Linked || st.LastOSCMS == 0 {
		t.Fatalf("after OSC: %+v", st)
	}

	// OSC heard while VRChat is closed must not count as a link.
	reset(false)
	noteOSCPacket()
	if st := CurrentVRChatStatus(); st.Linked {
		t.Fatalf("linked while VRChat is not running: %+v", st)
	}

	setOSCListener(false, errors.New("port in use"))
	if st := CurrentVRChatStatus(); st.Listening || st.ListenErr != "port in use" {
		t.Fatalf("listener error not reported: %+v", st)
	}
}
