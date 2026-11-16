package UI

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Behaviour is how the main window reacts to closing and minimising, whether
// it has a tray icon, and the global shortcut. It is applied on the window's
// thread; Apply can be called from anywhere, before or after the window exists.
type Behaviour struct {
	Tray           bool
	CloseToTray    bool
	MinimizeToTray bool
	HotkeyMods     uint32 // RegisterHotKey modifiers; HotkeyKey 0 means none
	HotkeyKey      uint32
	// Extra are more global shortcuts, such as the profile switches. Each one
	// calls OnExtraHotkey with its ID.
	Extra []Hotkey
}

// Hotkey is one more global shortcut.
type Hotkey struct {
	ID   string
	Mods uint32 // RegisterHotKey modifiers
	Key  uint32
}

// TrayItem is an entry in a tray menu section added by TrayItems.
type TrayItem struct {
	ID      string
	Label   string
	Checked bool
}

// OnHotkey runs on its own goroutine when the global shortcut is pressed.
var OnHotkey func()

// OnExtraHotkey runs on its own goroutine when one of Behaviour.Extra is
// pressed.
var OnExtraHotkey func(id string)

// TrayItems, when set, is asked for a submenu each time the tray menu opens;
// an empty list leaves it out. OnTrayItem runs on its own goroutine when one
// of its entries is picked.
var (
	TrayItems  func() (title string, items []TrayItem)
	OnTrayItem func(id string)
	// TrayToggles, when set, adds entries to the tray menu itself (not a
	// submenu), such as do not disturb. Picking one calls OnTrayItem too.
	TrayToggles func() []TrayItem
)

var (
	behaviourMu sync.Mutex
	wanted      Behaviour
	windowReady bool
	hotkeyErr   string
	extraErrs   map[string]string

	// Only touched on the main window's thread.
	applied     Behaviour
	trayShown   bool
	hotkeyOn    bool
	extraOn     []bool
	mainHwnd    uintptr
	trayIcon    uintptr
	taskbarMade uint32 // "TaskbarCreated": Explorer restarted, add the icon again
)

// Apply asks for a new behaviour.
func Apply(b Behaviour) {
	behaviourMu.Lock()
	wanted = b
	ready := windowReady
	behaviourMu.Unlock()
	if ready {
		mainView.Dispatch(applyBehaviour)
	}
}

// HotkeyError is why the shortcut could not be registered, if it could not.
func HotkeyError() string {
	behaviourMu.Lock()
	defer behaviourMu.Unlock()
	return hotkeyErr
}

// HotkeyErrors says, by ID, which of Behaviour.Extra could not be registered
// and why.
func HotkeyErrors() map[string]string {
	behaviourMu.Lock()
	defer behaviourMu.Unlock()
	out := make(map[string]string, len(extraErrs))
	for id, msg := range extraErrs {
		out[id] = msg
	}
	return out
}

// Quit closes Fern as if Quit were picked from the tray menu.
func Quit() {
	behaviourMu.Lock()
	ready := windowReady
	behaviourMu.Unlock()
	if ready {
		mainView.Dispatch(func() { mainView.Terminate() })
	}
}

// ShowWindow brings the panel back from the tray or the taskbar.
func ShowWindow() {
	behaviourMu.Lock()
	ready := windowReady
	behaviourMu.Unlock()
	if ready {
		mainView.Dispatch(func() { showMain() })
	}
}

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	procLoadImageW             = user32.NewProc("LoadImageW")
	procLoadIconW              = user32.NewProc("LoadIconW")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procRegisterHotKey         = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey       = user32.NewProc("UnregisterHotKey")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procSetMenuDefaultItem     = user32.NewProc("SetMenuDefaultItem")
)

const (
	wmNull          = 0x0000
	wmSize          = 0x0005
	wmHotkey        = 0x0312
	wmTrayCallback  = 0x8000 + 1 // WM_APP + 1
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	sizeMinimized   = 1

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 0x1
	nifIcon   = 0x2
	nifTip    = 0x4

	swHide     = 0
	swShow     = 5
	swMinimize = 6

	mfString    = 0x0
	mfChecked   = 0x8
	mfPopup     = 0x10
	mfSeparator = 0x800
	tpmRetCmd   = 0x100
	tpmRightBtn = 0x2

	menuShow = 1
	menuQuit = 2
	// menuExtra + i is entry i of TrayItems.
	menuExtra = 100
	// menuToggle + i is entry i of TrayToggles.
	menuToggle = 1000

	// extraHotkeyID + i is Behaviour.Extra[i].
	extraHotkeyID = 100

	hotkeyID       = 1
	modNoRepeat    = 0x4000
	imageIcon      = 1
	smCxSmIcon     = 49
	smCySmIcon     = 50
	idiApplication = 32512
)

