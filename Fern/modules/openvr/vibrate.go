package openvr

/*
#include <stdint.h>

// TriggerHapticVibrationAction takes four floats, which go in XMM registers
// and on the stack as floats; calling it through C keeps that exact.
typedef int32_t (*fern_vibrate_fn)(uint64_t action, float start, float duration, float frequency,
	float amplitude, uint64_t device);

static int32_t fern_call_vibrate(uintptr_t fn, uint64_t action, float start, float duration,
	float frequency, float amplitude, uint64_t device) {
	return ((fern_vibrate_fn)fn)(action, start, duration, frequency, amplitude, device);
}

// A stand-in with the same signature for the tests: it records what it got.
static uint64_t fern_vibrate_seen_action, fern_vibrate_seen_device;
static float fern_vibrate_seen[4];

static int32_t fern_vibrate_probe(uint64_t action, float start, float duration, float frequency,
	float amplitude, uint64_t device) {
	fern_vibrate_seen_action = action;
	fern_vibrate_seen_device = device;
	fern_vibrate_seen[0] = start;
	fern_vibrate_seen[1] = duration;
	fern_vibrate_seen[2] = frequency;
	fern_vibrate_seen[3] = amplitude;
	return 7;
}

static uintptr_t fern_vibrate_probe_addr(void) { return (uintptr_t)&fern_vibrate_probe; }

static void fern_vibrate_probe_seen(uint64_t *action, uint64_t *device, float *out) {
	*action = fern_vibrate_seen_action;
	*device = fern_vibrate_seen_device;
	for (int i = 0; i < 4; i++) out[i] = fern_vibrate_seen[i];
}
*/
import "C"

// callVibrate calls a TriggerHapticVibrationAction function pointer and
// returns its EVRInputError.
func callVibrate(fn uintptr, action uint64, start, duration, frequency, amplitude float32, device uint64) int32 {
	r := C.fern_call_vibrate(C.uintptr_t(fn), C.uint64_t(action), C.float(start), C.float(duration),
		C.float(frequency), C.float(amplitude), C.uint64_t(device))
	return int32(r)
}

// vibrateProbe is the address of a C function shaped like
// TriggerHapticVibrationAction, used by the tests.
func vibrateProbe() uintptr { return uintptr(C.fern_vibrate_probe_addr()) }

// vibrateProbeSeen returns what the probe was last called with.
func vibrateProbeSeen() (action, device uint64, floats [4]float32) {
	var a, d C.uint64_t
	var f [4]C.float
	C.fern_vibrate_probe_seen(&a, &d, &f[0])
	for i := range f {
		floats[i] = float32(f[i])
	}
	return uint64(a), uint64(d), floats
}
