package modules

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

const sampleBlocklist = `{"updated": 0, "source": "git", "worlds": [
	{"id": "wrld_b2d9f284-3a77-4a8a-a58e-f8427f87ba79", "name": "Club Orion", "comment": "Added by CyberKitsune"},
	{"id": "not a world", "name": "Bad"},
	{"id": "wrld_23c9382b-24cd-4f4f-8f79-22900e93bc4e", "name": "The Foundry Nightclub"}
]}`

const (
	orionID   = "wrld_b2d9f284-3a77-4a8a-a58e-f8427f87ba79"
	foundryID = "wrld_23c9382b-24cd-4f4f-8f79-22900e93bc4e"
)

// useBlocklistFakes points the blocked worlds code at a temp cache, a fake
// world and an empty list, and puts everything back afterwards.
func useBlocklistFakes(t *testing.T, w WorldInfo, inWorld bool) {
	t.Helper()
	oldFile, oldWorld, oldURL := blocklistFile, blockWorldFn, blocklistURL
	path := filepath.Join(t.TempDir(), "world-blocklist.json")
	blocklistFile = func() string { return path }
	blockWorldFn = func() (WorldInfo, bool) { return w, inWorld }
	reset := func() {
		blocklistMu.Lock()
		blocklistWorlds, blocklistFetched, blocklistTried, blocklistErr, blocklistBusy = map[string]string{}, time.Time{}, time.Time{}, "", false
		blocklistMu.Unlock()
		blockOverMu.Lock()
		blockOver = map[string]bool{}
		blockOverMu.Unlock()
	}
	reset()
	t.Cleanup(func() {
		blocklistFile, blockWorldFn, blocklistURL = oldFile, oldWorld, oldURL
		reset()
	})
}

func TestWorldBlockDefaultsAndSanitize(t *testing.T) {
	d := DefaultConfig().WorldBlock
	if !d.Enabled || !d.Community || !reflect.DeepEqual(d.Allow, []string{ModuleHeart}) || d.Worlds == nil || d.Exempt == nil {
		t.Fatalf("defaults %+v", d)
	}
	c := DefaultConfig()
	c.WorldBlock.Allow = []string{"bogus", ModuleHeart, ModuleHeart, ModuleTime}
	c.WorldBlock.Worlds = []BlockedWorld{{ID: " " + orionID + " ", Name: "  Club   Orion "}, {ID: orionID}, {ID: "wrld_"}, {ID: "usr_123"}}
	c.sanitize()
	o := c.WorldBlock
	if !reflect.DeepEqual(o.Allow, []string{ModuleHeart, ModuleTime}) {
		t.Errorf("allow %v", o.Allow)
	}
	if !reflect.DeepEqual(o.Worlds, []BlockedWorld{{ID: orionID, Name: "Club Orion"}}) {
		t.Errorf("worlds %+v", o.Worlds)
	}
}

func TestParseBlocklist(t *testing.T) {
	worlds, err := parseBlocklist([]byte(sampleBlocklist))
	if err != nil {
		t.Fatal(err)
	}
	if len(worlds) != 2 || worlds[0].Name != "Club Orion" || worlds[1].ID != foundryID {
		t.Errorf("%+v", worlds)
	}
	if _, err := parseBlocklist([]byte(`{"nope": 1}`)); err == nil {
		t.Error("a file with no worlds should fail")
	}
	if _, err := parseBlocklist([]byte(`<html>`)); err == nil {
		t.Error("HTML should fail")
	}
}

