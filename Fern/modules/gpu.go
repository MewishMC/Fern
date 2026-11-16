package modules

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// GPU load and VRAM come from the same performance counters Task Manager
// reads, so they work for every vendor without admin rights. Names and VRAM
// totals come from DXGI. Temperature needs a vendor library: NVIDIA's nvml.dll
// ships with the driver and is used when present; other vendors only expose it
// through their SDKs or a kernel driver, so they get no temperature.

var (
	pdh                              = syscall.NewLazyDLL("pdh.dll")
	procPdhOpenQueryW                = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounterW        = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData          = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCounterArrayW = pdh.NewProc("PdhGetFormattedCounterArrayW")

	dxgi                   = syscall.NewLazyDLL("dxgi.dll")
	procCreateDXGIFactory1 = dxgi.NewProc("CreateDXGIFactory1")
)

const (
	pdhFmtDouble   = 0x00000200
	pdhFmtNoCap100 = 0x00008000
	pdhMoreData    = 0x800007D2
	dxgiNotFound   = 0x887A0002
	dxgiSoftware   = 0x2
)

// GPUInfo is one graphics adapter.
type GPUInfo struct {
	Name       string  `json:"name"`
	LUID       string  `json:"luid"` // matches the performance counter instances
	VendorID   uint32  `json:"vendorId"`
	DeviceID   uint32  `json:"deviceId"`
	VRAMTotal  uint64  `json:"vramTotal"` // bytes
	VRAMUsed   uint64  `json:"vramUsed"`
	Usage      float64 `json:"usage"`   // percent, busiest engine like Task Manager
	TempC      float64 `json:"tempC"`   // 0 when unknown
	HasTemp    bool    `json:"hasTemp"` // a temperature source was found
	nvmlHandle uintptr
}

type dxgiAdapterDesc1 struct {
	Description           [128]uint16
	VendorID              uint32
	DeviceID              uint32
	SubSysID              uint32
	Revision              uint32
	DedicatedVideoMemory  uintptr
	DedicatedSystemMemory uintptr
	SharedSystemMemory    uintptr
	LuidLow               uint32
	LuidHigh              int32
	Flags                 uint32
}

func luidKey(high, low uint32) string { return fmt.Sprintf("0x%08x_0x%08x", high, low) }

// gpuCall calls method index i of a COM object's vtable.
func gpuCall(obj unsafe.Pointer, i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

// listGPUs enumerates hardware adapters through DXGI.
func listGPUs() []GPUInfo {
	if procCreateDXGIFactory1.Find() != nil {
		return nil
	}
	// IID_IDXGIFactory1 {770aae78-f26f-4dba-a829-253c83d1b387}
	iid := syscall.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba, Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	var factory unsafe.Pointer
	if r, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&factory))); r != 0 || factory == nil {
		return nil
	}
	defer gpuCall(factory, 2) // Release
	var out []GPUInfo
	seen := map[string]bool{}
	for i := uintptr(0); i < 16; i++ {
		var adapter unsafe.Pointer
		if r := gpuCall(factory, 12, i, uintptr(unsafe.Pointer(&adapter))); r == dxgiNotFound || adapter == nil { // EnumAdapters1
			break
		}
		var desc dxgiAdapterDesc1
		r := gpuCall(adapter, 10, uintptr(unsafe.Pointer(&desc))) // GetDesc1
		gpuCall(adapter, 2)
		if r != 0 || desc.Flags&dxgiSoftware != 0 {
			continue
		}
		key := luidKey(uint32(desc.LuidHigh), desc.LuidLow)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, GPUInfo{
			Name:      strings.TrimSpace(syscall.UTF16ToString(desc.Description[:])),
			LUID:      key,
			VendorID:  desc.VendorID,
			DeviceID:  desc.DeviceID,
			VRAMTotal: uint64(desc.DedicatedVideoMemory),
		})
	}
	return out
}

// pdhQuery wraps a query with wildcard counters.
type pdhQuery struct {
	handle   uintptr
	counters map[string]uintptr
}

func openPDH(paths ...string) (*pdhQuery, error) {
	if err := procPdhOpenQueryW.Find(); err != nil {
		return nil, err
	}
	q := &pdhQuery{counters: map[string]uintptr{}}
	if r, _, _ := procPdhOpenQueryW.Call(0, 0, uintptr(unsafe.Pointer(&q.handle))); r != 0 {
		return nil, fmt.Errorf("PdhOpenQuery: 0x%x", r)
	}
	for _, p := range paths {
		ptr, _ := syscall.UTF16PtrFromString(p)
		var c uintptr
		if r, _, _ := procPdhAddEnglishCounterW.Call(q.handle, uintptr(unsafe.Pointer(ptr)), 0, uintptr(unsafe.Pointer(&c))); r != 0 {
			return nil, fmt.Errorf("PdhAddEnglishCounter %s: 0x%x", p, r)
		}
		q.counters[p] = c
	}
	return q, nil
}

