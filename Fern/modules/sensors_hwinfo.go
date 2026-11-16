package modules

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// HWiNFO publishes its sensors in a shared memory block while "Shared Memory
// Support" is ticked in its settings (the free version turns it off again
// after 12 hours). The layout is HWiNFO's documented
// HWiNFO_SENSORS_SHARED_MEM2, packed to one byte:
//
//	header:  signature, version, revision (u32), poll time (i64),
//	         sensor offset, size, count, reading offset, size, count (u32)
//	sensor:  id, instance (u32), original name [128], user name [128],
//	         newer versions add a UTF-8 user name [128]
//	reading: type, sensor index, id (u32), original label [128],
//	         user label [128], unit [16], value, min, max, avg (f64),
//	         newer versions add a UTF-8 user label [128] and unit [16]

const hwinfoMapName = `Global\HWiNFO_SENS_SM2`

var (
	// The SDK writes the signature as the C constant 'HWiS'; accept the
	// bytes either way round.
	hwinfoSignatures = []uint32{binary.LittleEndian.Uint32([]byte("HWiS")), binary.BigEndian.Uint32([]byte("HWiS"))}
	hwinfoDead       = []uint32{binary.LittleEndian.Uint32([]byte("DEAD")), binary.BigEndian.Uint32([]byte("DEAD"))}

	procOpenFileMappingW = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenFileMappingW")
)

const (
	hwinfoHeaderSize  = 44
	hwinfoSensorBase  = 264
	hwinfoReadingBase = 316
	hwinfoMaxMap      = 64 << 20
)

// hwinfoTypes maps SENSOR_READING_TYPE.
var hwinfoTypes = map[uint32]string{
	1: SensorTemp, 2: SensorVoltage, 3: SensorFan, 4: SensorCurrent, 5: SensorPower, 6: SensorClock, 7: SensorLoad, 8: SensorOther,
}

// readHWiNFOShared copies HWiNFO's shared memory and parses it.
func readHWiNFOShared() ([]Sensor, error) {
	if procOpenFileMappingW.Find() != nil {
		return nil, errors.New("OpenFileMapping is missing")
	}
	name, _ := syscall.UTF16PtrFromString(hwinfoMapName)
	h, _, err := procOpenFileMappingW.Call(windows.FILE_MAP_READ, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil, errors.New("shared memory not found (HWiNFO not running, or Shared Memory Support is off)")
		}
		return nil, fmt.Errorf("shared memory: %v", err)
	}
	defer windows.CloseHandle(windows.Handle(h))
	addr, err := windows.MapViewOfFile(windows.Handle(h), windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil || addr == 0 {
		return nil, fmt.Errorf("shared memory: %v", err)
	}
	defer windows.UnmapViewOfFile(addr)
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(addr, &info, unsafe.Sizeof(info)); err != nil {
		return nil, fmt.Errorf("shared memory: %v", err)
	}
	size := int(info.RegionSize)
	if size > hwinfoMaxMap {
		size = hwinfoMaxMap
	}
	if size < hwinfoHeaderSize {
		return nil, errors.New("shared memory is too small")
	}
	// Copy first: HWiNFO rewrites the block while it polls.
	buf := make([]byte, size)
	// The view is memory Windows mapped, not Go memory, so turning its
	// address into a pointer is safe.
	view := *(*unsafe.Pointer)(unsafe.Pointer(&addr))
	copy(buf, unsafe.Slice((*byte)(view), size))
	return parseHWiNFO(buf)
}

// cString reads a NUL-terminated string. HWiNFO writes the legacy fields in
// the system code page; bytes that are not UTF-8 are read as Windows-1252,
// which covers the "°" in "°C".
func cString(b []byte) string {
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	if utf8.Valid(b) {
		return strings.TrimSpace(string(b))
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return strings.TrimSpace(string(r))
}

// hwinfoKind guesses the hardware kind from a HWiNFO sensor name, such as
// "CPU [#0]: AMD Ryzen 7 5800X: Enhanced" or "GPU [#0]: AMD Radeon RX 6800".
func hwinfoKind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "cpu ["), strings.HasPrefix(n, "cpu:"):
		return HWCPU
	case strings.HasPrefix(n, "gpu ["), strings.HasPrefix(n, "gpu:"):
		return HWGPU
	case strings.HasPrefix(n, "s.m.a.r.t."), strings.HasPrefix(n, "drive:"), strings.Contains(n, "nvme"):
		return HWStorage
	case strings.HasPrefix(n, "memory timings"), strings.HasPrefix(n, "dimm"), strings.HasPrefix(n, "spd hub"):
		return HWMemory
	case strings.Contains(n, "nuvoton"), strings.Contains(n, "ite it"), strings.Contains(n, "fintek"),
		strings.Contains(n, "(ec)"), strings.Contains(n, "embedded controller"), strings.Contains(n, "superio"):
		return HWBoard
	}
	if k := guessKind(name); k != HWOther {
		return k
	}
	// Board sensors are named after the board: "ASUS ROG STRIX B550-F (Nuvoton ...)".
	if strings.Contains(n, "(") && strings.Contains(n, ")") {
		return HWBoard
	}
	return HWOther
}

