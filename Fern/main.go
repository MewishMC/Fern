package main

import (
	"Fern/UI"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"

	"Fern/modules"
)

// appVersion is overridden by release builds with
// -ldflags "-X main.appVersion=<tag without v>", which is what the update
// check compares against the latest GitHub release.
var appVersion = "2.0"

var (
	sentCount  int64
	lastSentAt int64
	startedAt  = time.Now()
)

func main() {
	modules.Load()
	modules.StartWorkers()

	port, listener, err := openListener()
	if err != nil {
		log.Fatal("could not open a local port: ", err)
	}
	go StartServer(listener)
	go sendLoop()
	go watchUpdates()

	fmt.Printf("Fern %s listening on http://127.0.0.1:%d/\n", appVersion, port)

	UI.StartWebview(fmt.Sprintf("http://127.0.0.1:%d/", port))
}

// sendLoop pushes the chatbox to VRChat, re-sending instantly when the config
// changes instead of always waiting for the full timeout.
func sendLoop() {
	var lastBody string
	var lastSeq uint64
	for {
		cfg := modules.GetConfig()
		oscClients(cfg.OSCOptions)

		if cfg.DisplayOptions.Show {
			if cfg.DisplayOptions.Loading {
				modules.AdvanceLoading(int(modules.EffectiveRateMS(cfg)))
			}
			box := modules.ComposeChatbox()
			body, payload := modules.PayloadFor(box)
			if body != "" {
				notify := shouldNotify(cfg.ChatOptions, box, body, lastBody, lastSeq)
				msg := osc.NewMessage("/chatbox/input", payload, true, notify)
				if sendOSC(cfg.OSCOptions, msg) > 0 {
					lastBody = body
					if box.Hold {
						lastSeq = box.Seq
					}
					atomic.AddInt64(&sentCount, 1)
					atomic.StoreInt64(&lastSentAt, time.Now().UnixMilli())
					modules.AddHistory(body)
				}
			}
		}

		timeout := time.Duration(modules.EffectiveRateMS(cfg)) * time.Millisecond
		if timeout < 100*time.Millisecond {
			timeout = 100 * time.Millisecond
		}
		select {
		case <-modules.Changes():
		case <-time.After(timeout):
		}
	}
}

// shouldNotify decides whether VRChat plays its sound for this send. A sent
// message dings once, not on every re-send while it is held. The automatic
// chatbox dings only when its text changed, and only if asked: with the
// loading dots on that is about once a second.
func shouldNotify(opts modules.ChatOptions, box modules.Chatbox, body, lastBody string, lastSeq uint64) bool {
	if box.Hold {
		return opts.NotifySend && box.Seq != lastSeq
	}
	return opts.NotifyAuto && body != lastBody
}
