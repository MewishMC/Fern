package modules

import (
	"strings"
	"time"
)

var MessageN int
var text string

func init() {
	//add a button to the UI to start the timer
	//StartAfkTimer()
}

func TextBox() string {
	text = getMessage(MessageN)

	meow := []struct {
		flag bool
		text string
	}{
		{Config.DisplayOptions.Afk, GetAfkTime("AFK ")},            //GetAfktime
		{Config.DisplayOptions.Loading, Loading()},                 //The loading
		{Config.DisplayOptions.Time, time.Now().Format("3:04 PM")}, //Time
		{Config.DisplayOptions.Text, text},                         //Random Text/
		{Config.DisplayOptions.Memory, GetMem()},                   //Memory Usage
		{Config.DisplayOptions.CPU, GetCpuInfo(true, true)},
		{Config.DisplayOptions.ActiveWindow, GetActiveWindow()}, //Active Window
		{Config.DisplayOptions.VRChatLogs, GetJoinAndLeaveLogs()},
	}
	var result []string
	for _, condition := range meow {
		if !condition.flag {
			continue
		}

		result = append(result, condition.text)
	}

	return strings.Join(result, "\n")
}
