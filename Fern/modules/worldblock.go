package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Blocked worlds pause the chatbox in worlds where it is unwelcome, such as
// clubs and events. Like VRCOSC, Fern uses the community list kept at
// github.com/cyberkitsune/chatbox-club-blacklist, which club owners add their
// worlds to, plus the user's own list. While blocked, only the lines on the
// allow list (heart rate by default, as VRCOSC lets its heart rate modules
// through) and messages you send yourself still show.

// CommunityBlocklistURL is the community list VRCOSC reads.
const CommunityBlocklistURL = "https://raw.githubusercontent.com/cyberkitsune/chatbox-club-blacklist/master/npblacklist.json"

// Limits of the blocked worlds settings.
const (
	MaxBlockedWorlds = 200
	blocklistMaxSize = 1 << 20
	blocklistMaxIDs  = 5000
	blocklistMaxAge  = 24 * time.Hour
)

// Values of WorldBlockStatus.By.
const (
	BlockedByCommunity = "community"
	BlockedByYou       = "yours"
)

// WorldBlockOptions pauses the chatbox in blocked worlds. It is kept per PC.
type WorldBlockOptions struct {
	Enabled   bool           `json:"enabled"`
	Community bool           `json:"community"` // use the community list
	Allow     []string       `json:"allow"`     // line ids that still show
	Worlds    []BlockedWorld `json:"worlds"`    // your own blocked worlds
	Exempt    []BlockedWorld `json:"exempt"`    // community worlds you never block
}

// BlockedWorld is one world on a list.
type BlockedWorld struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var worldIDPattern = regexp.MustCompile(`^wrld_[A-Za-z0-9-]{1,64}$`)

// ValidWorldID reports whether id looks like a VRChat world id.
func ValidWorldID(id string) bool { return worldIDPattern.MatchString(id) }

func defaultWorldBlockOptions() WorldBlockOptions {
	return WorldBlockOptions{Enabled: true, Community: true, Allow: []string{ModuleHeart}, Worlds: []BlockedWorld{}, Exempt: []BlockedWorld{}}
}

func (o WorldBlockOptions) clone() WorldBlockOptions {
	o.Allow = append([]string{}, o.Allow...)
	o.Worlds = append([]BlockedWorld{}, o.Worlds...)
	o.Exempt = append([]BlockedWorld{}, o.Exempt...)
	return o
}

func sanitizeWorldList(in []BlockedWorld) []BlockedWorld {
	out := make([]BlockedWorld, 0, len(in))
	seen := map[string]bool{}
	for _, w := range in {
		w.ID = strings.TrimSpace(w.ID)
		if !ValidWorldID(w.ID) || seen[strings.ToLower(w.ID)] || len(out) >= MaxBlockedWorlds {
			continue
		}
		seen[strings.ToLower(w.ID)] = true
		w.Name = RuneLimit(strings.Join(strings.Fields(w.Name), " "), 80)
		out = append(out, w)
	}
	return out
}

func (o *WorldBlockOptions) sanitize() {
	allow := make([]string, 0, len(o.Allow))
	for _, id := range o.Allow {
		if knownModules[id] && !slices.Contains(allow, id) {
			allow = append(allow, id)
		}
	}
	o.Allow = allow
	o.Worlds = sanitizeWorldList(o.Worlds)
	o.Exempt = sanitizeWorldList(o.Exempt)
}

func listHas(list []BlockedWorld, id string) (BlockedWorld, bool) {
	for _, w := range list {
		if strings.EqualFold(w.ID, id) {
			return w, true
		}
	}
	return BlockedWorld{}, false
}

/* ---------------- community list ---------------- */

var (
	blocklistMu      sync.Mutex
	blocklistWorlds  = map[string]string{} // lower-case id -> name
	blocklistFetched time.Time
	blocklistTried   time.Time
	blocklistErr     string
	blocklistBusy    bool
	blocklistURL     = CommunityBlocklistURL
	blocklistFile    = func() string {
		if dir := DataDir(); dir != "" {
			return filepath.Join(dir, "world-blocklist.json")
		}
		return ""
	}
	blocklistClient = &http.Client{Timeout: 10 * time.Second}
)

// blocklistCache is what Fern keeps on disk between runs.
type blocklistCache struct {
	Fetched int64          `json:"fetched"` // unix ms
	Worlds  []BlockedWorld `json:"worlds"`
}

// parseBlocklist reads the community file: {"worlds": [{"id", "name", ...}]}.
func parseBlocklist(data []byte) ([]BlockedWorld, error) {
	var doc struct {
		Worlds []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"worlds"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the list is not valid JSON: %w", err)
	}
	if doc.Worlds == nil {
		return nil, errors.New("the list has no worlds")
	}
	out := make([]BlockedWorld, 0, len(doc.Worlds))
	for _, w := range doc.Worlds {
		id := strings.TrimSpace(w.ID)
		if !ValidWorldID(id) || len(out) >= blocklistMaxIDs {
			continue
		}
		out = append(out, BlockedWorld{ID: id, Name: RuneLimit(strings.TrimSpace(w.Name), 80)})
	}
	return out, nil
}

