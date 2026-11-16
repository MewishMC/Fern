package modules

// The Steam line: the game Steam says is running and how long this session
// has lasted. It reads the local Steam client (its registry keys and app
// manifests), so it needs no account; a Steam Web API key, kept in the
// encrypted secrets store, adds names for apps without a manifest and the
// total playtime.

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModuleSteam is the Steam line's id.
const ModuleSteam = "steam"

// SecretSteamKey is the optional Steam Web API key.
const SecretSteamKey = "steam_key"

// steamAPIBase is the Steam Web API, a variable so tests can use a local server.
var steamAPIBase = "https://api.steampowered.com"

// SteamOptions controls the Steam line.
type SteamOptions struct {
	Template string   `json:"template"`  // "🎮 {game} · {session}"
	IdleText string   `json:"idle_text"` // shown when no game runs; empty hides the line
	Ignore   []string `json:"ignore"`    // names (contains, or /regex/) or app ids the line skips
	SteamID  string   `json:"steam_id"`  // SteamID64 for the Web API; empty uses the signed-in account
	Label    string   `json:"label"`
}

func defaultSteamOptions() SteamOptions {
	return SteamOptions{
		Template: "🎮 {game} · {session}",
		Ignore:   []string{"VRChat", "SteamVR"},
	}
}

func sanitizeSteam(o *SteamOptions) {
	o.Template = oneLineTemplate(o.Template, 120)
	if o.Template == "" {
		o.Template = "🎮 {game} · {session}"
	}
	o.IdleText = oneLineTemplate(o.IdleText, 60)
	ignore := make([]string, 0, len(o.Ignore))
	seen := map[string]bool{}
	for _, s := range o.Ignore {
		s = RuneLimit(strings.TrimSpace(s), 100)
		if s == "" || seen[strings.ToLower(s)] || len(ignore) >= 30 {
			continue
		}
		seen[strings.ToLower(s)] = true
		ignore = append(ignore, s)
	}
	o.Ignore = ignore
	o.SteamID = strings.TrimSpace(o.SteamID)
	if !steamIDPattern.MatchString(o.SteamID) {
		o.SteamID = ""
	}
	o.Label = RuneLimit(strings.TrimSpace(o.Label), 16)
}

var steamIDPattern = regexp.MustCompile(`^7656119\d{10}$`)

// steamLocal is what the local client reports.
type steamLocal struct {
	Installed bool   // Steam's registry key exists
	AppID     uint32 // the running game, 0 for none
	Path      string // Steam's folder
	Account   uint32 // the signed-in account, 0 when signed out
	RegName   string // the app's name from the registry, when Steam wrote one
}

// steamReadLocal reads the client; tests replace it.
var steamReadLocal = readSteamLocal

// steamAccountBase turns a 32-bit account id into a SteamID64.
const steamAccountBase = 76561197960265728

// SteamStatus is what the panel shows about the Steam line.
type SteamStatus struct {
	State     string `json:"state"` // off, nosteam, idle, playing, ignored
	Game      string `json:"game"`
	AppID     uint32 `json:"appId"`
	StartedMS int64  `json:"startedMs"`
	TotalMin  int    `json:"totalMin"` // -1 when unknown
	HasKey    bool   `json:"hasKey"`
	SteamID   string `json:"steamId"`
	WebError  string `json:"webError"`
	Line      string `json:"line"`
}

var (
	steamMu        sync.RWMutex
	steamInstalled bool
	steamApp       uint32
	steamStarted   time.Time
	steamName      string
	steamTotal     = -1
	steamAccount   string // SteamID64 in use
	steamNames     = map[uint32]string{}
	steamWebFor    uint32 // the app the web data belongs to
	steamWebAt     time.Time
	steamWebRetry  time.Time
	steamWebFails  int
	steamWebErr    string
	steamKeySeen   string
	steamPrimed    bool // the first look is done; games already running then fire no event
	steamWake      = make(chan struct{}, 1)
)

func startSteamWorker() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			steamTick(time.Now())
			select {
			case <-ticker.C:
			case <-steamWake:
			}
		}
	}()
}

// RefreshSteam asks the Web API again now.
func RefreshSteam() {
	steamMu.Lock()
	steamWebAt, steamWebRetry, steamWebFails, steamWebFor = time.Time{}, time.Time{}, 0, 0
	steamMu.Unlock()
	select {
	case steamWake <- struct{}{}:
	default:
	}
}

