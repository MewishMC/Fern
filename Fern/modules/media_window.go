package modules

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	enumWindowsProc            = user32.NewProc("EnumWindows")
	getWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	queryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

const processQueryLimitedInformation = 0x1000

var (
	spotifyWindowTitle    string
	spotifyWindowCallback uintptr
)

func init() {
	spotifyWindowCallback = syscall.NewCallback(spotifyWindowEnum)
}

func getProcessPath(hwnd uintptr) (string, error) {
	var pid uint32
	_, _, _ = getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", fmt.Errorf("could not obtain process id")
	}

	handle, err := syscall.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", fmt.Errorf("OpenProcess failed: %v", err)
	}
	defer syscall.CloseHandle(handle)

	var size uint32 = 1024
	buf := make([]uint16, size)

	ret, _, err := queryFullProcessImageNameW.Call(
		uintptr(handle),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == 0 {
		return "", fmt.Errorf("QueryFullProcessImageNameW failed: %v", err)
	}

	return syscall.UTF16ToString(buf[:size]), nil
}

func spotifyWindowEnum(hwnd uintptr, _ uintptr) uintptr {
	path, err := getProcessPath(hwnd)
	if err != nil {
		return 1
	}
	if !strings.Contains(strings.ToLower(path), "spotify.exe") {
		return 1
	}
	title, _ := GetWindowText(hwnd)
	if strings.TrimSpace(title) == "" {
		return 1
	}
	spotifyWindowTitle = title
	return 0
}

// findSpotifyWindow returns the title of Spotify's main window, if it has one.
func findSpotifyWindow() (string, bool) {
	spotifyWindowTitle = ""
	enumWindowsProc.Call(spotifyWindowCallback, 0)
	return spotifyWindowTitle, spotifyWindowTitle != ""
}

// parseMediaInfo splits Spotify's "Artist - Song" window title.
func parseMediaInfo(title string) (artist, song string) {
	separators := []string{" – ", " - ", " — "}
	for _, sep := range separators {
		if strings.Contains(title, sep) {
			parts := strings.SplitN(title, sep, 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			}
		}
	}
	return "", title
}
