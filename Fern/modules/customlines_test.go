package modules

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// resetCustomLines clears the custom line values and restores the clock.
func resetCustomLines(t *testing.T) {
	t.Helper()
	clear := func() {
		customMu.Lock()
		customAPI = [MaxCustomLines]customValue{}
		customPoll = [MaxCustomLines]customValue{}
		customMu.Unlock()
	}
	clear()
	oldNow, oldRun := customNow, customRun
	t.Cleanup(func() {
		customNow, customRun = oldNow, oldRun
		clear()
	})
}

func TestCustomLinesDefaultsAndSanitize(t *testing.T) {
	d := DefaultConfig()
	if len(d.CustomLines.Lines) != MaxCustomLines {
		t.Fatalf("default has %d custom lines", len(d.CustomLines.Lines))
	}
	for i, l := range d.CustomLines.Lines {
		if l.Source != CustomSourceAPI || l.IntervalSec != 30 || l.TimeoutSec != 10 || l.Name == "" {
			t.Errorf("default line %d: %+v", i, l)
		}
	}
	for _, id := range CustomLineIDs {
		if !knownModules[id] || ModuleEnabled(id, d.DisplayOptions) {
			t.Errorf("%s: known %v, on by default %v", id, knownModules[id], ModuleEnabled(id, d.DisplayOptions))
		}
	}

	c := DefaultConfig()
	c.CustomLines.Lines = []CustomLine{{
		Name: "  Song\nrequest  ", Source: "shell", Label: "» ", Command: "echo a\r\nb",
		IntervalSec: 1, TimeoutSec: 999, MaxChars: 500, Fallback: strings.Repeat("x", 300),
	}}
	c.sanitize()
	if len(c.CustomLines.Lines) != MaxCustomLines {
		t.Fatalf("sanitize kept %d lines", len(c.CustomLines.Lines))
	}
	l := c.CustomLines.Lines[0]
	if l.Name != "Song request" || l.Source != CustomSourceAPI || l.Command != "echo a  b" {
		t.Errorf("text fields: %+v", l)
	}
	if l.IntervalSec != customMinInterval || l.TimeoutSec != customMaxTimeout || l.MaxChars != customMaxText || len([]rune(l.Fallback)) != customMaxText {
		t.Errorf("bounds: %+v", l)
	}
	if c.CustomLines.Lines[2] != defaultCustomLine(2) {
		t.Errorf("missing slot not filled with defaults: %+v", c.CustomLines.Lines[2])
	}
}

func TestCustomLinesStayOnThisPC(t *testing.T) {
	c := DefaultConfig()
	c.CustomLines.Lines[0].Command = "secret.exe"
	c.ControlAPI.Enabled = true
	c.Clipboard.Hotkey = "Ctrl+Shift+V"
	data, err := profileSettings(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"CustomLines", "ControlAPI", "Clipboard", "secret.exe"} {
		if strings.Contains(string(data), key) {
			t.Errorf("profile snapshot carries %s", key)
		}
	}
	next, err := applyProfileSettings(c, data)
	if err != nil {
		t.Fatal(err)
	}
	if next.CustomLines.Lines[0].Command != "secret.exe" || !next.ControlAPI.Enabled || next.Clipboard.Hotkey != "Ctrl+Shift+V" {
		t.Error("applying a profile lost the per-PC settings")
	}

	imported := DefaultConfig()
	imported.CustomLines.Lines[0].Command = "evil.exe"
	imported.CustomLines.Lines[1].Name = "Kept"
	imported.CustomLines.keepCommands(c.CustomLines)
	if imported.CustomLines.Lines[0].Command != "secret.exe" || imported.CustomLines.Lines[1].Command != "" || imported.CustomLines.Lines[1].Name != "Kept" {
		t.Errorf("keepCommands: %+v", imported.CustomLines.Lines)
	}
}

