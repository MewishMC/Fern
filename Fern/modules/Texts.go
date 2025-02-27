package modules

import (
	"math"
	"strconv"
	"strings"
)

func Loading() string {
	var result strings.Builder
	dots := MessageN % 2
	if int(math.Floor(float64(MessageN)/2))%2 == 1 {
		dots = 2 - dots
	}
	dots += 1

	for i := 0; i < dots; i++ {
		switch i % 3 {
		case 0:
			result.WriteString("⠋")
		case 1:
			result.WriteString("⠋")
		case 2:
			result.WriteString("⠋")

		}
	}
	return result.String()
}

func getMessage(message int) string {
	if len(Config.TextMessages) == 0 {
		return ""
	}

	if Config.TextOptions.Cycle == true {
		messages := make([]string, 0, len(Config.TextMessages))
		for _, text := range Config.TextMessages {
			messages = append(messages, text)
		}
		return messages[message%len(messages)]
	}

	return Config.TextMessages[strconv.Itoa(0)]
}
