package modules

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretRoundTrip(t *testing.T) {
	sealed, err := sealSecret(fakeKey)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, secretPrefix) || strings.Contains(sealed, fakeKey) {
		t.Fatalf("sealed = %q", sealed)
	}
	plain, err := openSecret(sealed)
	if err != nil || plain != fakeKey {
		t.Fatalf("open = %q, %v", plain, err)
	}
	if hint := keyHint(sealed); hint != "sk-…cdef" {
		t.Errorf("hint = %q", hint)
	}
	if empty, _ := sealSecret(""); empty != "" {
		t.Error("an empty key should stay empty")
	}
	if plain, _ := openSecret("plain-typed-key"); plain != "plain-typed-key" {
		t.Error("a hand-typed key should be used as is")
	}
	if _, err := openSecret(secretPrefix + "bm90IGRwYXBp"); err == nil {
		t.Error("garbage decrypted")
	}
}

func TestPublicConfigHasNoSecrets(t *testing.T) {
	c := DefaultConfig()
	c.AssistOptions.OpenAIKey = "dpapi:AAAA-openai"
	c.AssistOptions.AnthropicKey = "dpapi:BBBB-anthropic"
	c.STTOptions.Key = "dpapi:CCCC-stt"
	data, _ := json.Marshal(c.Public())
	for _, secret := range []string{"AAAA", "BBBB", "CCCC"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("public config contains %s", secret)
		}
	}
	if c.AssistOptions.OpenAIKey == "" {
		t.Error("Public changed the original")
	}
}

func TestSetConfigKeepsKeys(t *testing.T) {
	saved := cfgPath
	cfgPath = t.TempDir() + "/config.json"
	defer func() {
		saveMu.Lock()
		if saveAt != nil {
			saveAt.Stop()
		}
		saveMu.Unlock()
		cfgPath = saved
	}()

	SetConfig(DefaultConfig())
	if err := SetAssistKey(ProviderAnthropic, fakeKey); err != nil {
		t.Fatal(err)
	}
	// The panel sends configs without keys; they must survive.
	in := GetConfig().Public()
	in.AssistOptions.Style = "formal"
	SetConfig(in)
	got := GetConfig().AssistOptions
	if plain, _ := openSecret(got.AnthropicKey); plain != fakeKey || got.Style != "formal" {
		t.Errorf("key %q style %q", plain, got.Style)
	}
	if err := SetAssistKey(ProviderAnthropic, ""); err != nil || GetConfig().AssistOptions.AnthropicKey != "" {
		t.Errorf("clearing failed: %v", err)
	}
	if err := SetAssistKey("bogus", "x"); err == nil {
		t.Error("unknown key accepted")
	}
	if err := SetAssistKey(ProviderOpenAI, "has space"); err == nil {
		t.Error("key with spaces accepted")
	}
}

func TestSanitizeAssist(t *testing.T) {
	c := DefaultConfig()
	c.AssistOptions.Provider = "skynet"
	c.AssistOptions.OpenAIBaseURL = "ftp://nope"
	c.AssistOptions.AnthropicBaseURL = " https://proxy.example/anthropic/ "
	c.AssistOptions.TimeoutSec = 9999
	c.AssistOptions.MaxChars = 1000
	c.AssistOptions.AnthropicEffort = "ludicrous"
	c.TTSOptions.Rate = 50
	c.TTSOptions.Volume = -3
	c.TTSOptions.VoiceMode = "?"
	c.STTOptions.Gate = 300
	c.STTOptions.SilenceMS = 1
	c.STTOptions.Language = " EN "
	c.sanitize()
	a, tt, s := c.AssistOptions, c.TTSOptions, c.STTOptions
	checks := []struct {
		name      string
		got, want any
	}{
		{"provider", a.Provider, ProviderOpenAI},
		{"openai url", a.OpenAIBaseURL, "https://api.openai.com/v1"},
		{"anthropic url", a.AnthropicBaseURL, "https://proxy.example/anthropic"},
		{"timeout", a.TimeoutSec, 120},
		{"max chars", a.MaxChars, 144},
		{"effort", a.AnthropicEffort, "low"},
		{"rate", tt.Rate, 10},
		{"volume", tt.Volume, 0},
		{"voice mode", tt.VoiceMode, "toggle"},
		{"gate", s.Gate, 100},
		{"silence", s.SilenceMS, 300},
		{"language", s.Language, "en"},
	}
	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.name, ch.got, ch.want)
		}
	}
	// An old config without the new sections gets the defaults.
	old := Config{}
	old.sanitize()
	if old.AssistOptions.AnthropicModel != "claude-haiku-5-5" || old.STTOptions.Model == "" {
		t.Errorf("old config: %+v %+v", old.AssistOptions, old.STTOptions)
	}
}
