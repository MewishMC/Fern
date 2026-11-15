package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Secrets are tokens, client secrets and keys for the app integrations. They
// are kept out of config.json, which the panel receives in full on every state
// push, and stored in secrets.dat next to it, encrypted with DPAPI so only this
// Windows user can read them. The panel can set or clear a secret but never
// reads one back; it only learns whether each one is set.
const (
	SecretDiscordClientSecret = "discord_secret"
	SecretDiscordAccess       = "discord_access"
	SecretDiscordRefresh      = "discord_refresh"
	SecretTwitchToken         = "twitch_token"
	SecretTwitchRefresh       = "twitch_refresh"
	SecretVoicemodKey         = "voicemod_key"
)

// panelSecrets are the secrets the panel may write directly.
var panelSecrets = map[string]bool{
	SecretDiscordClientSecret: true,
	SecretTwitchToken:         true,
	SecretVoicemodKey:         true,
}

var (
	secretsMu     sync.Mutex
	secretsCache  map[string]string
	secretsLoaded bool
	secretsFile   string // overridden by tests
)

func secretsPath() string {
	if secretsFile != "" {
		return secretsFile
	}
	path := cfgPath
	if path == "" {
		path = configPath()
	}
	return filepath.Join(filepath.Dir(path), "secrets.dat")
}

func loadSecretsLocked() {
	if secretsLoaded {
		return
	}
	secretsLoaded = true
	secretsCache = map[string]string{}
	data, err := os.ReadFile(secretsPath())
	if err != nil {
		return
	}
	plain, err := dpapiDecrypt(data)
	if err != nil {
		fmt.Println("secrets: could not decrypt secrets.dat, starting empty")
		return
	}
	_ = json.Unmarshal(plain, &secretsCache)
}

// GetSecret returns a stored secret, or "".
func GetSecret(name string) string {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	loadSecretsLocked()
	return secretsCache[name]
}

// SetSecret stores or, with an empty value, removes a secret.
func SetSecret(name, value string) error {
	value = strings.TrimSpace(value)
	secretsMu.Lock()
	defer secretsMu.Unlock()
	loadSecretsLocked()
	if secretsCache[name] == value {
		return nil
	}
	if value == "" {
		delete(secretsCache, name)
	} else {
		secretsCache[name] = value
	}
	plain, err := json.Marshal(secretsCache)
	if err != nil {
		return err
	}
	sealed, err := dpapiEncrypt(plain)
	if err != nil {
		return err
	}
	path := secretsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	NotifyChange()
	return nil
}

// SetPanelSecret is SetSecret limited to the secrets the panel may write.
func SetPanelSecret(name, value string) error {
	if !panelSecrets[name] {
		return errors.New("unknown secret")
	}
	if name == SecretTwitchToken {
		value = strings.TrimPrefix(strings.TrimSpace(value), "oauth:")
		// A pasted token replaces any signed-in session.
		_ = SetSecret(SecretTwitchRefresh, "")
		resetTwitchUser()
	}
	if name == SecretDiscordClientSecret {
		discordKick()
	}
	if name == SecretVoicemodKey {
		voicemodKick()
	}
	return SetSecret(name, value)
}

// SecretsSet reports which secrets have a value, for the panel's masked fields.
func SecretsSet() map[string]bool {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	loadSecretsLocked()
	out := make(map[string]bool, len(secretsCache))
	for k, v := range secretsCache {
		out[k] = v != ""
	}
	return out
}

type dataBlob struct {
	size uint32
	data *byte
}

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

func blobOf(b []byte) *dataBlob {
	if len(b) == 0 {
		return &dataBlob{}
	}
	return &dataBlob{size: uint32(len(b)), data: &b[0]}
}

func (b *dataBlob) bytes() []byte {
	if b.data == nil || b.size == 0 {
		return nil
	}
	out := make([]byte, b.size)
	copy(out, unsafe.Slice(b.data, b.size))
	return out
}

func dpapiEncrypt(plain []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procCryptProtectData.Call(uintptr(unsafe.Pointer(blobOf(plain))), 0, 0, 0, 0,
		cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}

func dpapiDecrypt(sealed []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := procCryptUnprotectData.Call(uintptr(unsafe.Pointer(blobOf(sealed))), 0, 0, 0, 0,
		cryptProtectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}
