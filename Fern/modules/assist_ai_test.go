package modules

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const fakeKey = "sk-test-0123456789abcdef"

func TestOpenAIRequestAndResponse(t *testing.T) {
	var got struct {
		path, auth string
		body       map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"<think>hmm</think>\n\"Hello, can you hear me?\""}}],
			"usage":{"prompt_tokens":42,"completion_tokens":7,"total_tokens":49}}`)
	}))
	defer server.Close()

	r := aiRequest{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1", Model: "gpt-test", Key: fakeKey, System: "sys", User: "usr"}
	text, usage, err := callAI(context.Background(), server.Client(), r)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Hello, can you hear me?" {
		t.Errorf("text = %q", text)
	}
	if usage != (AIUsage{Input: 42, Output: 7}) {
		t.Errorf("usage = %+v", usage)
	}
	if got.path != "/v1/chat/completions" || got.auth != "Bearer "+fakeKey {
		t.Errorf("path %q auth %q", got.path, got.auth)
	}
	if got.body["model"] != "gpt-test" {
		t.Errorf("model = %v", got.body["model"])
	}
	msgs, _ := got.body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[1].(map[string]any)["content"] != "usr" {
		t.Errorf("messages = %v", got.body["messages"])
	}
	for _, field := range []string{"temperature", "max_tokens"} {
		if _, ok := got.body[field]; ok {
			t.Errorf("%s should not be sent; some models reject it", field)
		}
	}
}

func TestOpenAILocalServerWithoutKeyOrUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("sent an Authorization header without a key")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want the /v1 prefix added to a bare host", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hi there"}}]}`)
	}))
	defer server.Close()

	r := aiRequest{Provider: ProviderOpenAI, BaseURL: server.URL, Model: "llama3", System: "s", User: "a longer user prompt"}
	text, usage, err := callAI(context.Background(), server.Client(), r)
	if err != nil || text != "hi there" {
		t.Fatalf("text %q err %v", text, err)
	}
	if !usage.Estimated || usage.Input == 0 || usage.Output == 0 {
		t.Errorf("usage = %+v, want an estimate", usage)
	}
}

func TestOpenAIErrorIsReadableAndScrubbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key provided: `+fakeKey+`","type":"invalid_request_error"}}`)
	}))
	defer server.Close()

	r := aiRequest{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1", Model: "m", Key: fakeKey, System: "s", User: "u"}
	_, _, err := callAI(context.Background(), server.Client(), r)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), fakeKey) {
		t.Errorf("error leaks the key: %v", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %v", err)
	}
}

func TestOpenAIKeyRequiredForOpenAIHost(t *testing.T) {
	r := aiRequest{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1", Model: "m"}
	if _, _, err := callAI(context.Background(), http.DefaultClient, r); err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("err = %v", err)
	}
}

