package modules

import (
	"encoding/base64"
	"errors"
	"strings"
	"syscall"
	"unsafe"
)

// API keys are kept in config.json encrypted with DPAPI for the current
// Windows account, so copying the file to another account or PC does not copy
// a usable key. The stored form is "dpapi:" plus base64.

var (
	crypt32              = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect   = crypt32.NewProc("CryptUnprotectData")
	procLocalFree        = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

const secretPrefix = "dpapi:"

// cryptprotectUIForbidden keeps DPAPI from ever showing a prompt.
const cryptprotectUIForbidden = 0x1

type dataBlob struct {
	size uint32
	data *byte
}

func newBlob(b []byte) *dataBlob {
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

func dpapi(proc *syscall.LazyProc, in []byte) ([]byte, error) {
	var out dataBlob
	r, _, err := proc.Call(uintptr(unsafe.Pointer(newBlob(in))), 0, 0, 0, 0,
		cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	if r == 0 {
		return nil, err
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	return out.bytes(), nil
}

// sealSecret encrypts a key for storage. An empty key stays empty.
func sealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	sealed, err := dpapi(procCryptProtectData, []byte(plain))
	if err != nil {
		return "", errors.New("could not encrypt the key for this Windows account")
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// openSecret decrypts a stored key. A value without the prefix (typed into
// config.json by hand) is used as is.
func openSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, secretPrefix) {
		return stored, nil
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, secretPrefix))
	if err != nil {
		return "", errors.New("the stored key is damaged; enter it again")
	}
	plain, err := dpapi(procCryptUnprotect, raw)
	if err != nil {
		return "", errors.New("the stored key belongs to another Windows account; enter it again")
	}
	return string(plain), nil
}

// keyHint masks a key for display: the first three and last four characters.
func keyHint(stored string) string {
	plain, err := openSecret(stored)
	if err != nil {
		return "unreadable"
	}
	if plain == "" {
		return ""
	}
	runes := []rune(plain)
	if len(runes) < 12 {
		return "set"
	}
	return string(runes[:3]) + "…" + string(runes[len(runes)-4:])
}

// SetAssistKey stores (or with an empty key, clears) one of the API keys:
// which is "openai", "anthropic" or "stt".
func SetAssistKey(which, key string) error {
	key = strings.TrimSpace(key)
	if strings.ContainsAny(key, "\r\n\t ") {
		return errors.New("a key cannot contain spaces")
	}
	sealed, err := sealSecret(key)
	if err != nil {
		return err
	}
	var setErr error
	UpdateConfig(func(c *Config) {
		switch which {
		case ProviderOpenAI:
			c.AssistOptions.OpenAIKey = sealed
		case ProviderAnthropic:
			c.AssistOptions.AnthropicKey = sealed
		case "stt":
			c.STTOptions.Key = sealed
		default:
			setErr = errors.New("unknown key")
		}
	})
	return setErr
}

// KeyHints returns the masked form of each stored key, empty when unset.
func KeyHints(c Config) map[string]string {
	return map[string]string{
		ProviderOpenAI:    keyHint(c.AssistOptions.OpenAIKey),
		ProviderAnthropic: keyHint(c.AssistOptions.AnthropicKey),
		"stt":             keyHint(c.STTOptions.Key),
	}
}

// scrub removes any copy of the key from text that is shown or logged, in case
// a server echoes it back in an error.
func scrub(text, key string) string {
	if len(key) >= 6 {
		text = strings.ReplaceAll(text, key, "•••")
	}
	return text
}
