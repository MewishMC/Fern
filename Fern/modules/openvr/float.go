package openvr

/*
#include <stdint.h>

// GetFloatTrackedDeviceProperty returns its float in XMM0, which
// syscall.SyscallN never reads, so it is called through C instead.
typedef float (*fern_float_prop_fn)(uint32_t device, int32_t prop, int32_t *err);

static float fern_call_float_prop(uintptr_t fn, uint32_t device, int32_t prop, int32_t *err) {
	return ((fern_float_prop_fn)fn)(device, prop, err);
}

// A stand-in with the same signature, so the trampoline can be tested
// without SteamVR.
static float fern_float_probe(uint32_t device, int32_t prop, int32_t *err) {
	*err = (int32_t)device;
	return (float)prop / 4.0f;
}

static uintptr_t fern_float_probe_addr(void) { return (uintptr_t)&fern_float_probe; }
*/
import "C"

// callFloatProp calls a GetFloatTrackedDeviceProperty function pointer and
// returns the value and its ETrackedPropertyError.
func callFloatProp(fn uintptr, device uint32, prop int32) (float32, int32) {
	var perr C.int32_t
	v := C.fern_call_float_prop(C.uintptr_t(fn), C.uint32_t(device), C.int32_t(prop), &perr)
	return float32(v), int32(perr)
}

// floatProbe is the address of a C function shaped like
// GetFloatTrackedDeviceProperty, used by the tests.
func floatProbe() uintptr { return uintptr(C.fern_float_probe_addr()) }
