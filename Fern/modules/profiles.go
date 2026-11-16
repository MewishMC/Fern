package modules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Profiles are named snapshots of the chatbox setup: which lines are on, their
// order and drop order, every line's settings and the custom messages. They
// leave out what belongs to this PC rather than to a look: secrets and
// sign-ins, the window and tray, where the chatbox is sent, the speech and AI
// tools, and running state such as the manual AFK switch.
//
// While a profile is active every settings change is saved into it as well, so
// switching away and back returns to exactly what was last shown. Profiles are
// kept in profiles.json next to config.json, never inside it, so the panel's
// whole-config writes cannot overwrite them.

// MaxProfiles is how many profiles can be kept.
const MaxProfiles = 30

const (
	maxProfileName = 40
	// maxAppearanceBytes caps a profile's saved panel look. Backdrops are data
	// URLs, so a photo can be a few megabytes.
	maxAppearanceBytes = 6 << 20
)

// Profile event kinds passed to OnProfileChange listeners.
const (
	// ProfileSwitched: the active profile changed, or the same one was applied
	// again. Active is the new one ("" for none), Previous the old one.
	ProfileSwitched = "switched"
	// ProfileListChanged: a profile was created, renamed, duplicated, deleted,
	// overwritten, or had its shortcut or appearance changed.
	ProfileListChanged = "list"
)

// Profile is one stored profile.
type Profile struct {
	Name    string `json:"name"`
	Hotkey  string `json:"hotkey,omitempty"` // canonical, as ParseHotkey writes it
	Created int64  `json:"created"`          // unix ms
	Updated int64  `json:"updated"`          // unix ms
	// Settings is the Config snapshot with the per-PC parts left out.
	Settings json.RawMessage `json:"settings"`
	// Appearance is the panel's own look (an Appearance page export), when
	// the profile carries one.
	Appearance json.RawMessage `json:"appearance,omitempty"`
}

// ProfileInfo describes a profile for the panel and other features.
type ProfileInfo struct {
	Name          string `json:"name"`
	Hotkey        string `json:"hotkey"`
	Active        bool   `json:"active"`
	HasAppearance bool   `json:"hasAppearance"`
	Updated       int64  `json:"updated"`
}

// ProfileEvent is passed to OnProfileChange listeners.
type ProfileEvent struct {
	Kind     string // ProfileSwitched or ProfileListChanged
	Active   string // the active profile after the change; "" for none
	Previous string // the active profile before the change
}

// ProfileHotkey is a profile's global shortcut.
type ProfileHotkey struct {
	Name   string
	Hotkey string
}

type profileStore struct {
	Version  int       `json:"version"`
	Active   string    `json:"active"`
	Profiles []Profile `json:"profiles"`
}

var (
	profilesMu     sync.Mutex
	profiles       profileStore
	profilesLoaded bool
	profilesFile   string // overridden by tests
	profileSwitch  uint64 // counts switches, so the panel can apply a profile's look once

	listenersMu  sync.Mutex
	listeners    = map[int]func(ProfileEvent){}
	nextListener int
)

// profileSkipSections are the top-level config sections a profile never
// stores. Their contents come from the current config when one is applied.
var profileSkipSections = []string{"AppOptions", "OSC", "AssistOptions", "TTSOptions", "STTOptions", "Rules", "DND"}