func (q *pdhQuery) collect() { procPdhCollectQueryData.Call(q.handle) }

// values returns every instance of a wildcard counter.
func (q *pdhQuery) values(path string) map[string]float64 {
	c := q.counters[path]
	var size, count uint32
	r, _, _ := procPdhGetFormattedCounterArrayW.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if r != pdhMoreData || size == 0 {
		return nil
	}
	buf := make([]byte, size)
	r, _, _ = procPdhGetFormattedCounterArrayW.Call(c, pdhFmtDouble|pdhFmtNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return nil
	}
	type item struct {
		name   *uint16
		status uint32
		_      uint32
		value  float64
	}
	items := unsafe.Slice((*item)(unsafe.Pointer(&buf[0])), count)
	out := make(map[string]float64, count)
	for _, it := range items {
		if it.status > 1 { // PDH_CSTATUS_VALID_DATA or NEW_DATA
			continue
		}
		out[utf16PtrString(it.name)] += it.value
	}
	return out
}

func utf16PtrString(p *uint16) string {
	if p == nil {
		return ""
	}
	var s []uint16
	for ptr := unsafe.Pointer(p); ; ptr = unsafe.Add(ptr, 2) {
		c := *(*uint16)(ptr)
		if c == 0 {
			break
		}
		s = append(s, c)
	}
	return syscall.UTF16ToString(s)
}

var (
	luidPattern   = regexp.MustCompile(`(?i)luid_(0x[0-9a-f]{8})_(0x[0-9a-f]{8})`)
	enginePattern = regexp.MustCompile(`(?i)phys_(\d+)_eng_(\d+)`)
)

// engineLoad turns per-process engine counters into each adapter's load: the
// processes on one engine add up, and the adapter is as busy as its busiest
// engine, which is how Task Manager shows it.
func engineLoad(values map[string]float64) map[string]float64 {
	engines := map[string]float64{}
	for name, v := range values {
		l := luidPattern.FindStringSubmatch(name)
		e := enginePattern.FindStringSubmatch(name)
		if l == nil || e == nil {
			continue
		}
		engines[strings.ToLower(l[1]+"_"+l[2])+"|"+e[1]+"_"+e[2]] += v
	}
	out := map[string]float64{}
	for key, v := range engines {
		luid := key[:strings.IndexByte(key, '|')]
		out[luid] = math.Max(out[luid], math.Min(100, v))
	}
	return out
}

// adapterMemory sums dedicated memory per adapter.
func adapterMemory(values map[string]float64) map[string]uint64 {
	out := map[string]uint64{}
	for name, v := range values {
		if l := luidPattern.FindStringSubmatch(name); l != nil {
			out[strings.ToLower(l[1]+"_"+l[2])] += uint64(v)
		}
	}
	return out
}

const (
	gpuEnginePath = `\GPU Engine(*)\Utilization Percentage`
	gpuMemoryPath = `\GPU Adapter Memory(*)\Dedicated Usage`
)

var (
	gpuMu      sync.RWMutex
	gpuList    []GPUInfo
	gpuSampled bool
	gpuErr     string
)

func startGPUWorker() {
	go func() {
		list := listGPUs()
		attachNVML(list)
		q, err := openPDH(gpuEnginePath, gpuMemoryPath)
		gpuMu.Lock()
		gpuList = list
		if err != nil {
			gpuErr = err.Error()
		}
		gpuMu.Unlock()
		if err != nil {
			return
		}
		q.collect()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			q.collect()
			load := engineLoad(q.values(gpuEnginePath))
			mem := adapterMemory(q.values(gpuMemoryPath))
			gpuMu.Lock()
			for i := range gpuList {
				g := &gpuList[i]
				g.Usage = load[g.LUID]
				g.VRAMUsed = mem[g.LUID]
				if g.nvmlHandle != 0 {
					g.TempC, g.HasTemp = nvmlTemperature(g.nvmlHandle)
				}
			}
			gpuSampled = true
			gpuMu.Unlock()
		}
	}()
}

// GPUs returns the adapters with their latest numbers.
func GPUs() []GPUInfo {
	gpuMu.RLock()
	defer gpuMu.RUnlock()
	return slices.Clone(gpuList)
}

// pickGPU returns the adapter named in the settings, or the one with the most
// dedicated memory, which is the discrete card on a laptop or a desktop with
// integrated graphics.
func pickGPU(list []GPUInfo, name string) (GPUInfo, bool) {
	if len(list) == 0 {
		return GPUInfo{}, false
	}
	best := 0
	for i, g := range list {
		if name != "" && g.Name == name {
			return g, true
		}
		if g.VRAMTotal > list[best].VRAMTotal {
			best = i
		}
	}
	return list[best], true
}

