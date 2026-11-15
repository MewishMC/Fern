package modules

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// Text to speech through SAPI 5 (sapi.dll), which ships with every Windows
// install. The COM interfaces are called through their vtables; the slot
// numbers come from sapi.h (MinGW-w64 sapi54.h).

var (
	clsidSpVoice               = ole.NewGUID("{96749377-3391-11D2-9EE3-00C04F797396}")
	iidISpVoice                = ole.NewGUID("{6C44DF74-72B9-4992-A1EC-EF996E0422D4}")
	clsidSpObjectTokenCategory = ole.NewGUID("{A910187F-0C7A-45AC-92CC-59EDAFB77B53}")
	iidISpObjectTokenCategory  = ole.NewGUID("{2D3D3845-39AF-4850-BBF9-40B49780011D}")
	clsidSpObjectToken         = ole.NewGUID("{EF411752-3736-4CB4-9C8C-8EF4CCB58EFE}")
	iidISpObjectToken          = ole.NewGUID("{14056589-E16C-11D2-BB90-00C04F8EE6C0}")
	sapiVoicesCategory         = `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Speech\Voices`
	sapiOneCoreVoicesCategory  = `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Speech_OneCore\Voices`
	sapiAudioOutputCategory    = `HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Speech\AudioOutput`
	errSAPIUnavailable         = errors.New("Windows speech (SAPI) is not available")
)

// vtable slots.
const (
	slotRelease = 2

	// ISpDataKey (and so ISpObjectToken / ISpObjectTokenCategory)
	slotGetStringValue = 6
	// ISpObjectTokenCategory
	slotCategorySetID      = 15
	slotCategoryEnumTokens = 18
	// ISpObjectToken
	slotTokenSetID = 15
	slotTokenGetID = 16
	// IEnumSpObjectTokens
	slotEnumItem     = 7
	slotEnumGetCount = 8
	// ISpVoice
	slotVoiceSetOutput     = 13
	slotVoiceSetVoice      = 18
	slotVoiceSpeak         = 20
	slotVoiceSetRate       = 28
	slotVoiceSetVolume     = 30
	slotVoiceWaitUntilDone = 32
)

// Speak flags.
const (
	spfAsync             = 0x1
	spfPurgeBeforeSpeak  = 0x2
	spfIsNotXML          = 0x10
	sFalse               = 1
	rpcEChangedMode      = 0x80010106
	maxTokensToEnumerate = 200
)

func comCall(obj *ole.IUnknown, slot int, args ...uintptr) uintptr {
	method := (*[64]uintptr)(unsafe.Pointer(obj.RawVTable))[slot]
	hr, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)...)
	return hr
}

func comRelease(obj *ole.IUnknown) {
	if obj != nil {
		comCall(obj, slotRelease)
	}
}

func hrError(what string, hr uintptr) error {
	if hr == 0 {
		return nil
	}
	return fmt.Errorf("%s failed (0x%08X)", what, uint32(hr))
}

// coTaskString reads and frees a string SAPI allocated with CoTaskMemAlloc.
func coTaskString(p *uint16) string {
	if p == nil {
		return ""
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(p)))
	var units []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		u := *(*uint16)(ptr)
		if u == 0 || len(units) > 4096 {
			break
		}
		units = append(units, u)
	}
	return syscall.UTF16ToString(units)
}

// withCOM runs fn on a locked OS thread with COM initialised for it.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	if err == nil || isHR(err, sFalse) {
		defer ole.CoUninitialize()
	} else if !isHR(err, rpcEChangedMode) {
		return errSAPIUnavailable
	}
	return fn()
}

func isHR(err error, code uintptr) bool {
	var oe *ole.OleError
	return errors.As(err, &oe) && oe.Code() == code
}

// SpeechToken is a voice or an audio output that SAPI can use.
type SpeechToken struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// enumTokens lists the tokens of one SAPI category. Must run inside withCOM.
func enumTokens(category string) ([]SpeechToken, error) {
	cat, err := ole.CreateInstance(clsidSpObjectTokenCategory, iidISpObjectTokenCategory)
	if err != nil {
		return nil, errSAPIUnavailable
	}
	defer comRelease(cat)
	id, _ := syscall.UTF16PtrFromString(category)
	if hr := comCall(cat, slotCategorySetID, uintptr(unsafe.Pointer(id)), 0); hr != 0 {
		return nil, hrError("opening "+category, hr)
	}
	var enum *ole.IUnknown
	if hr := comCall(cat, slotCategoryEnumTokens, 0, 0, uintptr(unsafe.Pointer(&enum))); hr != 0 || enum == nil {
		return nil, hrError("listing "+category, hr)
	}
	defer comRelease(enum)
	var count uint32
	if hr := comCall(enum, slotEnumGetCount, uintptr(unsafe.Pointer(&count))); hr != 0 {
		return nil, hrError("counting tokens", hr)
	}
	var out []SpeechToken
	for i := uint32(0); i < count && i < maxTokensToEnumerate; i++ {
		var token *ole.IUnknown
		if hr := comCall(enum, slotEnumItem, uintptr(i), uintptr(unsafe.Pointer(&token))); hr != 0 || token == nil {
			continue
		}
		var idPtr, namePtr *uint16
		comCall(token, slotTokenGetID, uintptr(unsafe.Pointer(&idPtr)))
		// The unnamed default value of a token key is its display name.
		comCall(token, slotGetStringValue, 0, uintptr(unsafe.Pointer(&namePtr)))
		t := SpeechToken{ID: coTaskString(idPtr), Name: coTaskString(namePtr)}
		comRelease(token)
		if t.ID == "" {
			continue
		}
		if t.Name == "" {
			t.Name = t.ID
		}
		out = append(out, t)
	}
	return out, nil
}

