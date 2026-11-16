package main

import "Fern/modules"

// oscInState is how Fern hears VRChat and how the forward targets are
// doing, for the Output page.
type oscInState struct {
	modules.OSCInStatus
	Forward []modules.OSCForwardState `json:"forward"`
}

func currentOSCIn() oscInState {
	return oscInState{OSCInStatus: modules.CurrentOSCIn(), Forward: orEmpty(modules.OSCForwardStatus())}
}
