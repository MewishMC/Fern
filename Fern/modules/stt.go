package modules

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Speech to text: record from the microphone behind a noise gate, then send
// the clip to an OpenAI-compatible /audio/transcriptions endpoint.

// speechGate keeps only the part of a recording that contains speech. Audio
// before the first loud chunk is dropped except for a short pre-roll (so the
// first syllable is not clipped), and quiet after the last loud chunk is
// trimmed to a short tail.
type speechGate struct {
	threshold float64 // peak level that counts as speech, 0..1
	silence   time.Duration
	chunk     time.Duration
	preroll   int // chunks kept from before speech started
	tail      int // samples kept after the last loud chunk

	pre      [][]int16
	audio    []int16
	started  bool
	quiet    time.Duration
	loudEnd  int // len(audio) at the end of the last loud chunk
	recorded time.Duration
}

func newSpeechGate(gatePercent, silenceMS int) *speechGate {
	return &speechGate{
		threshold: float64(gatePercent) / 100,
		silence:   time.Duration(silenceMS) * time.Millisecond,
		chunk:     sttChunkMS * time.Millisecond,
		preroll:   300 / sttChunkMS,
		tail:      sttSampleRate * 300 / 1000,
	}
}

// peakLevel is the loudest sample of a chunk, 0..1.
func peakLevel(samples []int16) float64 {
	peak := 0
	for _, s := range samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return math.Min(1, float64(peak)/32767)
}

// feed adds one chunk and returns its level, whether it opened the gate, and
// whether speech has been followed by more quiet than the silence timeout.
func (g *speechGate) feed(samples []int16) (level float64, loud, silent bool) {
	level = peakLevel(samples)
	loud = level >= g.threshold
	g.recorded += g.chunk
	chunk := append([]int16(nil), samples...)
	if !g.started {
		if !loud {
			g.pre = append(g.pre, chunk)
			if len(g.pre) > g.preroll {
				g.pre = g.pre[1:]
			}
			return level, false, false
		}
		g.started = true
		for _, p := range g.pre {
			g.audio = append(g.audio, p...)
		}
		g.pre = nil
	}
	g.audio = append(g.audio, chunk...)
	if loud {
		g.quiet = 0
		g.loudEnd = len(g.audio)
	} else {
		g.quiet += g.chunk
	}
	return level, loud, g.silence > 0 && g.quiet >= g.silence
}

// heard reports whether anything got past the gate.
func (g *speechGate) heard() bool { return g.started }

// take returns the speech with the trailing quiet trimmed.
func (g *speechGate) take() []int16 {
	if !g.started {
		return nil
	}
	end := g.loudEnd + g.tail
	if end > len(g.audio) {
		end = len(g.audio)
	}
	return g.audio[:end]
}

// encodeWAV wraps 16-bit mono PCM in a WAV header.
func encodeWAV(samples []int16, rate int) []byte {
	var buf bytes.Buffer
	dataLen := uint32(len(samples) * 2)
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(36 + dataLen)
	buf.WriteString("WAVEfmt ")
	w(uint32(16))       // fmt chunk size
	w(uint16(1))        // PCM
	w(uint16(1))        // mono
	w(uint32(rate))     // sample rate
	w(uint32(rate * 2)) // byte rate
	w(uint16(2))        // block align
	w(uint16(16))       // bits per sample
	buf.WriteString("data")
	w(dataLen)
	w(samples)
	return buf.Bytes()
}

type transcribeRequest struct {
	BaseURL  string
	Key      string
	Model    string
	Language string
	WAV      []byte
}

func newTranscriptionRequest(ctx context.Context, r transcribeRequest) (*http.Request, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "speech.wav")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(r.WAV); err != nil {
		return nil, err
	}
	_ = form.WriteField("model", r.Model)
	_ = form.WriteField("response_format", "json")
	if r.Language != "" {
		_ = form.WriteField("language", r.Language)
	}
	if err := form.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIEndpoint(r.BaseURL, "/audio/transcriptions"), &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	if r.Key != "" {
		req.Header.Set("Authorization", "Bearer "+r.Key)
	}
	return req, nil
}

