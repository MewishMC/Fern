package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Backups hold the whole configuration and the profiles, never a secret:
// API keys, tokens and sign-ins stay on this PC (secrets.dat and the
// DPAPI-sealed keys are left out entirely). Fern writes one to
// %LOCALAPPDATA%\Fern\backups before an update is applied and before an
// import or restore replaces the settings, and keeps the newest
// KeepBackups of them.

// KeepBackups is how many automatic backups are kept.
const KeepBackups = 10

// Reasons a backup was made, the last part of its file name.
const (
	BackupUpdate  = "update"
	BackupImport  = "import"
	BackupRestore = "restore"
	BackupManual  = "manual"
)

// maxBackupBytes caps a backup file; profile looks can carry backdrops.
const maxBackupBytes = 64 << 20

const backupStamp = "2006-01-02_150405.000"

var backupName = regexp.MustCompile(`^fern-(\d{4}-\d{2}-\d{2}_\d{6}\.\d{3})-(update|import|restore|manual)\.json$`)

// BackupInfo describes one file in the backups folder.
type BackupInfo struct {
	Name    string `json:"name"`
	Created int64  `json:"created"` // unix ms
	Reason  string `json:"reason"`
	Size    int64  `json:"size"`
}

// backupFile is the export format.
type backupFile struct {
	App      string          `json:"app"`  // "fern"
	Kind     string          `json:"kind"` // "backup"
	Version  int             `json:"version"`
	Created  string          `json:"created"`
	Config   json.RawMessage `json:"config"`
	Profiles *profileStore   `json:"profiles,omitempty"`
}

var (
	backupMu      sync.Mutex
	backupList    []BackupInfo
	backupListed  bool
	backupDirPath string // overridden by tests
	backupNow     = time.Now
)

// BackupDir is where automatic backups go, or "" before a config is loaded.
func BackupDir() string {
	if backupDirPath != "" {
		return backupDirPath
	}
	if cfgPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(cfgPath), "backups")
}

// exportConfig is c without anything secret.
func exportConfig(c Config) Config {
	c = c.clone().Public()
	h := &c.HeartRate
	h.PulsoidToken, h.HypeRateKey, h.HypeRateID, h.CustomURL = "", "", "", ""
	return c
}

// ExportBackup returns the settings and profiles as a backup file.
func ExportBackup() ([]byte, error) {
	store := exportProfiles()
	conf, err := json.Marshal(exportConfig(GetConfig()))
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(backupFile{
		App: "fern", Kind: "backup", Version: 1,
		Created:  backupNow().Format(time.RFC3339),
		Config:   conf,
		Profiles: &store,
	}, "", " ")
}

