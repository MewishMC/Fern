package UI

import "testing"

func TestAllowedExternal(t *testing.T) {
	allowed := []string{
		"https://github.com/MewishMC/Fern/releases/tag/v2.2",
		"https://www.twitch.tv/activate",
		"https://www.twitch.tv/activate?public=true&device-code=ABCD",
		"https://dev.twitch.tv/console/apps/create",
		"https://discord.com/developers/applications",
	}
	for _, link := range allowed {
		if !allowedExternal(link) {
			t.Errorf("%s refused", link)
		}
	}
	refused := []string{
		"https://www.twitch.tv/activateevil",
		"https://www.twitch.tv.evil.com/activate",
		"https://www.twitch.tv/somebody",
		"https://discord.com/developers/applications\" & calc",
		"http://www.twitch.tv/activate",
		"file:///C:/Windows/System32/calc.exe",
	}
	for _, link := range refused {
		if allowedExternal(link) {
			t.Errorf("%s allowed", link)
		}
	}
}
