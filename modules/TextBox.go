package modules

import (
	"strings"
	"time"
)

var MessageN int
var text string

func TextBox() string {
	text = getMessage(MessageN)

	meow := []struct {
		flag bool
		text string
	}{
		{Config.TextOptions.LOADING, Loading()},                 //The loading
		{Config.TextOptions.TIME, time.Now().Format("3:04 PM")}, //Time
		{Config.TextOptions.TEXT, text},                         //Random Text
		{Config.TextOptions.MEM, GetMem()},                      //Memory Usage
		{Config.TextOptions.CPU, GetCpuInfo(true, true)},
		{Config.TextOptions.ActiveWindow, GetActiveWindow()}, //Active Window
		{Config.LOGS, GetJoinAndLeaveLogs()},
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
