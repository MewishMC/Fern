package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The avatars Fern has seen and what to do for each: the profile to switch
// to and the parameter values to save and put back. They are kept in
// avatars.json next to config.json, so the panel's whole-config writes never
// overwrite values Fern saved meanwhile.

const (
	maxRecentAvatars = 20  // avatars without settings that are remembered
	maxAvatarEntries = 200 // avatars kept in all
	maxSyncParams    = 64  // parameters saved per avatar
	maxAvatarIDLen   = 100
	maxAvatarNameLen = 60
)

// SavedParam is one saved parameter value with VRChat's type.
type SavedParam struct {
	Type  string  `json:"type"` // "bool", "int" or "float"
	Value float64 `json:"value"`
}

// AvatarEntry is what Fern knows and keeps about one avatar.
type AvatarEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	NameSet bool   `json:"nameSet"` // named by hand; the log no longer renames it
	Seen    int64  `json:"seen"`    // unix ms, last worn
	// Profile is switched to when you change into the avatar; "" is none.
	Profile string `json:"profile"`
	// Params are the parameters to save and put back.
	Params   []string              `json:"params"`
	Values   map[string]SavedParam `json:"values"`
	SavedAt  int64                 `json:"savedAt"`
	AutoSave bool                  `json:"autoSave"` // keep Values up to date while worn
	Restore  bool                  `json:"restore"`  // send Values when changing into it
}

func (e AvatarEntry) clone() AvatarEntry {
	e.Params = append([]string{}, e.Params...)
	values := make(map[string]SavedParam, len(e.Values))
	for k, v := range e.Values {
		values[k] = v
	}
	e.Values = values
	return e
}

// hasSettings reports whether forgetting the avatar would lose something.
func (e AvatarEntry) hasSettings() bool {
	return e.Profile != "" || len(e.Params) > 0 || len(e.Values) > 0 || e.NameSet
}

type avatarStoreFile struct {
	Version int           `json:"version"`
	Avatars []AvatarEntry `json:"avatars"`
}

var (
	avatarsMu     sync.Mutex
	avatarList    []AvatarEntry // most recently worn first
	avatarsLoaded bool
	avatarsFile   string // overridden by tests
	avatarsSaveAt *time.Timer
	avatarsNow    = time.Now
)

func avatarsPath() string {
	if avatarsFile != "" {
		return avatarsFile
	}
	if cfgPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfgPath), "avatars.json")
}

func loadAvatarsLocked() {
	if avatarsLoaded {
		return
	}
	avatarsLoaded = true
	avatarList = nil
	path := avatarsPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Println("avatars: could not read:", err)
		}
		return
	}
	var stored avatarStoreFile
	if err := json.Unmarshal(data, &stored); err != nil {
		fmt.Println("avatars: could not parse, starting empty:", err)
		return
	}
	avatarList = normalizeAvatars(stored.Avatars)
}

// normalizeAvatars checks entries read from disk: valid unique IDs, clean
// names and parameters, newest first, within the limits.
func normalizeAvatars(in []AvatarEntry) []AvatarEntry {
	out := make([]AvatarEntry, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		id, err := cleanAvatarID(e.ID)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		e.ID = id
		e.Name = cleanAvatarName(e.Name)
		e.Profile = RuneLimit(strings.TrimSpace(e.Profile), maxProfileName)
		e.Params = cleanParamList(e.Params)
		values := map[string]SavedParam{}
		for _, p := range e.Params {
			if v, ok := e.Values[p]; ok && validSaved(v) {
				values[p] = v
			}
		}
		e.Values = values
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seen > out[j].Seen })
	return trimAvatars(out)
}

// trimAvatars keeps the recent avatars and every avatar with settings, up
// to maxAvatarEntries.
func trimAvatars(list []AvatarEntry) []AvatarEntry {
	out := list[:0]
	plain := 0
	for _, e := range list {
		if !e.hasSettings() {
			if plain >= maxRecentAvatars {
				continue
			}
			plain++
		}
		out = append(out, e)
		if len(out) >= maxAvatarEntries {
			break
		}
	}
	return out
}

func validSaved(v SavedParam) bool {
	if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) {
		return false
	}
	switch v.Type {
	case "bool", "int", "float":
		return true
	}
	return false
}

func cleanAvatarID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxAvatarIDLen {
		return "", errors.New("that isn't an avatar ID")
	}
	for _, r := range id {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", errors.New("that isn't an avatar ID")
		}
	}
	return id, nil
}

func cleanAvatarName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	return RuneLimit(strings.Join(strings.Fields(name), " "), maxAvatarNameLen)
}

// cleanParamList cleans, de-duplicates and caps a list of parameter names.
func cleanParamList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, n := range in {
		n = cleanParamName(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) >= maxSyncParams {
			break
		}
	}
	return out
}

