package modules

import (
	"strings"
	"syscall"
	"unsafe"
)

var user32 = syscall.NewLazyDLL("user32.dll")

func GetWindowText(hwnd uintptr) (string, error) {
	getwindowtextv := user32.NewProc("GetWindowTextW")
	buf := make([]uint16, 1024)
	ret, _, err := getwindowtextv.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if err != nil && err.Error() != "The operation completed successfully." {
		return "", err
	}
	return syscall.UTF16ToString(buf[:ret]), nil
}

func Getforgroundwindow() (uintptr, error) {
	getforgroundwindowv := user32.NewProc("GetForegroundWindow")
	ret, _, err := getforgroundwindowv.Call()
	if err != nil && err.Error() != "The operation completed successfully." {
		return 0, err
	}
	return ret, nil
}

func GetActiveWindow() string {
	hwnd, _ := Getforgroundwindow()
	title, _ := GetWindowText(hwnd)
	if Config.WindowOptions.Simplified {
		switch {
		case strings.Contains(title, "FireFox"):
			title = "FireFox"
		case strings.Contains(title, "c:\\"):
			title = "Explorer"
		case strings.Contains(title, "Google Chrome"):
			title = "Chrome"
		case strings.Contains(title, "Discord"):
			title = "Discord"
		case strings.Contains(title, "Opera"):
			title = "Opera"
		case strings.Contains(title, "Brave"):
			title = "Brave"
		case strings.Contains(title, "Microsoft Edge"):
			title = "I use edge PLS MAKE FUN OF ME"
		}
	}
	return title
}
