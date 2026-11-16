package modules

import (
	"errors"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// Playing ready-made audio through the SAPI voice: the PCM goes into an HGLOBAL
// IStream, an SpStream gives it a wave format, and ISpVoice::SpeakStream plays
// it on the voice's output like any other speech. That keeps the output
// device, the queue, cut off and WaitUntilDone the same as for Windows voices.

var (
	clsidSpStream        = ole.NewGUID("{715D9C59-4442-11D2-9605-00C04F8EE628}")
	iidISpStream         = ole.NewGUID("{12E3CCA9-7518-44C5-A5E7-BA5A79CB929E}")
	spdfidWaveFormatEx   = ole.NewGUID("{C31ADBAE-527F-4FF5-A230-F62BB61FF70C}")
	procCreateStreamOnHG = syscall.NewLazyDLL("ole32.dll").NewProc("CreateStreamOnHGlobal")
)

const (
	slotStreamWrite         = 4
	slotStreamSeek          = 5
	slotStreamSetBaseStream = 15 // ISpStream, after IStream (3..13) and ISpStreamFormat::GetFormat (14)
	slotVoiceSpeakStream    = 21
)

// pcmWaveFormat is WAVEFORMATEX. Its fields fall on their natural alignment,
// so Go's layout matches the packed C one up to cbSize.
type pcmWaveFormat struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// speakPCM queues 16-bit audio on the voice; purge first stops whatever is
// being read. Must run on the voice's thread.
func (v *sapiVoice) speakPCM(a pcmAudio, purge bool) error {
	if len(a.Samples) == 0 {
		return errors.New("no audio to play")
	}
	if a.Channels < 1 || a.Channels > 2 {
		a = a.mono()
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&a.Samples[0])), len(a.Samples)*2)
	var mem *ole.IUnknown
	if hr, _, _ := procCreateStreamOnHG.Call(0, 1, uintptr(unsafe.Pointer(&mem))); hr != 0 || mem == nil {
		return errors.New("could not buffer the audio")
	}
	defer comRelease(mem)
	var written uint32
	if hr := comCall(mem, slotStreamWrite, uintptr(unsafe.Pointer(&raw[0])), uintptr(len(raw)), uintptr(unsafe.Pointer(&written))); hr != 0 {
		return hrError("buffering the audio", hr)
	}
	if hr := comCall(mem, slotStreamSeek, 0, 0, 0); hr != 0 { // back to the start (STREAM_SEEK_SET)
		return hrError("buffering the audio", hr)
	}

	stream, err := ole.CreateInstance(clsidSpStream, iidISpStream)
	if err != nil {
		return errSAPIUnavailable
	}
	defer comRelease(stream)
	block := uint16(a.Channels * 2)
	wfx := pcmWaveFormat{
		FormatTag:      1,
		Channels:       uint16(a.Channels),
		SamplesPerSec:  uint32(a.Rate),
		AvgBytesPerSec: uint32(a.Rate) * uint32(block),
		BlockAlign:     block,
		BitsPerSample:  16,
	}
	if hr := comCall(stream, slotStreamSetBaseStream, uintptr(unsafe.Pointer(mem)), uintptr(unsafe.Pointer(spdfidWaveFormatEx)), uintptr(unsafe.Pointer(&wfx))); hr != 0 {
		return hrError("preparing the audio", hr)
	}
	flags := uintptr(spfAsync)
	if purge {
		flags |= spfPurgeBeforeSpeak
	}
	return hrError("playing the audio", comCall(v.obj, slotVoiceSpeakStream, uintptr(unsafe.Pointer(stream)), flags, 0))
}
