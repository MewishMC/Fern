package modules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

// The first-run setup: the panel shows it while AppOptions.SetupDone is off.
// It checks that VRChat's OSC looks enabled, sends a test message and turns on
// a starter set of lines. Everything here is cheap enough for the state push.

// keepSetupForOldConfig marks the setup as done for a config file written
// before the setup existed, so people who already use Fern are not walked
// through it again after an update. Only a brand new config starts with it.
func keepSetupForOldConfig(data []byte, c *Config) {
	var probe struct {
		App struct {
			SetupDone *bool `json:"setup_done"`
		} `json:"AppOptions"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.App.SetupDone == nil {
		c.AppOptions.SetupDone = true
	}
}

// FinishSetup records that the setup was finished or skipped.
func FinishSetup() {
	if GetConfig().AppOptions.SetupDone {
		return
	}
	UpdateConfig(func(c *Config) { c.AppOptions.SetupDone = true })
}

// StarterLines are the lines the setup offers, in the order it lists them.
// The app integrations are left out: they need signing in or another app.
var StarterLines = []string{
	ModuleTime, ModuleMedia, ModuleLyrics, ModuleText, ModuleLoading, ModuleAFK,
	ModuleWorld, ModuleLogs, ModuleHeart, ModuleWeather,
	ModuleCPU, ModuleGPU, ModuleMemory, ModuleNetwork, ModuleWindow,
	ModuleBattery, ModuleVRPerf,
}

// ApplyStarterLines turns on exactly the starter lines in ids, turns the
// other starter lines off and switches the display on. Lines the setup does
// not offer, and unknown ids, are left alone. It returns the lines it turned on.
func ApplyStarterLines(ids []string) ([]string, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.ToLower(strings.TrimSpace(id))] = true
	}
	var on []string
	for _, id := range StarterLines {
		if want[id] {
			on = append(on, id)
		}
	}
	if len(on) == 0 {
		return nil, errors.New("pick at least one line")
	}
	UpdateConfig(func(c *Config) { applyStarterLines(&c.DisplayOptions, want) })
	return on, nil
}

func applyStarterLines(d *DisplayOptions, want map[string]bool) {
	for _, id := range StarterLines {
		if flag := moduleFlag(d, id); flag != nil {
			*flag = want[id]
		}
	}
	d.Show = true
}

// SetupStatus is what the setup knows about VRChat's OSC on this PC.
type SetupStatus struct {
	Done bool `json:"done"`
	// OSCFolder is VRChat's OSC folder (LocalLow\VRChat\VRChat\OSC). VRChat
	// makes it, with a file per avatar, once OSC has been on.
	OSCFolder     bool   `json:"oscFolder"`
	OSCFolderPath string `json:"oscFolderPath"`
	AvatarConfigs int    `json:"avatarConfigs"` // avatar files VRChat wrote there
	LastConfigMS  int64  `json:"lastConfigMs"`  // when the newest one was written, unix ms
	// OSCSetting is VRChat's own OSC switch as it saved it: "on", "off", or
	// "" when it could not be read.
	OSCSetting string `json:"oscSetting"`
}

type oscScan struct {
	exists  bool
	configs int
	newest  time.Time
}

var (
	setupMu     sync.Mutex
	setupAt     time.Time
	setupCached SetupStatus
)

// setupCacheFor keeps the folder walk and the registry read to one every few
// seconds, since the state is pushed several times a second.
const setupCacheFor = 3 * time.Second

// CurrentSetup reports the setup's checks.
func CurrentSetup() SetupStatus {
	done := GetConfig().AppOptions.SetupDone
	setupMu.Lock()
	defer setupMu.Unlock()
	if setupAt.IsZero() || time.Since(setupAt) > setupCacheFor {
		dir := filepath.Join(logDirectory(), "OSC")
		scan := scanOSCFolder(dir)
		setupCached = SetupStatus{
			OSCFolder:     scan.exists,
			OSCFolderPath: dir,
			AvatarConfigs: scan.configs,
			OSCSetting:    vrchatOSCSetting(),
		}
		if !scan.newest.IsZero() {
			setupCached.LastConfigMS = scan.newest.UnixMilli()
		}
		setupAt = time.Now()
	}
	st := setupCached
	st.Done = done
	return st
}

// scanOSCFolder counts the avatar files under dir\usr_*\Avatars.
func scanOSCFolder(dir string) oscScan {
	var s oscScan
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return s
	}
	s.exists = true
	users, err := os.ReadDir(dir)
	if err != nil {
		return s
	}
	for _, user := range users {
		if !user.IsDir() || !strings.HasPrefix(strings.ToLower(user.Name()), "usr_") {
			continue
		}
		avatars, err := os.ReadDir(filepath.Join(dir, user.Name(), "Avatars"))
		if err != nil {
			continue
		}
		for _, a := range avatars {
			if a.IsDir() || !strings.EqualFold(filepath.Ext(a.Name()), ".json") {
				continue
			}
			s.configs++
			if info, err := a.Info(); err == nil && info.ModTime().After(s.newest) {
				s.newest = info.ModTime()
			}
		}
	}
	return s
}

// vrchatOSCPref is the name Unity gives VRChat's OSC switch in the registry:
// the PlayerPrefs key with a hash of it appended.
var vrchatOSCPref = regexp.MustCompile(`(?i)^VRC_INPUT_OSC_h\d+$`)

// vrchatOSCSetting reads VRChat's saved OSC switch from its PlayerPrefs.
func vrchatOSCSetting() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\VRChat\VRChat`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	names, err := key.ReadValueNames(-1)
	if err != nil {
		return ""
	}
	return oscSettingFrom(names, func(name string) (uint64, error) {
		v, _, err := key.GetIntegerValue(name)
		return v, err
	})
}

// oscSettingFrom picks VRChat's OSC switch out of its PlayerPrefs values.
func oscSettingFrom(names []string, get func(string) (uint64, error)) string {
	for _, name := range names {
		if !vrchatOSCPref.MatchString(name) {
			continue
		}
		v, err := get(name)
		if err != nil {
			return ""
		}
		if v != 0 {
			return "on"
		}
		return "off"
	}
	return ""
}