func parseTranscription(status int, data []byte) (string, *AIUsage, error) {
	var resp struct {
		Text  string `json:"text"`
		Usage *struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
		Error *apiError `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		if status >= 300 {
			return "", nil, httpError(status, "")
		}
		return "", nil, errors.New("the server did not answer with JSON; check the base URL")
	}
	if status >= 300 || resp.Error != nil {
		msg := ""
		if resp.Error != nil {
			msg = resp.Error.Message
		}
		return "", nil, httpError(status, msg)
	}
	var usage *AIUsage
	if resp.Usage != nil {
		usage = &AIUsage{Input: resp.Usage.Input, Output: resp.Usage.Output}
	}
	return strings.TrimSpace(resp.Text), usage, nil
}

func transcribe(ctx context.Context, client *http.Client, r transcribeRequest) (string, *AIUsage, error) {
	req, err := newTranscriptionRequest(ctx, r)
	if err != nil {
		return "", nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, requestError(ctx, err, r.Key)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", nil, requestError(ctx, err, r.Key)
	}
	text, usage, err := parseTranscription(resp.StatusCode, data)
	if err != nil {
		return "", nil, errors.New(scrub(err.Error(), r.Key))
	}
	return text, usage, nil
}

// STTStatus is what the panel shows about speech to text.
type STTStatus struct {
	Recording  bool        `json:"recording"`
	Hold       bool        `json:"hold"`
	Level      float64     `json:"level"` // last chunk's peak, 0..1
	Heard      bool        `json:"heard"` // speech got past the gate
	MS         int         `json:"ms"`
	Busy       bool        `json:"busy"` // transcribing
	Seq        int         `json:"seq"`  // increases with every finished clip
	Text       string      `json:"text"`
	Original   string      `json:"original"` // before translation
	Translated bool        `json:"translated"`
	Error      string      `json:"error"`
	Mics       []MicDevice `json:"mics"`

	PTT     bool   `json:"ptt"`     // the last clip was recorded holding the mic button
	Command string `json:"command"` // the voice command that kept the last clip out of the chatbox

	Test   STTTestStatus   `json:"test"`
	Server STTServerStatus `json:"server"`
}

var (
	sttMu     sync.Mutex
	sttState  STTStatus
	sttCancel context.CancelFunc // aborts recording and transcription
	sttStop   chan struct{}      // ends recording and transcribes
	sttMics   []MicDevice
	sttMicsAt time.Time
)

// CurrentSTT returns the speech to text state.
func CurrentSTT() STTStatus {
	sttMu.Lock()
	defer sttMu.Unlock()
	if time.Since(sttMicsAt) > 10*time.Second {
		sttMics, sttMicsAt = ListMics(), time.Now()
	}
	out := sttState
	out.Mics = append([]MicDevice{}, sttMics...)
	out.Test = sttTestState
	out.Server = CurrentSTTServer()
	return out
}

// StartListening starts a recording. With hold set (push to talk), the
// silence timeout does not end it; ReleaseListening turns hold off.
func StartListening(hold bool) {
	sttMu.Lock()
	if sttState.Recording {
		sttMu.Unlock()
		return
	}
	if sttCancel != nil {
		sttCancel() // a transcription still running is replaced
	}
	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan struct{})
	sttCancel, sttStop = cancel, stop
	sttState.Recording, sttState.Hold, sttState.Busy = true, hold, false
	sttState.Level, sttState.Heard, sttState.MS, sttState.Error = 0, false, 0, ""
	sttMu.Unlock()

	// A local whisper server loads while you talk.
	go func() { _ = StartSTTServer() }()
	go runListening(ctx, cancel, stop)
}

// ReleaseListening switches a push-to-talk recording to stop on silence.
func ReleaseListening() {
	sttMu.Lock()
	sttState.Hold = false
	sttMu.Unlock()
}

// StopListening ends the recording and transcribes what was heard.
func StopListening() {
	sttMu.Lock()
	defer sttMu.Unlock()
	if sttState.Recording && sttStop != nil {
		close(sttStop)
		sttStop = nil
	}
}

// CancelListening drops the recording or transcription in progress.
func CancelListening() {
	sttMu.Lock()
	defer sttMu.Unlock()
	if sttCancel != nil {
		sttCancel()
		sttCancel = nil
	}
	sttStop = nil
	sttState.Recording, sttState.Busy, sttState.Level = false, false, 0
}

func sttFail(ctx context.Context, msg string) {
	sttMu.Lock()
	defer sttMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	sttState.Recording, sttState.Busy, sttState.Level = false, false, 0
	sttState.Error = msg
	sttState.Seq++
	sttState.Text, sttState.Command = "", ""
}

func runListening(ctx context.Context, cancel context.CancelFunc, stop chan struct{}) {
	defer cancel()
	cfg := GetConfig()
	o := cfg.STTOptions
	gate := newSpeechGate(o.Gate, o.SilenceMS)
	limit := time.Duration(o.MaxSec) * time.Second
	ptt := false

	err := recordMic(ctx, micIndex(o.Device), func(samples []int16) bool {
		level, _, silent := gate.feed(samples)
		sttMu.Lock()
		sttState.Level, sttState.Heard, sttState.MS = level, gate.heard(), int(gate.recorded.Milliseconds())
		hold := sttState.Hold
		sttMu.Unlock()
		ptt = hold
		select {
		case <-stop:
			return false
		default:
		}
		if gate.recorded >= limit {
			return false
		}
		return !(silent && !hold)
	})
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		sttFail(ctx, err.Error())
		return
	}
	audio := gate.take()
	if len(audio) < sttSampleRate/5 {
		sttFail(ctx, "didn't hear anything; speak up or lower the noise gate")
		return
	}

	sttMu.Lock()
	sttState.Recording, sttState.Busy, sttState.Level = false, true, 0
	sttMu.Unlock()

	r, err := sttRequestFor(cfg)
	if err != nil {
		sttFail(ctx, err.Error())
		return
	}
	r.WAV = encodeWAV(audio, sttSampleRate)
	if err := waitSTTServer(ctx, cfg); err != nil {
		sttFail(ctx, err.Error())
		return
	}
	timeout := time.Duration(cfg.AssistOptions.TimeoutSec)*time.Second + 15*time.Second
	tctx, tcancel := context.WithTimeout(ctx, timeout)
	defer tcancel()
	text, usage, err := transcribe(tctx, aiClient, r)
	if usage != nil {
		addSessionUsage(*usage)
	}
	if err != nil {
		sttFail(ctx, "transcription: "+err.Error())
		return
	}
	if text == "" {
		sttFail(ctx, "nothing was recognised")
		return
	}
	if ctx.Err() != nil {
		return
	}
	if consumed, name := handleVoiceCommand(text, ptt); consumed {
		sttMu.Lock()
		defer sttMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		sttState.Busy = false
		sttState.Seq++
		sttState.Text, sttState.Original, sttState.Translated = "", text, false
		sttState.PTT, sttState.Command = ptt, name
		return
	}
	original, translated := text, false
	if o.Translate {
		a := cfg.AssistOptions
		a.Language = o.TranslateTo
		if out, _, err := runAssistWith(ctx, a, OpTranslate, text); err == nil {
			text, translated = out, true
		} else if ctx.Err() == nil {
			sttMu.Lock()
			sttState.Error = "translation failed, kept the original: " + err.Error()
			sttMu.Unlock()
		}
	}

	sttMu.Lock()
	defer sttMu.Unlock()
	if ctx.Err() != nil {
		return
	}
	sttState.Busy = false
	sttState.Seq++
	sttState.Text, sttState.Original, sttState.Translated = FilterText(SurfaceDictated, text), original, translated
	sttState.PTT, sttState.Command = ptt, ""
}