// keepMachineSettings copies the parts of a config that are not part of a
// profile from cur into dst. With a zero cur it clears them.
func keepMachineSettings(dst *Config, cur Config) {
	dst.AppOptions = cur.AppOptions
	dst.OSCOptions = cur.OSCOptions
	dst.OSCOptions.Extra = append([]OSCTarget(nil), cur.OSCOptions.Extra...)
	dst.AssistOptions = cur.AssistOptions
	dst.TTSOptions = cur.TTSOptions
	dst.STTOptions = cur.STTOptions
	dst.Rules = cur.Rules.clone()
	dst.DND = cur.DND.clone()

	dst.DisplayOptions.Show = cur.DisplayOptions.Show
	dst.ChatOptions.History = append([]string(nil), cur.ChatOptions.History...)
	dst.AfkOptions.Manual = cur.AfkOptions.Manual
	dst.AfkOptions.ManualSince = cur.AfkOptions.ManualSince
	dst.BatteryOptions.Devices = append([]BatteryDevice(nil), cur.BatteryOptions.Devices...)
	dst.MediaOptions.BarPresets = append([]BarPreset(nil), cur.MediaOptions.BarPresets...)

	h, ch := &dst.HeartRate, cur.HeartRate
	h.Source, h.PulsoidToken, h.HypeRateKey, h.HypeRateID = ch.Source, ch.PulsoidToken, ch.HypeRateKey, ch.HypeRateID
	h.CustomURL, h.CustomPath, h.PollMS = ch.CustomURL, ch.CustomPath, ch.PollMS

	dst.DiscordOptions.ClientID = cur.DiscordOptions.ClientID
	dst.DiscordOptions.RedirectURI = cur.DiscordOptions.RedirectURI
	dst.TwitchOptions.ClientID = cur.TwitchOptions.ClientID
	dst.VoicemodOptions.Enabled = cur.VoicemodOptions.Enabled
	dst.TikTokOptions.URL = cur.TikTokOptions.URL
}

// profileSettings is the snapshot a profile stores for c.
func profileSettings(c Config) (json.RawMessage, error) {
	c = c.clone()
	keepMachineSettings(&c, Config{})
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(data, &sections); err != nil {
		return nil, err
	}
	for _, key := range profileSkipSections {
		delete(sections, key)
	}
	return json.Marshal(sections)
}

// applyProfileSettings is cur with a profile's snapshot laid over it: the
// snapshot on top of the defaults, as Load reads config.json, with the per-PC
// parts kept from cur.
func applyProfileSettings(cur Config, settings json.RawMessage) (Config, error) {
	next := DefaultConfig()
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &next); err != nil {
			return Config{}, fmt.Errorf("profile settings are damaged: %w", err)
		}
	}
	if next.TextMessages == nil {
		next.TextMessages = DefaultConfig().TextMessages
	}
	keepMachineSettings(&next, cur.clone())
	next.sanitize()
	return next, nil
}

func profilesPath() string {
	if profilesFile != "" {
		return profilesFile
	}
	// Without a loaded config (tests, mostly) profiles stay in memory, so
	// nothing ever touches the real per-user folder by accident.
	if cfgPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfgPath), "profiles.json")
}

func loadProfilesLocked() {
	if profilesLoaded {
		return
	}
	profilesLoaded = true
	profiles = profileStore{Version: 1}
	path := profilesPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("profiles: could not read:", err)
		}
		return
	}
	var stored profileStore
	if err := json.Unmarshal(data, &stored); err != nil {
		fmt.Println("profiles: could not parse, starting empty:", err)
		return
	}
	profiles = normalizeStore(stored)
}

func saveProfilesLocked() error {
	path := profilesPath()
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(profiles, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// normalizeStore checks a store read from disk or a backup: names valid and
// unique, settings re-validated through sanitize (which also drops anything
// a profile must not hold), shortcuts valid and unique, the active profile
// one that exists.
func normalizeStore(in profileStore) profileStore {
	out := profileStore{Version: 1, Profiles: []Profile{}}
	usedKeys := map[string]bool{}
	for _, p := range in.Profiles {
		if len(out.Profiles) >= MaxProfiles {
			break
		}
		name, err := cleanProfileName(p.Name)
		if err != nil || indexIn(out.Profiles, name) >= 0 {
			continue
		}
		cfg, err := applyProfileSettings(Config{}, p.Settings)
		if err != nil {
			continue
		}
		settings, err := profileSettings(cfg)
		if err != nil {
			continue
		}
		hotkey := ""
		if p.Hotkey != "" {
			if _, _, canonical, err := ParseHotkey(p.Hotkey); err == nil && !usedKeys[strings.ToLower(canonical)] {
				hotkey = canonical
				usedKeys[strings.ToLower(canonical)] = true
			}
		}
		appearance := p.Appearance
		if validAppearance(appearance) != nil {
			appearance = nil
		}
		out.Profiles = append(out.Profiles, Profile{
			Name: name, Hotkey: hotkey, Created: p.Created, Updated: p.Updated,
			Settings: settings, Appearance: appearance,
		})
	}
	if i := indexIn(out.Profiles, in.Active); i >= 0 {
		out.Active = out.Profiles[i].Name
	}
	return out
}

func cleanProfileName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", errors.New("a profile needs a name")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", errors.New("a profile name cannot contain control characters")
		}
	}
	if len([]rune(name)) > maxProfileName {
		return "", fmt.Errorf("a profile name can be at most %d characters", maxProfileName)
	}
	return name, nil
}

