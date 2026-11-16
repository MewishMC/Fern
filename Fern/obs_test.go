package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"Fern/UI"
)

// fakeOverlayText swaps the overlay's source for a settable string.
func fakeOverlayText(t *testing.T) func(string) {
	t.Helper()
	var mu sync.Mutex
	text := ""
	old := overlaySource
	overlaySource = func() string { mu.Lock(); defer mu.Unlock(); return text }
	overlayCache.Lock()
	overlayCache.at = time.Time{}
	overlayCache.Unlock()
	t.Cleanup(func() {
		overlaySource = old
		overlayCache.Lock()
		overlayCache.at = time.Time{}
		overlayCache.Unlock()
	})
	return func(s string) { mu.Lock(); text = s; mu.Unlock() }
}

func overlayMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/overlay", handleOverlay)
	mux.HandleFunc("/overlay/text", handleOverlayText)
	mux.HandleFunc("/overlay/stream", handleOverlayStream)
	return mux
}

func TestOverlayPageAndText(t *testing.T) {
	set := fakeOverlayText(t)
	set("hello\nworld")
	srv := httptest.NewServer(overlayMux())
	defer srv.Close()
	// httptest listens on 127.0.0.1, which counts as loopback.
	resp, err := http.Get(srv.URL + "/overlay?size=40")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != UI.OverlayPage || !strings.Contains(string(body), "/overlay/stream") {
		t.Errorf("page: %d", resp.StatusCode)
	}
	if strings.Contains(string(body), "http://") || strings.Contains(string(body), "https://") {
		t.Error("the overlay page makes an external request")
	}

	resp, err = http.Get(srv.URL + "/overlay/text")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	if got["text"] != "hello\nworld" {
		t.Errorf("text: %v", got)
	}
}

func TestOverlayRejectsForeignHosts(t *testing.T) {
	fakeOverlayText(t)
	for _, path := range []string{"/overlay", "/overlay/text", "/overlay/stream"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "evil.example:1932"
		rec := httptest.NewRecorder()
		overlayMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a foreign host: %d", path, rec.Code)
		}
		req = httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "127.0.0.1:1932"
		req.Header.Set("Origin", "https://example.com")
		rec = httptest.NewRecorder()
		overlayMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a foreign origin: %d", path, rec.Code)
		}
		req = httptest.NewRequest(http.MethodPost, path, nil)
		req.Host = "127.0.0.1:1932"
		rec = httptest.NewRecorder()
		overlayMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: %d", path, rec.Code)
		}
	}
}

func TestOverlayStreamFollowsTheChatbox(t *testing.T) {
	set := fakeOverlayText(t)
	set("first")
	srv := httptest.NewServer(overlayMux())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/overlay/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				lines <- data
			}
		}
		close(lines)
	}()
	next := func() string {
		t.Helper()
		select {
		case l := <-lines:
			var m map[string]string
			_ = json.Unmarshal([]byte(l), &m)
			return m["text"]
		case <-time.After(3 * time.Second):
			t.Fatal("no event")
			return ""
		}
	}
	if got := next(); got != "first" {
		t.Errorf("first event %q", got)
	}
	set("second line")
	if got := next(); got != "second line" {
		t.Errorf("after a change %q", got)
	}
	set("")
	if got := next(); got != "" {
		t.Errorf("after clearing %q", got)
	}
}
