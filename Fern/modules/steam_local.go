package modules

import (
	"strconv"
	"syscall"
	"unsafe"
)

// steamRegKey is where the Steam client keeps its live state.
const steamRegKey = `Software\Valve\Steam`

// readSteamLocal reads the running game, Steam's folder and the signed-in
// account from the current user's registry. Steam clears RunningAppID when a
// game closes and when it exits normally.
func readSteamLocal() (steamLocal, error) {
	var out steamLocal
	k, err := openUserKey(steamRegKey)
	if err != nil {
		return out, err
	}
	defer syscall.RegCloseKey(k)
	out.Installed = true
	if v, ok := regDWORD(k, "RunningAppID"); ok {
		out.AppID = v
	}
	out.Path, _ = regString(k, "SteamPath")
	if a, err := openUserKey(steamRegKey + `\ActiveProcess`); err == nil {
		if v, ok := regDWORD(a, "ActiveUser"); ok {
			out.Account = v
		}
		syscall.RegCloseKey(a)
	}
	if out.AppID != 0 {
		if a, err := openUserKey(steamRegKey + `\Apps\` + strconv.FormatUint(uint64(out.AppID), 10)); err == nil {
			out.RegName, _ = regString(a, "Name")
			syscall.RegCloseKey(a)
		}
	}
	return out, nil
}

func openUserKey(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var h syscall.Handle
	if err := syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, p, 0, syscall.KEY_READ, &h); err != nil {
		return 0, err
	}
	return h, nil
}

func regDWORD(k syscall.Handle, name string) (uint32, bool) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	var v, typ uint32
	size := uint32(unsafe.Sizeof(v))
	if err := syscall.RegQueryValueEx(k, n, nil, &typ, (*byte)(unsafe.Pointer(&v)), &size); err != nil || typ != syscall.REG_DWORD {
		return 0, false
	}
	return v, true
}

func regString(k syscall.Handle, name string) (string, bool) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", false
	}
	var typ, size uint32
	if err := syscall.RegQueryValueEx(k, n, nil, &typ, nil, &size); err != nil || (typ != syscall.REG_SZ && typ != syscall.REG_EXPAND_SZ) || size == 0 || size > 64*1024 {
		return "", false
	}
	buf := make([]uint16, size/2+1)
	if err := syscall.RegQueryValueEx(k, n, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}