func validAppearance(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > maxAppearanceBytes {
		return fmt.Errorf("the appearance is larger than %d MB; try a smaller backdrop", maxAppearanceBytes>>20)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return errors.New("the appearance is not a JSON object")
	}
	return nil
}

// indexIn finds a profile by name, ignoring case.
func indexIn(list []Profile, name string) int {
	if name == "" {
		return -1
	}
	for i, p := range list {
		if strings.EqualFold(p.Name, name) {
			return i
		}
	}
	return -1
}

func findLocked(name string) (int, error) {
	if i := indexIn(profiles.Profiles, strings.Join(strings.Fields(name), " ")); i >= 0 {
		return i, nil
	}
	return -1, fmt.Errorf("there is no profile called %q", name)
}

func nowMS() int64 { return time.Now().UnixMilli() }

// captureLocked saves the current settings into profile i. It reports
// whether anything changed.
func captureLocked(i int) bool {
	settings, err := profileSettings(GetConfig())
	if err != nil {
		fmt.Println("profiles: could not snapshot the settings:", err)
		return false
	}
	p := &profiles.Profiles[i]
	if bytes.Equal(settings, p.Settings) {
		return false
	}
	p.Settings = settings
	p.Updated = nowMS()
	return true
}

// captureActiveProfile saves the settings into the active profile. SaveNow
// calls it, so it follows config.json's own debounced saves.
func captureActiveProfile() {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	i := indexIn(profiles.Profiles, profiles.Active)
	if i < 0 || !captureLocked(i) {
		return
	}
	if err := saveProfilesLocked(); err != nil {
		fmt.Println("profiles: save failed:", err)
	}
}

// mutateProfiles runs fn under the lock, saves, and tells listeners. fn
// returns the event to send, or "" for none.
func mutateProfiles(fn func() (kind string, err error)) error {
	profilesMu.Lock()
	loadProfilesLocked()
	before := profiles.Active
	kind, err := fn()
	if err == nil && kind != "" {
		if saveErr := saveProfilesLocked(); saveErr != nil {
			fmt.Println("profiles: save failed:", saveErr)
		}
	}
	event := ProfileEvent{Kind: kind, Active: profiles.Active, Previous: before}
	profilesMu.Unlock()
	if err == nil && kind != "" {
		emitProfileEvent(event)
	}
	return err
}

func emitProfileEvent(ev ProfileEvent) {
	listenersMu.Lock()
	fns := make([]func(ProfileEvent), 0, len(listeners))
	for _, fn := range listeners {
		fns = append(fns, fn)
	}
	listenersMu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
	NotifyChange()
}

// OnProfileChange calls fn after every profile change, on the goroutine that
// made it and after Fern's own locks are released, so fn may call the
// profile functions. fn must not block. The returned function removes it.
func OnProfileChange(fn func(ProfileEvent)) (remove func()) {
	listenersMu.Lock()
	id := nextListener
	nextListener++
	listeners[id] = fn
	listenersMu.Unlock()
	return func() {
		listenersMu.Lock()
		delete(listeners, id)
		listenersMu.Unlock()
	}
}

// ListProfiles returns every profile in the user's order.
func ListProfiles() []ProfileInfo {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	out := make([]ProfileInfo, len(profiles.Profiles))
	for i, p := range profiles.Profiles {
		out[i] = ProfileInfo{
			Name: p.Name, Hotkey: p.Hotkey, Updated: p.Updated,
			Active:        p.Name == profiles.Active,
			HasAppearance: len(p.Appearance) > 0,
		}
	}
	return out
}

