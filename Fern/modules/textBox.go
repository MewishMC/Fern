package modules

import (
	"strings"
	"sync"
	"time"
)

// chatboxMarker is appended because VRChat expects a terminated input string.
const chatboxMarker = "\x03\xFE"

var (
	holdMu    sync.RWMutex
	holdText  string
	holdUntil time.Time
	holdPin   bool
	holdSeq   uint64 // bumped for every new message
	holdLive  bool   // the message is still being typed
)

// RuneLimit shortens s to at most n runes without splitting a character.
func RuneLimit(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// SetHold shows a custom message for a while. A duration of zero pins it until
// it is cleared.
func SetHold(text string, d time.Duration) {
	setHold(text, d, false)
}

// setHold shows text. A live message is one being typed: each change
// replaces the last one without counting as a new message.
func setHold(text string, d time.Duration, live bool) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	holdMu.Lock()
	if !live || !holdLive || holdText == "" {
		holdSeq++
	}
	holdLive = live
	holdText = RuneLimit(text, 143)
	if d <= 0 {
		holdPin = true
		holdUntil = time.Time{}
	} else {
		holdPin = false
		holdUntil = time.Now().Add(d)
	}
	holdMu.Unlock()
	NotifyChange()
}

// ClearHold removes a pinned or temporary message.
func ClearHold() {
	holdMu.Lock()
	holdText = ""
	holdLive = false
	holdPin = false
	holdUntil = time.Time{}
	holdMu.Unlock()
	NotifyChange()
}

// HoldState reports the active overridden message, if any.
func HoldState() (text string, until int64, pinned bool) {
	holdMu.RLock()
	defer holdMu.RUnlock()
	if holdText == "" {
		return "", 0, false
	}
	if holdPin {
		return holdText, 0, true
	}
	if time.Now().After(holdUntil) {
		return "", 0, false
	}
	return holdText, holdUntil.UnixMilli(), false
}

// holdOverlay returns the held message, pinned or timed, while one is active,
// its sequence number and whether it is still being typed.
func holdOverlay() (string, uint64, bool, bool) {
	text, _, _ := HoldState()
	holdMu.RLock()
	seq, live := holdSeq, holdLive
	holdMu.RUnlock()
	return text, seq, live, text != ""
}

func moduleText(id string, cfg Config) string {
	switch id {
	case ModuleAFK:
		return afkLine(cfg)
	case ModuleLoading:
		return Loading()
	case ModuleTime:
		return clockText(cfg)
	case ModuleText:
		return textLine(cfg)
	case ModuleMemory:
		return memoryLine(cfg)
	case ModuleCPU:
		return cpuLine(cfg)
	case ModuleMedia:
		return mediaLine(cfg)
	case ModuleWindow:
		return windowLine(cfg)
	case ModuleLogs:
		return logsLine(cfg)
	case ModuleBattery:
		return batteryLine(cfg)
	case ModuleVRPerf:
		return vrPerfLine(cfg)
	case ModuleHeart:
		return heartRateLine(cfg)
	case ModuleLyrics:
		return lyricsLine(cfg)
	case ModuleWeather:
		return weatherLine(cfg)
	case ModuleDiscord:
		return discordLine(cfg)
	case ModuleTwitch:
		return twitchLine(cfg)
	case ModuleSoundpad:
		return soundpadLine(cfg)
	case ModuleVoicemod:
		return voicemodLine(cfg)
	case ModuleNetwork:
		return networkLine(cfg)
	case ModuleGPU:
		return gpuLine(cfg)
	case ModuleWorld:
		return worldLine(cfg)
	}
	return ""
}

// withLabel puts an optional prefix in front of a module line.
func withLabel(label, text string) string {
	label = strings.TrimSpace(label)
	if label == "" || text == "" {
		return text
	}
	return label + " " + text
}

func textLine(cfg Config) string {
	return withLabel(cfg.TextOptions.Label, CurrentMessage())
}

// ModuleSamples renders every module's line whether it is enabled or not, so
// the panel can show what each one would add to the chatbox.
func ModuleSamples(cfg Config) map[string]string {
	samples := make(map[string]string, len(DefaultOrder))
	for _, id := range DefaultOrder {
		samples[id] = strings.TrimSpace(moduleText(id, cfg))
	}
	return samples
}

// lineVisible reports whether a line's VR / desktop setting lets it show.
func lineVisible(id string, d DisplayOptions, vr bool) bool {
	switch d.Visibility[id] {
	case ShowVR:
		return vr
	case ShowDesktop:
		return !vr
	}
	return true
}

// buildLines renders every enabled module in the configured order, with the
// shorter forms used to fit the chatbox. hidden lists enabled lines left out
// by their VR / desktop setting.
func buildLines(cfg Config, vr bool) (lines []FitLine, hidden []string) {
	d := cfg.DisplayOptions
	lines = make([]FitLine, 0, len(d.Order))
	for _, id := range d.Order {
		if !ModuleEnabled(id, d) {
			continue
		}
		if !lineVisible(id, d, vr) {
			hidden = append(hidden, id)
			continue
		}
		line := FitLine{ID: id}
		if id == ModuleMedia {
			line = mediaFitLine(cfg)
		} else {
			line.Variants = []string{moduleText(id, cfg)}
		}
		for i, v := range line.Variants {
			line.Variants[i] = strings.TrimSpace(v)
		}
		if len(line.Variants) == 0 || line.Variants[0] == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines, hidden
}

// Chatbox is what the chatbox shows right now and how it was fitted.
type Chatbox struct {
	Fit
	Hold   bool     `json:"hold"`   // a sent message is showing instead of the lines
	Hidden []string `json:"hidden"` // enabled lines hidden by their VR / desktop setting
	Seq    uint64   `json:"-"`      // changes with every new held message
	Live   bool     `json:"live"`   // the held message is still being typed
}

// sendLimit is the longest text Fern sends. VRChat takes 144 characters and
// the marker takes one of them.
func sendLimit(d DisplayOptions) int {
	limit := d.MaxChars - 1
	if limit < 8 {
		limit = 8
	}
	return limit
}

// ComposeChatbox renders the chatbox: a held message if one is active,
// otherwise every line fitted into VRChat's limits.
func ComposeChatbox() Chatbox {
	if text, seq, live, ok := holdOverlay(); ok {
		return Chatbox{Fit: Fit{Text: text}, Hold: true, Seq: seq, Live: live}
	}
	cfg := GetConfig()
	d := cfg.DisplayOptions
	if !d.Show {
		return Chatbox{}
	}
	// Looking for a VR runtime lists processes, so only when a line asks.
	vr := false
	if len(d.Visibility) > 0 {
		vr = InVR()
	}
	lines, hidden := buildLines(cfg, vr)
	fit := FitLines(lines, d.DropOrder, d.Separator, d.LinePrefix, d.LineSuffix, sendLimit(d), d.MaxLines)
	fit.Text = strings.TrimSpace(fit.Text)
	return Chatbox{Fit: fit, Hidden: hidden}
}

// PreviewText is what the chatbox will show, without VRChat's marker.
func PreviewText() string {
	return ComposeChatbox().Text
}

// BuildPayload returns the visible text and the exact OSC payload.
func BuildPayload() (body, payload string) {
	return PayloadFor(ComposeChatbox())
}

// PayloadFor returns the text of box and the exact OSC payload for it.
func PayloadFor(box Chatbox) (body, payload string) {
	body = strings.TrimSpace(RuneLimit(box.Text, sendLimit(GetConfig().DisplayOptions)))
	if body == "" {
		return "", ""
	}
	return body, body + chatboxMarker
}