func setBlocklist(worlds []BlockedWorld, fetched time.Time) {
	m := make(map[string]string, len(worlds))
	for _, w := range worlds {
		m[strings.ToLower(w.ID)] = w.Name
	}
	blocklistMu.Lock()
	blocklistWorlds, blocklistFetched = m, fetched
	blocklistMu.Unlock()
}

// loadBlocklistCache reads the copy saved by the last download.
func loadBlocklistCache() {
	path := blocklistFile()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var c blocklistCache
	if json.Unmarshal(data, &c) != nil {
		return
	}
	setBlocklist(sanitizeCached(c.Worlds), time.UnixMilli(c.Fetched))
}

func sanitizeCached(in []BlockedWorld) []BlockedWorld {
	out := make([]BlockedWorld, 0, len(in))
	for _, w := range in {
		if ValidWorldID(w.ID) && len(out) < blocklistMaxIDs {
			out = append(out, BlockedWorld{ID: w.ID, Name: RuneLimit(w.Name, 80)})
		}
	}
	return out
}

// fetchBlocklist downloads the community list and saves it. A failed
// download keeps the list Fern already has.
func fetchBlocklist() error {
	blocklistMu.Lock()
	if blocklistBusy {
		blocklistMu.Unlock()
		return nil
	}
	blocklistBusy = true
	blocklistTried = time.Now()
	url := blocklistURL
	blocklistMu.Unlock()

	err := func() error {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", webUserAgent)
		resp, err := blocklistClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return httpStatusError(resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, blocklistMaxSize+1))
		if err != nil {
			return err
		}
		if len(data) > blocklistMaxSize {
			return errors.New("the list is too large")
		}
		worlds, err := parseBlocklist(data)
		if err != nil {
			return err
		}
		now := time.Now()
		setBlocklist(worlds, now)
		if path := blocklistFile(); path != "" {
			if out, err := json.Marshal(blocklistCache{Fetched: now.UnixMilli(), Worlds: worlds}); err == nil {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					fmt.Println("world blocklist: could not save:", err)
				}
			}
		}
		return nil
	}()

	blocklistMu.Lock()
	blocklistBusy = false
	if err != nil {
		blocklistErr = "Could not update the community list: " + err.Error()
	} else {
		blocklistErr = ""
	}
	blocklistMu.Unlock()
	NotifyChange()
	NudgeRules()
	return err
}

// blocklistDue reports whether the community list should be downloaded:
// it is older than a day, and the last try was over an hour ago.
func blocklistDue(now time.Time) bool {
	blocklistMu.Lock()
	defer blocklistMu.Unlock()
	if blocklistBusy || (!blocklistTried.IsZero() && now.Sub(blocklistTried) < time.Hour) {
		return false
	}
	return blocklistFetched.IsZero() || now.Sub(blocklistFetched) >= blocklistMaxAge
}

// RefreshWorldBlocklist downloads the community list now, in the background.
func RefreshWorldBlocklist() {
	go func() { _ = fetchBlocklist() }()
}

func startWorldBlockWorker() {
	go func() {
		loadBlocklistCache()
		for {
			o := GetConfig().WorldBlock
			if o.Enabled && o.Community && blocklistDue(time.Now()) {
				_ = fetchBlocklist()
			}
			time.Sleep(10 * time.Minute)
		}
	}()
}

/* ---------------- blocking ---------------- */

var (
	blockOverMu sync.Mutex
	blockOver   = map[string]bool{} // world ids shown anyway until Fern closes
	// blockWorldFn is the world VRChat is in; swapped in tests.
	blockWorldFn = func() (WorldInfo, bool) {
		if !CurrentVRChatStatus().Running {
			return WorldInfo{}, false
		}
		return CurrentWorld()
	}
)

// worldBlock is whether the chatbox is paused in the current world.
type worldBlock struct {
	On       bool   // paused right now
	Listed   bool   // the world is on a list, override or not
	ID, Name string // the world
	By       string // BlockedByCommunity or BlockedByYou
	Allow    []string
}

func (b worldBlock) hides(id string) bool {
	return b.On && !slices.Contains(b.Allow, id)
}

