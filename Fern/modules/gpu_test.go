package modules

import (
	"testing"
	"time"
	"unsafe"
)

func TestDXGIAdapterDescLayout(t *testing.T) {
	var d dxgiAdapterDesc1
	if got := unsafe.Sizeof(d); got != 312 {
		t.Errorf("sizeof DXGI_ADAPTER_DESC1 = %d, want 312", got)
	}
	if got := unsafe.Offsetof(d.DedicatedVideoMemory); got != 272 {
		t.Errorf("DedicatedVideoMemory at %d, want 272", got)
	}
	if got := unsafe.Offsetof(d.LuidLow); got != 296 {
		t.Errorf("AdapterLuid at %d, want 296", got)
	}
}

func TestEngineLoadUsesBusiestEngine(t *testing.T) {
	values := map[string]float64{
		// Two processes on the 3D engine of one adapter add up...
		"pid_1200_luid_0x00000000_0x0000D1A8_phys_0_eng_0_engtype_3D": 30,
		"pid_4100_luid_0x00000000_0x0000D1A8_phys_0_eng_0_engtype_3D": 25,
		// ...and the video engine is less busy, so it does not count.
		"pid_4100_luid_0x00000000_0x0000D1A8_phys_0_eng_3_engtype_VideoDecode": 40,
		// Another adapter.
		"pid_77_luid_0x00000000_0x0000E000_phys_0_eng_0_engtype_3D": 3,
		// A runaway sum is capped.
		"pid_1_luid_0x00000000_0x0000F000_phys_0_eng_0_engtype_3D": 80,
		"pid_2_luid_0x00000000_0x0000F000_phys_0_eng_0_engtype_3D": 70,
		"bogus": 50,
	}
	got := engineLoad(values)
	want := map[string]float64{
		luidKey(0, 0xD1A8): 55,
		luidKey(0, 0xE000): 3,
		luidKey(0, 0xF000): 100,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestAdapterMemory(t *testing.T) {
	got := adapterMemory(map[string]float64{
		"luid_0x00000000_0x0000D1A8_phys_0": 6 << 30,
		"luid_0x00000000_0x0000E000_phys_0": 1 << 20,
	})
	if got[luidKey(0, 0xD1A8)] != 6<<30 || got[luidKey(0, 0xE000)] != 1<<20 {
		t.Errorf("got %v", got)
	}
}

func TestShortGPUName(t *testing.T) {
	cases := map[string]string{
		"NVIDIA GeForce RTX 4070":                 "RTX 4070",
		"AMD Radeon RX 7800 XT":                   "RX 7800 XT",
		"AMD Radeon(TM) Graphics":                 "Graphics",
		"Intel(R) UHD Graphics 770":               "UHD Graphics 770",
		"NVIDIA":                                  "NVIDIA",
		"Microsoft Basic Render Driver (testing)": "Microsoft Basic Render Driver (testing)",
	}
	for in, want := range cases {
		if got := shortGPUName(in); got != want {
			t.Errorf("shortGPUName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatGPU(t *testing.T) {
	g := GPUInfo{Name: "NVIDIA GeForce RTX 3080", VRAMTotal: 10 << 30, VRAMUsed: 6656 << 20, Usage: 41.6, TempC: 63, HasTemp: true}
	o := GPUOptions{Label: "gpu", ShowName: true, ShowUsage: true, ShowVRAM: true, ShowVRAMTotal: true, ShowTemp: true}
	if got, want := formatGPU(o, g, true), "gpu RTX 3080 · 42% · 6.5/10 GB · 63°C"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	o.CustomName, o.ShowVRAMTotal, o.Fahrenheit = "My Card", false, true
	if got, want := formatGPU(o, g, true), "gpu My Card · 42% · 6.5 GB · 145°F"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Before the first sample only the name is known.
	if got, want := formatGPU(o, g, false), "gpu My Card · 145°F"; got != want {
		t.Errorf("unsampled: got %q, want %q", got, want)
	}
	g.HasTemp = false
	o.ShowName = false
	if got, want := formatGPU(o, g, true), "gpu · 42% · 6.5 GB"; got != want {
		t.Errorf("no name, no temp: got %q, want %q", got, want)
	}
}

func TestPickGPU(t *testing.T) {
	list := []GPUInfo{{Name: "Intel UHD", VRAMTotal: 128 << 20}, {Name: "RTX", VRAMTotal: 8 << 30}}
	if g, _ := pickGPU(list, ""); g.Name != "RTX" {
		t.Errorf("auto = %q", g.Name)
	}
	if g, _ := pickGPU(list, "Intel UHD"); g.Name != "Intel UHD" {
		t.Errorf("named = %q", g.Name)
	}
	if _, ok := pickGPU(nil, ""); ok {
		t.Error("picked from an empty list")
	}
}

// The real adapters and counters, so a broken syscall shows up in CI on a
// machine with a GPU. Counters may be missing on minimal Windows installs.
func TestGPUSourcesOnThisMachine(t *testing.T) {
	list := listGPUs()
	attachNVML(list)
	t.Logf("adapters: %+v", list)
	q, err := openPDH(gpuEnginePath, gpuMemoryPath)
	if err != nil {
		t.Skip("GPU counters unavailable:", err)
	}
	q.collect()
	time.Sleep(300 * time.Millisecond)
	q.collect()
	load := engineLoad(q.values(gpuEnginePath))
	mem := adapterMemory(q.values(gpuMemoryPath))
	t.Logf("load %v, memory %v", load, mem)
	for _, g := range list {
		// Microsoft's software renderer (vendor 0x1414, the only adapter on
		// CI runners) has no dedicated memory counter.
		if g.VendorID == 0x1414 {
			continue
		}
		if _, ok := mem[g.LUID]; !ok {
			t.Errorf("no memory counter for %s (%s)", g.Name, g.LUID)
		}
	}
}
