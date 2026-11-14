package modules

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	textEpoch = time.Now()

	permMu    sync.Mutex
	permLen   int
	permCycle int
	perm      []int
)

var (
	loadingStep   atomic.Int32
	loadingLastAt atomic.Int64 // unix nano of the last step
)

// AdvanceLoading moves the dots on by one, but never faster than paceMS. Config
// and media changes wake the send loop early, and advancing on every wake raced
// through several dots between two looks at the screen, so the line appeared to
// jump from ".." to "......". The pace gate makes one dot mean one update at the
// rate the chatbox is really being refreshed at.
func AdvanceLoading(paceMS int) {
	if paceMS < 400 {
		paceMS = 400
	}
	now := time.Now().UnixNano()
	last := loadingLastAt.Load()
	if last != 0 && now-last < int64(paceMS)*int64(time.Millisecond) {
		return
	}
	if loadingLastAt.CompareAndSwap(last, now) {
		loadingStep.Add(1)
	}
}

// Loading renders a growing run of dots: ".", "..", "..." and so on, looping
// back to a single dot after the configured longest run. A custom loading text
// is shown in front of the dots, so "brb" reads "brb", "brb.", "brb.." and so
// on; with no text set the line is just the dots.
func Loading() string {
	o := GetConfig().LoadingOptions
	text := strings.TrimRight(strings.TrimSpace(o.Text), ".")
	if !o.ShowDots {
		return text
	}
	// sanitize keeps Dots between 1 and 6.
	n := int(loadingStep.Load())%o.Dots + 1
	return text + strings.Repeat(".", n)
}

func shuffleIndex(n, step int) int {
	permMu.Lock()
	defer permMu.Unlock()
	if n <= 0 {
		return 0
	}
	cycle := step / n
	if permLen != n || permCycle != cycle || len(perm) != n {
		permLen = n
		permCycle = cycle
		perm = rand.Perm(n)
	}
	return perm[step%n]
}

// CurrentMessage returns the message that should be shown right now.
func CurrentMessage() string {
	cfg := GetConfig()
	messages := cfg.TextMessages
	if len(messages) == 0 {
		return ""
	}
	if !cfg.TextOptions.Cycle || len(messages) == 1 {
		if cfg.TextOptions.Shuffle {
			return messages[shuffleIndex(len(messages), int(time.Since(textEpoch)/time.Second))]
		}
		return messages[0]
	}

	return messages[rotationIndex(cfg)]
}

// rotationIndex is the position of the message the timed rotation is on.
func rotationIndex(cfg Config) int {
	step := int(time.Since(textEpoch) / rotationSpeed(cfg))
	if cfg.TextOptions.Shuffle {
		return shuffleIndex(len(cfg.TextMessages), step)
	}
	return step % len(cfg.TextMessages)
}

func rotationSpeed(cfg Config) time.Duration {
	return time.Duration(cfg.TextOptions.Speed) * time.Millisecond
}

// MessageIndex is the zero based position of the message being shown.
func MessageIndex() int {
	cfg := GetConfig()
	if len(cfg.TextMessages) == 0 {
		return 0
	}
	return rotationIndex(cfg)
}

// SkipMessage advances to the next custom message immediately.
func SkipMessage() {
	textEpoch = textEpoch.Add(-rotationSpeed(GetConfig()))
	NotifyChange()
}
