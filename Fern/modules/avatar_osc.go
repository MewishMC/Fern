package modules

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// VRChat writes one OSC config per avatar you have worn with OSC on, under
// LocalLow\VRChat\VRChat\OSC\usr_<you>\Avatars\avtr_<id>.json. Each names
// the avatar and lists its parameters, which is how Fern names avatars when
// the log is off or the avatar was worn before Fern started, and how the
// Avatar page lists the parameters of an avatar you aren't wearing.
//
// There can be hundreds of these, some with hundreds of parameters, so the
// index only reads the id and name at the top of each file, and only again
// when the file changes. The parameters are read for the avatar the page
// shows.

// AvatarConfigParam is one parameter from an avatar's OSC config.
type AvatarConfigParam struct {
	Name string `json:"name"`
	Type string `json:"type"` // "bool", "int" or "float"
	// Input is set when VRChat takes the parameter over OSC; without it the
	// parameter can only be read.
	Input bool `json:"input"`
	// Address is where VRChat takes it, without the /avatar/parameters/
	// prefix. Usually the name.
	Address string `json:"-"`
}

// AvatarOSCConfig is an avatar's OSC config.
type AvatarOSCConfig struct {
	ID     string
	Name   string
	Params []AvatarConfigParam
}

const (
	oscIndexEvery     = 15 * time.Second // folder walk at most this often
	maxConfigParams   = 1024             // parameters read from one config
	maxOSCConfigBytes = 8 << 20          // a config bigger than this is skipped
	oscParamCacheSize = 8
)

// oscIndexFile is what the index keeps about one config file.
type oscIndexFile struct {
	Path string
	ID   string
	Name string
	User string // the usr_ folder, lower case
	Mod  time.Time
	Size int64
}

var (
	oscIdxMu    sync.Mutex
	oscIdxFiles = map[string]oscIndexFile{} // by path
	oscIdxByID  = map[string]oscIndexFile{} // the file picked for each avatar
	oscIdxAt    time.Time
	oscIdxSelf  string // the user the pick was made for
	oscIdxReads int    // files read by synchronous walks, for tests
	oscIdxBusy  bool   // a walk is running
	oscParams   = map[string]oscParamCache{}

	// Swapped by tests.
	oscConfigRoot = func() string { return filepath.Join(logDirectory(), "OSC") }
	oscSelfUser   = CurrentUserID
	oscIndexNow   = time.Now
	oscIndexAsync = true
)

type oscParamCache struct {
	mod  time.Time
	size int64
	cfg  AvatarOSCConfig
}

var oscBOM = []byte{0xEF, 0xBB, 0xBF}

// skipBOM drops the byte order mark VRChat puts in front of the JSON.
func skipBOM(r io.Reader) *bufio.Reader {
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, oscBOM) {
		_, _ = br.Discard(3)
	}
	return br
}

// readOSCHead reads the id and name of an OSC config. VRChat writes them
// first, so this stops long before the parameters.
func readOSCHead(r io.Reader) (id, name string, err error) {
	dec := json.NewDecoder(skipBOM(r))
	tok, err := dec.Token()
	if err != nil {
		return "", "", err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "", "", errors.New("not an OSC config")
	}
	gotID, gotName := false, false
	for dec.More() && !(gotID && gotName) {
		tok, err := dec.Token()
		if err != nil {
			return "", "", err
		}
		key, _ := tok.(string)
		switch key {
		case "id":
			err, gotID = dec.Decode(&id), true
		case "name":
			err, gotName = dec.Decode(&name), true
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return "", "", err
		}
	}
	return strings.TrimSpace(id), cleanAvatarName(name), nil
}

type oscConfigJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Parameters []struct {
		Name   string         `json:"name"`
		Input  *oscConfigAddr `json:"input"`
		Output *oscConfigAddr `json:"output"`
	} `json:"parameters"`
}

type oscConfigAddr struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

// oscParamType turns VRChat's "Bool", "Int" and "Float" into Fern's names.
func oscParamType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "bool":
		return "bool"
	case "int":
		return "int"
	case "float":
		return "float"
	}
	return ""
}

