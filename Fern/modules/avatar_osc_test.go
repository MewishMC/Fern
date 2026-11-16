package modules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	// Keep the tests off the real VRChat folder.
	oscConfigRoot = func() string { return filepath.Join(os.TempDir(), "fern-test-no-osc-folder") }
	oscSelfUser = func() string { return "" }
	oscIndexAsync = false
}

const sampleOSCConfig = "\xEF\xBB\xBF{\r\n" +
	`  "id": "avtr_aaa",
  "name": "  Fern   Fox ",
  "hash": 12,
  "parameters": [
    {"name": "VelocityX", "output": {"address": "/avatar/parameters/VelocityX", "type": "Float"}},
    {"name": "Outfit", "input": {"address": "/avatar/parameters/Outfit", "type": "Int"}, "output": {"address": "/avatar/parameters/Outfit", "type": "Int"}},
    {"name": "Glasses", "input": {"address": "/avatar/parameters/Glasses", "type": "Bool"}, "output": {"address": "/avatar/parameters/Glasses", "type": "Bool"}},
    {"name": "Odd", "input": {"address": "/avatar/parameters/Odd", "type": "String"}},
    {"name": "", "input": {"address": "/avatar/parameters/x", "type": "Bool"}}
  ]
}`

func TestReadOSCHeadWithBOM(t *testing.T) {
	id, name, err := readOSCHead(strings.NewReader(sampleOSCConfig))
	if err != nil || id != "avtr_aaa" || name != "Fern Fox" {
		t.Fatalf("got %q %q %v", id, name, err)
	}
	// Without a BOM, and with the name after other keys.
	id, name, err = readOSCHead(strings.NewReader(`{"hash":1,"parameters":[{"name":"a"}],"name":"B","id":"avtr_b"}`))
	if err != nil || id != "avtr_b" || name != "B" {
		t.Fatalf("got %q %q %v", id, name, err)
	}
	if _, _, err := readOSCHead(strings.NewReader(`[1]`)); err == nil {
		t.Fatal("an array is not a config")
	}
}

