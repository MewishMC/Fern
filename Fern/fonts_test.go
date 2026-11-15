package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemFontFamilies(t *testing.T) {
	list := systemFontFamilies()
	if len(list.Families) == 0 {
		t.Skip("no fonts reported (headless session?)")
	}
	seen := map[string]bool{}
	for _, name := range list.Families {
		if name == "" || name[0] == '@' || seen[name] || len([]rune(name)) >= 31 {
			t.Errorf("unexpected family %q", name)
		}
		seen[name] = true
	}
	if !seen["Segoe UI"] && !seen["Arial"] {
		t.Errorf("neither Segoe UI nor Arial listed among %d families", len(list.Families))
	}
	for _, name := range list.Symbol {
		if !seen[name] {
			t.Errorf("symbol font %q missing from the family list", name)
		}
	}

	rec := httptest.NewRecorder()
	handleSystemFonts(rec, httptest.NewRequest(http.MethodGet, "/fonts/system", nil))
	var body systemFonts
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Families) != len(list.Families) {
		t.Errorf("endpoint returned %d families (err %v), want %d", len(body.Families), err, len(list.Families))
	}
}

func TestCustomFontUploadAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fonts", "custom-font")
	old := customFontPath
	customFontPath = func() string { return path }
	defer func() { customFontPath = old }()

	server := httptest.NewServer(http.HandlerFunc(handleCustomFont))
	defer server.Close()

	do := func(method string, body []byte, origin string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL, bytes.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if got := do(http.MethodGet, nil, "").StatusCode; got != http.StatusNotFound {
		t.Errorf("GET before upload = %d, want 404", got)
	}
	if got := do(http.MethodPut, []byte("<html>not a font</html>"), server.URL).StatusCode; got != http.StatusUnsupportedMediaType {
		t.Errorf("PUT non-font = %d, want 415", got)
	}
	woff2 := append([]byte("wOF2"), make([]byte, 64)...)
	if got := do(http.MethodPut, woff2, "https://example.com").StatusCode; got != http.StatusForbidden {
		t.Errorf("PUT from a foreign origin = %d, want 403", got)
	}
	if got := do(http.MethodPut, woff2, server.URL).StatusCode; got != http.StatusNoContent {
		t.Fatalf("PUT woff2 = %d, want 204", got)
	}
	resp := do(http.MethodGet, nil, "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "font/woff2" {
		t.Errorf("GET after upload = %d %q, want 200 font/woff2", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	// Straight to the handler: over a real connection the server may hang up
	// before the client has finished sending 32 MB.
	rec := httptest.NewRecorder()
	handleCustomFont(rec, httptest.NewRequest(http.MethodPut, "/fonts/custom", bytes.NewReader(make([]byte, maxFontBytes+1))))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("PUT oversized = %d, want 413", rec.Code)
	}
	if got := do(http.MethodDelete, nil, "https://example.com").StatusCode; got != http.StatusForbidden {
		t.Errorf("DELETE from a foreign origin = %d, want 403", got)
	}
	if got := do(http.MethodDelete, nil, server.URL).StatusCode; got != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("font still on disk after DELETE: %v", err)
	}
}
