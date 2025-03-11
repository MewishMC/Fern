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

const (
	ProcessQueryLimitedInformation = 0x1000
)

type windowInfo struct {
	hwnd      uintptr
	title     string
	processID uint32
	procPath  string
}

var foundSpotifyWindow *windowInfo

func GetProcessPath(hwnd uintptr) (string, error) {
	var pid uint32
	_, _, _ = getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return "", fmt.Errorf("could not obtain process id")
	}

	handle, err := syscall.OpenProcess(ProcessQueryLimitedInformation, false, pid)
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

var spotifyWindowCallback uintptr

func init() {
	spotifyWindowCallback = syscall.NewCallback(spotifyWindowEnum)
}

func spotifyWindowEnum(hwnd uintptr, lParam uintptr) uintptr {
	path, err := GetProcessPath(hwnd)
	if err != nil {
		return 1
	}

	if strings.Contains(strings.ToLower(path), "spotify.exe") {
		title, _ := GetWindowText(hwnd)
		foundSpotifyWindow = &windowInfo{
			hwnd:     hwnd,
			title:    title,
			procPath: path,
		}
		return 0
	}

	return 1
}

func findSpotifyWindow() (*windowInfo, string) {
	foundSpotifyWindow = nil

	ret, _, err := enumWindowsProc.Call(spotifyWindowCallback, 0)

	if foundSpotifyWindow == nil {
		return nil, "Media not found"
	}

	if ret == 0 && err != nil && err.Error() != "The operation completed successfully." {
		return nil, "Media not found"
	}

	return foundSpotifyWindow, ""
}

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

func PrintMediaInfo() string {
	spotifyWindow, err := findSpotifyWindow()
	if err != "" {
		return "Media not found"
	}

	title := spotifyWindow.title
	lowerTitle := strings.ToLower(title)

	if strings.Contains(lowerTitle, "spotify") {
		return "Music paused"
	}

	artist, song := parseMediaInfo(title)
	if artist == "" || song == "" {
		return "Media not found"
	}

	return fmt.Sprintf("'%s' by %s", song, artist)
}