// parseBackup reads a backup file, or a plain config.json, into a config
// checked by sanitize and, when the file has them, profiles. Nothing is
// changed.
func parseBackup(data []byte) (Config, *profileStore, error) {
	var head map[string]json.RawMessage
	if err := json.Unmarshal(data, &head); err != nil || head == nil {
		return Config{}, nil, errors.New("that file is not a Fern backup")
	}
	var kind, app string
	_ = json.Unmarshal(head["kind"], &kind)
	_ = json.Unmarshal(head["app"], &app)

	var conf json.RawMessage
	var store *profileStore
	switch {
	case kind == "backup" && app == "fern":
		var file backupFile
		if err := json.Unmarshal(data, &file); err != nil {
			return Config{}, nil, fmt.Errorf("the backup is damaged: %w", err)
		}
		if file.Version > 1 {
			return Config{}, nil, errors.New("that backup is from a newer Fern; update Fern first")
		}
		conf = file.Config
		if file.Profiles != nil {
			normalized := normalizeStore(*file.Profiles)
			store = &normalized
		}
	case kind == "appearance":
		return Config{}, nil, errors.New("that is an appearance file; import it on the Appearance page")
	case head["display_options"] != nil:
		conf = data // a config.json copied by hand
	default:
		return Config{}, nil, errors.New("that file is not a Fern backup")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(conf, &obj); err != nil || obj == nil {
		return Config{}, nil, errors.New("the backup has no settings in it")
	}
	next := DefaultConfig()
	if err := json.Unmarshal(conf, &next); err != nil {
		return Config{}, nil, fmt.Errorf("the backup's settings are damaged: %w", err)
	}
	if next.TextMessages == nil {
		next.TextMessages = DefaultConfig().TextMessages
	}
	next.sanitize()
	return exportConfig(next), store, nil
}

// ImportBackup replaces the settings, and the profiles when the file has
// them, after backing up the current ones. Secrets are kept as they are.
func ImportBackup(data []byte) error { return importBackup(data, BackupImport) }

func importBackup(data []byte, reason string) error {
	if len(data) > maxBackupBytes {
		return errors.New("that file is too large to be a Fern backup")
	}
	next, store, err := parseBackup(data)
	if err != nil {
		return err
	}
	if _, err := AutoBackup(reason); err != nil {
		return fmt.Errorf("could not back up the current settings first, so nothing was changed: %w", err)
	}
	return mutateProfiles(func() (string, error) {
		cur := GetConfig()
		h, ch := &next.HeartRate, cur.HeartRate
		h.PulsoidToken, h.HypeRateKey, h.HypeRateID, h.CustomURL = ch.PulsoidToken, ch.HypeRateKey, ch.HypeRateID, ch.CustomURL
		SetConfig(next) // keeps the sealed API keys
		if store != nil {
			profiles = *store
		} else {
			// The active profile would otherwise soak up the imported settings.
			profiles.Active = ""
		}
		profileSwitch++
		return ProfileSwitched, nil
	})
}

// RestoreBackup imports one of the files in the backups folder.
func RestoreBackup(name string) error {
	if !backupName.MatchString(name) {
		return errors.New("unknown backup")
	}
	dir := BackupDir()
	if dir == "" {
		return errors.New("no backups folder")
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fmt.Errorf("could not read the backup: %w", err)
	}
	return importBackup(data, BackupRestore)
}

// AutoBackup writes a dated backup to the backups folder and keeps only the
// newest KeepBackups. It returns the file's name.
func AutoBackup(reason string) (string, error) {
	switch reason {
	case BackupUpdate, BackupImport, BackupRestore, BackupManual:
	default:
		return "", errors.New("unknown backup reason")
	}
	dir := BackupDir()
	if dir == "" {
		return "", errors.New("no backups folder")
	}
	data, err := ExportBackup()
	if err != nil {
		return "", err
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	stamp := backupNow()
	name := ""
	for {
		name = "fern-" + stamp.Format(backupStamp) + "-" + reason + ".json"
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			break
		}
		stamp = stamp.Add(time.Millisecond)
	}
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	list := listBackupsLocked(dir)
	for len(list) > KeepBackups {
		old := list[len(list)-1]
		if err := os.Remove(filepath.Join(dir, old.Name)); err != nil {
			fmt.Println("backups: could not remove", old.Name+":", err)
			break
		}
		list = list[:len(list)-1]
	}
	backupList, backupListed = list, true
	return name, nil
}

// listBackupsLocked reads the backups folder, newest first.
func listBackupsLocked(dir string) []BackupInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []BackupInfo{}
	}
	list := []BackupInfo{}
	for _, e := range entries {
		m := backupName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		when, err := time.ParseInLocation(backupStamp, m[1], time.Local)
		if err != nil {
			continue
		}
		var size int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		list = append(list, BackupInfo{Name: e.Name(), Created: when.UnixMilli(), Reason: m[2], Size: size})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name > list[j].Name })
	return list
}

// Backups lists the files in the backups folder, newest first.
func Backups() []BackupInfo {
	backupMu.Lock()
	defer backupMu.Unlock()
	if !backupListed {
		if dir := BackupDir(); dir != "" {
			backupList = listBackupsLocked(dir)
			backupListed = true
		}
	}
	return append([]BackupInfo{}, backupList...)
}

// RefreshBackups reads the backups folder again, for files added or removed
// by hand.
func RefreshBackups() {
	backupMu.Lock()
	backupListed = false
	backupMu.Unlock()
}
