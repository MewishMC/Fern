package modules

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Text to speech engines besides the Windows voices: any OpenAI-compatible
// /audio/speech server, ElevenLabs and Azure Speech. Each is asked for WAV
// or raw PCM, which is then played through the same SAPI voice as the
// Windows voices, so the output device, volume and the VRChat mic handling
// are the same whichever engine speaks.

// TTS engines.
const (
	TTSEngineSAPI       = "sapi"
	TTSEngineOpenAI     = "openai"
	TTSEngineElevenLabs = "elevenlabs"
	TTSEngineAzure      = "azure"
)

// Secrets of the TTS engines, kept in secrets.dat.
const (
	SecretTTSOpenAIKey   = "tts_openai_key"   // empty uses the Assist OpenAI-compatible key
	SecretElevenLabsKey  = "elevenlabs_key"   // xi-api-key
	SecretAzureSpeechKey = "azure_speech_key" // Ocp-Apim-Subscription-Key
)

func init() {
	panelSecrets[SecretTTSOpenAIKey] = true
	panelSecrets[SecretElevenLabsKey] = true
	panelSecrets[SecretAzureSpeechKey] = true
}

// Defaults of the engine settings.
const (
	defaultTTSOpenAIModel = "gpt-4o-mini-tts"
	defaultTTSOpenAIVoice = "alloy"
	defaultElevenModel    = "eleven_flash_v2_5"
	defaultAzureVoice     = "en-US-AvaMultilingualNeural"
	elevenPCMRate         = 22050
	maxTTSAudioBytes      = 32 << 20
	maxTTSChars           = 1000
)

// Endpoints, swapped by tests.
var (
	elevenLabsBase = "https://api.elevenlabs.io"
	azureSpeechURL = func(region string) string {
		return "https://" + region + ".tts.speech.microsoft.com"
	}
)

// openAITTSVoices are OpenAI's built-in voices, offered when a server does
// not list its own.
var openAITTSVoices = []string{"alloy", "ash", "ballad", "coral", "echo", "fable", "nova", "onyx", "sage", "shimmer", "verse"}

var azureRegionPattern = regexp.MustCompile(`^[a-z0-9]{2,40}$`)

func (t *TTSOptions) sanitizeEngine() {
	switch t.Engine {
	case TTSEngineOpenAI, TTSEngineElevenLabs, TTSEngineAzure:
	default:
		t.Engine = TTSEngineSAPI
	}
	t.OpenAIBaseURL = cleanURL(t.OpenAIBaseURL)
	if t.OpenAIModel = oneLine(t.OpenAIModel, 100); t.OpenAIModel == "" {
		t.OpenAIModel = defaultTTSOpenAIModel
	}
	if t.OpenAIVoice = oneLine(t.OpenAIVoice, 100); t.OpenAIVoice == "" {
		t.OpenAIVoice = defaultTTSOpenAIVoice
	}
	t.OpenAIInstructions = oneLine(t.OpenAIInstructions, 300)
	t.ElevenVoice = oneLine(t.ElevenVoice, 64)
	if t.ElevenModel = oneLine(t.ElevenModel, 64); t.ElevenModel == "" {
		t.ElevenModel = defaultElevenModel
	}
	t.AzureRegion = strings.ToLower(strings.ReplaceAll(oneLine(t.AzureRegion, 40), " ", ""))
	if !azureRegionPattern.MatchString(t.AzureRegion) {
		t.AzureRegion = ""
	}
	if t.AzureVoice = oneLine(t.AzureVoice, 100); t.AzureVoice == "" {
		t.AzureVoice = defaultAzureVoice
	}
}

// pcmAudio is decoded 16-bit audio.
type pcmAudio struct {
	Rate     int
	Channels int
	Samples  []int16 // interleaved
}

