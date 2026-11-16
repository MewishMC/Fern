package modules

// Clipboard to chatbox: a global shortcut that sends the text on the
// clipboard as a chatbox message straight away, with no confirmation.

import (
	"errors"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

// What the clipboard shortcut does with text longer than MaxChars.
const (
	ClipCut  = "cut"  // send the start with an ellipsis
	ClipSkip = "skip" // send nothing
)

// ClipboardOptions is the clipboard shortcut. It is kept per PC.
type ClipboardOptions struct {
	Hotkey   string `json:"hotkey"`    // canonical, e.g. Ctrl+Shift+V; "" is off
	MaxChars int    `json:"max_chars"` // longest message sent
	TooLong  string `json:"too_long"`  // cut or skip
	Seconds  int    `json:"seconds"`   // how long it shows; 0 pins it
	Speak    bool   `json:"speak"`     // read it out like a typed message
}

func defaultClipboard() ClipboardOptions {
	return ClipboardOptions{MaxChars: 143, TooLong: ClipCut, Seconds: 10}
}

func (o *ClipboardOptions) sanitize() {
	if o.Hotkey != "" {
		if _, _, canonical, err := ParseHotkey(o.Hotkey); err == nil {
			o.Hotkey = canonical
		} else {
			o.Hotkey = ""
		}
	}
	if o.MaxChars == 0 {
		o.MaxChars = 143
	}
	o.MaxChars = clampInt(o.MaxChars, 8, 143)
	if o.TooLong != ClipSkip {
		o.TooLong = ClipCut
	}
	o.Seconds = clampInt(o.Seconds, 0, 600)
}

// ClipboardMessage turns clipboard text into the message the shortcut
// sends: on one line, at most o.MaxChars long.
func ClipboardMessage(text string, o ClipboardOptions) (string, error) {
	text = strings.Join(strings.Fields(plainLine(text)), " ")
	if text == "" {
		return "", errors.New("the clipboard has no text")
	}
	if utf8.RuneCountInString(text) > o.MaxChars {
		if o.TooLong == ClipSkip {
			return "", errors.New("the clipboard text is longer than the limit, so nothing was sent")
		}
		text = ellipsize(text, o.MaxChars)
	}
	return text, nil
}

// readClipboard is swapped by tests.
var readClipboard = clipboardText

// SendClipboard sends the clipboard's text to the chatbox and returns what
// was sent.
func SendClipboard() (string, error) {
	o := GetConfig().Clipboard
	raw, err := readClipboard()
	if err != nil {
		return "", err
	}
	msg, err := ClipboardMessage(raw, o)
	if err != nil {
		return "", err
	}
	SetHold(msg, time.Duration(o.Seconds)*time.Second)
	if o.Speak {
		SpeakSent(msg)
	}
	AddChatHistory(msg)
	return msg, nil
}

const cfUnicodeText = 13

var (
	user32Clip           = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard    = user32Clip.NewProc("OpenClipboard")
	procCloseClipboard   = user32Clip.NewProc("CloseClipboard")
	procGetClipboardData = user32Clip.NewProc("GetClipboardData")
	procIsFormatAvail    = user32Clip.NewProc("IsClipboardFormatAvailable")
	kernel32Clip         = syscall.NewLazyDLL("kernel32.dll")
	procGlobalLock       = kernel32Clip.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32Clip.NewProc("GlobalUnlock")
	procGlobalSize       = kernel32Clip.NewProc("GlobalSize")
)

// clipboardText reads the clipboard's text. Another app may hold the
// clipboard for a moment, so opening it is retried briefly.
func clipboardText() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	opened := false
	for try := 0; try < 10; try++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return "", errors.New("another app is using the clipboard")
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procIsFormatAvail.Call(cfUnicodeText); r == 0 {
		return "", errors.New("the clipboard has no text")
	}
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", errors.New("the clipboard has no text")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", errors.New("could not read the clipboard")
	}
	defer procGlobalUnlock.Call(h)
	size, _, _ := procGlobalSize.Call(h)
	n := int(size / 2)
	// Only the start matters; a huge clipboard is not copied whole.
	if n > 8192 {
		n = 8192
	}
	if n == 0 {
		return "", errors.New("the clipboard has no text")
	}
	// p is memory Windows owns, locked until GlobalUnlock; going through a
	// pointer to it keeps vet's uintptr check quiet.
	units := unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n)
	for i, u := range units {
		if u == 0 {
			units = units[:i]
			break
		}
	}
	return syscall.UTF16ToString(units), nil
}