// steamTick follows the running game. It reads the registry every few
// seconds whether or not the line is on, so rules can follow games too; the
// Web API is only asked while the line is on.
func steamTick(now time.Time) {
	loc, err := steamReadLocal()
	cfg := GetConfig()
	key := GetSecret(SecretSteamKey)

	steamMu.Lock()
	steamInstalled = err == nil && loc.Installed
	if key != steamKeySeen {
		steamKeySeen = key
		steamWebAt, steamWebRetry, steamWebFails, steamWebFor, steamWebErr = time.Time{}, time.Time{}, 0, 0, ""
		steamTotal = -1
	}
	steamAccount = cfg.SteamOptions.SteamID
	if steamAccount == "" && loc.Account != 0 {
		steamAccount = strconv.FormatUint(steamAccountBase+uint64(loc.Account), 10)
	}
	app := loc.AppID
	if !steamInstalled {
		app = 0
	}
	var ended map[string]string
	switched := app != steamApp
	if switched {
		if steamApp != 0 {
			ended = map[string]string{"name": steamName, "id": strconv.FormatUint(uint64(steamApp), 10),
				"minutes": strconv.Itoa(int(now.Sub(steamStarted).Minutes()))}
		}
		steamApp, steamStarted, steamTotal = app, now, -1
		steamName = ""
		if app != 0 {
			steamName = steamNames[app]
		}
	}
	needName := app != 0 && steamName == ""
	steamMu.Unlock()

	if needName {
		name := loc.RegName
		if name == "" {
			name = steamManifestName(loc.Path, app)
		}
		steamMu.Lock()
		if steamApp == app && steamName == "" && name != "" {
			steamName = name
			steamNames[app] = name
		}
		steamMu.Unlock()
	}
	steamMu.Lock()
	primed := steamPrimed
	steamPrimed = true
	steamMu.Unlock()
	if ended != nil && primed {
		FireTrigger(TriggerSteamEnded, ended)
	}
	if switched && app != 0 && primed {
		steamMu.RLock()
		name := steamDisplayName(app, steamName)
		steamMu.RUnlock()
		FireTrigger(TriggerSteamStarted, map[string]string{"name": name, "id": strconv.FormatUint(uint64(app), 10)})
	}

	if app != 0 && key != "" && cfg.DisplayOptions.Steam {
		steamWebTick(now, app, key)
	}
}

// steamDisplayName is a game's name, or a stand-in while it is unknown.
func steamDisplayName(app uint32, name string) string {
	if name != "" {
		return name
	}
	return "Steam app " + strconv.FormatUint(uint64(app), 10)
}

// steamWebTick asks the Web API for the game's name, when the client did not
// know it, and its total playtime. Once per game, then every 15 minutes.
func steamWebTick(now time.Time, app uint32, key string) {
	steamMu.RLock()
	id := steamAccount
	due := id != "" && !now.Before(steamWebRetry) && (steamWebFor != app || now.Sub(steamWebAt) >= 15*time.Minute)
	needName := steamName == ""
	steamMu.RUnlock()
	if !due {
		return
	}
	name, total, err := fetchSteamWeb(key, id, app, needName)
	steamMu.Lock()
	defer steamMu.Unlock()
	if steamApp != app {
		return
	}
	if err != nil {
		steamWebFails++
		steamWebRetry = now.Add(time.Minute << min(steamWebFails-1, 4))
		steamWebErr = err.Error()
		return
	}
	steamWebFails, steamWebErr, steamWebRetry = 0, "", time.Time{}
	steamWebFor, steamWebAt = app, now
	if name != "" && steamName == "" {
		steamName = name
		steamNames[app] = name
	}
	steamTotal = total
}

// steamGetJSON is getJSON for URLs that carry the key: errors never include
// the address.
func steamGetJSON(u string, out any) (int, error) {
	status, _, err := getJSON(u, out)
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return status, errors.New("Steam timed out")
		}
		return status, errors.New("couldn't reach Steam")
	}
	if status == 401 || status == 403 {
		return status, errors.New("Steam refused the key")
	}
	if err != nil {
		return status, err
	}
	return status, nil
}