// parseWAV decodes a RIFF WAVE file into 16-bit samples. It accepts 8, 16,
// 24 and 32-bit integer PCM and 32-bit float, and the unknown lengths that
// streaming servers write (0 or 0xFFFFFFFF), where the data runs to the end.
func parseWAV(data []byte) (pcmAudio, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return pcmAudio{}, errors.New("the server did not send WAV audio")
	}
	var (
		format, channels, bits uint16
		rate                   uint32
		haveFmt                bool
	)
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(data) {
				return pcmAudio{}, errors.New("the WAV audio has a broken header")
			}
			format = binary.LittleEndian.Uint16(data[body:])
			channels = binary.LittleEndian.Uint16(data[body+2:])
			rate = binary.LittleEndian.Uint32(data[body+4:])
			bits = binary.LittleEndian.Uint16(data[body+14:])
			if format == 0xFFFE && size >= 26 && body+26 <= len(data) {
				format = binary.LittleEndian.Uint16(data[body+24:]) // sub-format GUID's first field
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return pcmAudio{}, errors.New("the WAV audio has no format")
			}
			end := body + size
			if size == 0 || size == 0xFFFFFFFF || end > len(data) || end < body {
				end = len(data)
			}
			return decodePCM(data[body:end], format, int(channels), int(rate), int(bits))
		}
		if size < 0 || body+size > len(data) {
			break
		}
		pos = body + size + size%2
	}
	return pcmAudio{}, errors.New("the WAV audio has no data")
}

func decodePCM(raw []byte, format uint16, channels, rate, bits int) (pcmAudio, error) {
	if channels < 1 || channels > 8 || rate < 4000 || rate > 192000 {
		return pcmAudio{}, fmt.Errorf("unsupported WAV audio (%d channels, %d Hz)", channels, rate)
	}
	width := bits / 8
	if width == 0 || (format != 1 && format != 3) || (format == 3 && bits != 32) || bits%8 != 0 || width > 4 {
		return pcmAudio{}, fmt.Errorf("unsupported WAV encoding (format %d, %d bits)", format, bits)
	}
	n := len(raw) / width
	out := make([]int16, 0, n)
	for i := 0; i+width <= len(raw); i += width {
		var s int16
		switch {
		case format == 3:
			f := math.Float32frombits(binary.LittleEndian.Uint32(raw[i:]))
			if f != f { // NaN
				f = 0
			}
			s = int16(math.Max(-1, math.Min(1, float64(f))) * 32767)
		case width == 1:
			s = int16((int(raw[i]) - 128) << 8)
		case width == 2:
			s = int16(binary.LittleEndian.Uint16(raw[i:]))
		case width == 3:
			s = int16(uint16(raw[i+1]) | uint16(raw[i+2])<<8)
		case width == 4:
			s = int16(binary.LittleEndian.Uint32(raw[i:]) >> 16)
		}
		out = append(out, s)
	}
	return pcmAudio{Rate: rate, Channels: channels, Samples: out}.mono(), nil
}

// mono mixes down anything with more than two channels, which SAPI's
// WAVEFORMATEX path does not take reliably.
func (a pcmAudio) mono() pcmAudio {
	if a.Channels <= 2 {
		return a
	}
	frames := len(a.Samples) / a.Channels
	out := make([]int16, frames)
	for f := 0; f < frames; f++ {
		sum := 0
		for c := 0; c < a.Channels; c++ {
			sum += int(a.Samples[f*a.Channels+c])
		}
		out[f] = int16(sum / a.Channels)
	}
	return pcmAudio{Rate: a.Rate, Channels: 1, Samples: out}
}