// ListVoices returns the installed voices: the classic SAPI ones plus the
// newer OneCore voices that Windows' Settings app installs, which SAPI can
// also drive. Voices with the same name are listed once.
func ListVoices() ([]SpeechToken, error) {
	var voices []SpeechToken
	err := withCOM(func() error {
		classic, err := enumTokens(sapiVoicesCategory)
		if err != nil {
			return err
		}
		voices = append(voices, classic...)
		onecore, _ := enumTokens(sapiOneCoreVoicesCategory)
		seen := map[string]bool{}
		for _, v := range classic {
			seen[voiceKey(v.Name)] = true
		}
		for _, v := range onecore {
			if !seen[voiceKey(v.Name)] {
				seen[voiceKey(v.Name)] = true
				voices = append(voices, v)
			}
		}
		return nil
	})
	return voices, err
}

// voiceKey makes "Microsoft David Desktop - English (United States)" and
// "Microsoft David - English (United States)" compare equal.
func voiceKey(name string) string {
	return strings.ReplaceAll(name, " Desktop", "")
}

// ListAudioOutputs returns the playback devices SAPI can speak to.
func ListAudioOutputs() ([]SpeechToken, error) {
	var outs []SpeechToken
	err := withCOM(func() error {
		var err error
		outs, err = enumTokens(sapiAudioOutputCategory)
		return err
	})
	return outs, err
}

// tokenFromID opens a token by its full registry id. Must run inside withCOM.
func tokenFromID(id string) (*ole.IUnknown, error) {
	token, err := ole.CreateInstance(clsidSpObjectToken, iidISpObjectToken)
	if err != nil {
		return nil, errSAPIUnavailable
	}
	p, _ := syscall.UTF16PtrFromString(id)
	if hr := comCall(token, slotTokenSetID, 0, uintptr(unsafe.Pointer(p)), 0); hr != 0 {
		comRelease(token)
		return nil, hrError("opening "+id, hr)
	}
	return token, nil
}

// sapiVoice wraps one ISpVoice. All methods must run on the thread that
// created it inside withCOM.
type sapiVoice struct {
	obj *ole.IUnknown
}

func newSAPIVoice() (*sapiVoice, error) {
	obj, err := ole.CreateInstance(clsidSpVoice, iidISpVoice)
	if err != nil {
		return nil, errSAPIUnavailable
	}
	return &sapiVoice{obj: obj}, nil
}

func (v *sapiVoice) release() { comRelease(v.obj) }

// setVoice selects a voice by token id; empty selects the default voice.
func (v *sapiVoice) setVoice(id string) error {
	if id == "" {
		return hrError("selecting the default voice", comCall(v.obj, slotVoiceSetVoice, 0))
	}
	token, err := tokenFromID(id)
	if err != nil {
		return err
	}
	defer comRelease(token)
	return hrError("selecting the voice", comCall(v.obj, slotVoiceSetVoice, uintptr(unsafe.Pointer(token))))
}

// setOutput selects an audio output token, or an IUnknown stream, or the
// default device with nil.
func (v *sapiVoice) setOutputObject(obj *ole.IUnknown) error {
	return hrError("selecting the output", comCall(v.obj, slotVoiceSetOutput, uintptr(unsafe.Pointer(obj)), 1))
}

func (v *sapiVoice) setOutput(id string) error {
	if id == "" {
		return v.setOutputObject(nil)
	}
	token, err := tokenFromID(id)
	if err != nil {
		return err
	}
	defer comRelease(token)
	return v.setOutputObject(token)
}

func (v *sapiVoice) setRate(rate int) {
	comCall(v.obj, slotVoiceSetRate, uintptr(int32(rate)))
}

func (v *sapiVoice) setVolume(volume int) {
	comCall(v.obj, slotVoiceSetVolume, uintptr(uint16(volume)))
}

// speak queues text; purge first stops whatever is being read.
func (v *sapiVoice) speak(text string, purge, async bool) error {
	flags := uintptr(spfIsNotXML)
	if async {
		flags |= spfAsync
	}
	if purge {
		flags |= spfPurgeBeforeSpeak
	}
	p, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return errors.New("the message contains a NUL character")
	}
	return hrError("speaking", comCall(v.obj, slotVoiceSpeak, uintptr(unsafe.Pointer(p)), flags, 0))
}

// stop purges everything queued.
func (v *sapiVoice) stop() {
	comCall(v.obj, slotVoiceSpeak, 0, spfAsync|spfPurgeBeforeSpeak, 0)
}

// done waits up to ms for the queue to finish and reports whether it has.
func (v *sapiVoice) done(ms uint32) bool {
	return comCall(v.obj, slotVoiceWaitUntilDone, uintptr(ms)) == 0
}
