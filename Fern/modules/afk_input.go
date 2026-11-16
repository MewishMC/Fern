package modules

import (
	"time"
	"unsafe"
)

var (
	getLastInputInfo = user32.NewProc("GetLastInputInfo")
	getTickCount     = kernel32.NewProc("GetTickCount")
)

type lastInputInfo struct {
	Size uint32
	Time uint32
}

// inputIdle is how long ago the keyboard or mouse was last used anywhere in
// the session, VRChat included. ok is false when Windows would not say.
func inputIdle() (idle time.Duration, ok bool) {
	info := lastInputInfo{Size: uint32(unsafe.Sizeof(lastInputInfo{}))}
	if r, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, false
	}
	now, _, _ := getTickCount.Call()
	return tickDiff(uint32(now), info.Time), true
}

// tickDiff subtracts two GetTickCount readings. The counter is 32 bits of
// milliseconds and wraps every 49.7 days; unsigned subtraction survives that.
func tickDiff(now, then uint32) time.Duration {
	return time.Duration(now-then) * time.Millisecond
}
