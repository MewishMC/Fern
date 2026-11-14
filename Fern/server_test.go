package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestSocketRejectsForeignOrigins(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(handleSocket))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")

	for origin, wantOK := range map[string]bool{
		server.URL:            true,
		"https://example.com": false,
		"http://127.0.0.1:1":  false,
	} {
		header := http.Header{"Origin": {origin}}
		conn, resp, err := websocket.DefaultDialer.Dial(url, header)
		if conn != nil {
			conn.Close()
		}
		if gotOK := err == nil; gotOK != wantOK {
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			t.Errorf("origin %s: connected=%v (status %d), want %v", origin, gotOK, status, wantOK)
		}
	}
}
