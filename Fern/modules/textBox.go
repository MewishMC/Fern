package modules

import (
	"strings"
	"time"
)

var MessageN int
var text string

func TextBox() string {
	text = getMessage()

	meow := []struct {
		flag bool
		text string
	}{
		{Config.DisplayOptions.Afk, GetAfkTime(Config.AfkOptions.AFKMessage)},
		{Config.DisplayOptions.Loading, Loading()},
		{Config.DisplayOptions.Time, time.Now().Format("3:04 PM")},
		{Config.DisplayOptions.Text, text},
		{Config.DisplayOptions.Memory, GetMem()},
		{Config.DisplayOptions.CPU, GetCpuInfo(true, true)},
		{Config.DisplayOptions.Media, PrintMediaInfo()},
		{Config.DisplayOptions.ActiveWindow, GetActiveWindow()},
		{Config.DisplayOptions.VRChatLogs, getLastPlayerActivity()},
	}
	var result []string
	for _, condition := range meow {
		if !condition.flag {
			continue
		}
		trimmedText := strings.TrimSpace(condition.text)
		if trimmedText == "" {
			continue
		}

		result = append(result, trimmedText)
	}

	return strings.Join(result, "\n")
}
