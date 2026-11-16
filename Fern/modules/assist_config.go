package modules

import (
	"strings"
)

// Assist providers.
const (
	ProviderOpenAI    = "openai"    // any OpenAI-compatible chat completions server
	ProviderAnthropic = "anthropic" // Anthropic Messages API
)

// AssistOptions configures the AI writing tools of the Assist page.
//
// The API keys are stored encrypted for the current Windows account (DPAPI)
// and never leave the backend: the panel gets a masked hint instead, and keys
// are set through their own command rather than the config round trip.
type AssistOptions struct {
	Provider         string `json:"provider"`
	OpenAIBaseURL    string `json:"openai_base_url"`
	OpenAIModel      string `json:"openai_model"`
	OpenAIKey        string `json:"openai_key"` // DPAPI-protected, see secret.go
	AnthropicBaseURL string `json:"anthropic_base_url"`
	AnthropicModel   string `json:"anthropic_model"`
	AnthropicEffort  string `json:"anthropic_effort"` // low/medium/high, or empty to not send one
	AnthropicKey     string `json:"anthropic_key"`    // DPAPI-protected
	TimeoutSec       int    `json:"timeout_sec"`
	Style            string `json:"style"`     // tone for rewrite, reply and starter
	Language         string `json:"language"`  // target for translate
	MaxChars         int    `json:"max_chars"` // target for shorten
	ShowBar          bool   `json:"show_bar"`  // AI buttons under the Send now box
}

// TTSOptions configures text to speech with the Windows voices.
type TTSOptions struct {
	SpeakSent bool   `json:"speak_sent"` // read out messages sent from the panel
	Voice     string `json:"voice"`      // SAPI token id; empty uses the Windows default
	Output    string `json:"output"`     // SAPI audio output token id; empty uses the default device
	Rate      int    `json:"rate"`       // -10..10
	Volume    int    `json:"volume"`     // 0..100
	CutOff    bool   `json:"cut_off"`    // a new message stops the one being read
	Unmute    bool   `json:"unmute"`     // open the VRChat mic while speaking
	VoiceMode string `json:"voice_mode"` // toggle or ptt, matching VRChat's mic setting

	// Engine picks who speaks: sapi (the Windows voices above), openai,
	// elevenlabs or azure; see tts_engines.go. Their keys are in secrets.dat.
	Engine             string `json:"engine"`
	OpenAIBaseURL      string `json:"openai_base_url"` // empty uses the Assist OpenAI-compatible base URL
	OpenAIModel        string `json:"openai_model"`
	OpenAIVoice        string `json:"openai_voice"`
	OpenAIInstructions string `json:"openai_instructions"` // tone, for models that take it
	ElevenVoice        string `json:"eleven_voice"`        // voice ID
	ElevenModel        string `json:"eleven_model"`
	AzureRegion        string `json:"azure_region"`
	AzureVoice         string `json:"azure_voice"` // short name, e.g. en-US-AvaNeural
}

// STTOptions configures speech to text.
type STTOptions struct {
	Device      string `json:"device"`   // microphone name; empty uses the Windows default
	BaseURL     string `json:"base_url"` // empty uses the OpenAI-compatible base URL
	Key         string `json:"key"`      // DPAPI-protected; empty uses the OpenAI-compatible key
	Model       string `json:"model"`
	Language    string `json:"language"`   // ISO-639-1 hint, e.g. en; empty lets the model detect it
	Gate        int    `json:"gate"`       // noise gate, percent of full scale
	SilenceMS   int    `json:"silence_ms"` // stop after this much quiet once speech started
	MaxSec      int    `json:"max_sec"`
	AutoSend    bool   `json:"auto_send"`
	Translate   bool   `json:"translate"` // translate the transcript with the AI tools
	TranslateTo string `json:"translate_to"`
}

func defaultAssistOptions() AssistOptions {
	return AssistOptions{
		Provider:         ProviderOpenAI,
		OpenAIBaseURL:    "https://api.openai.com/v1",
		OpenAIModel:      "gpt-4o-mini",
		AnthropicBaseURL: "https://api.anthropic.com",
		AnthropicModel:   "claude-haiku-5-5",
		AnthropicEffort:  "low",
		TimeoutSec:       20,
		Style:            "friendly",
		Language:         "English",
		MaxChars:         144,
		ShowBar:          true,
	}
}

func defaultTTSOptions() TTSOptions {
	return TTSOptions{
		Rate: 0, Volume: 60, CutOff: true, VoiceMode: "toggle",
		Engine:      TTSEngineSAPI,
		OpenAIModel: defaultTTSOpenAIModel,
		OpenAIVoice: defaultTTSOpenAIVoice,
		ElevenModel: defaultElevenModel,
		AzureVoice:  defaultAzureVoice,
	}
}

