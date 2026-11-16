package main

import (
	"testing"

	"Fern/modules"
)

func TestClearsChatboxOnlyWhenABlockedWorldEmptiesIt(t *testing.T) {
	blocked := modules.Chatbox{Blocked: "Club Orion"}
	if !clearsChatbox(blocked, "", "old text") {
		t.Error("a blocked world should take the last text down")
	}
	if clearsChatbox(blocked, "", "") {
		t.Error("cleared twice")
	}
	if clearsChatbox(blocked, "heart 80", "old text") {
		t.Error("cleared while an allowed line still shows")
	}
	if clearsChatbox(modules.Chatbox{}, "", "old text") {
		t.Error("cleared outside a blocked world")
	}
}
