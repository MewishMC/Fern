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
	setupProfiles()
	setupRules()
	setupClipboard()
	setupControlAPI()
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
	b.Extra = append(profileHotkeys(), ruleHotkeys()...)
	b.Extra = append(b.Extra, clipboardHotkeys()...)
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
	var lastBody string
	var lastSeq uint64
	var lastSend time.Time
	for {
		cfg := modules.GetConfig()
		oscClients(cfg.OSCOptions)

		if cfg.DisplayOptions.Show {
			if cfg.DisplayOptions.Loading {
				modules.AdvanceLoading(int(modules.EffectiveRateMS(cfg)))
			}
			box := modules.ComposeChatbox()
			body, payload := modules.PayloadFor(box)
			// Typed messages come in bursts. Keep them to VRChat's pace so none
			// is dropped, folding the keystrokes in between into one send.
			if wait := sendGap(cfg.ChatOptions, box, body, lastBody, lastSend); wait > 0 {
				time.Sleep(wait)
				continue
			}
			if body != "" {
				notify := shouldNotify(cfg.ChatOptions, box, body, lastBody, lastSeq)
				msg := osc.NewMessage("/chatbox/input", payload, true, notify)
				if sendOSC(cfg.OSCOptions, msg) > 0 {
					lastBody = body
					lastSend = time.Now()
					if box.Hold {
						lastSeq = box.Seq
					}
					atomic.AddInt64(&sentCount, 1)
					atomic.StoreInt64(&lastSentAt, time.Now().UnixMilli())
					if !box.Live {
						modules.AddHistory(body)
					}
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
	if box.Live {
		return false
	}
	if box.Hold {
		return opts.NotifySend && box.Seq != lastSeq
	}
	return opts.NotifyAuto && body != lastBody
}

// sendGap is how long to wait before sending a typed message, so messages
// go out no faster than the live typing rate. Everything else, and a
// message that is already showing, goes at once.
func sendGap(opts modules.ChatOptions, box modules.Chatbox, body, lastBody string, lastSend time.Time) time.Duration {
	if !box.Hold || body == "" || body == lastBody || lastSend.IsZero() {
		return 0
	}
	gap := time.Duration(opts.LiveRateMS)*time.Millisecond - time.Since(lastSend)
	if gap < 0 {
		return 0
	}
	return gap
}

// sendTyping turns VRChat's typing bubble on or off at every output. Like
// the chatbox it is only sent while the display is on.
func sendTyping(on bool) {
	cfg := modules.GetConfig()
	if !cfg.DisplayOptions.Show || (on && !cfg.ChatOptions.TypingIndicator) {
		return
	}
	sendOSC(cfg.OSCOptions, osc.NewMessage("/chatbox/typing", on))
}
