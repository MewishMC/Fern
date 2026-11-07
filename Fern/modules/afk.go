package modules

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

var (
	activityMu      sync.RWMutex
	lastActivity    = time.Now()
	prevWindowTitle string
	voiceActive     bool
)

func markActivity() {
	activityMu.Lock()
	lastActivity = time.Now()
	activityMu.Unlock()
}

func lastActivityTime() time.Time {
	activityMu.RLock()
	defer activityMu.RUnlock()
	return lastActivity
}

// AfkInfo reports whether the user counts as afk and for how long.
func AfkInfo() (active bool, idle time.Duration) {
	cfg := GetConfig()
	if cfg.AfkOptions.Bypass || !cfg.AfkOptions.AutoDetect {
		return false, 0
	}
	idle = time.Since(lastActivityTime())
	return idle.Seconds() >= cfg.AfkOptions.AFKTime, idle
}

// Talking reports whether the user spoke recently.
func Talking() bool {
	activityMu.RLock()
	defer activityMu.RUnlock()
	return voiceActive
}

// GetAfkTime renders the afk module line.
func GetAfkTime(custom string) string {
	active, duration := AfkInfo()
	if !active {
		return ""
	}
	if strings.TrimSpace(custom) == "" {
		custom = "AFK"
	}
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	secs := int(duration.Seconds()) % 60
	return fmt.Sprintf("%s %02d:%02d:%02d", custom, hours, minutes, secs)
}

func isActivityAddress(address string) bool {
	return containsAny(address, "/Velocity", "/AngularY", "/AngularX", "/Voice", "/Input")
}

func handlePackage(pkt osc.Packet) {
	cfg := GetConfig()
	if cfg.AfkOptions.Bypass {
		return
	}
	switch pk := pkt.(type) {
	case *osc.Message:
		if cfg.AfkOptions.Poopyafk && !containsAny(pk.Address, "/Velocity", "/AngularY") {
			return
		}
		if isActivityAddress(pk.Address) {
			markActivity()
		}
		if strings.Contains(pk.Address, "/Voice") {
			activityMu.Lock()
			voiceActive = true
			activityMu.Unlock()
		}
	case *osc.Bundle:
		for _, m := range pk.Messages {
			if cfg.AfkOptions.Poopyafk && !containsAny(m.Address, "/Velocity", "/AngularY") {
				continue
			}
			if isActivityAddress(m.Address) {
				markActivity()
				break
			}
		}
	}
}

func containsAny(target string, substrings ...string) bool {
	for _, sub := range substrings {
		if strings.Contains(target, sub) {
			return true
		}
	}
	return false
}

func startAfkWorker() {
	go listenOSC()
	go watchWindowActivity()
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			activityMu.Lock()
			voiceActive = false
			activityMu.Unlock()
		}
	}()
}

// listenOSC listens for VRChat's outgoing OSC so activity can be detected.
// A busy port is not fatal: another Fern instance may already own it.
func listenOSC() {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9001}
	buf := make([]byte, 2048)
	for {
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			time.Sleep(20 * time.Second)
			continue
		}
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				break
			}
			pkt, err := osc.ParsePacket(string(buf[:n]))
			if err != nil {
				continue
			}
			handlePackage(pkt)
		}
		conn.Close()
		time.Sleep(time.Second)
	}
}

// watchWindowActivity treats switching windows as activity, but only samples
// once every 1.5s instead of on every OSC packet.
func watchWindowActivity() {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		cfg := GetConfig()
		if cfg.AfkOptions.Bypass || cfg.AfkOptions.Poopyafk || !cfg.AfkOptions.AutoDetect {
			continue
		}
		title := foregroundTitle()
		activityMu.Lock()
		changed := title != "" && title != prevWindowTitle
		if changed {
			prevWindowTitle = title
			lastActivity = time.Now()
		}
		activityMu.Unlock()
	}
}
