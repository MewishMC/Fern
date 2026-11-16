package main

// Profiles and backups from the panel's side: its actions, the export
// download, the tray submenu and the per-profile global shortcuts.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"Fern/UI"
	"Fern/modules"
)

// noticeState is the outcome of the last action, for the panel to toast once
// per Seq.
type noticeState struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
	Bad  bool   `json:"bad"`
}

type profilesState struct {
	List     []modules.ProfileInfo `json:"list"`
	Active   string                `json:"active"`
	Switches uint64                `json:"switches"`
	// HotkeyErrs holds, by profile name, why Windows refused its shortcut.
	HotkeyErrs map[string]string `json:"hotkeyErrs"`
	Notice     noticeState       `json:"notice"`
}

type backupsState struct {
	List   []modules.BackupInfo `json:"list"`
	Dir    string               `json:"dir"`
	Notice noticeState          `json:"notice"`
}

var (
	noticeMu      sync.Mutex
	profileNotice noticeState
	backupNotice  noticeState
)

func setNotice(n *noticeState, text string, bad bool) {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	n.Seq++
	n.Text, n.Bad = text, bad
}

func readNotice(n *noticeState) noticeState {
	noticeMu.Lock()
	defer noticeMu.Unlock()
	return *n
}

// profileKeyPrefix marks the UI.Hotkey and UI.TrayItem IDs that are profiles.
const profileKeyPrefix = "profile:"

func currentProfiles() profilesState {
	errs := map[string]string{}
	for id, msg := range UI.HotkeyErrors() {
		if name, ok := strings.CutPrefix(id, profileKeyPrefix); ok {
			errs[name] = msg
		}
	}
	return profilesState{
		List:       orEmpty(modules.ListProfiles()),
		Active:     modules.ActiveProfile(),
		Switches:   modules.ProfileSwitches(),
		HotkeyErrs: errs,
		Notice:     readNotice(&profileNotice),
	}
}

func currentBackups() backupsState {
	return backupsState{
		List:   orEmpty(modules.Backups()),
		Dir:    modules.BackupDir(),
		Notice: readNotice(&backupNotice),
	}
}

// profileHotkeys are the profile shortcuts in the form the window registers.
func profileHotkeys() []UI.Hotkey {
	var keys []UI.Hotkey
	for _, p := range modules.ProfileHotkeys() {
		if mods, key, _, err := modules.ParseHotkey(p.Hotkey); err == nil {
			keys = append(keys, UI.Hotkey{ID: profileKeyPrefix + p.Name, Mods: mods, Key: key})
		}
	}
	return keys
}

// setupProfiles connects the tray menu and the shortcuts to the profiles.
func setupProfiles() {
	pick := func(id string) {
		if name, ok := strings.CutPrefix(id, profileKeyPrefix); ok {
			switchProfile(name)
		}
	}
	UI.OnExtraHotkey = pick
	UI.OnTrayItem = pick
	UI.TrayItems = trayProfiles
	modules.OnProfileChange(func(modules.ProfileEvent) {
		UI.Apply(behaviour(modules.GetConfig().AppOptions))
	})
}

func trayProfiles() (string, []UI.TrayItem) {
	list := modules.ListProfiles()
	items := make([]UI.TrayItem, 0, len(list))
	title := "Profile"
	for _, p := range list {
		items = append(items, UI.TrayItem{ID: profileKeyPrefix + p.Name, Label: p.Name, Checked: p.Active})
		if p.Active {
			title = "Profile: " + p.Name
		}
	}
	return title, items
}

func switchProfile(name string) {
	if err := modules.SwitchProfile(name); err != nil {
		setNotice(&profileNotice, err.Error(), true)
		return
	}
	setNotice(&profileNotice, "Switched to "+modules.ActiveProfile(), false)
}

