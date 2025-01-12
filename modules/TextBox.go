package modules

import (
	"fmt"
	"strings"
	"time"
)

var MessageN int
var text string
var Seconds int

func init() {
	//add a button to the UI to start the timer
	//StartAfkTimer()
}

func StartAfkTimer() {
	go func() {
		for {
			time.Sleep(1200 * time.Millisecond)
			Seconds++
		}
	}()
}

func GetAfkTime(CustomText string) string {

	duration := time.Duration(Seconds) * time.Second
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	secs := int(duration.Seconds()) % 60

	if CustomText != "" {
		return fmt.Sprintf("%v %02d:%02d:%02d", CustomText, hours, minutes, secs)
	} else {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, secs)
	}
}

func TextBox() string {
	text = getMessage(MessageN)

	meow := []struct {
		flag bool
		text string
	}{
		{Config.TextOptions.AFK, GetAfkTime("AFK ")},            //GetAfktime
		{Config.TextOptions.LOADING, Loading()},                 //The loading
		{Config.TextOptions.TIME, time.Now().Format("3:04 PM")}, //Time
		{Config.TextOptions.TEXT, text},                         //Random Text/
		{Config.TextOptions.MEM, GetMem()},                      //Memory Usage
		{Config.TextOptions.CPU, GetCpuInfo(true, true)},
		{Config.TextOptions.ActiveWindow, GetActiveWindow()}, //Active Window
		{Config.TextOptions.LOGS, GetJoinAndLeaveLogs()},
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