// pcmFromRaw reads headerless 16-bit little-endian mono PCM.
func pcmFromRaw(raw []byte, rate int) pcmAudio {
	out := make([]int16, len(raw)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return pcmAudio{Rate: rate, Channels: 1, Samples: out}
}

// scaled applies a volume of 0..100.
func (a pcmAudio) scaled(volume int) pcmAudio {
	if volume >= 100 {
		return a
	}
	out := make([]int16, len(a.Samples))
	for i, s := range a.Samples {
		out[i] = int16(int(s) * volume / 100)
	}
	a.Samples = out
	return a
}

// ttsSpeed maps the SAPI rate (-10..10) to a speed multiplier: 0.5 to 2.
func ttsSpeed(rate int) float64 {
	if rate < 0 {
		return 1 + float64(rate)*0.05
	}
	return 1 + float64(rate)*0.1
}

// ttsSynthRequest is one piece of text for a cloud engine.
type ttsSynthRequest struct {
	Engine       string
	BaseURL      string // OpenAI-compatible
	Key          string
	Model        string
	Voice        string
	Instructions string
	Region       string // Azure
	Rate         int
	Text         string
}

// ttsRequestFor builds the request for the current settings, resolving keys.
func ttsRequestFor(cfg Config, text string) (ttsSynthRequest, error) {
	t := cfg.TTSOptions
	r := ttsSynthRequest{Engine: t.Engine, Rate: t.Rate, Text: RuneLimit(text, maxTTSChars)}
	switch t.Engine {
	case TTSEngineOpenAI:
		r.BaseURL, r.Model, r.Voice, r.Instructions = t.OpenAIBaseURL, t.OpenAIModel, t.OpenAIVoice, t.OpenAIInstructions
		if r.BaseURL == "" {
			r.BaseURL = cfg.AssistOptions.OpenAIBaseURL
		}
		r.Key = GetSecret(SecretTTSOpenAIKey)
		if r.Key == "" {
			key, err := openSecret(cfg.AssistOptions.OpenAIKey)
			if err != nil {
				return r, err
			}
			r.Key = key
		}
		if r.Key == "" && strings.Contains(r.BaseURL, "api.openai.com") {
			return r, errors.New("add an OpenAI API key for text to speech")
		}
	case TTSEngineElevenLabs:
		r.Model, r.Voice, r.Key = t.ElevenModel, t.ElevenVoice, GetSecret(SecretElevenLabsKey)
		if r.Key == "" {
			return r, errors.New("add an ElevenLabs API key")
		}
		if r.Voice == "" {
			return r, errors.New("pick an ElevenLabs voice")
		}
	case TTSEngineAzure:
		r.Region, r.Voice, r.Key = t.AzureRegion, t.AzureVoice, GetSecret(SecretAzureSpeechKey)
		if r.Key == "" {
			return r, errors.New("add an Azure Speech key")
		}
		if r.Region == "" {
			return r, errors.New("enter the Azure Speech region, e.g. eastus")
		}
	default:
		return r, errors.New("not a cloud voice")
	}
	return r, nil
}

var ttsClient = &http.Client{Timeout: 60 * time.Second}

// synthesize asks the engine for audio.
func synthesize(ctx context.Context, client *http.Client, r ttsSynthRequest) (pcmAudio, error) {
	req, err := newSynthRequest(ctx, r)
	if err != nil {
		return pcmAudio{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return pcmAudio{}, requestError(ctx, err, r.Key)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTTSAudioBytes))
	if err != nil {
		return pcmAudio{}, requestError(ctx, err, r.Key)
	}
	if resp.StatusCode >= 300 {
		return pcmAudio{}, errors.New(scrub(engineError(resp.StatusCode, data).Error(), r.Key))
	}
	if r.Engine == TTSEngineElevenLabs {
		if len(data) < 2 {
			return pcmAudio{}, errors.New("ElevenLabs sent no audio")
		}
		return pcmFromRaw(data, elevenPCMRate), nil
	}
	audio, err := parseWAV(data)
	if err != nil && r.Engine == TTSEngineOpenAI {
		err = errors.New(err.Error() + "; the server must support response_format wav")
	}
	return audio, err
}

func newSynthRequest(ctx context.Context, r ttsSynthRequest) (*http.Request, error) {
	var (
		endpoint string
		body     []byte
		ctype    = "application/json"
	)
	speed := ttsSpeed(r.Rate)
	switch r.Engine {
	case TTSEngineOpenAI:
		payload := map[string]any{"model": r.Model, "input": r.Text, "voice": r.Voice, "response_format": "wav"}
		if r.Rate != 0 {
			payload["speed"] = speed
		}
		if r.Instructions != "" {
			payload["instructions"] = r.Instructions
		}
		body, _ = json.Marshal(payload)
		endpoint = openAIEndpoint(r.BaseURL, "/audio/speech")
	case TTSEngineElevenLabs:
		payload := map[string]any{"text": r.Text, "model_id": r.Model}
		if r.Rate != 0 {
			// ElevenLabs takes 0.7 to 1.2.
			payload["voice_settings"] = map[string]any{"speed": math.Max(0.7, math.Min(1.2, speed))}
		}
		body, _ = json.Marshal(payload)
		endpoint = strings.TrimRight(elevenLabsBase, "/") + "/v1/text-to-speech/" + url.PathEscape(r.Voice) +
			fmt.Sprintf("?output_format=pcm_%d", elevenPCMRate)
	case TTSEngineAzure:
		body = []byte(azureSSML(r.Voice, r.Text, r.Rate))
		ctype = "application/ssml+xml"
		endpoint = azureSpeechURL(r.Region) + "/cognitiveservices/v1"
	default:
		return nil, errors.New("unknown text to speech engine")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ctype)
	switch r.Engine {
	case TTSEngineOpenAI:
		if r.Key != "" {
			req.Header.Set("Authorization", "Bearer "+r.Key)
		}
	case TTSEngineElevenLabs:
		req.Header.Set("xi-api-key", r.Key)
		req.Header.Set("Accept", "audio/pcm")
	case TTSEngineAzure:
		req.Header.Set("Ocp-Apim-Subscription-Key", r.Key)
		req.Header.Set("X-Microsoft-OutputFormat", "riff-24khz-16bit-mono-pcm")
		req.Header.Set("User-Agent", "Fern")
	}
	return req, nil
}

// azureSSML wraps text for Azure. The language comes from the voice name
// ("en-US-AvaNeural" speaks en-US).
func azureSSML(voice, text string, rate int) string {
	lang := "en-US"
	if parts := strings.SplitN(voice, "-", 3); len(parts) == 3 {
		lang = parts[0] + "-" + parts[1]
	}
	var esc bytes.Buffer
	_ = xmlEscape(&esc, text)
	inner := esc.String()
	if rate != 0 {
		inner = fmt.Sprintf("<prosody rate=\"%+d%%\">%s</prosody>", int(math.Round((ttsSpeed(rate)-1)*100)), inner)
	}
	var v, l bytes.Buffer
	_ = xmlEscape(&v, voice)
	_ = xmlEscape(&l, lang)
	return "<speak version=\"1.0\" xmlns=\"http://www.w3.org/2001/10/synthesis\" xml:lang=\"" + l.String() + "\">" +
		"<voice name=\"" + v.String() + "\">" + inner + "</voice></speak>"
}

func xmlEscape(w *bytes.Buffer, s string) error {
	for _, r := range s {
		switch r {
		case '&':
			w.WriteString("&amp;")
		case '<':
			w.WriteString("&lt;")
		case '>':
			w.WriteString("&gt;")
		case '"':
			w.WriteString("&quot;")
		case '\'':
			w.WriteString("&apos;")
		default:
			if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
				continue // not allowed in XML 1.0
			}
			w.WriteRune(r)
		}
	}
	return nil
}