// parseOSCConfig reads a whole OSC config: the avatar and its parameters,
// sorted by name. Parameters without a known type are left out.
func parseOSCConfig(r io.Reader) (AvatarOSCConfig, error) {
	var raw oscConfigJSON
	if err := json.NewDecoder(skipBOM(r)).Decode(&raw); err != nil {
		return AvatarOSCConfig{}, err
	}
	cfg := AvatarOSCConfig{ID: strings.TrimSpace(raw.ID), Name: cleanAvatarName(raw.Name), Params: []AvatarConfigParam{}}
	seen := map[string]bool{}
	for _, p := range raw.Parameters {
		name := strings.TrimSpace(p.Name)
		if name == "" || seen[name] {
			continue
		}
		param := AvatarConfigParam{Name: name}
		if p.Input != nil {
			param.Type = oscParamType(p.Input.Type)
			if addr, ok := strings.CutPrefix(strings.TrimSpace(p.Input.Address), AvatarParamPrefix); ok && addr != "" {
				param.Input, param.Address = true, addr
			}
		}
		if param.Type == "" && p.Output != nil {
			param.Type = oscParamType(p.Output.Type)
		}
		if param.Type == "" {
			continue
		}
		seen[name] = true
		cfg.Params = append(cfg.Params, param)
		if len(cfg.Params) >= maxConfigParams {
			break
		}
	}
	sort.Slice(cfg.Params, func(i, j int) bool {
		return strings.ToLower(cfg.Params[i].Name) < strings.ToLower(cfg.Params[j].Name)
	})
	return cfg, nil
}

// lockOSCIndex locks the index and brings it up to date: the folder is
// walked at most every oscIndexEvery, in the background unless a test asks
// otherwise, so the panel never waits on hundreds of files.
func lockOSCIndex() {
	self := strings.ToLower(oscSelfUser())
	now := oscIndexNow()
	oscIdxMu.Lock()
	due := oscIdxAt.IsZero() || now.Sub(oscIdxAt) >= oscIndexEvery || now.Before(oscIdxAt)
	if due && !oscIdxBusy {
		oscIdxAt = now
		old, root := oscIdxFiles, oscConfigRoot()
		if !oscIndexAsync {
			oscIdxFiles, oscIdxReads = walkOSCFolder(root, old, oscIdxReads)
			pickOSCFilesLocked(self)
			return
		}
		oscIdxBusy = true
		go func() {
			found, _ := walkOSCFolder(root, old, 0)
			self := strings.ToLower(oscSelfUser())
			oscIdxMu.Lock()
			oscIdxFiles, oscIdxBusy = found, false
			pickOSCFilesLocked(self)
			oscIdxMu.Unlock()
		}()
	}
	if self != oscIdxSelf {
		pickOSCFilesLocked(self)
	}
}

// walkOSCFolder lists the configs under root\usr_*\Avatars, reading only
// the files that are new or changed since old. reads counts the files read.
func walkOSCFolder(root string, old map[string]oscIndexFile, reads int) (map[string]oscIndexFile, int) {
	found := map[string]oscIndexFile{}
	users, _ := os.ReadDir(root)
	for _, user := range users {
		if !user.IsDir() || !strings.HasPrefix(strings.ToLower(user.Name()), "usr_") {
			continue
		}
		dir := filepath.Join(root, user.Name(), "Avatars")
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.EqualFold(filepath.Ext(f.Name()), ".json") {
				continue
			}
			info, err := f.Info()
			if err != nil || info.Size() > maxOSCConfigBytes {
				continue
			}
			path := filepath.Join(dir, f.Name())
			if prev, ok := old[path]; ok && prev.Mod.Equal(info.ModTime()) && prev.Size == info.Size() {
				found[path] = prev
				continue
			}
			entry := oscIndexFile{Path: path, User: strings.ToLower(user.Name()), Mod: info.ModTime(), Size: info.Size()}
			if fh, err := os.Open(path); err == nil {
				reads++
				entry.ID, entry.Name, _ = readOSCHead(fh)
				fh.Close()
			}
			if entry.ID == "" {
				entry.ID = strings.TrimSuffix(f.Name(), filepath.Ext(f.Name()))
			}
			if _, err := cleanAvatarID(entry.ID); err != nil {
				continue
			}
			found[path] = entry
		}
	}
	return found, reads
}

