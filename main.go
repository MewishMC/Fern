package main

import (
	"VrChatBox/Ui"
	"VrChatBox/modules"
	"fmt"
	"github.com/hypebeast/go-osc/osc"
	"time"
)

//media to show what's playing

// ideas, add commands if possible (by typing in the ui)
//make it so you can send messages to the chatbox (by typing in the ui)
//make it so you can change the random text through the ui
//have the logs show in the ui to save more characters and saved as a file

var client = osc.NewClient("127.0.0.1", 9000)
var msg = osc.NewMessage("/chatbox/input", "", true)

func main() {
	fmt.Println("Starting up!")
	go UI.DisplayUI()
	for {
		modules.MessageN += 1
		msg.Arguments[0] = modules.TextBox()
		msg.Arguments[0] = msg.Arguments[0].(string)[:min(len(msg.Arguments[0].(string)), 142)] + "\x03\xFE"
		_ = client.Send(msg)
		time.Sleep(1800 * time.Millisecond)
	}
}
