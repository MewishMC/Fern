package modules

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// The speech worker owns one SAPI voice on its own OS thread. Requests reach
// it through ttsJobs; while something is being read it polls the voice so the
// VRChat microphone can be restored as soon as the queue runs dry.

type ttsJob struct {
	text  string
	stop  bool
	force bool // a preview from the panel: speak even when speaking sent messages is off
}

// TTSStatus is what the panel shows about text to speech.
type TTSStatus struct {
	Speaking bool          `json:"speaking"`
	Error    string        `json:"error"`
	Voices   []SpeechToken `json:"voices"`
	Outputs  []SpeechToken `json:"outputs"`
	Muted    *bool         `json:"muted"` // VRChat's MuteSelf, nil until VRChat reports it
}

var (
	ttsOnce  sync.Once
	ttsJobs  = make(chan ttsJob, 8)
	ttsMu    sync.Mutex
	ttsState TTSStatus

	muteMu    sync.Mutex
	muteKnown bool
	muteSelf  bool
)

func startTTS() {
	ttsOnce.Do(func() {
		go ttsWorker()
		go RefreshSpeechDevices()
	})
}

// RefreshSpeechDevices re-reads the voice and output lists.
func RefreshSpeechDevices() {
	voices, verr := ListVoices()
	outputs, _ := ListAudioOutputs()
	ttsMu.Lock()
	ttsState.Voices, ttsState.Outputs = voices, outputs
	if verr != nil {
		ttsState.Error = verr.Error()
	}
	ttsMu.Unlock()
}

// CurrentTTS returns the text to speech state.
func CurrentTTS() TTSStatus {
	startTTS()
	ttsMu.Lock()
	out := ttsState
	out.Voices = append([]SpeechToken{}, ttsState.Voices...)
	out.Outputs = append([]SpeechToken{}, ttsState.Outputs...)
	ttsMu.Unlock()
	muteMu.Lock()
	if muteKnown {
		m := muteSelf
		out.Muted = &m
	}
	muteMu.Unlock()
	return out
}

// SpeakSent reads out a message sent from the panel, when that is turned on.
func SpeakSent(text string) {
	if !GetConfig().TTSOptions.SpeakSent {
		return
	}
	queueTTS(ttsJob{text: text})
}

// SpeakPreview reads text regardless of the speak-sent switch.
func SpeakPreview(text string) { queueTTS(ttsJob{text: text, force: true}) }

// StopSpeaking cuts off whatever is being read.
func StopSpeaking() { queueTTS(ttsJob{stop: true}) }

func queueTTS(job ttsJob) {
	startTTS()
	job.text = strings.TrimSpace(job.text)
	if !job.stop && job.text == "" {
		return
	}
	select {
	case ttsJobs <- job:
	default:
		setTTSError("speech queue is full")
	}
}

func setTTSError(msg string) {
	ttsMu.Lock()
	ttsState.Error = msg
	ttsMu.Unlock()
}

func setSpeaking(on bool) {
	ttsMu.Lock()
	ttsState.Speaking = on
	ttsMu.Unlock()
}

// voiceSettings is what was last applied to the SAPI voice.
type voiceSettings struct {
	voice, output string
	rate, volume  int
	applied       bool
}

func ttsWorker() {
	_ = withCOM(func() error {
		voice, err := newSAPIVoice()
		if err != nil {
			setTTSError(err.Error())
			return err
		}
		defer voice.release()

		var current voiceSettings
		speaking := false
		mic := &micHold{}
		for {
			var job ttsJob
			if speaking {
				select {
				case job = <-ttsJobs:
				case <-time.After(80 * time.Millisecond):
					if voice.done(0) {
						speaking = false
						setSpeaking(false)
						mic.release()
					}
					continue
				}
			} else {
				job = <-ttsJobs
			}

			opts := GetConfig().TTSOptions
			if job.stop {
				voice.stop()
				continue
			}
			if err := applyVoiceSettings(voice, &current, opts); err != nil {
				setTTSError(err.Error())
			}
			if err := voice.speak(job.text, opts.CutOff, true); err != nil {
				setTTSError(err.Error())
				continue
			}
			setTTSError("")
			if !speaking {
				speaking = true
				setSpeaking(true)
				if opts.Unmute {
					mic.open(opts.VoiceMode)
				}
			}
		}
	})
}

func applyVoiceSettings(v *sapiVoice, cur *voiceSettings, o TTSOptions) error {
	var firstErr error
	if !cur.applied || cur.voice != o.Voice {
		if err := v.setVoice(o.Voice); err != nil {
			firstErr = fmt.Errorf("voice: %v", err)
			_ = v.setVoice("")
		}
	}
	if !cur.applied || cur.output != o.Output {
		if err := v.setOutput(o.Output); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("output device: %v; using the default device", err)
			_ = v.setOutput("")
		}
	}
	v.setRate(o.Rate)
	v.setVolume(o.Volume)
	*cur = voiceSettings{voice: o.Voice, output: o.Output, rate: o.Rate, volume: o.Volume, applied: true}
	return firstErr
}

// noteMuteSelf records VRChat's MuteSelf avatar parameter from its OSC output.
func noteMuteSelf(pkt osc.Packet) {
	msg, ok := pkt.(*osc.Message)
	if !ok || msg.Address != "/avatar/parameters/MuteSelf" || len(msg.Arguments) == 0 {
		return
	}
	if muted, ok := msg.Arguments[0].(bool); ok {
		muteMu.Lock()
		muteKnown, muteSelf = true, muted
		muteMu.Unlock()
	}
}

func knownMute() (muted, known bool) {
	muteMu.Lock()
	defer muteMu.Unlock()
	return muteSelf, muteKnown
}

// micHold opens the VRChat microphone for the length of the speech and puts
// it back afterwards. VRChat's /input/Voice works in two ways that match its
// own mic setting: in toggle mode a press (0 then 1) flips mute, in push to
// talk mode the mic is open while the value is 1.
type micHold struct {
	mode    string
	toggled bool
	held    bool
}

func (m *micHold) open(mode string) {
	m.mode = mode
	if mode == "ptt" {
		sendVoiceInput(1)
		m.held = true
		return
	}
	// Only toggle when VRChat said the mic is muted; toggling blind could mute
	// someone who was already talking.
	if muted, known := knownMute(); known && muted {
		pressVoice()
		m.toggled = true
	}
}

func (m *micHold) release() {
	if m.held {
		sendVoiceInput(0)
		m.held = false
	}
	if m.toggled {
		if muted, known := knownMute(); !known || !muted {
			pressVoice()
		}
		m.toggled = false
	}
}

func pressVoice() {
	sendVoiceInput(0)
	time.Sleep(60 * time.Millisecond)
	sendVoiceInput(1)
	time.Sleep(60 * time.Millisecond)
	sendVoiceInput(0)
}

func sendVoiceInput(v int32) {
	o := GetConfig().OSCOptions
	client := osc.NewClient(o.Host, o.Port)
	msg := osc.NewMessage("/input/Voice")
	msg.Append(v)
	_ = client.Send(msg)
}
