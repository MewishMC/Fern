package main

import (
	"testing"
	"time"

	"Fern/modules"
)

func TestSendGap(t *testing.T) {
	opts := modules.ChatOptions{LiveRateMS: 1500}
	held := modules.Chatbox{Hold: true}
	now := time.Now()
	cases := []struct {
		name     string
		box      modules.Chatbox
		body     string
		lastBody string
		lastSend time.Time
		wantWait bool
	}{
		{"new typed text right after a send waits", held, "hel", "he", now, true},
		{"new typed text after the rate goes", held, "hel", "he", now.Add(-2 * time.Second), false},
		{"re-sending the same text goes", held, "he", "he", now, false},
		{"automatic chatbox never waits", modules.Chatbox{}, "a", "b", now, false},
		{"first send ever goes", held, "a", "", time.Time{}, false},
	}
	for _, c := range cases {
		wait := sendGap(opts, c.box, c.body, c.lastBody, c.lastSend)
		if (wait > 0) != c.wantWait || wait > 1500*time.Millisecond {
			t.Errorf("%s: wait %v", c.name, wait)
		}
	}
}

func TestLiveTypingIsOneSilentMessage(t *testing.T) {
	defer modules.ClearHold()
	modules.SetLive("he", 10*time.Second)
	first := modules.ComposeChatbox()
	modules.SetLive("hello", 10*time.Second)
	second := modules.ComposeChatbox()
	if !first.Live || second.Text != "hello" || first.Seq != second.Seq {
		t.Fatalf("typing should update one message: %+v / %+v", first, second)
	}
	opts := modules.ChatOptions{NotifySend: true}
	if shouldNotify(opts, second, "hello", "he", 0) {
		t.Error("live typing played the sound")
	}

	modules.SetHold("hello", 10*time.Second)
	final := modules.ComposeChatbox()
	if final.Live || final.Seq == second.Seq || !shouldNotify(opts, final, "hello", "hello", second.Seq) {
		t.Errorf("sending the typed message should be a new message with sound: %+v", final)
	}

	modules.SetLive("", 0)
	if !modules.ComposeChatbox().Hold {
		t.Error("clearing the box took down a message that was sent, not typed")
	}
	modules.SetLive("next", 10*time.Second)
	modules.SetLive("", 0)
	if modules.ComposeChatbox().Hold {
		t.Error("clearing the box should take the live message down")
	}
}
