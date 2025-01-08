package modules

import (
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
			result.WriteString("z")
		case 1:
			result.WriteString("Z")
		case 2:
			result.WriteString("ᶻ")
		}
	}
	return result.String()
}

func getMessage(message int) string {
	messages := []string{
		//"meow", "woof", "rawr", "bark", "grr", "(˃ᆺ˂)", "lilys phone number is 406- uhm i forgot",
		"I WANT KANKAN TO BEAT ME <3", "I WANNA BE KANKANS GF SO HE BEATS ME <3",
	}
	return messages[message%len(messages)]
}
