package openvr

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// The sizes come from compiling openvr_capi.h (v2.15.6) with gcc.
func TestStructSizesMatchHeader(t *testing.T) {
	if got := unsafe.Sizeof(TrackedDevicePose{}); got != 80 {
		t.Errorf("TrackedDevicePose is %d bytes, want 80", got)
	}
	if got := unsafe.Sizeof(FrameTiming{}); got != 192 {
		t.Errorf("FrameTiming is %d bytes, want 192", got)
	}
	if got := unsafe.Offsetof(FrameTiming{}.HmdPose); got != 96 {
		t.Errorf("FrameTiming.HmdPose at %d, want 96", got)
	}
	if got := unsafe.Offsetof(FrameTiming{}.PreSubmitGpuMs); got != 32 {
		t.Errorf("FrameTiming.PreSubmitGpuMs at %d, want 32", got)
	}
	if got := unsafe.Sizeof(CumulativeStats{}); got != 112 {
		t.Errorf("CumulativeStats is %d bytes, want 112", got)
	}
	if got := unsafe.Offsetof(CumulativeStats{}.SumCompositorCPUTimeMS); got != 64 {
		t.Errorf("CumulativeStats.SumCompositorCPUTimeMS at %d, want 64", got)
	}
	if got := unsafe.Offsetof(CumulativeStats{}.NumFramesWithDepth); got != 104 {
		t.Errorf("CumulativeStats.NumFramesWithDepth at %d, want 104", got)
	}
}

func TestLayoutsStayInsideTheirTables(t *testing.T) {
	for _, l := range systemLayouts {
		for _, slot := range []int{l.controllerRole, l.deviceClass, l.connected, l.boolProp, l.floatProp,
			l.int32Prop, l.stringProp, l.pollEvent, l.ackQuit, l.setSDK} {
			if slot >= l.slots {
				t.Errorf("%s: slot %d outside a table of %d", l.version, slot, l.slots)
			}
		}
	}
	for _, l := range compositorLayouts {
		if l.frameTiming >= l.slots || l.cumulativeStats >= l.slots {
			t.Errorf("%s: slot outside a table of %d", l.version, l.slots)
		}
	}
	if systemLayouts[0].version != "IVRSystem_026" || compositorLayouts[0].version != "IVRCompositor_029" {
		t.Error("the newest layouts must match the pinned header and come first")
	}
}

func TestEmbeddedDLLMatchesChecksum(t *testing.T) {
	sum := sha256.Sum256(dllData)
	if hex.EncodeToString(sum[:]) != DLLSHA256 {
		t.Fatal("embedded openvr_api.dll does not match DLLSHA256")
	}
	if !strings.Contains(License, "Valve Corporation") {
		t.Error("licence text missing")
	}
}

func TestEnsureFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	data := []byte("pretend dll")
	raw := sha256.Sum256(data)
	sum := hex.EncodeToString(raw[:])

	path, wrote, err := ensureFile(dir, "x.dll", data, sum)
	if err != nil || !wrote {
		t.Fatalf("first write: wrote=%v err=%v", wrote, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(data) {
		t.Fatalf("wrote %q", got)
	}

	// A matching file is left alone.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(path, old, old)
	if _, wrote, err = ensureFile(dir, "x.dll", data, sum); err != nil || wrote {
		t.Fatalf("matching file: wrote=%v err=%v", wrote, err)
	}
	if info, _ := os.Stat(path); !info.ModTime().Equal(old) {
		t.Error("matching file was touched")
	}

	// A damaged or different file is replaced.
	_ = os.WriteFile(path, []byte("tampered"), 0o644)
	if _, wrote, err = ensureFile(dir, "x.dll", data, sum); err != nil || !wrote {
		t.Fatalf("damaged file: wrote=%v err=%v", wrote, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(data) {
		t.Fatalf("damaged file not replaced: %q", got)
	}

	// Data that does not match its own checksum is never written.
	if _, _, err = ensureFile(filepath.Join(dir, "other"), "y.dll", []byte("nope"), sum); err == nil {
		t.Error("accepted data with the wrong checksum")
	}
	if _, err := os.Stat(filepath.Join(dir, "other", "y.dll")); !errors.Is(err, os.ErrNotExist) {
		t.Error("wrote data with the wrong checksum")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left a temporary file behind: %s", e.Name())
		}
	}
}

func TestFloatTrampoline(t *testing.T) {
	v, perr := callFloatProp(floatProbe(), 7, 3)
	if v != 0.75 || perr != 7 {
		t.Fatalf("got %v, %d; want 0.75, 7", v, perr)
	}
}

// Loads the real embedded DLL from a scratch LOCALAPPDATA and calls the
// functions that are safe without SteamVR. Init is only attempted to check it
// fails cleanly (or, with SteamVR running, connects and disconnects).
func TestLoadEmbeddedDLL(t *testing.T) {
	// Not t.TempDir: Windows keeps a loaded DLL locked, so its cleanup fails.
	base, err := os.MkdirTemp("", "fern-openvr-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("LOCALAPPDATA", base)
	path, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "Fern", "openvr", Version, "openvr_api.dll")
	if !strings.EqualFold(path, want) {
		t.Fatalf("loaded %s, want %s", path, want)
	}
	if sum, _ := fileSHA256(path); sum != DLLSHA256 {
		t.Fatal("extracted DLL has the wrong checksum")
	}
	installed, err := IsRuntimeInstalled()
	if err != nil {
		t.Fatal(err)
	}
	present, err := IsHmdPresent()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("runtime installed: %v, HMD present: %v", installed, present)

	if text := initError(initErrorNoServerForBackground).Error(); !strings.Contains(text, "121") || len(text) < 8 {
		t.Errorf("init error text %q", text)
	}

	s, err := Init()
	if err != nil {
		var vrErr *Error
		if !errors.As(err, &vrErr) {
			t.Fatalf("Init returned %T: %v", err, err)
		}
		t.Logf("Init without SteamVR: %v (not running: %v)", err, vrErr.NotRunning())
		return
	}
	sys, comp := s.Versions()
	t.Logf("connected to SteamVR: %s, %s", sys, comp)
	s.Shutdown()
	s.Shutdown()
}
