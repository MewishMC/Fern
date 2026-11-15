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
	for {
		cfg := modules.GetConfig()
		oscClients(cfg.OSCOptions)

		if cfg.DisplayOptions.Show {
			if cfg.DisplayOptions.Loading {
				modules.AdvanceLoading(int(modules.EffectiveRateMS(cfg)))
			}
			body, payload := modules.BuildPayload()
			if body != "" {
				// Third argument false: do not play the notification sound on every
				// update. While the loading animation is on this fires about once a
				// second, which would ding everyone nearby non-stop.
				msg := osc.NewMessage("/chatbox/input", payload, true, false)
				if sendOSC(cfg.OSCOptions, msg) > 0 {
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