func TestParseOSCConfig(t *testing.T) {
	cfg, err := parseOSCConfig(strings.NewReader(sampleOSCConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ID != "avtr_aaa" || cfg.Name != "Fern Fox" {
		t.Fatalf("got %+v", cfg)
	}
	want := []AvatarConfigParam{
		{Name: "Glasses", Type: "bool", Input: true, Address: "Glasses"},
		{Name: "Outfit", Type: "int", Input: true, Address: "Outfit"},
		{Name: "VelocityX", Type: "float"}, // no input: read-only
	}
	if len(cfg.Params) != len(want) {
		t.Fatalf("params %+v", cfg.Params)
	}
	for i := range want {
		if cfg.Params[i] != want[i] {
			t.Errorf("param %d = %+v, want %+v", i, cfg.Params[i], want[i])
		}
	}
	if _, err := parseOSCConfig(strings.NewReader("\xEF\xBB\xBF{broken")); err == nil {
		t.Fatal("broken JSON parsed")
	}
}

// useOSCFolder points the index at a temporary OSC folder.
func useOSCFolder(t *testing.T, self string) (root string, clock *time.Time) {
	t.Helper()
	root = t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	clock = &now
	oldRoot, oldSelf, oldNow := oscConfigRoot, oscSelfUser, oscIndexNow
	reset := func() {
		oscIdxMu.Lock()
		oscIdxFiles, oscIdxByID, oscParams = map[string]oscIndexFile{}, map[string]oscIndexFile{}, map[string]oscParamCache{}
		oscIdxAt, oscIdxSelf, oscIdxReads, oscIdxBusy = time.Time{}, "", 0, false
		oscIdxMu.Unlock()
	}
	reset()
	oscConfigRoot = func() string { return root }
	oscSelfUser = func() string { return self }
	oscIndexNow = func() time.Time { return *clock }
	t.Cleanup(func() {
		oscConfigRoot, oscSelfUser, oscIndexNow = oldRoot, oldSelf, oldNow
		reset()
	})
	return root, clock
}

func writeOSCConfig(t *testing.T, root, user, id, name string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(root, user, "Avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".json")
	body := "\xEF\xBB\xBF" + `{"id":"` + id + `","name":"` + name + `","parameters":[{"name":"Hat","input":{"address":"/avatar/parameters/Hat","type":"Bool"}}]}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOSCIndexCachesByModTime(t *testing.T) {
	root, clock := useOSCFolder(t, "")
	base := time.Unix(1_600_000_000, 0)
	writeOSCConfig(t, root, "usr_a", "avtr_1", "One", base)
	path2 := writeOSCConfig(t, root, "usr_a", "avtr_2", "Two", base)
	if err := os.WriteFile(filepath.Join(root, "usr_a", "Avatars", "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := OSCAvatarName("avtr_1"); got != "One" {
		t.Fatalf("name %q", got)
	}
	if oscIdxReads != 2 {
		t.Fatalf("read %d files, want 2", oscIdxReads)
	}
	// Within the interval nothing is walked again.
	writeOSCConfig(t, root, "usr_a", "avtr_2", "Two again", base.Add(time.Hour))
	if got := OSCAvatarName("avtr_2"); got != "Two" || oscIdxReads != 2 {
		t.Fatalf("name %q after %d reads", got, oscIdxReads)
	}
	// After it, only the changed file is read.
	*clock = clock.Add(oscIndexEvery + time.Second)
	if got := OSCAvatarName("avtr_2"); got != "Two again" || oscIdxReads != 3 {
		t.Fatalf("name %q after %d reads", got, oscIdxReads)
	}
	*clock = clock.Add(oscIndexEvery + time.Second)
	_ = OSCAvatarName("avtr_1")
	if oscIdxReads != 3 {
		t.Fatalf("unchanged files read again: %d", oscIdxReads)
	}
	// A removed file drops out.
	if err := os.Remove(path2); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(oscIndexEvery + time.Second)
	if got := OSCAvatarName("avtr_2"); got != "" {
		t.Fatalf("removed avatar still named %q", got)
	}

	// The parameters are read on demand and cached.
	cfg, ok := AvatarConfig("avtr_1")
	if !ok || len(cfg.Params) != 1 || cfg.Params[0].Name != "Hat" || !cfg.Params[0].Input {
		t.Fatalf("config %+v %v", cfg, ok)
	}
	oscIdxMu.Lock()
	cached := len(oscParams)
	oscIdxMu.Unlock()
	if cached != 1 {
		t.Fatalf("%d configs cached", cached)
	}
	if _, ok := AvatarConfig("avtr_none"); ok {
		t.Fatal("unknown avatar has a config")
	}
}

func TestOSCIndexPrefersLoggedInUser(t *testing.T) {
	root, _ := useOSCFolder(t, "usr_ME")
	base := time.Unix(1_600_000_000, 0)
	writeOSCConfig(t, root, "usr_me", "avtr_1", "Mine", base)
	writeOSCConfig(t, root, "usr_other", "avtr_1", "Theirs", base.Add(time.Hour))
	writeOSCConfig(t, root, "usr_other", "avtr_2", "Only theirs", base)
	if got := OSCAvatarName("avtr_1"); got != "Mine" {
		t.Fatalf("name %q, want the logged-in user's", got)
	}
	// Names still come from other users' folders.
	if got := OSCAvatarName("avtr_2"); got != "Only theirs" {
		t.Fatalf("name %q", got)
	}
	// The picker lists only the logged-in user's avatars.
	list, total := AvatarCatalog("", 0)
	if total != 1 || list[0].ID != "avtr_1" || !list[0].Config {
		t.Fatalf("catalog %+v", list)
	}

	// Without a known user, the newest file wins and all are listed.
	oscSelfUser = func() string { return "" }
	if got := OSCAvatarName("avtr_1"); got != "Theirs" {
		t.Fatalf("name %q, want the newest", got)
	}
	if _, total := AvatarCatalog("", 0); total != 2 {
		t.Fatalf("catalog total %d", total)
	}
}

func TestAvatarNamePrecedence(t *testing.T) {
	cases := []struct {
		e    AvatarEntry
		osc  string
		want string
	}{
		{AvatarEntry{Name: "Typed", NameSet: true}, "Config", "Typed"},
		{AvatarEntry{Name: "From log"}, "Config", "Config"},
		{AvatarEntry{Name: "From log"}, "", "From log"},
		{AvatarEntry{}, "", ""},
	}
	for _, c := range cases {
		if got := avatarName(c.e, c.osc); got != c.want {
			t.Errorf("avatarName(%+v, %q) = %q, want %q", c.e, c.osc, got, c.want)
		}
	}
}

func TestAvatarEntriesAndCatalogUseOSCNames(t *testing.T) {
	useTempAvatars(t)
	root, _ := useOSCFolder(t, "")
	base := time.Unix(1_600_000_000, 0)
	writeOSCConfig(t, root, "usr_a", "avtr_named", "Config name", base)
	writeOSCConfig(t, root, "usr_a", "avtr_typed", "Config typed", base)
	writeOSCConfig(t, root, "usr_a", "avtr_never", "Never worn", base)
	oldNow := avatarsNow
	t.Cleanup(func() { avatarsNow = oldNow })
	for i, id := range []string{"avtr_named", "avtr_typed", "avtr_raw"} {
		at := time.Unix(1_700_000_000+int64(i), 0)
		avatarsNow = func() time.Time { return at }
		noteAvatarWorn(id)
	}
	if err := RenameAvatar("avtr_typed", "Mine"); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range AvatarEntries() {
		got[e.ID] = e.Name
	}
	if got["avtr_named"] != "Config name" || got["avtr_typed"] != "Mine" || got["avtr_raw"] != "" {
		t.Fatalf("names %v", got)
	}
	// The stored entry keeps no config name, so it follows the config.
	if e, _ := AvatarInfo("avtr_named"); e.Name != "" {
		t.Fatalf("stored name %q", e.Name)
	}

	list, total := AvatarCatalog("", 0)
	if total != 4 {
		t.Fatalf("catalog %+v", list)
	}
	byID := map[string]AvatarCatalogItem{}
	for _, it := range list {
		byID[it.ID] = it
	}
	if it := byID["avtr_never"]; it.Known || !it.Config || it.Name != "Never worn" {
		t.Fatalf("never worn %+v", it)
	}
	if it := byID["avtr_raw"]; !it.Known || it.Config {
		t.Fatalf("raw %+v", it)
	}
	if list[0].ID != "avtr_raw" { // most recently worn first
		t.Fatalf("order %+v", list)
	}
	if list, total := AvatarCatalog("NEVER", 0); total != 1 || list[0].ID != "avtr_never" {
		t.Fatalf("search %+v", list)
	}
	if list, total := AvatarCatalog("avtr_", 2); total != 4 || len(list) != 2 {
		t.Fatalf("limit %d of %d", len(list), total)
	}
	if _, ok := AvatarInfo("avtr_never"); ok {
		t.Fatal("listing an avatar made an entry")
	}
}

func TestParamSavedBounds(t *testing.T) {
	cases := []struct {
		typ  string
		in   float64
		want float64
	}{
		{"bool", 0, 0}, {"bool", 1, 1}, {"bool", 5, 1},
		{"int", -3, 0}, {"int", 300, 255}, {"int", 7.6, 8},
		{"float", -2, -1}, {"float", 1.5, 1}, {"float", 0.123456, 0.1235},
	}
	for _, c := range cases {
		got, err := paramSaved(c.typ, c.in)
		if err != nil || got.Type != c.typ || got.Value != c.want {
			t.Errorf("paramSaved(%q, %v) = %+v, %v; want %v", c.typ, c.in, got, err, c.want)
		}
	}
	if _, err := paramSaved("string", 1); err == nil {
		t.Error("unknown type accepted")
	}
	nan := 0.0
	if _, err := paramSaved("float", nan/nan); err == nil {
		t.Error("NaN accepted")
	}
}

func TestSetAvatarSavedValueMakesEntry(t *testing.T) {
	useTempAvatars(t)
	useOSCFolder(t, "")
	if err := SetAvatarSavedValue("avtr_new", "Hat", "int", 400); err != nil {
		t.Fatal(err)
	}
	e, ok := AvatarInfo("avtr_new")
	if !ok || !e.Restore || !containsString(e.Params, "Hat") || e.Values["Hat"] != (SavedParam{Type: "int", Value: 255}) {
		t.Fatalf("entry %+v %v", e, ok)
	}
	if err := SetAvatarSavedValue("avtr_new", "Hat", "float", 0.5); err != nil {
		t.Fatal(err)
	}
	if e, _ := AvatarInfo("avtr_new"); len(e.Params) != 1 || e.Values["Hat"].Value != 0.5 {
		t.Fatalf("entry %+v", e)
	}
	// Renaming with no name makes no entry.
	if err := RenameAvatar("avtr_other", ""); err == nil {
		t.Fatal("cleared the name of an unknown avatar")
	}
	if err := RenameAvatar("avtr_other", "Named"); err != nil {
		t.Fatal(err)
	}
	if e, ok := AvatarInfo("avtr_other"); !ok || !e.NameSet || e.Name != "Named" {
		t.Fatalf("renamed %+v", e)
	}
}

func TestSendAvatarValueChecksTheConfig(t *testing.T) {
	sent := captureSent(t)
	root, _ := useOSCFolder(t, "")
	dir := filepath.Join(root, "usr_a", "Avatars")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "avtr_aaa.json"), []byte(sampleOSCConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SendAvatarValue("avtr_aaa", "Outfit", "int", 3); err == nil {
		t.Fatal("sent to an avatar not worn")
	}
	wearAvatar(t, "avtr_aaa", nil)
	if err := SendAvatarValue("avtr_aaa", "VelocityX", "float", 0.5); err == nil {
		t.Fatal("sent a read-only parameter")
	}
	if err := SendAvatarValue("avtr_aaa", "Missing", "bool", 1); err == nil {
		t.Fatal("sent a parameter the avatar lacks")
	}
	if err := SendAvatarValue("avtr_aaa", "Outfit", "bool", 1); err == nil {
		t.Fatal("sent the wrong type")
	}
	if err := SendAvatarValue("avtr_aaa", "Outfit", "int", 999); err != nil {
		t.Fatal(err)
	}
	if err := SendAvatarValue("avtr_aaa", "Glasses", "bool", 1); err != nil {
		t.Fatal(err)
	}
	got := sent.list()
	if len(got) != 2 || got[0] != "Outfit=255" || got[1] != "Glasses=true" {
		t.Fatalf("sent %v", got)
	}
}
