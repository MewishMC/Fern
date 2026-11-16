package main

// Fern asks GitHub for the latest release and, when it is newer than this
// build, offers an update in the panel. Applying it hands over to update.exe
// (which swaps Fern.exe and starts it again) and quits, because a running
// Windows executable can't replace itself.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	latestReleaseAPI = "https://api.github.com/repos/MewishMC/Fern/releases/latest"
	updaterName      = "update.exe"
	updateInterval   = 6 * time.Hour
)

var updateClient = &http.Client{Timeout: 30 * time.Second}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Name    string    `json:"name"` // release title, e.g. "Fern 2.1 - Spring cleaning"
	Body    string    `json:"body"` // the release notes, from CHANGELOG.md
	HTMLURL string    `json:"html_url"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	Digest      string `json:"digest"` // "sha256:<hex>"
}

func (r *ghRelease) asset(name string) *ghAsset {
	for i := range r.Assets {
		if strings.EqualFold(r.Assets[i].Name, name) {
			return &r.Assets[i]
		}
	}
	return nil
}

// updateState is what the panel shows about updates.
type updateState struct {
	Available bool   `json:"available"`
	Latest    string `json:"latest"`
	Name      string `json:"name"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`
	Error     string `json:"error"`
	Busy      bool   `json:"busy"`
}

var (
	updateMu      sync.Mutex
	latest        *ghRelease
	updateErr     string
	updateRunning bool
)

func currentUpdate() updateState {
	updateMu.Lock()
	defer updateMu.Unlock()
	st := updateState{Error: updateErr, Busy: updateRunning}
	if latest != nil && newerVersion(latest.TagName, appVersion) {
		st.Available = true
		st.Latest = strings.TrimPrefix(latest.TagName, "v")
		st.Name = latest.Name
		st.Notes = latest.Body
		st.URL = latest.HTMLURL
	}
	return st
}

// watchUpdates checks at startup and then every few hours. Builds run with
// `go run` live in a temporary folder and are never offered updates.
func watchUpdates() {
	if exe, err := os.Executable(); err != nil || strings.Contains(exe, "go-build") {
		return
	}
	for {
		rel, err := fetchLatestRelease(releaseAPI())
		updateMu.Lock()
		if err == nil {
			latest = rel
		} else {
			fmt.Println("update check:", err)
		}
		updateMu.Unlock()
		time.Sleep(updateInterval)
	}
}

// releaseAPI can be pointed at a local fake with FERN_RELEASE_API to try the
// update flow without publishing a release.
func releaseAPI() string {
	if v := os.Getenv("FERN_RELEASE_API"); v != "" {
		return v
	}
	return latestReleaseAPI
}

func fetchLatestRelease(apiURL string) (*ghRelease, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Fern-updater")
	res, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, nil // no release published yet: nothing to offer
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("checking for a release failed: %s", res.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if rel.asset("Fern.exe") == nil {
		return nil, nil // a release without a build can't be installed
	}
	return &rel, nil
}

// newerVersion reports whether tag (like "v2.1") is a later version than
// current (like "2.0"). Missing parts count as zero, so 2.1 == 2.1.0.
func newerVersion(tag, current string) bool {
	a, okA := versionParts(tag)
	b, okB := versionParts(current)
	if !okA || !okB {
		return false
	}
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func versionParts(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// startUpdate makes sure update.exe sits next to Fern.exe (fetching it from
// the release if it is missing), starts it, and exits so it can swap the file.
func startUpdate() {
	updateMu.Lock()
	rel := latest
	if updateRunning || rel == nil {
		updateMu.Unlock()
		return
	}
	updateRunning = true
	updateErr = ""
	updateMu.Unlock()

	fail := func(err error) {
		updateMu.Lock()
		updateRunning = false
		updateErr = err.Error()
		updateMu.Unlock()
	}

	exe, err := os.Executable()
	if err != nil {
		fail(err)
		return
	}
	updater, err := ensureUpdater(rel, filepath.Dir(exe))
	if err != nil {
		fail(fmt.Errorf("could not get the updater: %w", err))
		return
	}
	backupBeforeUpdate()
	if _, err := os.StartProcess(updater, []string{updater, exe}, &os.ProcAttr{
		Dir:   filepath.Dir(exe),
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}); err != nil {
		fail(fmt.Errorf("could not start the updater: %w", err))
		return
	}
	// Give the socket a moment to tell the panel, then get out of the way.
	time.Sleep(300 * time.Millisecond)
	os.Exit(0)
}

// ensureUpdater returns the path of update.exe in dir, downloading it from
// the release first when it isn't there.
func ensureUpdater(rel *ghRelease, dir string) (string, error) {
	path := filepath.Join(dir, updaterName)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	a := rel.asset(updaterName)
	if a == nil {
		return "", fmt.Errorf("%s is missing and release %s doesn't include it", updaterName, rel.TagName)
	}
	req, err := http.NewRequest(http.MethodGet, a.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Fern-updater")
	res, err := updateClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", res.Status)
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("downloaded updater is empty")
	}
	sum := sha256.Sum256(data)
	if want := strings.TrimPrefix(strings.ToLower(a.Digest), "sha256:"); want != "" && want != hex.EncodeToString(sum[:]) {
		return "", errors.New("downloaded updater does not match its published checksum")
	}
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return "", err
	}
	return path, nil
}
