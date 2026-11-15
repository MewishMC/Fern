package modules

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	versionDLL              = syscall.NewLazyDLL("version.dll")
	getFileVersionInfoSizeW = versionDLL.NewProc("GetFileVersionInfoSizeW")
	getFileVersionInfoW     = versionDLL.NewProc("GetFileVersionInfoW")
	verQueryValueW          = versionDLL.NewProc("VerQueryValueW")
)

// fileDescription returns an executable's FileDescription (falling back to
// ProductName), e.g. "Visual Studio Code" for Code.exe, or "" if it has none.
func fileDescription(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	size, _, _ := getFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(p)), 0)
	if size == 0 || size > 1<<20 {
		return ""
	}
	data := make([]byte, size)
	if ok, _, _ := getFileVersionInfoW.Call(uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&data[0]))); ok == 0 {
		return ""
	}

	// Try each language the file declares, then the common US English pages.
	langs := []string{}
	if block := verQuery(data, `\VarFileInfo\Translation`, 1); len(block) >= 4 {
		for i := 0; i+4 <= len(block); i += 4 {
			lang := uint16(block[i]) | uint16(block[i+1])<<8
			page := uint16(block[i+2]) | uint16(block[i+3])<<8
			langs = append(langs, fmt.Sprintf("%04x%04x", lang, page))
		}
	}
	langs = append(langs, "040904b0", "040904e4")
	for _, key := range []string{"FileDescription", "ProductName"} {
		for _, lang := range langs {
			if s := verString(data, `\StringFileInfo\`+lang+`\`+key); s != "" {
				return RuneLimit(s, 40)
			}
		}
	}
	return ""
}

// verQuery returns the part of data that VerQueryValueW points at. unit is
// the size of the length it reports: bytes for binary values, 2 for strings,
// whose length is in characters. Working on offsets into data keeps the
// result inside the buffer Go owns.
func verQuery(data []byte, sub string, unit int) []byte {
	s, err := syscall.UTF16PtrFromString(sub)
	if err != nil {
		return nil
	}
	var ptr uintptr
	var n uint32
	ok, _, _ := verQueryValueW.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(unsafe.Pointer(s)),
		uintptr(unsafe.Pointer(&ptr)), uintptr(unsafe.Pointer(&n)))
	if ok == 0 || ptr == 0 {
		return nil
	}
	start := int(ptr - uintptr(unsafe.Pointer(&data[0])))
	end := start + int(n)*unit
	if start < 0 || end > len(data) || end < start {
		return nil
	}
	return data[start:end]
}

func verString(data []byte, sub string) string {
	b := verQuery(data, sub, 2)
	chars := make([]uint16, len(b)/2)
	for i := range chars {
		chars[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return strings.TrimSpace(syscall.UTF16ToString(chars))
}
