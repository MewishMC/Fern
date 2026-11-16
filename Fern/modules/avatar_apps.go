package modules

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unsafe"
)

// Opening and closing apps for rules: "when I put on this avatar, open
// VRCFT", "when this parameter turns on, close OBS".

// protectedExes are never closed: Windows itself, the desktop and Fern.
var protectedExes = map[string]bool{
	"csrss.exe": true, "winlogon.exe": true, "wininit.exe": true, "lsass.exe": true, "services.exe": true,
	"smss.exe": true, "svchost.exe": true, "dwm.exe": true, "explorer.exe": true, "system": true,
	"fern.exe": true, "update.exe": true, "conhost.exe": true, "fontdrvhost.exe": true,
}

// exeName is the lower-case file name of an app, with ".exe" added when
// there is no extension: "C:\OBS\obs64.exe" and "OBS64" are both
// "obs64.exe".
func exeName(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if s == "" {
		return ""
	}
	s = strings.ToLower(filepath.Base(strings.ReplaceAll(s, "/", `\`)))
	if filepath.Ext(s) == "" {
		s += ".exe"
	}
	return s
}

// splitArgs splits a command line the simple way: spaces separate, double
// quotes group.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
			have = true
		case unicode.IsSpace(r) && !inQuote:
			if have {
				out = append(out, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		out = append(out, cur.String())
	}
	return out
}

type procInfo struct {
	PID uint32
	Exe string // lower-case
}

var (
	procListFn     = listProcesses
	closeWindowsFn = closeProcessWindows
	terminateFn    = terminateProcess
	startAppFn     = startApp
	shellOpenFn    = shellOpen
)

func listProcesses() ([]procInfo, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.CloseHandle(snap)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var out []procInfo
	for err = syscall.Process32First(snap, &entry); err == nil; err = syscall.Process32Next(snap, &entry) {
		out = append(out, procInfo{PID: entry.ProcessID, Exe: strings.ToLower(syscall.UTF16ToString(entry.ExeFile[:]))})
	}
	return out, nil
}

// matchingProcesses lists the processes of an app, never Fern's own.
func matchingProcesses(exe string) ([]procInfo, error) {
	list, err := procListFn()
	if err != nil {
		return nil, err
	}
	self := uint32(os.Getpid())
	var out []procInfo
	for _, p := range list {
		if p.Exe == exe && p.PID != self && p.PID != 0 {
			out = append(out, p)
		}
	}
	return out, nil
}

// AppRunning reports whether an app (by path or exe name) is running.
func AppRunning(app string) bool {
	list, err := matchingProcesses(exeName(app))
	return err == nil && len(list) > 0
}

var (
	isWindowVisibleProc = user32.NewProc("IsWindowVisible")
	postMessageWProc    = user32.NewProc("PostMessageW")
	closeEnumMu         sync.Mutex
	closeEnumPIDs       map[uint32]bool
	closeEnumHits       int
	closeEnumCallback   = syscall.NewCallback(closeWindowEnum)
)

const wmClose = 0x0010

func closeWindowEnum(hwnd uintptr, _ uintptr) uintptr {
	var pid uint32
	_, _, _ = getWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if !closeEnumPIDs[pid] {
		return 1
	}
	if visible, _, _ := isWindowVisibleProc.Call(hwnd); visible == 0 {
		return 1
	}
	if ok, _, _ := postMessageWProc.Call(hwnd, wmClose, 0, 0); ok != 0 {
		closeEnumHits++
	}
	return 1
}

// closeProcessWindows asks the visible windows of the processes to close,
// like clicking their X. It returns how many were asked.
func closeProcessWindows(pids []uint32) int {
	closeEnumMu.Lock()
	defer closeEnumMu.Unlock()
	closeEnumPIDs = map[uint32]bool{}
	for _, p := range pids {
		closeEnumPIDs[p] = true
	}
	closeEnumHits = 0
	enumWindowsProc.Call(closeEnumCallback, 0)
	return closeEnumHits
}

const processTerminate = 0x0001

func terminateProcess(pid uint32) error {
	h, err := syscall.OpenProcess(processTerminate, false, pid)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	return syscall.TerminateProcess(h, 1)
}

// CloseApp closes every running copy of an app. Without force it asks the
// app's windows to close, so the app can save its work; with force it ends
// the processes like Task Manager's End task. It returns how many processes
// it acted on.
func CloseApp(app string, force bool) (int, error) {
	exe := exeName(app)
	if exe == "" {
		return 0, errors.New("name the app, e.g. obs64.exe")
	}
	if protectedExes[exe] {
		return 0, fmt.Errorf("Fern won't close %s", exe)
	}
	list, err := matchingProcesses(exe)
	if err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, nil
	}
	pids := make([]uint32, len(list))
	for i, p := range list {
		pids[i] = p.PID
	}
	if !force {
		if closeWindowsFn(pids) == 0 {
			return 0, fmt.Errorf("%s has no window to close; use End task instead", exe)
		}
		return len(pids), nil
	}
	done := 0
	var firstErr error
	for _, pid := range pids {
		if err := terminateFn(pid); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		done++
	}
	if done == 0 && firstErr != nil {
		return 0, fmt.Errorf("couldn't end %s: %w", exe, firstErr)
	}
	return done, nil
}

// startApp starts a program and returns its process ID.
func startApp(path string, args []string) (int, error) {
	cmd := exec.Command(path, args...)
	if filepath.IsAbs(path) {
		cmd.Dir = filepath.Dir(path)
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

var shellExecuteWProc = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

// shellOpen opens a file, shortcut or folder the way double-clicking it does.
func shellOpen(path, args string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var params *uint16
	if args != "" {
		if params, err = syscall.UTF16PtrFromString(args); err != nil {
			return err
		}
	}
	const swShowNormal = 1
	r, _, _ := shellExecuteWProc.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, swShowNormal)
	if r <= 32 {
		return fmt.Errorf("Windows couldn't open %s (error %d)", filepath.Base(path), r)
	}
	return nil
}

// cleanAppPath checks a path or exe name from a rule.
func cleanAppPath(path string) (string, error) {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if path == "" {
		return "", errors.New("name the app to open, e.g. C:\\Program Files\\obs-studio\\bin\\64bit\\obs64.exe")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return "", errors.New("that path has control characters in it")
		}
	}
	return path, nil
}

// OpenApp starts an app by path or exe name, or opens a file or shortcut.
// It returns the process ID when Fern started the program itself, or 0.
func OpenApp(path, args string) (int, error) {
	path, err := cleanAppPath(path)
	if err != nil {
		return 0, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".exe" || ext == "" {
		if !strings.ContainsAny(path, `\/`) {
			if found, err := exec.LookPath(path); err == nil {
				path = found
			} else if ext == "" {
				// Not a program on PATH: let Windows find it (App Paths).
				return 0, shellOpenFn(path, args)
			}
		}
		pid, err := startAppFn(path, splitArgs(args))
		if err != nil {
			return 0, fmt.Errorf("couldn't start %s: %w", filepath.Base(path), err)
		}
		return pid, nil
	}
	return 0, shellOpenFn(path, args)
}

// closePID asks one process to close, as the undo of opening it.
func closePID(pid int) {
	list, err := procListFn()
	if err != nil {
		return
	}
	for _, p := range list {
		if int(p.PID) == pid {
			closeWindowsFn([]uint32{p.PID})
			return
		}
	}
}

// IDs of the rule actions that open and close apps.
const (
	ActionAppOpen  = "app.open"
	ActionAppClose = "app.close"
)

func init() {
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionAppOpen, Label: "Open an app", Category: "You",
		Description: "Starts a program, or opens a file or shortcut. Undo closes what it started.",
		Fields: []RuleField{
			{Key: "path", Label: "App", Type: FieldText, MaxLen: 260, Placeholder: `e.g. C:\Program Files\obs-studio\bin\64bit\obs64.exe`,
				Hint: "A full path, or the name of a program Windows knows, like notepad.exe"},
			{Key: "args", Label: "Arguments", Type: FieldText, MaxLen: 260, Placeholder: "optional"},
			{Key: "once", Label: "Not if it's running", Type: FieldBool, Default: true, Hint: "Skips opening a second copy"},
		},
		Run: runAppOpenAction,
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionAppClose, Label: "Close an app", Category: "You",
		Description: "Closes every copy of an app that is running",
		Fields: []RuleField{
			{Key: "app", Label: "App", Type: FieldText, Source: SourceApps, MaxLen: 260, Placeholder: "e.g. obs64.exe"},
			{Key: "mode", Label: "How", Type: FieldSelect, Options: []RuleOption{
				{"close", "Ask it to close"}, {"kill", "End task"},
			}, Hint: "Asking lets the app save first; End task stops it at once"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			app := strings.TrimSpace(p.String("app"))
			force := p.String("mode") == "kill"
			if exeName(app) == "" {
				return nil, errors.New("name the app, e.g. obs64.exe")
			}
			if protectedExes[exeName(app)] {
				return nil, fmt.Errorf("Fern won't close %s", exeName(app))
			}
			// Closing asks windows one by one; keep it off the rules goroutine.
			go func() {
				if _, err := CloseApp(app, force); err != nil {
					fmt.Println("rules: close app:", err)
				}
			}()
			return nil, nil
		},
	}))
}

func runAppOpenAction(_ RuleContext, p RuleParams) (func(), error) {
	path, err := cleanAppPath(p.String("path"))
	if err != nil {
		return nil, err
	}
	if p.Bool("once") && AppRunning(path) {
		return nil, nil
	}
	pid, err := OpenApp(path, strings.TrimSpace(p.String("args")))
	if err != nil {
		return nil, err
	}
	if pid == 0 {
		return nil, nil
	}
	return func() { closePID(pid) }, nil
}
