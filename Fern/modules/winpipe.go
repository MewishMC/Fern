package modules

import (
	"errors"
	"syscall"
	"unsafe"
)

// winPipe is a client end of a local named pipe (Discord's IPC pipe and
// Soundpad's remote control pipe). The handle is synchronous, and Windows
// serialises synchronous I/O on one handle, so a blocked read would also block
// every write. Callers therefore only read once Available reports data.
type winPipe struct {
	h syscall.Handle
}

var (
	procPeekNamedPipe           = syscall.NewLazyDLL("kernel32.dll").NewProc("PeekNamedPipe")
	procSetNamedPipeHandleState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetNamedPipeHandleState")
)

const (
	errorMoreData      syscall.Errno = 234
	pipeReadModeMesage uint32        = 0x2
)

func openPipe(name string) (*winPipe, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	return &winPipe{h: h}, nil
}

// messageMode switches reads to whole messages; pipes created in byte mode
// refuse it, which is fine.
func (p *winPipe) messageMode() {
	mode := pipeReadModeMesage
	_, _, _ = procSetNamedPipeHandleState.Call(uintptr(p.h), uintptr(unsafe.Pointer(&mode)), 0, 0)
}

func (p *winPipe) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	var n uint32
	err := syscall.ReadFile(p.h, b, &n, nil)
	if err == errorMoreData {
		// The rest of the message is returned by the next read.
		return int(n), nil
	}
	if err != nil {
		return int(n), err
	}
	if n == 0 {
		return 0, syscall.ERROR_BROKEN_PIPE
	}
	return int(n), nil
}

func (p *winPipe) Write(b []byte) (int, error) {
	var n uint32
	err := syscall.WriteFile(p.h, b, &n, nil)
	return int(n), err
}

// Available is how many bytes can be read without blocking.
func (p *winPipe) Available() (int, error) {
	var avail uint32
	r, _, err := procPeekNamedPipe.Call(uintptr(p.h), 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0)
	if r == 0 {
		if err == nil {
			err = errors.New("peek failed")
		}
		return 0, err
	}
	return int(avail), nil
}

func (p *winPipe) Close() error {
	return syscall.CloseHandle(p.h)
}