// fetchSteamWeb reads the current game's name (from the player summary) and
// its total playtime in minutes (from recently played games), -1 if unknown.
func fetchSteamWeb(key, steamID string, app uint32, needName bool) (string, int, error) {
	name := ""
	if needName {
		v := url.Values{}
		v.Set("key", key)
		v.Set("steamids", steamID)
		var sum struct {
			Response struct {
				Players []struct {
					Game   string `json:"gameextrainfo"`
					GameID string `json:"gameid"`
				} `json:"players"`
			} `json:"response"`
		}
		if _, err := steamGetJSON(steamAPIBase+"/ISteamUser/GetPlayerSummaries/v2/?"+v.Encode(), &sum); err != nil {
			return "", -1, err
		}
		for _, p := range sum.Response.Players {
			if p.GameID == strconv.FormatUint(uint64(app), 10) {
				name = p.Game
			}
		}
	}
	v := url.Values{}
	v.Set("key", key)
	v.Set("steamid", steamID)
	var recent struct {
		Response struct {
			Games []struct {
				AppID    uint32 `json:"appid"`
				Name     string `json:"name"`
				Playtime int    `json:"playtime_forever"`
			} `json:"games"`
		} `json:"response"`
	}
	if _, err := steamGetJSON(steamAPIBase+"/IPlayerService/GetRecentlyPlayedGames/v1/?"+v.Encode(), &recent); err != nil {
		return name, -1, err
	}
	total := -1
	for _, g := range recent.Response.Games {
		if g.AppID == app {
			total = g.Playtime
			if name == "" {
				name = g.Name
			}
		}
	}
	return name, total, nil
}

var (
	vdfPathPattern = regexp.MustCompile(`"path"\s+"((?:[^"\\]|\\.)*)"`)
	acfNamePattern = regexp.MustCompile(`"name"\s+"((?:[^"\\]|\\.)*)"`)
)

// vdfUnescape undoes the backslash escapes of Valve's text format.
func vdfUnescape(s string) string {
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, " ", `\t`, " ").Replace(s)
}

// steamLibraries lists the steamapps folders: Steam's own and every library
// in libraryfolders.vdf.
func steamLibraries(steamPath string) []string {
	if steamPath == "" {
		return nil
	}
	main := filepath.Join(filepath.Clean(steamPath), "steamapps")
	libs := []string{main}
	data, err := os.ReadFile(filepath.Join(main, "libraryfolders.vdf"))
	if err != nil {
		return libs
	}
	for _, m := range vdfPathPattern.FindAllStringSubmatch(string(data), 64) {
		dir := filepath.Join(filepath.Clean(vdfUnescape(m[1])), "steamapps")
		if !strings.EqualFold(dir, main) {
			libs = append(libs, dir)
		}
	}
	return libs
}

// steamManifestName reads a game's name from its appmanifest_<id>.acf.
func steamManifestName(steamPath string, app uint32) string {
	file := "appmanifest_" + strconv.FormatUint(uint64(app), 10) + ".acf"
	for _, lib := range steamLibraries(steamPath) {
		data, err := os.ReadFile(filepath.Join(lib, file))
		if err != nil {
			continue
		}
		if m := acfNamePattern.FindStringSubmatch(string(data)); m != nil {
			return strings.Join(strings.Fields(vdfUnescape(m[1])), " ")
		}
	}
	return ""
}

// steamIgnored reports whether the line skips a game.
func steamIgnored(o SteamOptions, app uint32, name string) bool {
	id := strconv.FormatUint(uint64(app), 10)
	for _, pat := range o.Ignore {
		if pat == id || (name != "" && MatchText(pat, name)) {
			return true
		}
	}
	return false
}

// formatPlaytime writes minutes as "42m" or "3h 05m".
func formatPlaytime(d time.Duration) string {
	mins := int(d / time.Minute)
	if mins < 60 {
		return strconv.Itoa(max(mins, 0)) + "m"
	}
	return strconv.Itoa(mins/60) + "h " + twoDigits(mins%60) + "m"
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// emptyBrackets are left behind by a placeholder with no value.
var emptyBrackets = strings.NewReplacer("()", "", "[]", "", "( )", "", "[ ]", "")

// tidyTemplate cleans a filled template: no doubled spaces, no separators
// left dangling at either end or twice in a row by an empty placeholder.
func tidyTemplate(s string) string {
	s = strings.Join(strings.Fields(emptyBrackets.Replace(s)), " ")
	for _, sep := range []string{"·", "|", "—", "-", ","} {
		double := " " + sep + " " + sep + " "
		for strings.Contains(s, double) {
			s = strings.ReplaceAll(s, double, " "+sep+" ")
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), sep))
		s = strings.TrimSpace(strings.TrimPrefix(s, sep))
	}
	return s
}