// worldBlockFor checks the current world against the lists.
func worldBlockFor(cfg Config) worldBlock {
	o := cfg.WorldBlock
	if !o.Enabled {
		return worldBlock{}
	}
	w, ok := blockWorldFn()
	if !ok || w.ID == "" {
		return worldBlock{}
	}
	b := worldBlock{ID: w.ID, Name: w.Name, Allow: o.Allow}
	if mine, ok := listHas(o.Worlds, w.ID); ok {
		b.Listed, b.By = true, BlockedByYou
		if b.Name == "" {
			b.Name = mine.Name
		}
	} else if o.Community {
		if _, exempt := listHas(o.Exempt, w.ID); !exempt {
			blocklistMu.Lock()
			name, listed := blocklistWorlds[strings.ToLower(w.ID)]
			blocklistMu.Unlock()
			if listed {
				b.Listed, b.By = true, BlockedByCommunity
				if b.Name == "" {
					b.Name = name
				}
			}
		}
	}
	b.On = b.Listed && !WorldBlockOverridden(w.ID)
	return b
}

// worldBlockQuieted lists the enabled lines a blocked world is hiding.
func worldBlockQuieted(cfg Config, b worldBlock) []string {
	if !b.On {
		return nil
	}
	var out []string
	for _, id := range cfg.DisplayOptions.Order {
		if ModuleEnabled(id, cfg.DisplayOptions) && b.hides(id) {
			out = append(out, id)
		}
	}
	return out
}

func currentBlockWorldID() string {
	if w, ok := blockWorldFn(); ok {
		return w.ID
	}
	return ""
}

// WorldBlockOverridden reports whether the chatbox shows in world id
// although it is blocked.
func WorldBlockOverridden(id string) bool {
	blockOverMu.Lock()
	defer blockOverMu.Unlock()
	return blockOver[strings.ToLower(id)]
}

func setWorldBlockOverride(id string, show bool) {
	blockOverMu.Lock()
	if show {
		blockOver[strings.ToLower(id)] = true
	} else {
		delete(blockOver, strings.ToLower(id))
	}
	blockOverMu.Unlock()
	NotifyChange()
	NudgeRules()
}

// SetWorldBlockOverride shows the chatbox in the current world until Fern
// closes (show true), or pauses it there again.
func SetWorldBlockOverride(show bool) error {
	id := currentBlockWorldID()
	if id == "" {
		return errors.New("you are not in a world")
	}
	setWorldBlockOverride(id, show)
	return nil
}

// BlockCurrentWorld adds the world you are in to your own list (block true)
// or takes it off. Taking a community world off adds it to the exempt list.
func BlockCurrentWorld(block bool) error {
	w, ok := blockWorldFn()
	if !ok || w.ID == "" {
		return errors.New("you are not in a world")
	}
	var err error
	UpdateConfig(func(c *Config) {
		o := &c.WorldBlock
		o.Worlds = slices.DeleteFunc(o.Worlds, func(b BlockedWorld) bool { return strings.EqualFold(b.ID, w.ID) })
		o.Exempt = slices.DeleteFunc(o.Exempt, func(b BlockedWorld) bool { return strings.EqualFold(b.ID, w.ID) })
		if block {
			if len(o.Worlds) >= MaxBlockedWorlds {
				err = fmt.Errorf("your list is full (%d worlds)", MaxBlockedWorlds)
				return
			}
			o.Worlds = append(o.Worlds, BlockedWorld{ID: w.ID, Name: w.Name})
			return
		}
		blocklistMu.Lock()
		_, listed := blocklistWorlds[strings.ToLower(w.ID)]
		blocklistMu.Unlock()
		if listed && len(o.Exempt) < MaxBlockedWorlds {
			o.Exempt = append(o.Exempt, BlockedWorld{ID: w.ID, Name: w.Name})
		}
	})
	if err == nil {
		setWorldBlockOverride(w.ID, false)
	}
	return err
}

// WorldBlockStatus is the blocked worlds state for the panel.
type WorldBlockStatus struct {
	Blocked   bool   `json:"blocked"`   // the chatbox is paused here
	Listed    bool   `json:"listed"`    // this world is on a list
	Override  bool   `json:"override"`  // shown anyway until Fern closes
	World     string `json:"world"`     // name of the world you are in
	WorldID   string `json:"worldId"`   // "" when not in one
	By        string `json:"by"`        // community or yours
	Community int    `json:"community"` // worlds on the community list
	Fetched   int64  `json:"fetched"`   // unix ms of the last download
	Error     string `json:"error"`
	Busy      bool   `json:"busy"`
}

// CurrentWorldBlock reports the blocked worlds state.
func CurrentWorldBlock(cfg Config) WorldBlockStatus {
	b := worldBlockFor(cfg)
	st := WorldBlockStatus{Blocked: b.On, Listed: b.Listed, World: b.Name, WorldID: b.ID, By: b.By}
	if !cfg.WorldBlock.Enabled {
		if w, ok := blockWorldFn(); ok {
			st.World, st.WorldID = w.Name, w.ID
		}
	}
	st.Override = b.Listed && !b.On
	blocklistMu.Lock()
	st.Community = len(blocklistWorlds)
	if !blocklistFetched.IsZero() {
		st.Fetched = blocklistFetched.UnixMilli()
	}
	st.Error, st.Busy = blocklistErr, blocklistBusy
	blocklistMu.Unlock()
	return st
}