// handleProfileCommand runs a Profiles page action. Module names the profile
// and Text carries a new name or a shortcut.
func handleProfileCommand(cmd command) {
	var err error
	done := ""
	switch cmd.Action {
	case "switch":
		switchProfile(cmd.Module)
		return
	case "create":
		if err = modules.CreateProfile(cmd.Text); err == nil {
			done = "Saved the current setup as " + modules.ActiveProfile()
		}
	case "overwrite":
		if err = modules.OverwriteProfile(cmd.Module); err == nil {
			done = "Saved the current setup into " + cmd.Module
		}
	case "duplicate":
		if err = modules.DuplicateProfile(cmd.Module, cmd.Text); err == nil {
			done = "Copied " + cmd.Module + " as " + strings.TrimSpace(cmd.Text)
		}
	case "rename":
		if err = modules.RenameProfile(cmd.Module, cmd.Text); err == nil {
			done = "Renamed " + cmd.Module + " to " + strings.TrimSpace(cmd.Text)
		}
	case "delete":
		if err = modules.DeleteProfile(cmd.Module); err == nil {
			done = "Deleted " + cmd.Module
		}
	case "deactivate":
		if err = modules.DeactivateProfile(); err == nil {
			done = "No profile active; changes are no longer saved to one"
		}
	case "up", "down":
		delta := 1
		if cmd.Action == "up" {
			delta = -1
		}
		err = modules.MoveProfile(cmd.Module, delta)
	case "hotkey":
		if err = modules.SetProfileHotkey(cmd.Module, cmd.Text); err == nil {
			if cmd.Text == "" {
				done = "Shortcut for " + cmd.Module + " off"
			} else {
				done = "Shortcut for " + cmd.Module + " set"
			}
		}
	case "appearance":
		if err = modules.SetProfileAppearance(cmd.Module, cmd.Config); err == nil {
			done = "Saved this look with " + cmd.Module
		}
	case "clearAppearance":
		if err = modules.SetProfileAppearance(cmd.Module, nil); err == nil {
			done = cmd.Module + " no longer changes the look"
		}
	default:
		return
	}
	if err != nil {
		setNotice(&profileNotice, err.Error(), true)
	} else if done != "" {
		setNotice(&profileNotice, done, false)
	}
}

// handleBackupCommand runs an App page backup action. Config carries an
// imported file and Text the name of a backup to restore.
func handleBackupCommand(cmd command) {
	switch cmd.Action {
	case "import":
		if err := modules.ImportBackup(cmd.Config); err != nil {
			setNotice(&backupNotice, err.Error(), true)
			return
		}
		setNotice(&backupNotice, "Imported; the previous settings are in the backup list", false)
	case "restore":
		if err := modules.RestoreBackup(cmd.Text); err != nil {
			setNotice(&backupNotice, err.Error(), true)
			return
		}
		setNotice(&backupNotice, "Restored; the settings from just before are in the backup list", false)
	case "now":
		if _, err := modules.AutoBackup(modules.BackupManual); err != nil {
			setNotice(&backupNotice, "Could not make a backup: "+err.Error(), true)
			return
		}
		setNotice(&backupNotice, "Backup made", false)
	case "refresh":
		modules.RefreshBackups()
	}
}

// backupBeforeUpdate keeps a copy of the settings in case the new version
// reads them differently.
func backupBeforeUpdate() {
	if name, err := modules.AutoBackup(modules.BackupUpdate); err != nil {
		fmt.Println("update: could not back up the settings:", err)
	} else {
		fmt.Println("update: settings backed up as", name)
	}
}

// localRequest accepts only requests addressed to the loopback panel itself,
// so neither another website nor a DNS-rebound name can read the settings.
func localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
	default:
		return false
	}
	return sameOrigin(r)
}

// handleBackupExport serves the settings and profiles as a download.
func handleBackupExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, err := modules.ExportBackup()
	if err != nil {
		http.Error(w, "could not export the settings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="fern-backup-`+time.Now().Format("2006-01-02")+`.json"`)
	_, _ = w.Write(data)
}

// handleProfileAppearance serves the look saved with a profile.
func handleProfileAppearance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, ok := modules.ProfileAppearance(r.URL.Query().Get("name"))
	if !ok || !json.Valid(data) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
