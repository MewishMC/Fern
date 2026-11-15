package main

// Fonts for the Appearance page: the installed families the picker lists, and
// the one font a user can upload, kept in %LOCALAPPDATA%\Fern\fonts so it
// survives a restart without filling the page's localStorage.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"Fern/modules"
)

// maxFontBytes caps an uploaded font. Large CJK families run to ~20 MB.
const maxFontBytes = 32 << 20

var (
	gdi32                   = syscall.NewLazyDLL("gdi32.dll")
	fontUser32              = syscall.NewLazyDLL("user32.dll")
	procEnumFontFamiliesExW = gdi32.NewProc("EnumFontFamiliesExW")
	procGetDC               = fontUser32.NewProc("GetDC")
	procReleaseDC           = fontUser32.NewProc("ReleaseDC")
)

// logFont mirrors the head of LOGFONTW; only the face name is read.
type logFont struct {
	Height, Width, Escapement, Orientation, Weight int32
	Italic, Underline, StrikeOut, CharSet          byte
	OutPrecision, ClipPrecision, Quality, Pitch    byte
	FaceName                                       [32]uint16
}

const (
	rasterFontType = 0x1
	defaultCharset = 1
	symbolCharset  = 2
)

var (
	enumMu       sync.Mutex
	enumFamilies map[string]bool // family -> is a symbol font
	enumCallback = sync.OnceValue(func() uintptr {
		return syscall.NewCallback(func(lf *logFont, _ uintptr, fontType uintptr, _ uintptr) uintptr {
			if fontType&rasterFontType != 0 {
				return 1
			}
			units := 0
			for units < len(lf.FaceName) && lf.FaceName[units] != 0 {
				units++
			}
			name := syscall.UTF16ToString(lf.FaceName[:units])
			// "@Name" is the vertical-writing twin of a CJK family, and a
			// name filling all 31 characters was cut short by GDI, so CSS
			// could not find the font by it.
			if name == "" || strings.HasPrefix(name, "@") || units >= len(lf.FaceName)-1 {
				return 1
			}
			enumFamilies[name] = enumFamilies[name] || lf.CharSet == symbolCharset
			return 1
		})
	})
)

// legacyVectorFonts are GDI plotter fonts that browsers cannot draw.
var legacyVectorFonts = map[string]bool{"Modern": true, "Roman": true, "Script": true}

// systemFonts is what /fonts/system returns. Symbol lists the families whose
// letters are pictures (Wingdings and the like), so the picker can label
// them in a readable face.
type systemFonts struct {
	Families []string `json:"families"`
	Symbol   []string `json:"symbol"`
}

// systemFontFamilies lists the installed font families by the names CSS
// expects, sorted case-insensitively. GDI reports one entry per family, so
// styles like "Arial Bold" fold into "Arial" without guessing at suffixes.
func systemFontFamilies() systemFonts {
	list := systemFonts{Families: []string{}, Symbol: []string{}}
	enumMu.Lock()
	defer enumMu.Unlock()

	dc, _, _ := procGetDC.Call(0)
	if dc == 0 {
		return list
	}
	defer procReleaseDC.Call(0, dc)

	enumFamilies = map[string]bool{}
	query := logFont{CharSet: defaultCharset} // every script
	_, _, _ = procEnumFontFamiliesExW.Call(dc, uintptr(unsafe.Pointer(&query)), enumCallback(), 0, 0)

	for name, symbol := range enumFamilies {
		if legacyVectorFonts[name] {
			continue
		}
		list.Families = append(list.Families, name)
		if symbol {
			list.Symbol = append(list.Symbol, name)
		}
	}
	enumFamilies = nil
	byName := func(names []string) func(i, j int) bool {
		return func(i, j int) bool {
			a, b := strings.ToLower(names[i]), strings.ToLower(names[j])
			if a != b {
				return a < b
			}
			return names[i] < names[j]
		}
	}
	sort.Slice(list.Families, byName(list.Families))
	sort.Slice(list.Symbol, byName(list.Symbol))
	return list
}

// fontFormat names an uploaded font's type from its first bytes, or "" if the
// data is not a TrueType, OpenType, WOFF or WOFF2 font.
func fontFormat(head []byte) string {
	if len(head) < 4 {
		return ""
	}
	switch string(head[:4]) {
	case "\x00\x01\x00\x00", "true":
		return "font/ttf"
	case "OTTO":
		return "font/otf"
	case "wOFF":
		return "font/woff"
	case "wOF2":
		return "font/woff2"
	}
	return ""
}

// customFontPath is where the uploaded font lives; "" when there is no data folder.
var customFontPath = func() string {
	dir := modules.DataDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "fonts", "custom-font")
}

// sameOrigin rejects requests a browser sent from another site. Requests with
// no Origin header come from tools, not pages, and are let through, the same
// rule the websocket upgrader applies.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

func handleSystemFonts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(systemFontFamilies())
}

func handleCustomFont(w http.ResponseWriter, r *http.Request) {
	path := customFontPath()
	if path == "" {
		http.Error(w, "no data folder", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodGet, http.MethodHead:
		data, err := os.ReadFile(path)
		format := fontFormat(data)
		if err != nil || format == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", format)
		if r.Method == http.MethodGet {
			_, _ = w.Write(data)
		}

	case http.MethodPut:
		if !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFontBytes))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "font is larger than 32 MB", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "could not read the upload", http.StatusBadRequest)
			return
		}
		if fontFormat(data) == "" {
			http.Error(w, "not a TrueType, OpenType or WOFF font", http.StatusUnsupportedMediaType)
			return
		}
		if err := writeFileAtomic(path, data); err != nil {
			http.Error(w, "could not save the font", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodDelete:
		if !sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			http.Error(w, "could not remove the font", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// writeFileAtomic replaces path in one step, so a failed write never leaves
// half a font behind.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
