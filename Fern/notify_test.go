package main

import (
	"testing"

	"Fern/modules"
)

func TestShouldNotify(t *testing.T) {
	both := modules.ChatOptions{NotifySend: true, NotifyAuto: true}
	none := modules.ChatOptions{}
	sendOnly := modules.ChatOptions{NotifySend: true}
	held := func(seq uint64) modules.Chatbox { return modules.Chatbox{Hold: true, Seq: seq} }
	auto := modules.Chatbox{}
	cases := []struct {
		name     string
		opts     modules.ChatOptions
		box      modules.Chatbox
		body     string
		lastBody string
		lastSeq  uint64
		want     bool
	}{
		{"new sent message", sendOnly, held(2), "hi", "x", 1, true},
		{"same message sent again while held", sendOnly, held(2), "hi", "hi", 2, false},
		{"sent message with the sound off", none, held(2), "hi", "x", 1, false},
		{"automatic change with the sound off", sendOnly, auto, "b", "a", 0, false},
		{"automatic change with the sound on", both, auto, "b", "a", 0, true},
		{"automatic re-send of the same text", both, auto, "a", "a", 0, false},
		{"the same text sent again counts as new", both, held(3), "a", "a", 2, true},
	}
	for _, c := range cases {
		if got := shouldNotify(c.opts, c.box, c.body, c.lastBody, c.lastSeq); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSetHoldBumpsSequence(t *testing.T) {
	modules.SetHold("one", 0)
	first := modules.ComposeChatbox()
	modules.SetHold("one", 0)
	second := modules.ComposeChatbox()
	modules.ClearHold()
	if !first.Hold || !second.Hold || first.Seq == second.Seq {
		t.Errorf("sending the same text twice should be two messages: %+v %+v", first, second)
	}
}
