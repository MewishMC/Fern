package modules

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wavBytes builds a WAV file with the given format and raw sample bytes.
func wavBytes(format uint16, channels, rate, bits int, raw []byte, dataSize uint32) []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + len(raw)))
	b.WriteString("WAVE")
	b.WriteString("LIST") // an unrelated chunk before fmt, odd-sized
	w(uint32(3))
	b.Write([]byte{1, 2, 3, 0})
	b.WriteString("fmt ")
	w(uint32(16))
	w(format)
	w(uint16(channels))
	w(uint32(rate))
	w(uint32(rate * channels * bits / 8))
	w(uint16(channels * bits / 8))
	w(uint16(bits))
	b.WriteString("data")
	w(dataSize)
	b.Write(raw)
	return b.Bytes()
}

func TestParseWAV(t *testing.T) {
	pcm16 := []byte{0x00, 0x40, 0x00, 0xC0} // 16384, -16384
	a, err := parseWAV(wavBytes(1, 1, 24000, 16, pcm16, 4))
	if err != nil || a.Rate != 24000 || a.Channels != 1 || len(a.Samples) != 2 || a.Samples[0] != 16384 || a.Samples[1] != -16384 {
		t.Fatalf("16-bit: %+v %v", a, err)
	}
	// Streaming servers write an unknown length.
	for _, size := range []uint32{0, 0xFFFFFFFF, 1000} {
		if a, err := parseWAV(wavBytes(1, 1, 24000, 16, pcm16, size)); err != nil || len(a.Samples) != 2 {
			t.Errorf("data size %#x: %+v %v", size, a, err)
		}
	}
	f := make([]byte, 8)
	binary.LittleEndian.PutUint32(f, math.Float32bits(0.5))
	binary.LittleEndian.PutUint32(f[4:], math.Float32bits(-2))
	if a, err := parseWAV(wavBytes(3, 1, 22050, 32, f, 8)); err != nil || a.Samples[0] != 16383 || a.Samples[1] != -32767 {
		t.Errorf("float: %+v %v", a, err)
	}
	if a, err := parseWAV(wavBytes(1, 1, 16000, 24, []byte{0xFF, 0x00, 0x40}, 3)); err != nil || a.Samples[0] != 0x4000 {
		t.Errorf("24-bit: %+v %v", a, err)
	}
	if a, err := parseWAV(wavBytes(1, 1, 16000, 8, []byte{128, 255}, 2)); err != nil || a.Samples[0] != 0 || a.Samples[1] != 127<<8 {
		t.Errorf("8-bit: %+v %v", a, err)
	}
	// Four channels are mixed down to one.
	quad := []byte{0x00, 0x10, 0x00, 0x10, 0x00, 0x10, 0x00, 0x10}
	if a, err := parseWAV(wavBytes(1, 4, 16000, 16, quad, 8)); err != nil || a.Channels != 1 || len(a.Samples) != 1 || a.Samples[0] != 0x1000 {
		t.Errorf("quad: %+v %v", a, err)
	}
	for name, data := range map[string][]byte{
		"mp3":      []byte("ID3\x04\x00\x00\x00\x00\x00\x00"),
		"no data":  wavBytes(1, 1, 16000, 16, nil, 0)[:len(wavBytes(1, 1, 16000, 16, nil, 0))-8],
		"a-law":    wavBytes(6, 1, 8000, 8, []byte{1}, 1),
		"bad rate": wavBytes(1, 1, 10, 16, pcm16, 4),
	} {
		if _, err := parseWAV(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPCMHelpers(t *testing.T) {
	a := pcmFromRaw([]byte{0x00, 0x40, 0x00, 0xC0, 0x01}, 22050)
	if a.Rate != 22050 || len(a.Samples) != 2 || a.Samples[1] != -16384 {
		t.Fatalf("raw: %+v", a)
	}
	if s := a.scaled(50).Samples; s[0] != 8192 || s[1] != -8192 || a.Samples[0] != 16384 {
		t.Errorf("scaled: %v (original %v)", s, a.Samples)
	}
	if ttsSpeed(0) != 1 || ttsSpeed(10) != 2 || ttsSpeed(-10) != 0.5 {
		t.Errorf("speeds: %v %v %v", ttsSpeed(0), ttsSpeed(10), ttsSpeed(-10))
	}
}

func TestTTSEngineSanitize(t *testing.T) {
	var c Config
	_ = json.Unmarshal([]byte(`{"TTSOptions":{"engine":"bogus","openai_base_url":"ftp://x","azure_region":"East US","eleven_voice":"  abc  "}}`), &c)
	c.sanitize()
	o := c.TTSOptions
	if o.Engine != TTSEngineSAPI || o.OpenAIBaseURL != "" || o.OpenAIModel != defaultTTSOpenAIModel ||
		o.OpenAIVoice != defaultTTSOpenAIVoice || o.ElevenModel != defaultElevenModel || o.AzureVoice != defaultAzureVoice {
		t.Errorf("defaults: %+v", o)
	}
	if o.AzureRegion != "eastus" || o.ElevenVoice != "abc" {
		t.Errorf("cleaned: region %q voice %q", o.AzureRegion, o.ElevenVoice)
	}
	c.TTSOptions.AzureRegion = "east.us/../x"
	c.sanitize()
	if c.TTSOptions.AzureRegion != "" {
		t.Errorf("a region that is not a plain name must be dropped, got %q", c.TTSOptions.AzureRegion)
	}
	if d := DefaultConfig().TTSOptions; d.Engine != TTSEngineSAPI {
		t.Errorf("default engine %q", d.Engine)
	}
	for _, s := range []string{SecretTTSOpenAIKey, SecretElevenLabsKey, SecretAzureSpeechKey} {
		if !panelSecrets[s] {
			t.Errorf("%s cannot be set from the panel", s)
		}
	}
}

func TestSynthesizeOpenAI(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write(wavBytes(1, 1, 24000, 16, []byte{1, 0, 2, 0}, 4))
	}))
	defer srv.Close()
	a, err := synthesize(context.Background(), srv.Client(), ttsSynthRequest{
		Engine: TTSEngineOpenAI, BaseURL: srv.URL + "/v1", Key: "sk-test", Model: "tts-1", Voice: "nova",
		Instructions: "cheerful", Rate: 5, Text: "hello",
	})
	if err != nil || len(a.Samples) != 2 || a.Rate != 24000 {
		t.Fatalf("audio %+v err %v", a, err)
	}
	if path != "/v1/audio/speech" || auth != "Bearer sk-test" {
		t.Errorf("path %q auth %q", path, auth)
	}
	if got["model"] != "tts-1" || got["voice"] != "nova" || got["input"] != "hello" || got["response_format"] != "wav" ||
		got["speed"] != 1.5 || got["instructions"] != "cheerful" {
		t.Errorf("body %v", got)
	}
}

func TestSynthesizeOpenAIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "mp3") {
			_, _ = w.Write([]byte("ID3 not wav"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key sk-secret-123"}}`))
	}))
	defer srv.Close()
	_, err := synthesize(context.Background(), srv.Client(), ttsSynthRequest{Engine: TTSEngineOpenAI, BaseURL: srv.URL, Key: "sk-secret-123", Text: "x"})
	if err == nil || strings.Contains(err.Error(), "sk-secret-123") || !strings.Contains(err.Error(), "Incorrect API key") {
		t.Errorf("401: %v", err)
	}
	_, err = synthesize(context.Background(), srv.Client(), ttsSynthRequest{Engine: TTSEngineOpenAI, BaseURL: srv.URL + "/mp3", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "response_format wav") {
		t.Errorf("not wav: %v", err)
	}
}

func TestSynthesizeElevenLabs(t *testing.T) {
	var got map[string]any
	var key, path, format string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, format = r.URL.Path, r.Header.Get("xi-api-key"), r.URL.Query().Get("output_format")
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte{0x00, 0x40, 0x00, 0xC0})
	}))
	defer srv.Close()
	old := elevenLabsBase
	elevenLabsBase = srv.URL
	t.Cleanup(func() { elevenLabsBase = old })
	a, err := synthesize(context.Background(), srv.Client(), ttsSynthRequest{
		Engine: TTSEngineElevenLabs, Key: "xi", Model: "eleven_flash_v2_5", Voice: "abc/def", Rate: 10, Text: "hi",
	})
	if err != nil || a.Rate != elevenPCMRate || len(a.Samples) != 2 {
		t.Fatalf("audio %+v err %v", a, err)
	}
	if path != "/v1/text-to-speech/abc/def" || key != "xi" || format != "pcm_22050" {
		t.Errorf("path %q key %q format %q", path, key, format)
	}
	settings, _ := got["voice_settings"].(map[string]any)
	if got["text"] != "hi" || got["model_id"] != "eleven_flash_v2_5" || settings["speed"] != 1.2 {
		t.Errorf("body %v", got)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":{"status":"voice_not_found","message":"A voice with that ID does not exist"}}`))
	}))
	defer srv2.Close()
	elevenLabsBase = srv2.URL
	if _, err := synthesize(context.Background(), srv2.Client(), ttsSynthRequest{Engine: TTSEngineElevenLabs, Key: "xi", Voice: "x", Text: "hi"}); err == nil ||
		!strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %v", err)
	}
}

