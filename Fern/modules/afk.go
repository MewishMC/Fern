package modules

import (
	"fmt"
	"github.com/hypebeast/go-osc/osc"
	"net"
	"strings"
	"time"
)

var lastVelocityTime = time.Now()

func handleMessage(msg *osc.Message) {
	if strings.Contains(msg.Address, "/velocity") {
		lastVelocityTime = time.Now()
	}
}

func init() {
	go func() {
		listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9001})
		if err != nil {
			panic(err)
		}
		for {
			buf := make([]byte, 1024)
			n, _, err := listener.ReadFromUDP(buf)
			if err != nil {
				continue
			}
			packet, err := osc.ParsePacket(string(buf[:n]))
			if err != nil {
				continue
			}
			switch pk := packet.(type) {
			case *osc.Message:
				handleMessage(pk)
			case *osc.Bundle:
				for _, msg := range pk.Messages {
					handleMessage(msg)
				}
			default:
				fmt.Printf("unknown packet type %T\n", pk)
			}
		}
	}()
}

func GetAfkTime(CustomText string) string {
	if time.Since(lastVelocityTime).Seconds() < 10 {
		return "i am here :D"
	}
	duration := time.Since(lastVelocityTime)
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	secs := int(duration.Seconds()) % 60

	if CustomText != "" {
		return fmt.Sprintf("%v %02d:%02d:%02d", CustomText, hours, minutes, secs)
	} else {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	}
}
