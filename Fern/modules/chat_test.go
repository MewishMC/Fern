package modules

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCleanChatHistory(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := cleanChatHistory([]string{" hi ", "", "two\nlines", "hi", long}, true)
	want := []string{"hi", "two lines", strings.Repeat("a", 143)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := cleanChatHistory([]string{"hi"}, false); len(got) != 0 {
		t.Errorf("history kept while off: %q", got)
	}
	many := make([]string, 30)
	for i := range many {
		many[i] = fmt.Sprint("message ", i)
	}
	if got := cleanChatHistory(many, true); len(got) != ChatHistoryLimit || got[0] != "message 0" {
		t.Errorf("got %d messages starting %q", len(got), got[0])
	}
	if got := cleanChatHistory(nil, true); got == nil {
		t.Error("nil history should become an empty list")
	}
}

// useTestConfig points the config at a temporary file for one test.
func useTestConfig(t *testing.T, c Config) {
	t.Helper()
	oldPath, oldCfg := cfgPath, GetConfig()
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	SetConfig(c)
	t.Cleanup(func() {
		saveMu.Lock()
		if saveAt != nil {
			saveAt.Stop()
		}
		saveMu.Unlock()
		cfgMu.Lock()
		cfg = oldCfg
		cfgMu.Unlock()
		cfgPath = oldPath
	})
}

func TestAddChatHistory(t *testing.T) {
	useTestConfig(t, DefaultConfig())
	AddChatHistory("first")
	AddChatHistory("second")
	AddChatHistory("first")
	AddChatHistory("first")
	AddChatHistory("   ")
	if got, want := GetConfig().ChatOptions.History, []string{"first", "second"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	ClearChatHistory()
	if got := GetConfig().ChatOptions.History; len(got) != 0 {
		t.Errorf("after clearing: %q", got)
	}

	off := DefaultConfig()
	off.ChatOptions.KeepHistory = false
	SetConfig(off)
	AddChatHistory("secret")
	if got := GetConfig().ChatOptions.History; len(got) != 0 {
		t.Errorf("remembered %q with history off", got)
	}
}