// scheduleAvatarsSaveLocked writes avatars.json a moment later, so a burst
// of changes (an auto-saved parameter moving) is one write.
func scheduleAvatarsSaveLocked() {
	if avatarsPath() == "" {
		return
	}
	if avatarsSaveAt != nil {
		avatarsSaveAt.Stop()
	}
	avatarsSaveAt = time.AfterFunc(2*time.Second, func() {
		if err := SaveAvatarsNow(); err != nil {
			fmt.Println("avatars: could not save:", err)
		}
	})
}

// SaveAvatarsNow writes avatars.json straight away.
func SaveAvatarsNow() error {
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	path := avatarsPath()
	if path == "" || !avatarsLoaded {
		return nil
	}
	data, err := json.MarshalIndent(avatarStoreFile{Version: 1, Avatars: avatarList}, "", " ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func avatarIndexLocked(id string) int {
	for i, e := range avatarList {
		if e.ID == id {
			return i
		}
	}
	return -1
}

// editAvatar runs fn on the avatar's entry, adding one when create is set.
// The change is saved unless fn fails.
func editAvatar(id string, create bool, fn func(e *AvatarEntry) error) error {
	id, err := cleanAvatarID(id)
	if err != nil {
		return err
	}
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	loadAvatarsLocked()
	i := avatarIndexLocked(id)
	if i < 0 {
		if !create {
			return errors.New("Fern doesn't know that avatar")
		}
		avatarList = append(avatarList, AvatarEntry{ID: id, Params: []string{}, Values: map[string]SavedParam{}})
		i = len(avatarList) - 1
	}
	e := avatarList[i].clone()
	if err := fn(&e); err != nil {
		return err
	}
	avatarList[i] = e
	avatarList = trimAvatars(avatarList)
	scheduleAvatarsSaveLocked()
	return nil
}

// AvatarEntries lists the avatars Fern knows, most recently worn first.
func AvatarEntries() []AvatarEntry {
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	loadAvatarsLocked()
	out := make([]AvatarEntry, len(avatarList))
	for i, e := range avatarList {
		out[i] = e.clone()
	}
	return out
}

// AvatarInfo returns one avatar's entry.
func AvatarInfo(id string) (AvatarEntry, bool) {
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	loadAvatarsLocked()
	if i := avatarIndexLocked(strings.TrimSpace(id)); i >= 0 {
		return avatarList[i].clone(), true
	}
	return AvatarEntry{}, false
}

// noteAvatarWorn moves the avatar to the top of the recent list.
func noteAvatarWorn(id string) {
	id, err := cleanAvatarID(id)
	if err != nil {
		return
	}
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	loadAvatarsLocked()
	e := AvatarEntry{ID: id, Params: []string{}, Values: map[string]SavedParam{}}
	if i := avatarIndexLocked(id); i >= 0 {
		e = avatarList[i]
		avatarList = append(avatarList[:i], avatarList[i+1:]...)
	}
	e.Seen = avatarsNow().UnixMilli()
	avatarList = append([]AvatarEntry{e}, avatarList...)
	avatarList = trimAvatars(avatarList)
	scheduleAvatarsSaveLocked()
}

// noteAvatarName names an avatar from VRChat's log, unless it was named by
// hand.
func noteAvatarName(id, name string) {
	name = cleanAvatarName(name)
	if name == "" {
		return
	}
	_ = editAvatar(id, false, func(e *AvatarEntry) error {
		if e.NameSet || e.Name == name {
			return errors.New("unchanged")
		}
		e.Name = name
		return nil
	})
}

// RenameAvatar names an avatar by hand. An empty name lets VRChat's log name
// it again.
func RenameAvatar(id, name string) error {
	return editAvatar(id, false, func(e *AvatarEntry) error {
		name = cleanAvatarName(name)
		e.NameSet = name != ""
		if name != "" {
			e.Name = name
		}
		return nil
	})
}

// SetAvatarProfile picks the profile to switch to when you change into the
// avatar; "" picks none.
func SetAvatarProfile(id, profile string) error {
	profile = strings.TrimSpace(profile)
	if profile != "" {
		found := ""
		for _, p := range ListProfiles() {
			if foldName(p.Name) == foldName(profile) {
				found = p.Name
			}
		}
		if found == "" {
			return errors.New("there is no profile called " + profile)
		}
		profile = found
	}
	return editAvatar(id, true, func(e *AvatarEntry) error {
		e.Profile = profile
		return nil
	})
}

// ForgetAvatar removes an avatar and everything kept for it.
func ForgetAvatar(id string) error {
	id = strings.TrimSpace(id)
	avatarsMu.Lock()
	defer avatarsMu.Unlock()
	loadAvatarsLocked()
	i := avatarIndexLocked(id)
	if i < 0 {
		return errors.New("Fern doesn't know that avatar")
	}
	avatarList = append(avatarList[:i], avatarList[i+1:]...)
	scheduleAvatarsSaveLocked()
	return nil
}

// SetAvatarSyncParams picks which parameters are saved for the avatar.
// Saved values of parameters no longer picked are dropped.
func SetAvatarSyncParams(id string, names []string) error {
	names = cleanParamList(names)
	return editAvatar(id, true, func(e *AvatarEntry) error {
		e.Params = names
		for k := range e.Values {
			if !containsString(names, k) {
				delete(e.Values, k)
			}
		}
		return nil
	})
}

// SetAvatarSyncFlags turns auto-saving and restoring on or off.
func SetAvatarSyncFlags(id string, autoSave, restore bool) error {
	return editAvatar(id, true, func(e *AvatarEntry) error {
		e.AutoSave, e.Restore = autoSave, restore
		return nil
	})
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// savedFromValue records a value VRChat sent.
func savedFromValue(v any) (SavedParam, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return SavedParam{Type: "bool", Value: 1}, true
		}
		return SavedParam{Type: "bool", Value: 0}, true
	case int32:
		return SavedParam{Type: "int", Value: float64(n)}, true
	case int:
		return SavedParam{Type: "int", Value: float64(n)}, true
	case float32:
		if math.IsNaN(float64(n)) {
			return SavedParam{}, false
		}
		return SavedParam{Type: "float", Value: math.Round(float64(n)*1e4) / 1e4}, true
	case float64:
		if math.IsNaN(n) {
			return SavedParam{}, false
		}
		return SavedParam{Type: "float", Value: math.Round(n*1e4) / 1e4}, true
	}
	return SavedParam{}, false
}