// engineError turns an error reply from any of the engines into a message.
func engineError(status int, data []byte) error {
	var body struct {
		Error  json.RawMessage `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	msg := ""
	if json.Unmarshal(data, &body) == nil {
		for _, raw := range []json.RawMessage{body.Error, body.Detail} {
			if len(raw) == 0 {
				continue
			}
			var s string
			var obj struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(raw, &s) == nil {
				msg = s
			} else if json.Unmarshal(raw, &obj) == nil {
				msg = obj.Message
			}
			if msg != "" {
				break
			}
		}
	}
	return httpError(status, msg)
}

/* ---------------- voice lists ---------------- */

// engineVoices caches the voices of the cloud engines, keyed by engine and
// the settings that change the answer.
var (
	engineVoicesMu  sync.Mutex
	engineVoicesKey string
	engineVoiceList []SpeechToken
	engineVoicesErr string
	engineVoicesRun bool
)

func engineVoicesCacheKey(cfg Config) string {
	t := cfg.TTSOptions
	switch t.Engine {
	case TTSEngineOpenAI:
		base := t.OpenAIBaseURL
		if base == "" {
			base = cfg.AssistOptions.OpenAIBaseURL
		}
		return t.Engine + "|" + base
	case TTSEngineAzure:
		return t.Engine + "|" + t.AzureRegion
	}
	return t.Engine
}

// CurrentEngineVoices returns the voices listed for the current engine, and
// the error of the last attempt.
func CurrentEngineVoices() ([]SpeechToken, string) {
	cfg := GetConfig()
	if cfg.TTSOptions.Engine == TTSEngineSAPI {
		return nil, ""
	}
	engineVoicesMu.Lock()
	defer engineVoicesMu.Unlock()
	if engineVoicesKey != engineVoicesCacheKey(cfg) {
		if cfg.TTSOptions.Engine == TTSEngineOpenAI {
			return tokens(openAITTSVoices), ""
		}
		return nil, ""
	}
	return append([]SpeechToken{}, engineVoiceList...), engineVoicesErr
}

func engineKey(cfg Config) string {
	switch cfg.TTSOptions.Engine {
	case TTSEngineOpenAI:
		if k := GetSecret(SecretTTSOpenAIKey); k != "" {
			return k
		}
		k, _ := openSecret(cfg.AssistOptions.OpenAIKey)
		return k
	case TTSEngineElevenLabs:
		return GetSecret(SecretElevenLabsKey)
	case TTSEngineAzure:
		return GetSecret(SecretAzureSpeechKey)
	}
	return ""
}

// RefreshEngineVoices fetches the voice list of the current engine in the
// background.
func RefreshEngineVoices() {
	cfg := GetConfig()
	if cfg.TTSOptions.Engine == TTSEngineSAPI {
		return
	}
	engineVoicesMu.Lock()
	if engineVoicesRun {
		engineVoicesMu.Unlock()
		return
	}
	engineVoicesRun = true
	engineVoicesMu.Unlock()
	go func() {
		key := engineKey(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		list, err := listEngineVoices(ctx, ttsClient, cfg, key)
		engineVoicesMu.Lock()
		engineVoicesRun = false
		engineVoicesKey = engineVoicesCacheKey(cfg)
		engineVoiceList, engineVoicesErr = list, ""
		if err != nil {
			engineVoicesErr = scrub(err.Error(), key)
		}
		engineVoicesMu.Unlock()
	}()
}

func tokens(names []string) []SpeechToken {
	out := make([]SpeechToken, len(names))
	for i, n := range names {
		out[i] = SpeechToken{ID: n, Name: n}
	}
	return out
}

// listEngineVoices asks the engine for its voices. For OpenAI-compatible
// servers it tries /audio/voices (Kokoro and other local servers answer it)
// and otherwise offers OpenAI's built-in voices.
func listEngineVoices(ctx context.Context, client *http.Client, cfg Config, key string) ([]SpeechToken, error) {
	t := cfg.TTSOptions
	var (
		endpoint string
		header   = http.Header{}
	)
	switch t.Engine {
	case TTSEngineOpenAI:
		base := t.OpenAIBaseURL
		if base == "" {
			base = cfg.AssistOptions.OpenAIBaseURL
		}
		if strings.Contains(base, "api.openai.com") {
			return tokens(openAITTSVoices), nil
		}
		endpoint = openAIEndpoint(base, "/audio/voices")
		if key != "" {
			header.Set("Authorization", "Bearer "+key)
		}
	case TTSEngineElevenLabs:
		if key == "" {
			return nil, errors.New("add an ElevenLabs API key to list its voices")
		}
		endpoint = strings.TrimRight(elevenLabsBase, "/") + "/v1/voices"
		header.Set("xi-api-key", key)
	case TTSEngineAzure:
		if key == "" || t.AzureRegion == "" {
			return nil, errors.New("add the Azure Speech key and region to list its voices")
		}
		endpoint = azureSpeechURL(t.AzureRegion) + "/cognitiveservices/voices/list"
		header.Set("Ocp-Apim-Subscription-Key", key)
	default:
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header
	resp, err := client.Do(req)
	if err != nil {
		if t.Engine == TTSEngineOpenAI {
			return tokens(openAITTSVoices), nil
		}
		return nil, requestError(ctx, err, key)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, requestError(ctx, err, key)
	}
	if resp.StatusCode >= 300 {
		if t.Engine == TTSEngineOpenAI {
			return tokens(openAITTSVoices), nil
		}
		return nil, engineError(resp.StatusCode, data)
	}
	return parseVoiceList(t.Engine, data)
}

func parseVoiceList(engine string, data []byte) ([]SpeechToken, error) {
	var out []SpeechToken
	switch engine {
	case TTSEngineOpenAI:
		var resp struct {
			Voices []json.RawMessage `json:"voices"`
		}
		if err := json.Unmarshal(data, &resp); err != nil || len(resp.Voices) == 0 {
			return tokens(openAITTSVoices), nil
		}
		for _, raw := range resp.Voices {
			var name string
			var obj struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &name) == nil && name != "" {
				out = append(out, SpeechToken{ID: name, Name: name})
			} else if json.Unmarshal(raw, &obj) == nil && (obj.ID != "" || obj.Name != "") {
				if obj.ID == "" {
					obj.ID = obj.Name
				}
				if obj.Name == "" {
					obj.Name = obj.ID
				}
				out = append(out, SpeechToken{ID: obj.ID, Name: obj.Name})
			}
		}
	case TTSEngineElevenLabs:
		var resp struct {
			Voices []struct {
				ID       string `json:"voice_id"`
				Name     string `json:"name"`
				Category string `json:"category"`
			} `json:"voices"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, errors.New("ElevenLabs answered with something that is not a voice list")
		}
		for _, v := range resp.Voices {
			if v.ID == "" {
				continue
			}
			name := v.Name
			if name == "" {
				name = v.ID
			}
			if v.Category != "" && v.Category != "premade" {
				name += " (" + v.Category + ")"
			}
			out = append(out, SpeechToken{ID: v.ID, Name: name})
		}
	case TTSEngineAzure:
		var resp []struct {
			ShortName   string `json:"ShortName"`
			DisplayName string `json:"DisplayName"`
			LocaleName  string `json:"LocaleName"`
			Locale      string `json:"Locale"`
			Gender      string `json:"Gender"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, errors.New("Azure answered with something that is not a voice list")
		}
		for _, v := range resp {
			if v.ShortName == "" {
				continue
			}
			label := v.DisplayName
			if label == "" {
				label = v.ShortName
			}
			where := v.LocaleName
			if where == "" {
				where = v.Locale
			}
			if where != "" {
				label += " – " + where
			}
			if v.Gender != "" {
				label += " (" + v.Gender + ")"
			}
			out = append(out, SpeechToken{ID: v.ShortName, Name: label})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	}
	if len(out) > 2000 {
		out = out[:2000]
	}
	return out, nil
}