func TestFetchBlocklistCachesAndKeepsOldOnError(t *testing.T) {
	useBlocklistFakes(t, WorldInfo{}, false)
	var fail atomic.Bool
	var agent atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent.Store(r.UserAgent())
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(sampleBlocklist))
	}))
	defer srv.Close()
	blocklistURL = srv.URL

	if !blocklistDue(time.Now()) {
		t.Fatal("an empty list should be due")
	}
	if err := fetchBlocklist(); err != nil {
		t.Fatal(err)
	}
	if agent.Load() != webUserAgent {
		t.Errorf("user agent %v", agent.Load())
	}
	st := CurrentWorldBlock(DefaultConfig())
	if st.Community != 2 || st.Fetched == 0 || st.Error != "" {
		t.Errorf("status %+v", st)
	}
	if blocklistDue(time.Now()) {
		t.Error("a fresh list should not be due")
	}
	if blocklistDue(time.Now().Add(23 * time.Hour)) {
		t.Error("due before a day passed")
	}
	if !blocklistDue(time.Now().Add(25 * time.Hour)) {
		t.Error("not due after a day")
	}

	fail.Store(true)
	if err := fetchBlocklist(); err == nil {
		t.Fatal("want an error")
	}
	st = CurrentWorldBlock(DefaultConfig())
	if st.Community != 2 || st.Error == "" {
		t.Errorf("a failed update lost the list: %+v", st)
	}
	if blocklistDue(time.Now().Add(30 * time.Minute)) {
		t.Error("retried within the hour")
	}

	// The saved copy comes back on the next start.
	setBlocklist(nil, time.Time{})
	if _, err := os.Stat(blocklistFile()); err != nil {
		t.Fatal(err)
	}
	loadBlocklistCache()
	if st := CurrentWorldBlock(DefaultConfig()); st.Community != 2 || st.Fetched == 0 {
		t.Errorf("cache %+v", st)
	}
}

func TestWorldBlockHidesLines(t *testing.T) {
	useBlocklistFakes(t, WorldInfo{ID: orionID, Name: "Club Orion"}, true)
	worlds, _ := parseBlocklist([]byte(sampleBlocklist))
	setBlocklist(worlds, time.Now())

	cfg := DefaultConfig()
	cfg.DisplayOptions.Time, cfg.DisplayOptions.Text, cfg.DisplayOptions.Memory = true, true, true
	cfg.WorldBlock.Allow = []string{ModuleTime}
	b := worldBlockFor(cfg)
	if !b.On || b.By != BlockedByCommunity || b.Name != "Club Orion" {
		t.Fatalf("%+v", b)
	}
	lines, _ := buildLines(cfg, false)
	for _, l := range lines {
		if l.ID != ModuleTime {
			t.Errorf("%s shown in a blocked world", l.ID)
		}
	}
	paused := worldBlockQuieted(cfg, b)
	if len(paused) == 0 {
		t.Error("nothing listed as paused")
	}
	for _, id := range paused {
		if id == ModuleTime {
			t.Error("an allowed line is listed as paused")
		}
	}

	// Shown anyway until Fern closes.
	if err := SetWorldBlockOverride(true); err != nil {
		t.Fatal(err)
	}
	if b := worldBlockFor(cfg); b.On || !b.Listed {
		t.Errorf("override %+v", b)
	}
	if st := CurrentWorldBlock(cfg); !st.Override || st.Blocked {
		t.Errorf("status %+v", st)
	}
	_ = SetWorldBlockOverride(false)
	if !worldBlockFor(cfg).On {
		t.Error("pausing again did not block")
	}

	// Exempt worlds and the community switch.
	cfg.WorldBlock.Exempt = []BlockedWorld{{ID: orionID}}
	if worldBlockFor(cfg).On {
		t.Error("an exempt world is blocked")
	}
	cfg.WorldBlock.Exempt = nil
	cfg.WorldBlock.Community = false
	if worldBlockFor(cfg).On {
		t.Error("community list off still blocks")
	}
	// Your own list works without the community list, and wins over exempt.
	cfg.WorldBlock.Worlds = []BlockedWorld{{ID: orionID, Name: "Mine"}}
	cfg.WorldBlock.Exempt = []BlockedWorld{{ID: orionID}}
	if b := worldBlockFor(cfg); !b.On || b.By != BlockedByYou {
		t.Errorf("own list %+v", b)
	}
	cfg.WorldBlock.Enabled = false
	if worldBlockFor(cfg).On {
		t.Error("switched off still blocks")
	}
}

func TestWorldBlockOutsideWorld(t *testing.T) {
	useBlocklistFakes(t, WorldInfo{}, false)
	cfg := DefaultConfig()
	cfg.WorldBlock.Worlds = []BlockedWorld{{ID: orionID}}
	if worldBlockFor(cfg).On {
		t.Error("blocked outside VRChat")
	}
	if err := SetWorldBlockOverride(true); err == nil {
		t.Error("override outside a world should fail")
	}
}