// shortGPUName drops the vendor and trademark noise: "NVIDIA GeForce RTX 4070"
// becomes "RTX 4070", "AMD Radeon RX 7800 XT" becomes "RX 7800 XT".
func shortGPUName(name string) string {
	for _, junk := range []string{"(R)", "(TM)", "(tm)", "®", "™"} {
		name = strings.ReplaceAll(name, junk, "")
	}
	drop := map[string]bool{"nvidia": true, "geforce": true, "amd": true, "radeon": true, "intel": true}
	fields := strings.Fields(name)
	out := fields[:0]
	for _, f := range fields {
		if !drop[strings.ToLower(f)] {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return strings.TrimSpace(name)
	}
	return strings.Join(out, " ")
}

// gpuLine renders the GPU module line.
func gpuLine(cfg Config) string {
	g, ok := pickGPU(GPUs(), cfg.GPUOptions.Adapter)
	if !ok {
		return ""
	}
	gpuMu.RLock()
	sampled := gpuSampled
	gpuMu.RUnlock()
	return formatGPU(cfg.GPUOptions, g, sampled)
}

func formatGPU(o GPUOptions, g GPUInfo, sampled bool) string {
	var parts []string
	label := strings.TrimSpace(o.Label)
	if o.ShowName {
		name := strings.TrimSpace(o.CustomName)
		if name == "" {
			name = shortGPUName(g.Name)
		}
		parts = append(parts, withLabel(label, name))
	} else if label != "" {
		parts = append(parts, label)
	}
	if sampled && o.ShowUsage {
		parts = append(parts, fmt.Sprintf("%.0f%%", g.Usage))
	}
	if sampled && o.ShowVRAM && g.VRAMTotal > 0 {
		const gb = 1024 * 1024 * 1024
		used := float64(g.VRAMUsed) / gb
		if o.ShowVRAMTotal {
			parts = append(parts, fmt.Sprintf("%.1f/%.0f GB", used, math.Round(float64(g.VRAMTotal)/gb)))
		} else {
			parts = append(parts, fmt.Sprintf("%.1f GB", used))
		}
	}
	if o.ShowTemp && g.HasTemp {
		if o.Fahrenheit {
			parts = append(parts, strconv.Itoa(int(math.Round(g.TempC*9/5+32)))+"°F")
		} else {
			parts = append(parts, strconv.Itoa(int(math.Round(g.TempC)))+"°C")
		}
	}
	return strings.Join(parts, " · ")
}

// NVML: loaded only if the NVIDIA driver installed it. Never required.
var (
	nvml                   = syscall.NewLazyDLL("nvml.dll")
	procNvmlInit           = nvml.NewProc("nvmlInit_v2")
	procNvmlCount          = nvml.NewProc("nvmlDeviceGetCount_v2")
	procNvmlHandleByIndex  = nvml.NewProc("nvmlDeviceGetHandleByIndex_v2")
	procNvmlPciInfo        = nvml.NewProc("nvmlDeviceGetPciInfo_v3")
	procNvmlGetTemperature = nvml.NewProc("nvmlDeviceGetTemperature")
)

const (
	nvmlVendorID          = 0x10DE
	nvmlTemperatureGPU    = 0
	nvmlPciInfoSize       = 68 // sizeof(nvmlPciInfo_t)
	nvmlPciDeviceIDOffset = 28 // offsetof(nvmlPciInfo_t, pciDeviceId)
)

// attachNVML links each NVIDIA adapter to its NVML device by PCI id.
func attachNVML(list []GPUInfo) {
	hasNvidia := false
	for _, g := range list {
		hasNvidia = hasNvidia || g.VendorID == nvmlVendorID
	}
	if !hasNvidia {
		return
	}
	if procNvmlInit.Find() != nil {
		return
	}
	if r, _, _ := procNvmlInit.Call(); r != 0 {
		return
	}
	var count uint32
	if r, _, _ := procNvmlCount.Call(uintptr(unsafe.Pointer(&count))); r != 0 {
		return
	}
	used := map[uintptr]bool{}
	for i := range list {
		g := &list[i]
		if g.VendorID != nvmlVendorID {
			continue
		}
		want := g.DeviceID<<16 | g.VendorID
		for n := uint32(0); n < count; n++ {
			var h uintptr
			if r, _, _ := procNvmlHandleByIndex.Call(uintptr(n), uintptr(unsafe.Pointer(&h))); r != 0 || used[h] {
				continue
			}
			var pci [nvmlPciInfoSize]byte
			if r, _, _ := procNvmlPciInfo.Call(h, uintptr(unsafe.Pointer(&pci[0]))); r != 0 {
				continue
			}
			id := *(*uint32)(unsafe.Pointer(&pci[nvmlPciDeviceIDOffset]))
			if id == want || (count == 1 && id&0xFFFF == nvmlVendorID) {
				g.nvmlHandle = h
				used[h] = true
				g.TempC, g.HasTemp = nvmlTemperature(h)
				break
			}
		}
	}
}

func nvmlTemperature(h uintptr) (float64, bool) {
	var t uint32
	if r, _, _ := procNvmlGetTemperature.Call(h, nvmlTemperatureGPU, uintptr(unsafe.Pointer(&t))); r != 0 {
		return 0, false
	}
	return float64(t), true
}
