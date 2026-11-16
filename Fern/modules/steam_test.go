package modules

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTidyTemplate(t *testing.T) {
	for in, want := range map[string]string{
		"🎮 Game · 1h 05m":  "🎮 Game · 1h 05m",
		"🎮 Game ·  · 42m":  "🎮 Game · 42m",
		"🎮 Game · 42m · ":  "🎮 Game · 42m",
		"Game (42m) ()":    "Game (42m)",
		" · Game":          "Game",
		"Half-Life - 3h |": "Half-Life - 3h",
	} {
		if got := tidyTemplate(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestFormatSteam(t *testing.T) {
	o := defaultSteamOptions()
	if got := formatSteam(o, "Portal 2", 65*time.Minute, -1); got != "🎮 Portal 2 · 1h 05m" {
		t.Errorf("default %q", got)
	}
	o.Template = "{game} · {session} · {total} total"
	if got := formatSteam(o, "Portal 2", 5*time.Minute, 125*60); got != "Portal 2 · 5m · 125h total" {
		t.Errorf("total %q", got)
	}
	o.Template = "{game} ({total})"
	if got := formatSteam(o, "Portal 2", 0, -1); got != "Portal 2" {
		t.Errorf("missing total %q", got)
	}
}

func TestSteamIgnoredAndSanitize(t *testing.T) {
	o := defaultSteamOptions()
	if !steamIgnored(o, 438100, "VRChat") || steamIgnored(o, 620, "Portal 2") {
		t.Error("default ignore list")
	}
	o.Ignore = []string{"620"}
	if !steamIgnored(o, 620, "") {
		t.Error("app id not ignored")
	}
	s := SteamOptions{SteamID: "123", Ignore: []string{" a ", "A", ""}, Template: ""}
	sanitizeSteam(&s)
	if s.SteamID != "" || len(s.Ignore) != 1 || s.Template == "" {
		t.Errorf("sanitized %+v", s)
	}
	s.SteamID = "76561198000000000"
	sanitizeSteam(&s)
	if s.SteamID != "76561198000000000" {
		t.Error("valid SteamID dropped")
	}
}

func TestSteamLibrariesAndManifest(t *testing.T) {
	root := t.TempDir()
	steam := filepath.Join(root, "Steam")
	lib := filepath.Join(root, "Games")
	for _, d := range []string{filepath.Join(steam, "steamapps"), filepath.Join(lib, "steamapps")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	vdf := "\"libraryfolders\"\n{\n\t\"0\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(steam, `\`, `\\`) + "\"\n\t}\n\t\"1\"\n\t{\n\t\t\"path\"\t\t\"" + strings.ReplaceAll(lib, `\`, `\\`) + "\"\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(steam, "steamapps", "libraryfolders.vdf"), []byte(vdf), 0o600); err != nil {
		t.Fatal(err)
	}
	acf := "\"AppState\"\n{\n\t\"appid\"\t\t\"620\"\n\t\"name\"\t\t\"Portal  2 \\\"Deluxe\\\"\"\n}\n"
	if err := os.WriteFile(filepath.Join(lib, "steamapps", "appmanifest_620.acf"), []byte(acf), 0o600); err != nil {
		t.Fatal(err)
	}
	if libs := steamLibraries(steam); len(libs) != 2 {
		t.Errorf("libraries %q", libs)
	}
	if got := steamManifestName(steam, 620); got != `Portal 2 "Deluxe"` {
		t.Errorf("manifest name %q", got)
	}
	if got := steamManifestName(steam, 999); got != "" {
		t.Errorf("missing manifest gave %q", got)
	}
}

func resetSteam(t *testing.T) {
	t.Helper()
	reset := func() {
		steamMu.Lock()
		steamInstalled, steamApp, steamStarted, steamName, steamTotal = false, 0, time.Time{}, "", -1
		steamNames = map[uint32]string{}
		steamWebFor, steamWebAt, steamWebRetry, steamWebFails, steamWebErr = 0, time.Time{}, time.Time{}, 0, ""
		steamKeySeen, steamPrimed, steamAccount = "", false, ""
		steamMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
	old := steamReadLocal
	t.Cleanup(func() { steamReadLocal = old })
	secretsMu.Lock()
	oldCache, oldLoaded := secretsCache, secretsLoaded
	secretsCache, secretsLoaded = map[string]string{}, true
	secretsMu.Unlock()
	t.Cleanup(func() {
		secretsMu.Lock()
		secretsCache, secretsLoaded = oldCache, oldLoaded
		secretsMu.Unlock()
	})
}

func TestSteamTickFollowsTheGame(t *testing.T) {
	resetSteam(t)
	local := steamLocal{Installed: true}
	steamReadLocal = func() (steamLocal, error) { return local, nil }
	c := DefaultConfig()
	c.DisplayOptions.Steam = true
	useConfig(t, c)
	now := time.Now()

	steamTick(now)
	if st := CurrentSteamStatus(); st.State != "idle" || st.Line != "" {
		t.Errorf("idle status %+v", st)
	}
	local.AppID, local.RegName = 620, "Portal 2"
	steamTick(now.Add(time.Minute))
	if got := steamText(c, now.Add(time.Minute+42*time.Minute)); got != "🎮 Portal 2 · 42m" {
		t.Errorf("line %q", got)
	}
	// The same game keeps its session start.
	steamTick(now.Add(2 * time.Minute))
	if got := steamText(c, now.Add(61*time.Minute)); got != "🎮 Portal 2 · 1h 00m" {
		t.Errorf("session restarted: %q", got)
	}
	local.AppID = 438100
	local.RegName = "VRChat"
	steamTick(now.Add(3 * time.Minute))
	if st := CurrentSteamStatus(); st.State != "ignored" || st.Line != "" {
		t.Errorf("ignored status %+v", st)
	}
	c.SteamOptions.IdleText = "not gaming"
	if got := steamText(c, now); got != "not gaming" {
		t.Errorf("idle text %q", got)
	}
	steamReadLocal = func() (steamLocal, error) { return steamLocal{}, os.ErrNotExist }
	steamTick(now.Add(4 * time.Minute))
	if st := CurrentSteamStatus(); st.State != "nosteam" || st.AppID != 0 {
		t.Errorf("no Steam %+v", st)
	}
}

func TestSteamWebAPI(t *testing.T) {
	resetSteam(t)
	var sumHits, recentHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("key") != "k3y" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/ISteamUser/GetPlayerSummaries/v2/":
			sumHits.Add(1)
			if q.Get("steamids") != "76561197960265738" {
				t.Errorf("steamids %q", q.Get("steamids"))
			}
			_, _ = w.Write([]byte(`{"response":{"players":[{"gameextrainfo":"Space Game","gameid":"4242"}]}}`))
		case "/IPlayerService/GetRecentlyPlayedGames/v1/":
			recentHits.Add(1)
			_, _ = w.Write([]byte(`{"response":{"games":[{"appid":4242,"name":"Space Game","playtime_forever":600}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := steamAPIBase
	steamAPIBase = srv.URL
	t.Cleanup(func() { steamAPIBase = old })

	steamReadLocal = func() (steamLocal, error) {
		return steamLocal{Installed: true, AppID: 4242, Account: 10}, nil
	}
	c := DefaultConfig()
	c.DisplayOptions.Steam = true
	c.SteamOptions.Template = "{game} · {total}"
	useConfig(t, c)
	secretsMu.Lock()
	secretsCache[SecretSteamKey] = "k3y"
	secretsMu.Unlock()
	now := time.Now()
	steamTick(now)
	if got := steamText(c, now); got != "Space Game · 10h" {
		t.Errorf("line %q", got)
	}
	steamTick(now.Add(time.Minute))
	if sumHits.Load() != 1 || recentHits.Load() != 1 {
		t.Errorf("asked again too soon: %d %d", sumHits.Load(), recentHits.Load())
	}
	// A refused key is reported without the key or the address.
	secretsMu.Lock()
	secretsCache[SecretSteamKey] = "wrong-key"
	secretsMu.Unlock()
	steamTick(now.Add(2 * time.Minute))
	st := CurrentSteamStatus()
	if st.WebError == "" || strings.Contains(st.WebError, "wrong-key") || strings.Contains(st.WebError, "http") {
		t.Errorf("web error %q", st.WebError)
	}
	if !st.HasKey || st.SteamID != "76561197960265738" {
		t.Errorf("status %+v", st)
	}
}