// value is what SendAvatarParam takes for the saved value.
func (s SavedParam) value() any {
	switch s.Type {
	case "bool":
		return s.Value != 0
	case "int":
		return int(math.Round(s.Value))
	}
	return s.Value
}

// SaveAvatarParams saves the current values of the avatar's picked
// parameters. It works for the avatar you wear, since only its values are
// known. It returns how many were saved.
func SaveAvatarParams(id string) (int, error) {
	if id == "" || id != CurrentAvatarID() {
		return 0, errors.New("put the avatar on first; Fern only knows the values of the one you wear")
	}
	current := AvatarParams()
	saved := 0
	err := editAvatar(id, true, func(e *AvatarEntry) error {
		if len(e.Params) == 0 {
			return errors.New("pick the parameters to save first")
		}
		for _, name := range e.Params {
			if v, ok := savedFromValue(current[name]); ok {
				e.Values[name] = v
				saved++
			}
		}
		if saved == 0 {
			return errors.New("VRChat hasn't sent any of those parameters yet")
		}
		e.SavedAt = avatarsNow().UnixMilli()
		return nil
	})
	return saved, err
}

// storeAvatarValue keeps an auto-saved value. It reports whether the value
// was one to keep.
func storeAvatarValue(id, name string, v any) bool {
	sv, ok := savedFromValue(v)
	if !ok {
		return false
	}
	kept := false
	_ = editAvatar(id, false, func(e *AvatarEntry) error {
		if !e.AutoSave || !containsString(e.Params, name) {
			return errors.New("not kept")
		}
		if old, ok := e.Values[name]; ok && old == sv {
			kept = true
			return errors.New("unchanged")
		}
		e.Values[name] = sv
		e.SavedAt = avatarsNow().UnixMilli()
		kept = true
		return nil
	})
	return kept
}

// CopyAvatarParams copies one avatar's picked parameters and saved values
// to another, replacing what that one had.
func CopyAvatarParams(from, to string) error {
	src, ok := AvatarInfo(from)
	if !ok {
		return errors.New("Fern doesn't know that avatar")
	}
	if strings.TrimSpace(to) == src.ID {
		return errors.New("pick a different avatar to copy to")
	}
	if len(src.Values) == 0 {
		return errors.New("that avatar has no saved values to copy")
	}
	return editAvatar(to, true, func(e *AvatarEntry) error {
		e.Params = append([]string{}, src.Params...)
		e.Values = src.clone().Values
		e.SavedAt = avatarsNow().UnixMilli()
		e.Restore = true
		return nil
	})
}

// avatarParamValues lists an avatar's saved values in the order the
// parameters were picked.
func avatarParamValues(e AvatarEntry) []avatarValue {
	out := make([]avatarValue, 0, len(e.Values))
	for _, name := range e.Params {
		if v, ok := e.Values[name]; ok {
			out = append(out, avatarValue{Name: name, Value: v.value()})
		}
	}
	return out
}

type avatarValue struct {
	Name  string
	Value any
}

// RestoreAvatarParams sends the avatar's saved values now. It works for the
// avatar you wear and returns how many were sent.
func RestoreAvatarParams(id string) (int, error) {
	if id == "" || id != CurrentAvatarID() {
		return 0, errors.New("put the avatar on first")
	}
	e, ok := AvatarInfo(id)
	if !ok || len(e.Values) == 0 {
		return 0, errors.New("nothing is saved for this avatar")
	}
	return sendAvatarValues(avatarParamValues(e))
}

func sendAvatarValues(values []avatarValue) (int, error) {
	sent := 0
	var firstErr error
	for _, v := range values {
		if err := sendAvatarOut(v.Name, v.Value); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sent++
	}
	if sent == 0 && firstErr != nil {
		return 0, firstErr
	}
	return sent, nil
}
