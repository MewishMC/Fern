package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Output page reads these keys; forward must be a list, never null.
func TestOSCInStateJSON(t *testing.T) {
	data, err := json.Marshal(currentOSCIn())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"oscquery":`, `"port":`, `"queryPort":`, `"service":`, `"fallback":`, `"error":`, `"queriedMs":`, `"forward":[]`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("%s missing from %s", key, data)
		}
	}
}
