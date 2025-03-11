package modules

import (
	"fmt"
	"math"
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

func getMessage() string {
	if len(Config.TextMessages) == 0 {
		return ""
	}

	if Config.TextOptions.Cycle {
		for {
			fmt.Println(MessageN)
			return Config.TextMessages[MessageN%len(Config.TextMessages)]
		}
	}

	return Config.TextMessages[0]
}
