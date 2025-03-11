package main

import (
	"Fern/UI"
	"Fern/modules"
	"fmt"
	"github.com/hypebeast/go-osc/osc"
	"time"
)

//media to show what's playing
//Get the prosses id of spotify and just display the windowname

// ideas, add commands if possible (by typing in the ui)
//make it so you can send messages to the chatbox (by typing in the ui)
//make it so you can change the random text through the ui
//have the logs show in the ui to save more characters and saved as a file

var client = osc.NewClient("127.0.0.1", 9000)
var msg = osc.NewMessage("/chatbox/input", "", true)

func main() {
	fmt.Println("Starting up!")
	go UI.StartWebview()
	go func() {
		for len(modules.Config.TextMessages) < 1 {
			time.Sleep(time.Second)
		}
		for {
			modules.MessageN += 1
			time.Sleep(time.Millisecond * time.Duration(modules.Config.TextOptions.Speed))
		}
	}()

	for {
		msg.Arguments[0] = modules.TextBox()
		msg.Arguments[0] = msg.Arguments[0].(string)[:min(len(msg.Arguments[0].(string)), 142)] + "\x03\xFE"

		if modules.Config.DisplayOptions.Show {
			_ = client.Send(msg)
		}

		modules.SearchForPeople()
		time.Sleep(time.Millisecond * time.Duration(modules.Config.TimeoutMS))
	}
}
