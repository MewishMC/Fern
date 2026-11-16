package main

import "Fern/modules"

// handleVRInputCommand runs the Controllers page's buttons:
// {type:"vrInput", action:"test", module:"heartbeat"|"joined"|"param"}.
func handleVRInputCommand(cmd command) {
	switch cmd.Action {
	case "test":
		modules.TestHapticFromPanel(cmd.Module)
	}
}
