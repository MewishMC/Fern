package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestHoldBleepEndsOnRelease(t *testing.T) {
	var polls atomic.Int32
	old := keyDown
	keyDown = func(uint32) bool { return polls.Add(1) < 4 }
	t.Cleanup(func() { keyDown = old })

	done := make(chan struct{})
	go func() { holdBleep(0x42); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the bleep did not end when the key came up")
	}
	if polls.Load() < 4 {
		t.Errorf("polled %d times", polls.Load())
	}
	if bleepHeld.Load() {
		t.Error("still marked as held")
	}
}
