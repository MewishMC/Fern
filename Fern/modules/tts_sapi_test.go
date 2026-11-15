package modules

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"github.com/go-ole/go-ole"
)

func TestListVoicesFindsAVoice(t *testing.T) {
	voices, err := ListVoices()
	if err != nil {
		t.Fatalf("ListVoices: %v", err)
	}
	if len(voices) == 0 {
		t.Fatal("no SAPI voices installed; every Windows install ships at least one")
	}
	for _, v := range voices {
		if v.ID == "" || v.Name == "" {
			t.Errorf("voice without id or name: %+v", v)
		}
		t.Logf("voice %q (%s)", v.Name, v.ID)
	}
}

func TestListAudioOutputs(t *testing.T) {
	outs, err := ListAudioOutputs()
	if err != nil {
		t.Fatalf("ListAudioOutputs: %v", err)
	}
	for _, o := range outs {
		t.Logf("output %q (%s)", o.Name, o.ID)
	}
}

// waveFormatEx mirrors WAVEFORMATEX.
type waveFormatEx struct {
	formatTag      uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	size           uint16
}

// TestSpeakToFile renders each voice to a WAV file instead of the speakers,
// which checks the ISpVoice vtable slots without making any noise.
func TestSpeakToFile(t *testing.T) {
	voices, err := ListVoices()
	if err != nil || len(voices) == 0 {
		t.Skip("no voices")
	}
	dir := t.TempDir()
	for i, v := range voices {
		path := filepath.Join(dir, "voice"+string(rune('a'+i%26))+".wav")
		err := withCOM(func() error {
			voice, err := newSAPIVoice()
			if err != nil {
				return err
			}
			defer voice.release()
			if err := voice.setVoice(v.ID); err != nil {
				return err
			}
			stream, err := ole.CreateInstance(ole.NewGUID("{715D9C59-4442-11D2-9605-00C04F8EE628}"),
				ole.NewGUID("{12E3CCA9-7518-44C5-A5E7-BA5A79CB929E}"))
			if err != nil {
				return err
			}
			defer comRelease(stream)
			format := waveFormatEx{formatTag: 1, channels: 1, samplesPerSec: 16000, avgBytesPerSec: 32000, blockAlign: 2, bitsPerSample: 16}
			p, _ := syscall.UTF16PtrFromString(path)
			waveFormatID := ole.NewGUID("{C31ADBAE-527F-4FF5-A230-F62BB61FF70C}")
			const spfmCreateAlways = 3
			if hr := comCall(stream, 17, uintptr(unsafe.Pointer(p)), spfmCreateAlways,
				uintptr(unsafe.Pointer(waveFormatID)), uintptr(unsafe.Pointer(&format)), 0); hr != 0 {
				return hrError("BindToFile", hr)
			}
			if err := voice.setOutputObject(stream); err != nil {
				return err
			}
			voice.setRate(2)
			voice.setVolume(80)
			if err := voice.speak("Hello from Fern.", true, false); err != nil {
				return err
			}
			comCall(stream, 18) // Close
			return nil
		})
		if err != nil {
			t.Errorf("%s: %v", v.Name, err)
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.Size() < 1000 {
			t.Errorf("%s: wrote no audio (%v)", v.Name, err)
		}
	}
}
