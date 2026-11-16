package modules

import (
	"context"
	"errors"
	"sync"
)

// The cloud engines make audio over HTTP, which can take a second or two, so
// that happens on its own goroutine: one message at a time, in order. The
// finished audio is handed to the speech worker, which plays it like a
// Windows voice. A new message with "cut off" on, or Stop, cancels the one
// being made.

var (
	synthOnce   sync.Once
	synthMu     sync.Mutex
	synthCancel context.CancelFunc
	synthBusy   bool
	synthGen    uint64 // bumped by cancelSynth; jobs queued before it are dropped
)

// engineNames label errors in the panel.
var engineNames = map[string]string{
	TTSEngineOpenAI:     "OpenAI-compatible voice",
	TTSEngineElevenLabs: "ElevenLabs",
	TTSEngineAzure:      "Azure Speech",
}

type synthItem struct {
	job ttsJob
	gen uint64
}

var synthQueue = make(chan synthItem, 8)

func queueSynth(job ttsJob, cutOff bool) {
	synthOnce.Do(func() { go synthWorker() })
	if cutOff {
		cancelSynth()
		select {
		case ttsJobs <- ttsJob{stop: true}:
		default:
		}
	}
	synthMu.Lock()
	gen := synthGen
	synthMu.Unlock()
	select {
	case synthQueue <- synthItem{job: job, gen: gen}:
	default:
		setTTSError("speech queue is full")
	}
}

// cancelSynth stops the message being made and drops the queued ones.
func cancelSynth() {
	synthMu.Lock()
	synthGen++
	if synthCancel != nil {
		synthCancel()
	}
	synthMu.Unlock()
}

func synthesizing() bool {
	synthMu.Lock()
	defer synthMu.Unlock()
	return synthBusy
}

func synthWorker() {
	for item := range synthQueue {
		synthMu.Lock()
		if item.gen != synthGen {
			synthMu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		synthCancel, synthBusy = cancel, true
		synthMu.Unlock()

		audio, err := synthOne(ctx, item.job.text)

		synthMu.Lock()
		synthBusy = false
		synthCancel = nil
		stale := item.gen != synthGen
		synthMu.Unlock()
		cancel()
		if stale || errors.Is(err, context.Canceled) {
			continue
		}
		if err != nil {
			setTTSError(err.Error())
			continue
		}
		ttsJobs <- ttsJob{audio: &audio, purge: false}
	}
}

// synthOne makes the audio for text with the current settings.
func synthOne(ctx context.Context, text string) (pcmAudio, error) {
	cfg := GetConfig()
	name := engineNames[cfg.TTSOptions.Engine]
	req, err := ttsRequestFor(cfg, text)
	if err != nil {
		return pcmAudio{}, err
	}
	audio, err := synthesize(ctx, ttsClient, req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return pcmAudio{}, err
		}
		return pcmAudio{}, errors.New(name + ": " + err.Error())
	}
	return audio.scaled(cfg.TTSOptions.Volume), nil
}
