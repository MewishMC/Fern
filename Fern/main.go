package main

import (
	"Fern/UI"
	"fmt"
	"log"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/hypebeast/go-osc/osc"

	"Fern/modules"
)

// appVersion is overridden by release builds with
// -ldflags "-X main.appVersion=<tag without v>", which is what the update
// check compares against the latest GitHub release.
var appVersion = "2.0"

var (
	client    *osc.Client  // owned by sendLoop
	clientKey string       // owned by sendLoop
	oscAddr   atomic.Value // host:port of client, read by the UI server

	sentCount  int64
	lastSentAt int64
	startedAt  = time.Now()
)

func main() {
	// SteamVR starts Fern with --steamvr. If Fern is already open, that second
	// copy would send a duplicate chatbox, so it bows out.
	fromSteamVR := slices.Contains(os.Args, "--steamvr")
	if alreadyRunning() && fromSteamVR {
		fmt.Println("Fern is already running")
		return
	}

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

	UI.OnHotkey = toggleMic
	cfg := modules.GetConfig()
	UI.Apply(behaviour(cfg.AppOptions))
	go watchAppOptions(cfg.AppOptions)
	if fromSteamVR {
		go quitWithSteamVR()
	}
	UI.StartWebview(fmt.Sprintf("http://127.0.0.1:%d/", port), cfg.AppOptions.StartMinimized || fromSteamVR)
}

// alreadyRunning claims a per-user mutex and reports whether another Fern
// holds it. The handle is kept open for the life of the process.
func alreadyRunning() bool {
	name, _ := syscall.UTF16PtrFromString("Local\\FernChatbox")
	h, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(name)))
	return h != 0 && err == syscall.ERROR_ALREADY_EXISTS
}

func behaviour(o modules.AppOptions) UI.Behaviour {
	b := UI.Behaviour{Tray: o.Tray, CloseToTray: o.CloseToTray, MinimizeToTray: o.MinimizeToTray}
	if mods, key, _, err := modules.ParseHotkey(o.MicHotkey); err == nil {
		b.HotkeyMods, b.HotkeyKey = mods, key
	}
	return b
}

// watchAppOptions hands window and shortcut settings to the UI when they
// change. A cheap comparison twice a second, off the send loop.
func watchAppOptions(last modules.AppOptions) {
	for range time.Tick(500 * time.Millisecond) {
		if o := modules.GetConfig().AppOptions; o != last {
			last = o
			UI.Apply(behaviour(o))
		}
	}
}

// toggleMic presses VRChat's voice button over OSC: 1 then 0 toggles the mic
// (or talks for a moment with push-to-talk).
func toggleMic() {
	opts := modules.GetConfig().OSCOptions
	c := osc.NewClient(opts.Host, opts.Port)
	_ = c.Send(osc.NewMessage("/input/Voice", int32(1)))
	time.Sleep(120 * time.Millisecond)
	_ = c.Send(osc.NewMessage("/input/Voice", int32(0)))
}

// quitWithSteamVR closes Fern once SteamVR, which started it, has closed.
func quitWithSteamVR() {
	seen, gone := false, 0
	for range time.Tick(5 * time.Second) {
		if steamVRRunning() {
			seen, gone = true, 0
			continue
		}
		if gone++; seen && gone >= 2 {
			UI.Quit()
			return
		}
	}
}

// sendLoop pushes the chatbox to VRChat, re-sending instantly when the config
// changes instead of always waiting for the full timeout.
func sendLoop() {
	for {
		cfg := modules.GetConfig()
		ensureClient(cfg.OSCOptions)

		if cfg.DisplayOptions.Show {
			if cfg.DisplayOptions.Loading {
				modules.AdvanceLoading(int(modules.EffectiveRateMS(cfg)))
			}
			body, payload := modules.BuildPayload()
			if body != "" && client != nil {
				// Third argument false: do not play the notification sound on every
				// update. While the loading animation is on this fires about once a
				// second, which would ding everyone nearby non-stop.
				msg := osc.NewMessage("/chatbox/input", payload, true, false)
				if err := client.Send(msg); err == nil {
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

func ensureClient(opts modules.OSCOptions) {
	key := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	if client != nil && clientKey == key {
		return
	}
	client = osc.NewClient(opts.Host, opts.Port)
	clientKey = key
	oscAddr.Store(key)
}

func oscTarget() string {
	if addr, ok := oscAddr.Load().(string); ok {
		return addr
	}
	return "127.0.0.1:9000"
}
