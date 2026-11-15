package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v2.1", "2.0", true},
		{"v2.0", "2.0", false},
		{"v2.0.0", "2.0", false},
		{"v2.0.1", "2.0", true},
		{"v1.9", "2.0", false},
		{"v10.0", "9.9", true},
		{"2.1", "2.0", true},
		{"nightly", "2.0", false},
		{"v2.1", "dev", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.tag, c.current); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func releaseServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestFetchLatestRelease(t *testing.T) {
	if rel, err := fetchLatestRelease(releaseServer(t, 404, "")); rel != nil || err != nil {
		t.Errorf("no release: got %v, %v; want nothing to offer and no error", rel, err)
	}
	if rel, err := fetchLatestRelease(releaseServer(t, 200, `{"tag_name":"v3","assets":[{"name":"notes.txt"}]}`)); rel != nil || err != nil {
		t.Errorf("release without Fern.exe: got %v, %v; want nothing to offer", rel, err)
	}
	if _, err := fetchLatestRelease(releaseServer(t, 403, "")); err == nil {
		t.Error("rate limited: want an error")
	}
	rel, err := fetchLatestRelease(releaseServer(t, 200, `{"tag_name":"v3","assets":[{"name":"Fern.exe","browser_download_url":"x"}]}`))
	if err != nil || rel == nil || rel.TagName != "v3" {
		t.Fatalf("got %v, %v", rel, err)
	}
}

func TestCurrentUpdateCarriesReleaseNotes(t *testing.T) {
	updateMu.Lock()
	saved := latest
	latest = &ghRelease{TagName: "v99.0", Name: "Fern 99.0 - Moonlight", Body: "- Added a thing", HTMLURL: "https://example.invalid/r"}
	updateMu.Unlock()
	t.Cleanup(func() { updateMu.Lock(); latest = saved; updateMu.Unlock() })

	st := currentUpdate()
	if !st.Available || st.Latest != "99.0" || st.Name != "Fern 99.0 - Moonlight" || st.Notes != "- Added a thing" || st.URL == "" {
		t.Errorf("got %+v", st)
	}
}

func TestEnsureUpdaterDownloadsWhenMissing(t *testing.T) {
	payload := []byte("updater build")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()
	rel := &ghRelease{TagName: "v3", Assets: []ghAsset{{Name: "update.exe", DownloadURL: srv.URL, Digest: "sha256:" + hex.EncodeToString(sum[:])}}}

	dir := t.TempDir()
	path, err := ensureUpdater(rel, dir)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(payload) {
		t.Errorf("updater content = %q", data)
	}

	// A wrong checksum must not leave a file behind.
	rel.Assets[0].Digest = "sha256:00"
	other := t.TempDir()
	if _, err := ensureUpdater(rel, other); err == nil {
		t.Error("checksum mismatch: want an error")
	}
	if _, err := os.Stat(filepath.Join(other, "update.exe")); !os.IsNotExist(err) {
		t.Error("mismatched updater was written")
	}
}

func TestEnsureUpdaterKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "update.exe")
	if err := os.WriteFile(existing, []byte("mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := ensureUpdater(&ghRelease{TagName: "v3"}, dir)
	if err != nil || path != existing {
		t.Fatalf("got %q, %v", path, err)
	}
}