// pickOSCFilesLocked picks one file per avatar: the logged-in user's, else
// the newest.
func pickOSCFilesLocked(self string) {
	oscIdxSelf = self
	better := func(f, old oscIndexFile) bool {
		if self != "" && (f.User == self) != (old.User == self) {
			return f.User == self
		}
		if !f.Mod.Equal(old.Mod) {
			return f.Mod.After(old.Mod)
		}
		return f.Path < old.Path
	}
	byID := make(map[string]oscIndexFile, len(oscIdxFiles))
	for _, f := range oscIdxFiles {
		if old, ok := byID[f.ID]; !ok || better(f, old) {
			byID[f.ID] = f
		}
	}
	oscIdxByID = byID
}

// OSCAvatarName is the avatar's name from its VRChat OSC config, or "".
func OSCAvatarName(id string) string {
	lockOSCIndex()
	defer oscIdxMu.Unlock()
	return oscIdxByID[strings.TrimSpace(id)].Name
}

// oscAvatarNames maps every avatar with an OSC config to its name.
func oscAvatarNames() map[string]string {
	lockOSCIndex()
	defer oscIdxMu.Unlock()
	out := make(map[string]string, len(oscIdxByID))
	for id, f := range oscIdxByID {
		if f.Name != "" {
			out[id] = f.Name
		}
	}
	return out
}

// AvatarConfig reads the avatar's OSC config, parameters included. It is
// cached until the file changes.
func AvatarConfig(id string) (AvatarOSCConfig, bool) {
	id = strings.TrimSpace(id)
	lockOSCIndex()
	f, ok := oscIdxByID[id]
	if c, hit := oscParams[f.Path]; ok && hit && c.mod.Equal(f.Mod) && c.size == f.Size {
		oscIdxMu.Unlock()
		return c.cfg, true
	}
	oscIdxMu.Unlock()
	if !ok {
		return AvatarOSCConfig{}, false
	}
	path := f.Path
	fh, err := os.Open(path)
	if err != nil {
		return AvatarOSCConfig{}, false
	}
	cfg, err := parseOSCConfig(fh)
	fh.Close()
	if err != nil {
		return AvatarOSCConfig{}, false
	}
	if cfg.ID == "" {
		cfg.ID = id
	}
	oscIdxMu.Lock()
	if len(oscParams) >= oscParamCacheSize {
		oscParams = map[string]oscParamCache{}
	}
	oscParams[path] = oscParamCache{mod: f.Mod, size: f.Size, cfg: cfg}
	oscIdxMu.Unlock()
	return cfg, true
}

// avatarName picks the name to show: a name typed by hand, then the OSC
// config's, then the one VRChat's log gave.
func avatarName(e AvatarEntry, oscName string) string {
	if e.NameSet && e.Name != "" {
		return e.Name
	}
	if oscName != "" {
		return oscName
	}
	return e.Name
}

// AvatarDisplayName is the name Fern shows for an avatar, or "".
func AvatarDisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	e, _ := AvatarInfo(id)
	if name := avatarName(e, OSCAvatarName(id)); name != "" {
		return name
	}
	if id == CurrentAvatarID() {
		return cleanAvatarName(CurrentAvatarName())
	}
	return ""
}

// AvatarCatalogItem is one avatar the Avatar page can pick.
type AvatarCatalogItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Known  bool   `json:"known"`  // Fern keeps an entry for it
	Config bool   `json:"config"` // VRChat has an OSC config for it
	Worn   bool   `json:"worn"`
	Seen   int64  `json:"seen"` // unix ms: last worn, or the config's last change
}