// formatSteam fills in the template.
func formatSteam(o SteamOptions, name string, session time.Duration, totalMin int) string {
	total := ""
	if totalMin >= 0 {
		total = strconv.Itoa(totalMin/60) + "h"
		if totalMin < 60 {
			total = strconv.Itoa(totalMin) + "m"
		}
	}
	r := strings.NewReplacer("{game}", name, "{session}", formatPlaytime(session), "{total}", total)
	return tidyTemplate(r.Replace(o.Template))
}

// steamText is the line's text at now.
func steamText(cfg Config, now time.Time) string {
	if !cfg.DisplayOptions.Steam {
		return ""
	}
	o := cfg.SteamOptions
	steamMu.RLock()
	app, name, started, total := steamApp, steamName, steamStarted, steamTotal
	steamMu.RUnlock()
	if app == 0 || steamIgnored(o, app, name) {
		return o.IdleText
	}
	return formatSteam(o, steamDisplayName(app, name), now.Sub(started), total)
}

func steamLine(cfg Config) string {
	return withLabel(cfg.SteamOptions.Label, steamText(cfg, time.Now()))
}

// CurrentSteamStatus reports the Steam line for the panel.
func CurrentSteamStatus() SteamStatus {
	cfg := GetConfig()
	steamMu.RLock()
	st := SteamStatus{AppID: steamApp, TotalMin: steamTotal, SteamID: steamAccount, WebError: steamWebErr, HasKey: steamKeySeen != ""}
	installed := steamInstalled
	if steamApp != 0 {
		st.Game = steamDisplayName(steamApp, steamName)
		st.StartedMS = steamStarted.UnixMilli()
	}
	steamMu.RUnlock()
	switch {
	case !cfg.DisplayOptions.Steam:
		st.State = "off"
	case !installed:
		st.State = "nosteam"
	case st.AppID == 0:
		st.State = "idle"
	case steamIgnored(cfg.SteamOptions, st.AppID, st.Game):
		st.State = "ignored"
	default:
		st.State = "playing"
	}
	st.Line = steamText(cfg, time.Now())
	return st
}

// Rule trigger ids for Steam.
const (
	TriggerSteamPlaying = "steam.playing"
	TriggerSteamStarted = "steam.started"
	TriggerSteamEnded   = "steam.ended"
)

func init() {
	lineLabels[ModuleSteam] = "Steam"
	panelSecrets[SecretSteamKey] = true
	gameField := RuleField{Key: "game", Label: "Game", Type: FieldText, Placeholder: "any game", Hint: "Name or app id. Contains, or /regex/. Empty matches every game.", MaxLen: 200}
	matchGame := func(pat, name, id string) bool {
		pat = strings.TrimSpace(pat)
		return pat == "" || pat == id || MatchText(pat, name)
	}
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSteamPlaying, Label: "Playing on Steam", Category: "Steam",
		Description: "While Steam says a matching game is running",
		Fields:      []RuleField{gameField},
		Check: func(p RuleParams) bool {
			steamMu.RLock()
			app, name := steamApp, steamName
			steamMu.RUnlock()
			return app != 0 && matchGame(p.String("game"), steamDisplayName(app, name), strconv.FormatUint(uint64(app), 10))
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSteamStarted, Label: "Steam game started", Category: "Steam",
		Description: "When Steam starts a matching game. {name} in a chat action is the game.",
		Fields:      []RuleField{gameField},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return matchGame(p.String("game"), ev.Data["name"], ev.Data["id"])
		},
	}))
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerSteamEnded, Label: "Steam game closed", Category: "Steam",
		Description: "When a matching Steam game stops. {name} in a chat action is the game.",
		Fields:      []RuleField{gameField},
		Match: func(p RuleParams, ev RuleEvent) bool {
			return matchGame(p.String("game"), ev.Data["name"], ev.Data["id"])
		},
	}))
}
