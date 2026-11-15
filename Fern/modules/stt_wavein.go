package modules

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Microphone capture through winmm's waveIn API, polled rather than driven by
// callbacks: a few small buffers are queued, and each one that comes back
// marked done is handed on and queued again.

var (
	winmm                 = syscall.NewLazyDLL("winmm.dll")
	procWaveInGetNumDevs  = winmm.NewProc("waveInGetNumDevs")
	procWaveInGetDevCaps  = winmm.NewProc("waveInGetDevCapsW")
	procWaveInOpen        = winmm.NewProc("waveInOpen")
	procWaveInClose       = winmm.NewProc("waveInClose")
	procWaveInPrepare     = winmm.NewProc("waveInPrepareHeader")
	procWaveInUnprepare   = winmm.NewProc("waveInUnprepareHeader")
	procWaveInAddBuffer   = winmm.NewProc("waveInAddBuffer")
	procWaveInStart       = winmm.NewProc("waveInStart")
	procWaveInReset       = winmm.NewProc("waveInReset")
	errNoMicrophone       = errors.New("no microphone found")
	errMicrophoneDisabled = errors.New("could not open the microphone; check Windows' microphone privacy settings")
)

const (
	waveMapper    = 0xFFFFFFFF
	whdrDone      = 0x1
	sttSampleRate = 16000 // what speech models expect; Windows resamples for us
	sttChunkMS    = 50
	sttBuffers    = 8
)

type waveFormat struct {
	formatTag      uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	size           uint16
}

type waveHdr struct {
	data          *byte
	bufferLength  uint32
	bytesRecorded uint32
	user          uintptr
	flags         uint32
	loops         uint32
	next          uintptr
	reserved      uintptr
}

type waveInCaps struct {
	mid           uint16
	pid           uint16
	driverVersion uint32
	name          [32]uint16
	formats       uint32
	channels      uint16
	reserved      uint16
}

// MicDevice is one capture device. Windows cuts names to 31 characters.
type MicDevice struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ListMics returns the capture devices in waveIn order.
func ListMics() []MicDevice {
	n, _, _ := procWaveInGetNumDevs.Call()
	out := make([]MicDevice, 0, n)
	for i := uintptr(0); i < n && i < 64; i++ {
		var caps waveInCaps
		if r, _, _ := procWaveInGetDevCaps.Call(i, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps)); r != 0 {
			continue
		}
		out = append(out, MicDevice{ID: int(i), Name: syscall.UTF16ToString(caps.name[:])})
	}
	return out
}

// micIndex resolves a saved device name to its current index; the default
// device is used when the name is empty or the device is gone.
func micIndex(name string) uintptr {
	if name == "" {
		return waveMapper
	}
	for _, m := range ListMics() {
		if m.Name == name {
			return uintptr(m.ID)
		}
	}
	return waveMapper
}

// recordMic captures 16 kHz mono 16-bit audio from device until ctx ends or
// chunk returns false. chunk gets about 50ms of samples at a time.
func recordMic(ctx context.Context, device uintptr, chunk func([]int16) bool) error {
	// The handle and buffers belong to this goroutine for its whole life.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if n, _, _ := procWaveInGetNumDevs.Call(); n == 0 {
		return errNoMicrophone
	}
	format := waveFormat{formatTag: 1, channels: 1, samplesPerSec: sttSampleRate,
		avgBytesPerSec: sttSampleRate * 2, blockAlign: 2, bitsPerSample: 16}
	var handle uintptr
	if r, _, _ := procWaveInOpen.Call(uintptr(unsafe.Pointer(&handle)), device,
		uintptr(unsafe.Pointer(&format)), 0, 0, 0); r != 0 {
		if device != waveMapper {
			return fmt.Errorf("could not open that microphone (error %d); pick another one", r)
		}
		return errMicrophoneDisabled
	}
	defer procWaveInClose.Call(handle)

	// The buffers stay referenced by headers and bufs until the device is
	// reset and the headers unprepared below; Go does not move heap memory.
	const bytesPerBuffer = sttSampleRate * 2 * sttChunkMS / 1000
	headers := make([]waveHdr, sttBuffers)
	bufs := make([][]byte, sttBuffers)
	hdrSize := unsafe.Sizeof(waveHdr{})
	for i := range headers {
		bufs[i] = make([]byte, bytesPerBuffer)
		headers[i] = waveHdr{data: &bufs[i][0], bufferLength: bytesPerBuffer}
		if r, _, _ := procWaveInPrepare.Call(handle, uintptr(unsafe.Pointer(&headers[i])), hdrSize); r != 0 {
			return fmt.Errorf("could not prepare the microphone buffer (error %d)", r)
		}
		procWaveInAddBuffer.Call(handle, uintptr(unsafe.Pointer(&headers[i])), hdrSize)
	}
	defer func() {
		procWaveInReset.Call(handle)
		for i := range headers {
			procWaveInUnprepare.Call(handle, uintptr(unsafe.Pointer(&headers[i])), hdrSize)
		}
		runtime.KeepAlive(bufs)
	}()
	if r, _, _ := procWaveInStart.Call(handle); r != 0 {
		return fmt.Errorf("could not start the microphone (error %d)", r)
	}

	next := 0
	samples := make([]int16, 0, bytesPerBuffer/2)
	for {
		if ctx.Err() != nil {
			return nil
		}
		h := &headers[next]
		if atomic.LoadUint32(&h.flags)&whdrDone == 0 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		n := int(atomic.LoadUint32(&h.bytesRecorded))
		samples = samples[:0]
		for i := 0; i+1 < n; i += 2 {
			samples = append(samples, int16(uint16(bufs[next][i])|uint16(bufs[next][i+1])<<8))
		}
		atomic.StoreUint32(&h.flags, atomic.LoadUint32(&h.flags)&^whdrDone)
		procWaveInAddBuffer.Call(handle, uintptr(unsafe.Pointer(h)), hdrSize)
		next = (next + 1) % sttBuffers
		if len(samples) > 0 && !chunk(samples) {
			return nil
		}
	}
}