// notifyIconData mirrors NOTIFYICONDATAW (976 bytes on 64-bit Windows).
type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        [16]byte
	BalloonIcon     uintptr
}

// setupMain runs once on the window thread after the window is created.
func setupMain(hwnd uintptr) {
	mainHwnd = hwnd
	name, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
	taskbarMade = uint32(r)
	behaviourMu.Lock()
	windowReady = true
	behaviourMu.Unlock()
	applyBehaviour()
}

func applyBehaviour() {
	behaviourMu.Lock()
	b := wanted
	behaviourMu.Unlock()

	if b.Tray != trayShown {
		if b.Tray {
			addTrayIcon()
		} else {
			removeTrayIcon()
		}
	}
	if !trayShown && !windowVisible() {
		// The tray icon was the only way back to a hidden window.
		showMain()
	}
	if b.HotkeyMods != applied.HotkeyMods || b.HotkeyKey != applied.HotkeyKey || !hotkeyOn && b.HotkeyKey != 0 {
		setHotkey(b.HotkeyMods, b.HotkeyKey)
	}
	if !slices.Equal(b.Extra, applied.Extra) {
		setExtraHotkeys(b.Extra)
	}
	applied = b
}

func setHotkey(mods, key uint32) {
	if hotkeyOn {
		_, _, _ = procUnregisterHotKey.Call(mainHwnd, hotkeyID)
		hotkeyOn = false
	}
	msg := ""
	if key != 0 {
		r, _, err := procRegisterHotKey.Call(mainHwnd, hotkeyID, uintptr(mods|modNoRepeat), uintptr(key))
		if r == 0 {
			msg = fmt.Sprintf("Windows refused the shortcut, probably because another app uses it (%v)", err)
		} else {
			hotkeyOn = true
		}
	}
	behaviourMu.Lock()
	hotkeyErr = msg
	behaviourMu.Unlock()
}

func unregisterExtra() {
	for i, on := range extraOn {
		if on {
			_, _, _ = procUnregisterHotKey.Call(mainHwnd, uintptr(extraHotkeyID+i))
		}
	}
	extraOn = nil
}

func setExtraHotkeys(keys []Hotkey) {
	unregisterExtra()
	errs := map[string]string{}
	extraOn = make([]bool, len(keys))
	for i, k := range keys {
		if k.Key == 0 {
			continue
		}
		r, _, err := procRegisterHotKey.Call(mainHwnd, uintptr(extraHotkeyID+i), uintptr(k.Mods|modNoRepeat), uintptr(k.Key))
		if r == 0 {
			errs[k.ID] = fmt.Sprintf("Windows refused the shortcut, probably because another app uses it (%v)", err)
			continue
		}
		extraOn[i] = true
	}
	behaviourMu.Lock()
	extraErrs = errs
	behaviourMu.Unlock()
}

func loadTrayIcon() uintptr {
	if trayIcon != 0 {
		return trayIcon
	}
	module, _, _ := procGetModuleHandleW.Call(0)
	cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	// Resource 1 is Fern's icon (Fern.rc). go run builds have it too, since
	// the compiled resource (Fern.syso) is linked into every build.
	icon, _, _ := procLoadImageW.Call(module, 1, imageIcon, cx, cy, 0)
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	trayIcon = icon
	return icon
}

func trayData() *notifyIconData {
	d := &notifyIconData{Wnd: mainHwnd, ID: 1, Flags: nifMsg | nifIcon | nifTip, CallbackMessage: wmTrayCallback, Icon: loadTrayIcon()}
	d.Size = uint32(unsafe.Sizeof(*d))
	copy(d.Tip[:len(d.Tip)-1], syscall.StringToUTF16("Fern"))
	return d
}

func addTrayIcon() {
	r, _, _ := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(trayData())))
	trayShown = r != 0
}

func removeTrayIcon() {
	if !trayShown {
		return
	}
	d := &notifyIconData{Wnd: mainHwnd, ID: 1}
	d.Size = uint32(unsafe.Sizeof(*d))
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(d)))
	trayShown = false
}

func windowVisible() bool {
	r, _, _ := procIsWindowVisible.Call(mainHwnd)
	return r != 0
}

func hideMain() { _, _, _ = procShowWindow.Call(mainHwnd, swHide) }

func showMain() {
	_, _, _ = procShowWindow.Call(mainHwnd, swShow)
	focusWindow(mainHwnd)
}

// startHidden puts a new window straight into the tray or the taskbar.
func startHidden(minimized bool) {
	behaviourMu.Lock()
	tray := wanted.Tray
	behaviourMu.Unlock()
	if !minimized {
		return
	}
	if tray && trayShown {
		hideMain()
	} else {
		_, _, _ = procShowWindow.Call(mainHwnd, swMinimize)
	}
}