// AvatarCatalog lists the avatars to pick from: the ones Fern knows and the
// ones VRChat has OSC configs for (only the logged-in user's when known),
// worn first, then most recent. query matches the name or ID; total is how
// many matched before limit.
func AvatarCatalog(query string, limit int) (list []AvatarCatalogItem, total int) {
	worn := CurrentAvatarID()
	items := map[string]*AvatarCatalogItem{}
	lockOSCIndex()
	mine := false
	if oscIdxSelf != "" {
		for _, f := range oscIdxByID {
			if f.User == oscIdxSelf {
				mine = true
				break
			}
		}
	}
	for id, f := range oscIdxByID {
		if mine && f.User != oscIdxSelf {
			continue
		}
		items[id] = &AvatarCatalogItem{ID: id, Name: f.Name, Config: true, Seen: f.Mod.UnixMilli()}
	}
	names := make(map[string]string, len(oscIdxByID))
	for id, f := range oscIdxByID {
		names[id] = f.Name
	}
	oscIdxMu.Unlock()

	for _, e := range AvatarEntries() {
		it := items[e.ID]
		if it == nil {
			it = &AvatarCatalogItem{ID: e.ID}
			items[e.ID] = it
		}
		it.Known = true
		it.Name = avatarName(e, names[e.ID])
		if e.Seen > 0 {
			it.Seen = e.Seen
		}
	}
	if worn != "" {
		if it := items[worn]; it != nil {
			it.Worn = true
		} else {
			items[worn] = &AvatarCatalogItem{ID: worn, Name: cleanAvatarName(CurrentAvatarName()), Worn: true}
		}
	}

	q := strings.ToLower(strings.TrimSpace(query))
	for _, it := range items {
		if q != "" && !strings.Contains(strings.ToLower(it.Name), q) && !strings.Contains(strings.ToLower(it.ID), q) {
			continue
		}
		list = append(list, *it)
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Worn != b.Worn {
			return a.Worn
		}
		if a.Seen != b.Seen {
			return a.Seen > b.Seen
		}
		return a.ID < b.ID
	})
	total = len(list)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, total
}

// paramSaved checks a value typed on the Avatar page against VRChat's range
// for the type: bools are 0 or 1, ints whole numbers 0..255, floats -1..1.
func paramSaved(typ string, v float64) (SavedParam, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return SavedParam{}, errors.New("that isn't a number")
	}
	switch typ {
	case "bool":
		if v != 0 {
			v = 1
		}
	case "int":
		v = math.Max(0, math.Min(255, math.Round(v)))
	case "float":
		v = round4(math.Max(-1, math.Min(1, v)))
	default:
		return SavedParam{}, errors.New("a parameter is a bool, an int or a float")
	}
	return SavedParam{Type: typ, Value: v}, nil
}

// configParam finds a parameter in the avatar's OSC config. ok is false
// when there is no config; found tells whether the config lists it.
func configParam(id, name string) (p AvatarConfigParam, found, ok bool) {
	cfg, ok := AvatarConfig(id)
	if !ok {
		return AvatarConfigParam{}, false, false
	}
	for _, x := range cfg.Params {
		if x.Name == name {
			return x, true, true
		}
	}
	return AvatarConfigParam{}, false, true
}

// SendAvatarValue sets a parameter of the avatar you wear, as typed on the
// Avatar page. Parameters the avatar's OSC config lists without an input
// can't be set.
func SendAvatarValue(id, name, typ string, v float64) error {
	id = strings.TrimSpace(id)
	if id == "" || id != CurrentAvatarID() {
		return errors.New("put the avatar on first; values are sent to the one you wear")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errNoParamName
	}
	sv, err := paramSaved(typ, v)
	if err != nil {
		return err
	}
	addr := name
	if p, found, ok := configParam(id, name); ok {
		if !found {
			return errors.New("this avatar has no parameter called " + name)
		}
		if !p.Input {
			return errors.New(name + " can only be read; VRChat doesn't take it over OSC")
		}
		if p.Type != "" && p.Type != sv.Type {
			return errors.New(name + " is a " + p.Type)
		}
		addr = p.Address
	}
	return sendAvatarOut(addr, sv.value())
}

// SetAvatarSavedValue saves one value for the avatar, ticking the
// parameter if it isn't yet and turning putting back on, so the value is
// sent when you change into the avatar. The entry is made if Fern had none.
func SetAvatarSavedValue(id, name, typ string, v float64) error {
	sv, err := paramSaved(typ, v)
	if err != nil {
		return err
	}
	name = cleanParamName(name)
	if name == "" {
		return errNoParamName
	}
	return editAvatar(id, true, func(e *AvatarEntry) error {
		if !containsString(e.Params, name) {
			if len(e.Params) >= maxSyncParams {
				return errors.New("an avatar keeps up to 64 parameters; untick one first")
			}
			e.Params = append(e.Params, name)
		}
		e.Values[name] = sv
		e.SavedAt = avatarsNow().UnixMilli()
		e.Restore = true
		return nil
	})
}