// ActiveProfile is the profile settings are being saved to, or "".
func ActiveProfile() string {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	return profiles.Active
}

// ProfileSwitches counts switches since Fern started.
func ProfileSwitches() uint64 {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	return profileSwitch
}

// ProfileHotkeys lists the profiles that have a shortcut.
func ProfileHotkeys() []ProfileHotkey {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	var out []ProfileHotkey
	for _, p := range profiles.Profiles {
		if p.Hotkey != "" {
			out = append(out, ProfileHotkey{Name: p.Name, Hotkey: p.Hotkey})
		}
	}
	return out
}

// ProfileAppearance returns a profile's saved panel look, if it has one.
func ProfileAppearance(name string) (json.RawMessage, bool) {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	i, err := findLocked(name)
	if err != nil || len(profiles.Profiles[i].Appearance) == 0 {
		return nil, false
	}
	return append(json.RawMessage(nil), profiles.Profiles[i].Appearance...), true
}

// SwitchProfile applies a profile's settings and makes it the active one.
// Changes not yet saved go into the profile being left first, so switching
// to the active profile changes nothing.
func SwitchProfile(name string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		if prev := indexIn(profiles.Profiles, profiles.Active); prev >= 0 {
			captureLocked(prev)
		}
		next, err := applyProfileSettings(GetConfig(), profiles.Profiles[i].Settings)
		if err != nil {
			return "", err
		}
		SetConfig(next)
		profiles.Active = profiles.Profiles[i].Name
		profileSwitch++
		return ProfileSwitched, nil
	})
}

// DeactivateProfile stops saving settings into the active profile. The
// settings themselves stay as they are.
func DeactivateProfile() error {
	return mutateProfiles(func() (string, error) {
		if profiles.Active == "" {
			return "", nil
		}
		if i := indexIn(profiles.Profiles, profiles.Active); i >= 0 {
			captureLocked(i)
		}
		profiles.Active = ""
		return ProfileSwitched, nil
	})
}

// CreateProfile saves the current settings as a new profile and makes it the
// active one.
func CreateProfile(name string) error {
	return mutateProfiles(func() (string, error) {
		name, err := cleanProfileName(name)
		if err != nil {
			return "", err
		}
		if indexIn(profiles.Profiles, name) >= 0 {
			return "", fmt.Errorf("there already is a profile called %q", name)
		}
		if len(profiles.Profiles) >= MaxProfiles {
			return "", fmt.Errorf("Fern keeps up to %d profiles", MaxProfiles)
		}
		if prev := indexIn(profiles.Profiles, profiles.Active); prev >= 0 {
			captureLocked(prev)
		}
		settings, err := profileSettings(GetConfig())
		if err != nil {
			return "", err
		}
		now := nowMS()
		profiles.Profiles = append(profiles.Profiles, Profile{Name: name, Created: now, Updated: now, Settings: settings})
		profiles.Active = name
		return ProfileSwitched, nil
	})
}

// OverwriteProfile replaces a profile's settings with the current ones and
// makes it the active one.
func OverwriteProfile(name string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		if prev := indexIn(profiles.Profiles, profiles.Active); prev >= 0 && prev != i {
			captureLocked(prev)
		}
		captureLocked(i)
		profiles.Profiles[i].Updated = nowMS()
		if profiles.Active == profiles.Profiles[i].Name {
			return ProfileListChanged, nil
		}
		profiles.Active = profiles.Profiles[i].Name
		return ProfileSwitched, nil
	})
}

// DuplicateProfile copies a profile, appearance included, under a new name.
// The copy has no shortcut and is not made active.
func DuplicateProfile(name, newName string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		newName, err := cleanProfileName(newName)
		if err != nil {
			return "", err
		}
		if indexIn(profiles.Profiles, newName) >= 0 {
			return "", fmt.Errorf("there already is a profile called %q", newName)
		}
		if len(profiles.Profiles) >= MaxProfiles {
			return "", fmt.Errorf("Fern keeps up to %d profiles", MaxProfiles)
		}
		if profiles.Profiles[i].Name == profiles.Active {
			captureLocked(i)
		}
		src := profiles.Profiles[i]
		now := nowMS()
		profiles.Profiles = append(profiles.Profiles, Profile{
			Name: newName, Created: now, Updated: now,
			Settings:   append(json.RawMessage(nil), src.Settings...),
			Appearance: append(json.RawMessage(nil), src.Appearance...),
		})
		return ProfileListChanged, nil
	})
}