func TestCustomLineAPIText(t *testing.T) {
	resetCustomLines(t)
	now := time.Unix(1000, 0)
	customNow = func() time.Time { return now }
	c := DefaultConfig()
	c.CustomLines.Lines[0].Label = "Now:"
	c.CustomLines.Lines[0].MaxChars = 10
	c.CustomLines.Lines[1].Fallback = "nothing yet"

	if got := customLine(c, ModuleCustom1); got != "" {
		t.Errorf("empty line rendered %q", got)
	}
	if got := customLine(c, ModuleCustom2); got != "nothing yet" {
		t.Errorf("fallback: %q", got)
	}
	if err := SetCustomLineText(ModuleCustom1, "Hello\nthere, everyone", 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := customLine(c, ModuleCustom1); got != "Now: Hello the…" {
		t.Errorf("label and limit: %q", got)
	}
	now = now.Add(6 * time.Second)
	if got := customLine(c, ModuleCustom1); got != "" {
		t.Errorf("expired text still shows: %q", got)
	}
	if err := SetCustomLineText("custom9", "x", 0); err == nil {
		t.Error("unknown line accepted")
	}

	// The line only shows API text while its source is the API.
	_ = SetCustomLineText(ModuleCustom1, "pinned", 0)
	c.CustomLines.Lines[0].Source = CustomSourceURL
	if got := customLine(c, ModuleCustom1); got != "" {
		t.Errorf("URL line showed API text %q", got)
	}
}

func TestCustomLineFitsBeforeDropping(t *testing.T) {
	resetCustomLines(t)
	c := DefaultConfig()
	_ = SetCustomLineText(ModuleCustom1, "a long custom line of text", 0)
	line := customFitLine(c, ModuleCustom1)
	if line.Variants[0] != "a long custom line of text" {
		t.Fatalf("variant %q", line.Variants[0])
	}
	if got := line.Trim(12); got != "a long cust…" {
		t.Errorf("trim: %q", got)
	}
	if got := line.Trim(4); got != "" {
		t.Errorf("trim below 8 should refuse, got %q", got)
	}
	for _, id := range CustomLineIDs {
		found := false
		for _, d := range DefaultDropOrder {
			found = found || d == id
		}
		if !found {
			t.Errorf("%s missing from DefaultDropOrder", id)
		}
		if LineLabel(id) == id {
			t.Errorf("%s has no label", id)
		}
	}
}

func TestCustomReplyText(t *testing.T) {
	cases := []struct{ body, path, want string }{
		{"hello\nworld", "", "hello"},
		{"\n\n  second  \n", "", "second"},
		{`"quoted\nvalue"`, "", "quoted"},
		{`{"data":{"viewers":42}}`, "data.viewers", "42"},
		{`{"items":[{"title":"Song A"},{"title":"Song B"}]}`, "items[1].title", "Song B"},
		{`{"items":[{"title":"Song A"}]}`, "items.0.Title", "Song A"},
		{`{"live":true}`, "live", "true"},
		{`{"n":null}`, "n", ""},
		{`{"o":{"a":1}}`, "o", `{"a":1}`},
		{`{"v":1.50}`, "$.v", "1.50"},
	}
	for _, c := range cases {
		got, err := customReplyText([]byte(c.body), c.path)
		if err != nil || got != c.want {
			t.Errorf("%q at %q: got %q, %v; want %q", c.body, c.path, got, err, c.want)
		}
	}
	if _, err := customReplyText([]byte("not json"), "a"); err == nil {
		t.Error("non-JSON with a path gave no error")
	}
	if _, err := customReplyText([]byte(`{"a":1}`), "b"); err == nil {
		t.Error("missing field gave no error")
	}
}

func TestFetchCustomURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Fern" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/json":
			_ = json.NewEncoder(w).Encode(map[string]any{"stream": map[string]any{"viewers": 7}})
		case "/slow":
			time.Sleep(500 * time.Millisecond)
		default:
			http.Error(w, "nope", http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	got, err := fetchCustomURL(context.Background(), srv.URL+"/json?key=abc", "stream.viewers")
	if err != nil || got != "7" {
		t.Errorf("json: %q, %v", got, err)
	}
	if _, err := fetchCustomURL(context.Background(), srv.URL+"/down?key=abc", ""); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("status error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = fetchCustomURL(ctx, srv.URL+"/slow?key=abc", "")
	if err == nil || strings.Contains(err.Error(), "key=abc") {
		t.Errorf("timeout error should not repeat the URL: %v", err)
	}
	if _, err := fetchCustomURL(context.Background(), "file:///C:/Windows/win.ini", ""); err == nil {
		t.Error("file URL accepted")
	}
}

func TestRunCustomCommand(t *testing.T) {
	got, err := runCustomCommand(context.Background(), `echo   first line & echo second`)
	if err != nil || got != "first line" {
		t.Errorf("echo: %q, %v", got, err)
	}
	if _, err := runCustomCommand(context.Background(), `echo oops 1>&2 & exit /b 3`); err == nil || !strings.Contains(err.Error(), "exit code 3: oops") {
		t.Errorf("exit code: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runCustomCommand(ctx, `ping -n 20 127.0.0.1 >nul`); err == nil {
		t.Error("a command past its timeout returned no error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("timeout took %v", d)
	}
}

func TestCustomTickRunsOnlyWhenOnOrForced(t *testing.T) {
	resetCustomLines(t)
	var runs atomic.Int32
	done := make(chan struct{}, 4)
	customRun = func(_ context.Context, l CustomLine) (string, error) {
		runs.Add(1)
		defer func() { done <- struct{}{} }()
		return "  from " + l.Command + "\n", nil
	}
	c := DefaultConfig()
	c.CustomLines.Lines[0].Source = CustomSourceCommand
	c.CustomLines.Lines[0].Command = "tool"
	useConfig(t, c)

	customTick(c)
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatal("ran while the line is off")
	}

	if err := RefreshCustomLine(ModuleCustom1); err != nil {
		t.Fatal(err)
	}
	customTick(c)
	waitRun(t, done)
	if got := customLine(c, ModuleCustom1); got != "from tool" {
		t.Errorf("forced run gave %q", got)
	}
	st := CustomLineStatuses()[0]
	if st.Text != "from tool" || st.Runs != 1 || st.Busy || st.UpdatedMS == 0 {
		t.Errorf("status %+v", st)
	}

	// Not due again until the interval has passed.
	c.DisplayOptions.Custom1 = true
	customTick(c)
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 1 {
		t.Errorf("ran again before its interval: %d", runs.Load())
	}

	// A new command drops the old value and runs once the edit settles.
	c.CustomLines.Lines[0].Command = "other"
	if got := customLine(c, ModuleCustom1); got != "" {
		t.Errorf("old value shown for a new command: %q", got)
	}
	customTick(c)
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 1 {
		t.Errorf("a just-edited command ran at once: %d", runs.Load())
	}
	later := time.Now().Add(2 * customSettle)
	customNow = func() time.Time { return later }
	customTick(c)
	waitRun(t, done)
	if got := customLine(c, ModuleCustom1); got != "from other" {
		t.Errorf("after change %q", got)
	}

	if err := RefreshCustomLine(ModuleCustom2); err == nil {
		t.Error("refreshing an API line gave no error")
	}
}

func waitRun(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the run never happened")
	}
	// customRunOnce stores the result right after customRun returns.
	time.Sleep(30 * time.Millisecond)
}

func TestFirstLineStripsTerminalCodes(t *testing.T) {
	if got := firstLine("\uFEFF\x1b[32mgreen\x1b[0m text\r\nnext"); got != "green text" {
		t.Errorf("got %q", got)
	}
}