// mainMessage handles the main window's tray, hotkey, close and minimise
// messages. It returns true when the message was dealt with.
func mainMessage(msg, wParam, lParam uintptr) bool {
	switch {
	case msg == wmTrayCallback:
		switch lParam & 0xFFFF {
		case wmLButtonUp, wmLButtonDblClk:
			showMain()
		case wmRButtonUp, wmContextMenu:
			trayMenu()
		}
		return true
	case msg == wmHotkey && wParam == hotkeyID:
		if OnHotkey != nil {
			go OnHotkey()
		}
		return true
	case msg == wmHotkey && wParam >= extraHotkeyID && wParam < extraHotkeyID+uintptr(len(applied.Extra)):
		if OnExtraHotkey != nil {
			go OnExtraHotkey(applied.Extra[wParam-extraHotkeyID].ID)
		}
		return true
	case taskbarMade != 0 && uint32(msg) == taskbarMade:
		if trayShown {
			addTrayIcon()
		}
		return false
	case msg == wmClose:
		if trayShown && applied.CloseToTray {
			hideMain()
			return true
		}
	case msg == wmSize && wParam == sizeMinimized:
		if trayShown && applied.MinimizeToTray {
			hideMain()
			return false
		}
	}
	return false
}

func trayMenu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	show, _ := syscall.UTF16PtrFromString("Show Fern")
	quit, _ := syscall.UTF16PtrFromString("Quit Fern")
	_, _, _ = procAppendMenuW.Call(menu, mfString, menuShow, uintptr(unsafe.Pointer(show)))
	_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
	var extra []TrayItem
	if TrayItems != nil {
		var title string
		title, extra = TrayItems()
		if len(extra) > 0 {
			if sub, _, _ := procCreatePopupMenu.Call(); sub != 0 {
				// The submenu belongs to menu from here on and goes with it.
				for i, item := range extra {
					flags := uintptr(mfString)
					if item.Checked {
						flags |= mfChecked
					}
					label, _ := syscall.UTF16PtrFromString(menuLabel(item.Label))
					_, _, _ = procAppendMenuW.Call(sub, flags, uintptr(menuExtra+i), uintptr(unsafe.Pointer(label)))
				}
				name, _ := syscall.UTF16PtrFromString(menuLabel(title))
				_, _, _ = procAppendMenuW.Call(menu, mfPopup, sub, uintptr(unsafe.Pointer(name)))
				_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			}
		}
	}
	var toggles []TrayItem
	if TrayToggles != nil {
		toggles = TrayToggles()
		for i, item := range toggles {
			flags := uintptr(mfString)
			if item.Checked {
				flags |= mfChecked
			}
			label, _ := syscall.UTF16PtrFromString(menuLabel(item.Label))
			_, _, _ = procAppendMenuW.Call(menu, flags, uintptr(menuToggle+i), uintptr(unsafe.Pointer(label)))
		}
		if len(toggles) > 0 {
			_, _, _ = procAppendMenuW.Call(menu, mfSeparator, 0, 0)
		}
	}
	_, _, _ = procAppendMenuW.Call(menu, mfString, menuQuit, uintptr(unsafe.Pointer(quit)))
	_, _, _ = procSetMenuDefaultItem.Call(menu, menuShow, 0)
	var pt struct{ X, Y int32 }
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without this the menu stays open when clicking elsewhere (documented
	// TrackPopupMenu quirk), and the WM_NULL afterwards makes it close properly.
	_, _, _ = procSetForeground.Call(mainHwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRetCmd|tpmRightBtn, uintptr(pt.X), uintptr(pt.Y), 0, mainHwnd, 0)
	_, _, _ = procPostMessageW.Call(mainHwnd, wmNull, 0, 0)
	switch cmd {
	case menuShow:
		showMain()
	case menuQuit:
		mainView.Terminate()
	default:
		if i := int(cmd) - menuToggle; i >= 0 && i < len(toggles) && OnTrayItem != nil {
			go OnTrayItem(toggles[i].ID)
		} else if i := int(cmd) - menuExtra; i >= 0 && i < len(extra) && OnTrayItem != nil {
			go OnTrayItem(extra[i].ID)
		}
	}
}

// menuLabel keeps a name from turning a letter into a menu accelerator or
// cutting the label short.
func menuLabel(s string) string {
	s = strings.ReplaceAll(s, "&", "&&")
	return strings.Map(func(r rune) rune {
		if r == 0 || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// cleanupMain removes the tray icon so it does not linger after exit.
func cleanupMain() {
	if hotkeyOn {
		_, _, _ = procUnregisterHotKey.Call(mainHwnd, hotkeyID)
		hotkeyOn = false
	}
	unregisterExtra()
	removeTrayIcon()
}
