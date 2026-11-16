package modules

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"github.com/go-ole/go-ole"
)

// TestSpeakPCMToFile plays ready-made audio through the voice into a WAV
// file instead of the speakers, which checks the SpeakStream path (memory
// stream, SpStream base stream, vtable slots) without making any noise.
func TestSpeakPCMToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pcm.wav")
	in := pcmAudio{Rate: 22050, Channels: 1, Samples: make([]int16, 22050/2)}
	for i := range in.Samples {
		in.Samples[i] = int16((i % 50) * 200)
	}
	err := withCOM(func() error {
		voice, err := newSAPIVoice()
		if err != nil {
			return err
		}
		defer voice.release()
		out, err := ole.CreateInstance(clsidSpStream, iidISpStream)
		if err != nil {
			return err
		}
		defer comRelease(out)
		format := pcmWaveFormat{FormatTag: 1, Channels: 1, SamplesPerSec: 16000, AvgBytesPerSec: 32000, BlockAlign: 2, BitsPerSample: 16}
		p, _ := syscall.UTF16PtrFromString(path)
		const spfmCreateAlways = 3
		if hr := comCall(out, 17, uintptr(unsafe.Pointer(p)), spfmCreateAlways,
			uintptr(unsafe.Pointer(spdfidWaveFormatEx)), uintptr(unsafe.Pointer(&format)), 0); hr != 0 {
			return hrError("BindToFile", hr)
		}
		if err := voice.setOutputObject(out); err != nil {
			return err
		}
		if err := voice.speakPCM(in, true); err != nil {
			return err
		}
		if !voice.done(10000) {
			t.Error("the audio never finished")
		}
		comCall(out, 18) // Close
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseWAV(data)
	if err != nil {
		t.Fatalf("parse rendered audio: %v", err)
	}
	// Half a second at 22.05 kHz, converted by SAPI to 16 kHz: about 8000 samples.
	if got.Rate != 16000 || len(got.Samples) < 7000 || len(got.Samples) > 9000 {
		t.Fatalf("rendered %d samples at %d Hz, want about 8000 at 16000", len(got.Samples), got.Rate)
	}
}