// parseHWiNFO parses a copy of the shared memory block.
func parseHWiNFO(b []byte) ([]Sensor, error) {
	if len(b) < hwinfoHeaderSize {
		return nil, errors.New("shared memory is too small")
	}
	le := binary.LittleEndian
	switch sig := le.Uint32(b[0:]); {
	case slices.Contains(hwinfoSignatures, sig):
	case slices.Contains(hwinfoDead, sig):
		return nil, errors.New("HWiNFO has stopped sharing sensors (Shared Memory Support timed out or was turned off)")
	default:
		return nil, errors.New("shared memory is not HWiNFO's")
	}
	sOff, sSize, sCount := int(le.Uint32(b[20:])), int(le.Uint32(b[24:])), int(le.Uint32(b[28:]))
	rOff, rSize, rCount := int(le.Uint32(b[32:])), int(le.Uint32(b[36:])), int(le.Uint32(b[40:]))
	if sSize < hwinfoSensorBase || rSize < hwinfoReadingBase || sCount > 4096 || rCount > 65536 {
		return nil, errors.New("shared memory layout is not one Fern knows")
	}
	if sOff < 0 || sOff+sSize*sCount > len(b) || rOff < 0 || rOff+rSize*rCount > len(b) {
		return nil, errors.New("shared memory is cut short")
	}

	type hwSensor struct {
		id, inst uint32
		name     string
	}
	sensors := make([]hwSensor, sCount)
	for i := range sensors {
		e := b[sOff+i*sSize : sOff+(i+1)*sSize]
		name := cString(e[136:264])
		if sSize >= hwinfoSensorBase+128 {
			if u := cString(e[264:392]); u != "" {
				name = u
			}
		}
		if name == "" {
			name = cString(e[8:136])
		}
		sensors[i] = hwSensor{id: le.Uint32(e[0:]), inst: le.Uint32(e[4:]), name: name}
	}

	out := make([]Sensor, 0, rCount)
	for i := 0; i < rCount; i++ {
		e := b[rOff+i*rSize : rOff+(i+1)*rSize]
		t, idx, id := le.Uint32(e[0:]), int(le.Uint32(e[4:])), le.Uint32(e[8:])
		typ, ok := hwinfoTypes[t]
		if !ok || idx < 0 || idx >= len(sensors) {
			continue
		}
		label := cString(e[140:268])
		unit := cString(e[268:284])
		if rSize >= hwinfoReadingBase+144 {
			if u := cString(e[316:444]); u != "" {
				label = u
			}
			if u := cString(e[444:460]); u != "" {
				unit = u
			}
		}
		if label == "" {
			label = cString(e[12:140])
		}
		f := func(off int) float64 {
			v := math.Float64frombits(le.Uint64(e[off:]))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0
			}
			return v
		}
		hs := sensors[idx]
		s := Sensor{
			Key:      fmt.Sprintf("hwi:%x.%x/%x", hs.id, hs.inst, id),
			Hardware: hs.name,
			Kind:     hwinfoKind(hs.name),
			Name:     label,
			Type:     typ,
			Value:    f(284),
			Min:      f(292),
			Max:      f(300),
			Unit:     unit,
		}
		// HWiNFO's "usage" type also carries plain values like "Yes/No"; only
		// percent readings are loads.
		if typ == SensorLoad && unit != "%" {
			s.Type = SensorOther
		}
		if typ != SensorOther && s.Type != SensorOther {
			normalise(&s)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, errors.New("HWiNFO shares no sensors")
	}
	return out, nil
}
