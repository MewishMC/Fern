package main

// The first-run setup from the panel's side: its test send, the starter
// lines and finishing. The checks themselves are in modules/setup.go.

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"Fern/modules"

	"github.com/hypebeast/go-osc/osc"
)

// setupTestText is the message the setup's test send puts in the chatbox.
const setupTestText = "Fern test message ✓ If you can read this, Fern reaches your chatbox."

// setupTestHold is how long the test message stays up.
const setupTestHold = 10 * time.Second

type setupState struct {
	modules.SetupStatus
	// TestMS is when the last test message went out, unix ms, 0 if none has.
	TestMS int64 `json:"testMs"`
	// TestTargets lists the outputs it went to.
	TestTargets []string `json:"testTargets"`
	// Lines are the starter lines the setup offers, in order.
	Lines  []string    `json:"lines"`
	Notice noticeState `json:"notice"`
}

var (
	setupMu      sync.Mutex
	setupTestAt  time.Time
	setupTargets []string
	setupNotice  noticeState
)

func currentSetup() setupState {
	setupMu.Lock()
	st := setupState{TestTargets: append([]string{}, setupTargets...), Lines: modules.StarterLines}
	if !setupTestAt.IsZero() {
		st.TestMS = setupTestAt.UnixMilli()
	}
	setupMu.Unlock()
	st.SetupStatus = modules.CurrentSetup()
	st.Notice = readNotice(&setupNotice)
	return st
}

// setupSend is how the test message leaves; tests swap it.
var setupSend = func(opts modules.OSCOptions, msg *osc.Message) int { return sendOSC(opts, msg) }

// setupTest sends the test message. With the display on it is held like a
// Send now message, so the rotation does not cover it straight away; with the
// display off it goes out once on its own.
func setupTest() {
	cfg := modules.GetConfig()
	targets := modules.OSCDestinations(cfg.OSCOptions)
	if cfg.DisplayOptions.Show {
		modules.SetHold(setupTestText, setupTestHold)
	} else if setupSend(cfg.OSCOptions, osc.NewMessage("/chatbox/input", setupTestText, true, true)) == 0 {
		setNotice(&setupNotice, "Couldn't send the test message", true)
		return
	}
	setupMu.Lock()
	setupTestAt = time.Now()
	setupTargets = targets
	setupMu.Unlock()
	setNotice(&setupNotice, "Test message sent to "+strings.Join(targets, ", "), false)
}

func handleSetupCommand(cmd command) {
	switch cmd.Action {
	case "test":
		setupTest()
	case "lines":
		on, err := modules.ApplyStarterLines(cmd.Order)
		if err != nil {
			setNotice(&setupNotice, "Couldn't set up the lines: "+err.Error(), true)
			return
		}
		text := "Turned on " + strconv.Itoa(len(on)) + " lines"
		if len(on) == 1 {
			text = "Turned on 1 line"
		}
		setNotice(&setupNotice, text, false)
	case "finish":
		modules.FinishSetup()
	}
}