// RenameProfile renames a profile, keeping it active if it was.
func RenameProfile(name, newName string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		newName, err := cleanProfileName(newName)
		if err != nil {
			return "", err
		}
		if j := indexIn(profiles.Profiles, newName); j >= 0 && j != i {
			return "", fmt.Errorf("there already is a profile called %q", newName)
		}
		old := profiles.Profiles[i].Name
		if old == newName {
			return "", nil
		}
		profiles.Profiles[i].Name = newName
		if profiles.Active == old {
			profiles.Active = newName
		}
		return ProfileListChanged, nil
	})
}

// DeleteProfile removes a profile. Deleting the active one leaves the
// settings as they are, with no profile active.
func DeleteProfile(name string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		wasActive := profiles.Profiles[i].Name == profiles.Active
		profiles.Profiles = append(profiles.Profiles[:i:i], profiles.Profiles[i+1:]...)
		if wasActive {
			profiles.Active = ""
			return ProfileSwitched, nil
		}
		return ProfileListChanged, nil
	})
}

// MoveProfile moves a profile up (delta -1) or down (delta 1) in the list.
func MoveProfile(name string, delta int) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		j := i + delta
		if j < 0 || j >= len(profiles.Profiles) || delta == 0 {
			return "", nil
		}
		profiles.Profiles[i], profiles.Profiles[j] = profiles.Profiles[j], profiles.Profiles[i]
		return ProfileListChanged, nil
	})
}

// SetProfileHotkey gives a profile a global shortcut that switches to it, or
// with "" removes it. A shortcut used by another profile or the mic toggle is
// refused.
func SetProfileHotkey(name, hotkey string) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		canonical := ""
		if strings.TrimSpace(hotkey) != "" {
			_, _, c, err := ParseHotkey(hotkey)
			if err != nil {
				return "", err
			}
			canonical = c
			for j, p := range profiles.Profiles {
				if j != i && strings.EqualFold(p.Hotkey, canonical) {
					return "", fmt.Errorf("%s already switches to %s", canonical, p.Name)
				}
			}
			if _, _, mic, err := ParseHotkey(GetConfig().AppOptions.MicHotkey); err == nil && strings.EqualFold(mic, canonical) {
				return "", fmt.Errorf("%s is the mic shortcut", canonical)
			}
		}
		if profiles.Profiles[i].Hotkey == canonical {
			return "", nil
		}
		profiles.Profiles[i].Hotkey = canonical
		return ProfileListChanged, nil
	})
}

// SetProfileAppearance stores the panel's look with a profile, or with an
// empty value removes it.
func SetProfileAppearance(name string, appearance json.RawMessage) error {
	return mutateProfiles(func() (string, error) {
		i, err := findLocked(name)
		if err != nil {
			return "", err
		}
		if bytes.Equal(bytes.TrimSpace(appearance), []byte("null")) {
			appearance = nil
		}
		if err := validAppearance(appearance); err != nil {
			return "", err
		}
		profiles.Profiles[i].Appearance = append(json.RawMessage(nil), appearance...)
		return ProfileListChanged, nil
	})
}

// exportProfiles is the profile store for a backup, with the active profile
// brought up to date first.
func exportProfiles() profileStore {
	profilesMu.Lock()
	defer profilesMu.Unlock()
	loadProfilesLocked()
	if i := indexIn(profiles.Profiles, profiles.Active); i >= 0 && captureLocked(i) {
		_ = saveProfilesLocked()
	}
	out := profileStore{Version: 1, Active: profiles.Active, Profiles: make([]Profile, len(profiles.Profiles))}
	copy(out.Profiles, profiles.Profiles)
	return out
}