func TestAnthropicRequestAndResponse(t *testing.T) {
	var got struct {
		path    string
		headers http.Header
		body    map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.headers, got.body = r.URL.Path, r.Header.Clone(), nil
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &got.body)
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"thinking","thinking":""},{"type":"text","text":"Bonjour à tous !"}],
			"stop_reason":"end_turn","usage":{"input_tokens":30,"output_tokens":9}}`)
	}))
	defer server.Close()

	r := aiRequest{Provider: ProviderAnthropic, BaseURL: server.URL, Model: "claude-haiku-5-5", Key: fakeKey, Effort: "low", System: "sys", User: "usr"}
	text, usage, err := callAI(context.Background(), server.Client(), r)
	if err != nil {
		t.Fatal(err)
	}
	if text != "Bonjour à tous !" || usage != (AIUsage{Input: 30, Output: 9}) {
		t.Errorf("text %q usage %+v", text, usage)
	}
	if got.path != "/v1/messages" {
		t.Errorf("path = %q", got.path)
	}
	if got.headers.Get("x-api-key") != fakeKey || got.headers.Get("anthropic-version") != anthropicVersion {
		t.Errorf("headers = %v", got.headers)
	}
	if got.headers.Get("Authorization") != "" {
		t.Error("Anthropic takes x-api-key, not Authorization")
	}
	if got.body["model"] != "claude-haiku-5-5" || got.body["system"] != "sys" || got.body["max_tokens"] == nil {
		t.Errorf("body = %v", got.body)
	}
	if oc, _ := got.body["output_config"].(map[string]any); oc["effort"] != "low" {
		t.Errorf("output_config = %v", got.body["output_config"])
	}
	if _, ok := got.body["temperature"]; ok {
		t.Error("temperature is rejected by this API version")
	}

	// No effort: the field is left out for models that do not support it.
	r.Effort = ""
	r.BaseURL = server.URL + "/v1/" // a base that already ends in /v1
	if _, _, err := callAI(context.Background(), server.Client(), r); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.body["output_config"]; ok || got.path != "/v1/messages" {
		t.Errorf("path %q body %v", got.path, got.body)
	}
}

func TestAnthropicErrorsAndRefusal(t *testing.T) {
	status, reply := 0, ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer server.Close()
	r := aiRequest{Provider: ProviderAnthropic, BaseURL: server.URL, Model: "m", Key: fakeKey, System: "s", User: "u"}

	status, reply = 404, `{"type":"error","error":{"type":"not_found_error","message":"model: nope"}}`
	if _, _, err := callAI(context.Background(), server.Client(), r); err == nil || !strings.Contains(err.Error(), "model: nope") {
		t.Errorf("404: %v", err)
	}
	status, reply = 200, `{"content":[],"stop_reason":"refusal","usage":{"input_tokens":5,"output_tokens":0}}`
	if _, _, err := callAI(context.Background(), server.Client(), r); err == nil || !strings.Contains(err.Error(), "declined") {
		t.Errorf("refusal: %v", err)
	}
	status, reply = 502, `<html>bad gateway</html>`
	if _, _, err := callAI(context.Background(), server.Client(), r); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("502: %v", err)
	}
	r.Key = ""
	if _, _, err := callAI(context.Background(), server.Client(), r); err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("no key: %v", err)
	}
}

func TestCallAITimeoutAndCancel(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	r := aiRequest{Provider: ProviderOpenAI, BaseURL: server.URL + "/v1", Model: "m", System: "s", User: "u"}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := callAI(ctx, server.Client(), r); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("timeout: %v", err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if _, _, err := callAI(ctx, server.Client(), r); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel: %v", err)
	}
}

func TestBuildPrompt(t *testing.T) {
	o := defaultAssistOptions()
	o.Style, o.Language, o.MaxChars = "pirate", "Japanese", 100
	cases := map[string]string{
		OpFix:       "spelling",
		OpRewrite:   "pirate",
		OpShorten:   "100 characters",
		OpTranslate: "Japanese",
		OpReply:     "reply",
	}
	for op, want := range cases {
		p, err := buildPrompt(op, "hello  ", o)
		if err != nil || !strings.Contains(p, want) || !strings.Contains(p, "<message>\nhello\n</message>") {
			t.Errorf("%s: %q, %v", op, p, err)
		}
		if _, err := buildPrompt(op, "   ", o); err == nil {
			t.Errorf("%s accepted empty text", op)
		}
	}
	if p, err := buildPrompt(OpStarter, "", o); err != nil || strings.Contains(p, "<message>") {
		t.Errorf("starter without topic: %q %v", p, err)
	}
	if p, _ := buildPrompt(OpStarter, "cats", o); !strings.Contains(p, "cats") {
		t.Errorf("starter with topic: %q", p)
	}
	if _, err := buildPrompt("bogus", "x", o); err == nil {
		t.Error("unknown op accepted")
	}
	long, _ := buildPrompt(OpFix, strings.Repeat("a", 5000), o)
	if len(long) > aiInputLimit+500 {
		t.Errorf("input not limited: %d", len(long))
	}
}

func TestCleanResult(t *testing.T) {
	for in, want := range map[string]string{
		"  hi  ":                          "hi",
		`"quoted"`:                        "quoted",
		"“curly”":                         "curly",
		`"one" and "two"`:                 `"one" and "two"`,
		"<think>\nlong\n</think>\nanswer": "answer",
		"<message>\nhey\n</message>":      "hey",
		"it's fine":                       "it's fine",
	} {
		if got := cleanResult(in); got != want {
			t.Errorf("cleanResult(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartAssistReportsMissingTextWithoutCalling(t *testing.T) {
	before := CurrentAI().Seq
	StartAssist(OpFix, "  ")
	st := CurrentAI()
	if st.Seq != before+1 || st.Error == "" || st.Busy {
		t.Errorf("state = %+v", st)
	}
}