func defaultSTTOptions() STTOptions {
	return STTOptions{
		Model:       "gpt-4o-mini-transcribe",
		Gate:        6,
		SilenceMS:   1500,
		MaxSec:      30,
		TranslateTo: "English",
	}
}

// cleanURL trims a base URL and its trailing slashes; anything that is not
// http(s) is dropped so a typo cannot send a key somewhere unexpected.
func cleanURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	lower := strings.ToLower(u)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	return RuneLimit(u, 300)
}

func oneLine(s string, max int) string {
	return RuneLimit(strings.Join(strings.Fields(s), " "), max)
}

func (c *Config) sanitizeAssist() {
	def := defaultAssistOptions()
	a := &c.AssistOptions
	if a.Provider != ProviderAnthropic {
		a.Provider = ProviderOpenAI
	}
	if a.OpenAIBaseURL = cleanURL(a.OpenAIBaseURL); a.OpenAIBaseURL == "" {
		a.OpenAIBaseURL = def.OpenAIBaseURL
	}
	if a.AnthropicBaseURL = cleanURL(a.AnthropicBaseURL); a.AnthropicBaseURL == "" {
		a.AnthropicBaseURL = def.AnthropicBaseURL
	}
	if a.OpenAIModel = oneLine(a.OpenAIModel, 100); a.OpenAIModel == "" {
		a.OpenAIModel = def.OpenAIModel
	}
	if a.AnthropicModel = oneLine(a.AnthropicModel, 100); a.AnthropicModel == "" {
		a.AnthropicModel = def.AnthropicModel
	}
	switch a.AnthropicEffort {
	case "", "low", "medium", "high":
	default:
		a.AnthropicEffort = def.AnthropicEffort
	}
	if a.TimeoutSec == 0 {
		a.TimeoutSec = def.TimeoutSec
	}
	a.TimeoutSec = clampInt(a.TimeoutSec, 5, 120)
	if a.Style = oneLine(a.Style, 40); a.Style == "" {
		a.Style = def.Style
	}
	if a.Language = oneLine(a.Language, 40); a.Language == "" {
		a.Language = def.Language
	}
	if a.MaxChars == 0 {
		a.MaxChars = def.MaxChars
	}
	a.MaxChars = clampInt(a.MaxChars, 20, 144)
	a.OpenAIKey = strings.TrimSpace(a.OpenAIKey)
	a.AnthropicKey = strings.TrimSpace(a.AnthropicKey)

	t := &c.TTSOptions
	t.Voice = RuneLimit(strings.TrimSpace(t.Voice), 300)
	t.Output = RuneLimit(strings.TrimSpace(t.Output), 300)
	t.Rate = clampInt(t.Rate, -10, 10)
	t.Volume = clampInt(t.Volume, 0, 100)
	if t.VoiceMode != "ptt" {
		t.VoiceMode = "toggle"
	}
	t.sanitizeEngine()

	sdef := defaultSTTOptions()
	s := &c.STTOptions
	s.Device = RuneLimit(strings.TrimSpace(s.Device), 64)
	s.BaseURL = cleanURL(s.BaseURL)
	s.Key = strings.TrimSpace(s.Key)
	if s.Model = oneLine(s.Model, 100); s.Model == "" {
		s.Model = sdef.Model
	}
	s.Language = strings.ToLower(oneLine(s.Language, 8))
	s.Gate = clampInt(s.Gate, 0, 100)
	if s.SilenceMS == 0 {
		s.SilenceMS = sdef.SilenceMS
	}
	s.SilenceMS = clampInt(s.SilenceMS, 300, 10000)
	if s.MaxSec == 0 {
		s.MaxSec = sdef.MaxSec
	}
	s.MaxSec = clampInt(s.MaxSec, 5, 120)
	if s.TranslateTo = oneLine(s.TranslateTo, 40); s.TranslateTo == "" {
		s.TranslateTo = sdef.TranslateTo
	}
}

// keepSecrets copies the stored keys from prev. The panel never receives the
// keys, so every config it sends back has them blank; keys only change through
// SetAssistKey.
func (c *Config) keepSecrets(prev Config) {
	c.AssistOptions.OpenAIKey = prev.AssistOptions.OpenAIKey
	c.AssistOptions.AnthropicKey = prev.AssistOptions.AnthropicKey
	c.STTOptions.Key = prev.STTOptions.Key
}

// Public returns the config with every secret removed, for the panel.
func (c Config) Public() Config {
	c.AssistOptions.OpenAIKey = ""
	c.AssistOptions.AnthropicKey = ""
	c.STTOptions.Key = ""
	return c
}
