package modules

import (
	"fmt"
	"github.com/hypebeast/go-osc/osc"
	"net"
	"strings"
	"sync"
	"time"
)

var (
	lastVelocityTime = time.Now()
	windowMutex      sync.Mutex
	prevWindowTitle  string
)

func IsActiveWindowChanged() bool {
	windowMutex.Lock()
	defer windowMutex.Unlock()

	currentTitle := GetActiveWindow()
	if currentTitle != prevWindowTitle {
		prevWindowTitle = currentTitle
		return true
	}
	return false
}

func handleMessage(msg *osc.Message) {
	if Config.AfkOptions.Bypass {
		return
	}

	if Config.AfkOptions.Poopyafk {
		if containsAny(msg.Address, "/Velocity", "/AngularY") {
			lastVelocityTime = time.Now()
		}
		return
	}

	if IsActiveWindowChanged() || containsAny(msg.Address, "/Velocity", "/AngularY", "/Voice") {
		lastVelocityTime = time.Now()
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

func startUDPListener() {
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9001}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		panic(fmt.Errorf("failed to start UDP listener: %w", err))
	}

	defer listener.Close()

	messageQueue := make(chan osc.Packet, 100)

	go func() {
		for packet := range messageQueue {
			switch pk := packet.(type) {
			case *osc.Message:
				go handleMessage(pk)
			case *osc.Bundle:
				for _, msg := range pk.Messages {
					go handleMessage(msg)
				}
			default:
				fmt.Printf("Unknown packet type %T\n", pk)
			}
		}
	}()

	buf := make([]byte, 2048)
	for {
		n, _, err := listener.ReadFromUDP(buf)
		if err != nil {
			fmt.Println("UDP Read error:", err)
			continue
		}

		packet, err := osc.ParsePacket(string(buf[:n]))
		if err != nil {
			fmt.Println("OSC Parse error:", err)
			continue
		}

		select {
		case messageQueue <- packet:
		default:
			fmt.Println("Warning: Message queue full, dropping packet")
		}
	}
}

func GetAfkTime(CustomText string) string {
	if !Config.AfkOptions.AutoDetect || time.Since(lastVelocityTime).Seconds() < Config.AfkOptions.AFKTime {
		return ""
	}

	duration := time.Since(lastVelocityTime)
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	secs := int(duration.Seconds()) % 60

	return fmt.Sprintf("%v %02d:%02d:%02d", CustomText, hours, minutes, secs)
}

func init() {
	go startUDPListener()
}