func TestSynthesizeAzure(t *testing.T) {
	var body, key, format, ctype, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body, key, format, ctype, path = string(data), r.Header.Get("Ocp-Apim-Subscription-Key"),
			r.Header.Get("X-Microsoft-OutputFormat"), r.Header.Get("Content-Type"), r.URL.Path
		_, _ = w.Write(wavBytes(1, 1, 24000, 16, []byte{1, 0}, 2))
	}))
	defer srv.Close()
	old := azureSpeechURL
	var region string
	azureSpeechURL = func(r string) string { region = r; return srv.URL }
	t.Cleanup(func() { azureSpeechURL = old })
	_, err := synthesize(context.Background(), srv.Client(), ttsSynthRequest{
		Engine: TTSEngineAzure, Key: "az", Region: "westeurope", Voice: "de-DE-KatjaNeural", Rate: -4, Text: `Tom & "Jerry" <3`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if region != "westeurope" || path != "/cognitiveservices/v1" || key != "az" || format != "riff-24khz-16bit-mono-pcm" || ctype != "application/ssml+xml" {
		t.Errorf("region %q path %q key %q format %q type %q", region, path, key, format, ctype)
	}
	for _, want := range []string{`xml:lang="de-DE"`, `<voice name="de-DE-KatjaNeural">`, `<prosody rate="-20%">`, `Tom &amp; &quot;Jerry&quot; &lt;3`} {
		if !strings.Contains(body, want) {
			t.Errorf("SSML %s lacks %s", body, want)
		}
	}
}

func TestListEngineVoices(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/audio/voices", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"voices":["af_bella",{"id":"am_adam","name":"Adam"}]}`))
	})
	mux.HandleFunc("/v1/voices", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") != "xi" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"voices":[{"voice_id":"v1","name":"Rachel","category":"premade"},{"voice_id":"v2","name":"Me","category":"cloned"}]}`))
	})
	mux.HandleFunc("/cognitiveservices/voices/list", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"ShortName":"en-US-AvaNeural","DisplayName":"Ava","LocaleName":"English (United States)","Gender":"Female"}]`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	oldEleven, oldAzure := elevenLabsBase, azureSpeechURL
	elevenLabsBase, azureSpeechURL = srv.URL, func(string) string { return srv.URL }
	t.Cleanup(func() { elevenLabsBase, azureSpeechURL = oldEleven, oldAzure })

	c := DefaultConfig()
	c.TTSOptions.Engine, c.TTSOptions.OpenAIBaseURL = TTSEngineOpenAI, srv.URL+"/v1"
	list, err := listEngineVoices(context.Background(), srv.Client(), c, "")
	if err != nil || len(list) != 2 || list[0].ID != "af_bella" || list[1].Name != "Adam" {
		t.Errorf("openai-compatible: %+v %v", list, err)
	}
	c.TTSOptions.OpenAIBaseURL = srv.URL + "/none"
	if list, err := listEngineVoices(context.Background(), srv.Client(), c, ""); err != nil || len(list) != len(openAITTSVoices) {
		t.Errorf("a server without a voice list falls back to OpenAI's voices: %d %v", len(list), err)
	}
	c.TTSOptions.Engine = TTSEngineElevenLabs
	list, err = listEngineVoices(context.Background(), srv.Client(), c, "xi")
	if err != nil || len(list) != 2 || list[0].Name != "Rachel" || list[1].Name != "Me (cloned)" {
		t.Errorf("elevenlabs: %+v %v", list, err)
	}
	if _, err := listEngineVoices(context.Background(), srv.Client(), c, "wrong"); err == nil {
		t.Error("elevenlabs with a bad key should fail")
	}
	c.TTSOptions.Engine, c.TTSOptions.AzureRegion = TTSEngineAzure, "eastus"
	list, err = listEngineVoices(context.Background(), srv.Client(), c, "az")
	if err != nil || len(list) != 1 || list[0].ID != "en-US-AvaNeural" || !strings.Contains(list[0].Name, "Ava") {
		t.Errorf("azure: %+v %v", list, err)
	}
}

func TestTTSRequestForKeys(t *testing.T) {
	useTempSecrets(t)
	c := DefaultConfig()
	c.TTSOptions.Engine = TTSEngineElevenLabs
	if _, err := ttsRequestFor(c, "hi"); err == nil {
		t.Error("ElevenLabs without a key should fail")
	}
	if err := SetSecret(SecretElevenLabsKey, "xi"); err != nil {
		t.Fatal(err)
	}
	if _, err := ttsRequestFor(c, "hi"); err == nil || !strings.Contains(err.Error(), "voice") {
		t.Errorf("without a voice: %v", err)
	}
	c.TTSOptions.ElevenVoice = "v1"
	if r, err := ttsRequestFor(c, "hi"); err != nil || r.Key != "xi" || r.Voice != "v1" {
		t.Errorf("request %+v %v", r, err)
	}
	c.TTSOptions.Engine = TTSEngineOpenAI
	if _, err := ttsRequestFor(c, "hi"); err == nil {
		t.Error("api.openai.com without any key should fail")
	}
	c.TTSOptions.OpenAIBaseURL = "http://127.0.0.1:8880/v1"
	if r, err := ttsRequestFor(c, "hi"); err != nil || r.BaseURL != "http://127.0.0.1:8880/v1" {
		t.Errorf("a local server needs no key: %+v %v", r, err)
	}
	c.TTSOptions.Engine = TTSEngineAzure
	_ = SetSecret(SecretAzureSpeechKey, "az")
	if _, err := ttsRequestFor(c, "hi"); err == nil || !strings.Contains(err.Error(), "region") {
		t.Errorf("azure without a region: %v", err)
	}
}
