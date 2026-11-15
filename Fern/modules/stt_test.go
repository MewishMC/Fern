package modules

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEncodeWAV(t *testing.T) {
	samples := []int16{0, 1000, -1000, 32767, -32768}
	wav := encodeWAV(samples, 16000)
	if len(wav) != 44+len(samples)*2 {
		t.Fatalf("len = %d", len(wav))
	}
	le := binary.LittleEndian
	checks := []struct {
		name      string
		got, want any
	}{
		{"RIFF", string(wav[0:4]), "RIFF"},
		{"riff size", le.Uint32(wav[4:8]), uint32(36 + len(samples)*2)},
		{"WAVE", string(wav[8:16]), "WAVEfmt "},
		{"fmt size", le.Uint32(wav[16:20]), uint32(16)},
		{"pcm", le.Uint16(wav[20:22]), uint16(1)},
		{"channels", le.Uint16(wav[22:24]), uint16(1)},
		{"rate", le.Uint32(wav[24:28]), uint32(16000)},
		{"byte rate", le.Uint32(wav[28:32]), uint32(32000)},
		{"block align", le.Uint16(wav[32:34]), uint16(2)},
		{"bits", le.Uint16(wav[34:36]), uint16(16)},
		{"data", string(wav[36:40]), "data"},
		{"data size", le.Uint32(wav[40:44]), uint32(len(samples) * 2)},
		{"last sample", int16(le.Uint16(wav[len(wav)-2:])), int16(-32768)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func chunkOf(level int16) []int16 {
	c := make([]int16, sttSampleRate*sttChunkMS/1000)
	for i := range c {
		if i%2 == 0 {
			c[i] = level
		} else {
			c[i] = -level
		}
	}
	return c
}

func TestSpeechGate(t *testing.T) {
	g := newSpeechGate(10, 500) // 10%, 500ms of silence
	quiet, loud := int16(1000), int16(10000)
	chunkLen := len(chunkOf(0))

	// Ten quiet chunks before speaking: only the pre-roll is kept.
	for i := 0; i < 10; i++ {
		if level, isLoud, silent := g.feed(chunkOf(quiet)); isLoud || silent || level > 0.1 {
			t.Fatalf("quiet chunk %d: level %v loud %v silent %v", i, level, isLoud, silent)
		}
	}
	if g.heard() || g.take() != nil {
		t.Fatal("gate opened on noise")
	}
	for i := 0; i < 4; i++ {
		if _, isLoud, _ := g.feed(chunkOf(loud)); !isLoud {
			t.Fatal("speech did not open the gate")
		}
	}
	if !g.heard() {
		t.Fatal("heard() = false after speech")
	}
	// Quiet after speech: the silence timeout trips after 500ms (10 chunks).
	tripped := -1
	for i := 0; i < 12; i++ {
		if _, _, silent := g.feed(chunkOf(quiet)); silent && tripped < 0 {
			tripped = i
		}
	}
	if tripped != 9 {
		t.Errorf("silence tripped at chunk %d, want 9", tripped)
	}
	audio := g.take()
	// pre-roll (6 chunks) + speech (4 chunks) + 300ms tail.
	want := (g.preroll+4)*chunkLen + g.tail
	if len(audio) != want {
		t.Errorf("kept %d samples, want %d", len(audio), want)
	}
}

func TestSpeechGateZeroThresholdKeepsEverything(t *testing.T) {
	g := newSpeechGate(0, 1000)
	g.feed(chunkOf(0))
	if !g.heard() {
		t.Error("a 0% gate should always be open")
	}
}

func TestPeakLevel(t *testing.T) {
	if got := peakLevel([]int16{0, -32768, 5}); got != 1 {
		t.Errorf("peak of full scale = %v", got)
	}
	if got := peakLevel(nil); got != 0 {
		t.Errorf("peak of nothing = %v", got)
	}
}

func TestTranscriptionRequestAndResponse(t *testing.T) {
	wav := encodeWAV([]int16{1, 2, 3}, 16000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+fakeKey {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "gpt-4o-mini-transcribe" || r.FormValue("language") != "de" || r.FormValue("response_format") != "json" {
			t.Errorf("form = %v", r.MultipartForm.Value)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, wav) || !strings.HasSuffix(header.Filename, ".wav") {
			t.Errorf("file %q of %d bytes", header.Filename, len(data))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":  " Hallo zusammen ",
			"usage": map[string]any{"type": "tokens", "input_tokens": 12, "output_tokens": 3, "total_tokens": 15},
		})
	}))
	defer server.Close()

	r := transcribeRequest{BaseURL: server.URL + "/v1", Key: fakeKey, Model: "gpt-4o-mini-transcribe", Language: "de", WAV: wav}
	text, usage, err := transcribe(context.Background(), server.Client(), r)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hallo zusammen" || usage == nil || usage.Input != 12 || usage.Output != 3 {
		t.Errorf("text %q usage %+v", text, usage)
	}
}

func TestTranscriptionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Audio file is too short"}}`)
	}))
	defer server.Close()
	r := transcribeRequest{BaseURL: server.URL, Model: "whisper-1", WAV: encodeWAV(nil, 16000)}
	if _, _, err := transcribe(context.Background(), server.Client(), r); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("err = %v", err)
	}
	// whisper-1 without usage in the reply.
	if text, usage, err := parseTranscription(200, []byte(`{"text":"hi"}`)); err != nil || text != "hi" || usage != nil {
		t.Errorf("text %q usage %v err %v", text, usage, err)
	}
}

func TestListMicsDoesNotFail(t *testing.T) {
	for _, m := range ListMics() {
		if m.Name == "" {
			t.Errorf("microphone %d has no name", m.ID)
		}
		t.Logf("mic %d: %s", m.ID, m.Name)
	}
	if got := micIndex(""); got != waveMapper {
		t.Errorf("micIndex(\"\") = %d", got)
	}
	if got := micIndex("no such microphone"); got != waveMapper {
		t.Errorf("unknown mic = %d", got)
	}
}
