// Package openvr talks to SteamVR through Valve's OpenVR flat C API.
//
// Fern ships as a single executable, so openvr_api.dll is embedded here and
// written to %LOCALAPPDATA%\Fern\openvr\<version>\ the first time it is
// needed. It is only rewritten when the file there is missing or differs from
// the embedded copy, and it is always loaded from that full path, never from
// the search path.
//
// The DLL is Valve's official build from https://github.com/ValveSoftware/openvr
// at tag v2.15.6, file bin/win64/openvr_api.dll, 837272 bytes,
// SHA-256 bab8ac6ef64e68a9ca53315b0014d131088584b2efdfa6db511d67ec03cfcb4a.
// It is distributed under Valve's BSD 3-clause licence, see LICENSE-openvr.
package openvr

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Version is the OpenVR SDK release the embedded DLL and headers come from.
const Version = "v2.15.6"

// DLLSHA256 is the checksum of the embedded openvr_api.dll.
const DLLSHA256 = "bab8ac6ef64e68a9ca53315b0014d131088584b2efdfa6db511d67ec03cfcb4a"

//go:embed openvr_api.dll
var dllData []byte

// License is Valve's licence text for the embedded DLL.
//
//go:embed LICENSE-openvr
var License string

// dllDir is where the DLL is extracted: %LOCALAPPDATA%\Fern\openvr\<version>.
func dllDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "Fern", "openvr", Version), nil
}

// fileSHA256 returns the hex checksum of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ensureFile writes data to dir\name unless a file with the same SHA-256 is
// already there. It reports whether it wrote the file. The new copy goes to a
// temporary name first, so a crash never leaves half a DLL behind.
func ensureFile(dir, name string, data []byte, sum string) (string, bool, error) {
	path := filepath.Join(dir, name)
	if have, err := fileSHA256(path); err == nil && have == sum {
		return path, false, nil
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return "", false, errors.New("openvr: embedded DLL does not match its checksum")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	tmp, err := os.CreateTemp(dir, name+".*.tmp")
	if err != nil {
		return "", false, err
	}
	_, werr := io.Copy(tmp, bytes.NewReader(data))
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return "", false, werr
	}
	return path, true, nil
}

var (
	loadOnce sync.Once
	loadErr  error
	dll      *syscall.LazyDLL

	procInitInternal2      *syscall.LazyProc
	procShutdownInternal   *syscall.LazyProc
	procIsHmdPresent       *syscall.LazyProc
	procIsRuntimeInstalled *syscall.LazyProc
	procGetGenericIface    *syscall.LazyProc
	procIsIfaceVersionOK   *syscall.LazyProc
	procInitErrorEnglish   *syscall.LazyProc
)

// load extracts the DLL if needed and resolves the flat API. Safe to call
// repeatedly; the work happens once.
func load() error {
	loadOnce.Do(func() {
		dir, err := dllDir()
		if err != nil {
			loadErr = err
			return
		}
		path, _, err := ensureFile(dir, "openvr_api.dll", dllData, DLLSHA256)
		if err != nil {
			loadErr = fmt.Errorf("openvr: could not extract DLL: %w", err)
			return
		}
		d := syscall.NewLazyDLL(path)
		if err := d.Load(); err != nil {
			loadErr = fmt.Errorf("openvr: could not load %s: %w", path, err)
			return
		}
		dll = d
		procInitInternal2 = d.NewProc("VR_InitInternal2")
		procShutdownInternal = d.NewProc("VR_ShutdownInternal")
		procIsHmdPresent = d.NewProc("VR_IsHmdPresent")
		procIsRuntimeInstalled = d.NewProc("VR_IsRuntimeInstalled")
		procGetGenericIface = d.NewProc("VR_GetGenericInterface")
		procIsIfaceVersionOK = d.NewProc("VR_IsInterfaceVersionValid")
		procInitErrorEnglish = d.NewProc("VR_GetVRInitErrorAsEnglishDescription")
		for _, p := range []*syscall.LazyProc{procInitInternal2, procShutdownInternal, procIsHmdPresent,
			procIsRuntimeInstalled, procGetGenericIface, procIsIfaceVersionOK, procInitErrorEnglish} {
			if err := p.Find(); err != nil {
				loadErr = fmt.Errorf("openvr: %w", err)
				return
			}
		}
	})
	return loadErr
}

// Load makes sure the DLL is extracted and loaded, and returns its path.
func Load() (string, error) {
	if err := load(); err != nil {
		return "", err
	}
	return dll.Name, nil
}
