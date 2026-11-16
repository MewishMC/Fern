package openvr

import (
	"errors"
	"strings"
	"testing"
	"unsafe"
)

// Sizes and offsets from compiling openvr_capi.h (v2.15.6 and v2.5.1) with gcc.
func TestInputStructSizesMatchHeader(t *testing.T) {
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"ControllerState", unsafe.Sizeof(ControllerState{}), 64},
		{"ControllerState.Pressed", unsafe.Offsetof(ControllerState{}.Pressed), 8},
		{"ControllerState.Axis", unsafe.Offsetof(ControllerState{}.Axis), 24},
		{"AnalogActionData", unsafe.Sizeof(AnalogActionData{}), 48},
		{"AnalogActionData.X", unsafe.Offsetof(AnalogActionData{}.X), 16},
		{"AnalogActionData.UpdateTime", unsafe.Offsetof(AnalogActionData{}.UpdateTime), 40},
		{"DigitalActionData", unsafe.Sizeof(DigitalActionData{}), 24},
		{"DigitalActionData.State", unsafe.Offsetof(DigitalActionData{}.State), 16},
		{"DigitalActionData.UpdateTime", unsafe.Offsetof(DigitalActionData{}.UpdateTime), 20},
		{"ActiveActionSet", unsafe.Sizeof(ActiveActionSet{}), 32},
		{"ActiveActionSet.Priority", unsafe.Offsetof(ActiveActionSet{}.Priority), 28},
		{"SkeletalSummary", unsafe.Sizeof(SkeletalSummary{}), 36},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s is %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestInputSlotsStayInsideTheirTables(t *testing.T) {
	for _, l := range systemLayouts {
		if l.controllerState >= l.slots || l.hapticPulse >= l.slots || l.controllerState == 0 || l.hapticPulse == 0 {
			t.Errorf("%s: controller slots %d/%d for a table of %d", l.version, l.controllerState, l.hapticPulse, l.slots)
		}
	}
	for _, l := range inputLayouts {
		for _, slot := range []int{l.setManifest, l.actionSet, l.action, l.source, l.update, l.digital, l.analog, l.summary, l.vibrate} {
			if slot >= l.slots {
				t.Errorf("%s: slot %d outside a table of %d", l.version, slot, l.slots)
			}
		}
	}
	if inputLayouts[0].version != "IVRInput_011" {
		t.Error("the newest input layout must match the pinned header and come first")
	}
}

func TestControllerStateButtons(t *testing.T) {
	c := ControllerState{Pressed: 1 << ButtonTrigger, Touched: 1<<ButtonTouchpad | 1<<ButtonA}
	if !c.IsPressed(ButtonTrigger) || c.IsPressed(ButtonGrip) {
		t.Error("pressed bits")
	}
	if !c.IsTouched(ButtonTouchpad) || !c.IsTouched(ButtonA) || c.IsTouched(ButtonTrigger) {
		t.Error("touched bits")
	}
}

func TestVibrateTrampoline(t *testing.T) {
	code := callVibrate(vibrateProbe(), 0x1122334455667788, 0, 0.25, 160, 0.5, 0x99aabbccddeeff00)
	if code != 7 {
		t.Fatalf("returned %d, want 7", code)
	}
	action, device, f := vibrateProbeSeen()
	if action != 0x1122334455667788 || device != 0x99aabbccddeeff00 {
		t.Errorf("handles %x %x", action, device)
	}
	if f != [4]float32{0, 0.25, 160, 0.5} {
		t.Errorf("floats %v", f)
	}
}

func TestInputErrors(t *testing.T) {
	if !strings.Contains(InputMismatchedManifest.Error(), "mismatched") {
		t.Error(InputMismatchedManifest.Error())
	}
	if inputErr(0) != nil {
		t.Error("0 is no error")
	}
	var ie InputError
	if err := inputErr(13); !errors.As(err, &ie) || ie != InputNoData {
		t.Errorf("got %v", err)
	}
	// Methods on a session without the interface fail cleanly.
	s := &Session{}
	if s.HasInput() || s.InputVersion() != "" {
		t.Error("empty session has input")
	}
	if err := s.UpdateActionState(1, 0); err == nil {
		t.Error("update on an empty session")
	}
	if err := s.Vibrate(1, 1, 1, 1, 0); err == nil {
		t.Error("vibrate on an empty session")
	}
	if _, ok := s.ControllerState(0); ok {
		t.Error("state on an empty session")
	}
	s.HapticPulse(0, 100)
}
