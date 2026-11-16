package modules

// Voicemod's microphone controls: mute and the bleep (Voicemod's "bad
// language" beep, which covers your voice while it is on). Both go through
// the Control API session in voicemod.go. The bleep is held: it starts with
// bleepOn and stops with bleepOff, and it always stops by itself after
// BleepMaxSec in case the release never arrives.

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IDs of the Voicemod rule trigger and actions.
const (
	TriggerVoicemodMuted = "voicemod.muted"
	ActionVoicemodMute   = "voicemod.mute"
	ActionVoicemodBleep  = "voicemod.bleep"
)

var (
	vmBleepMu    sync.Mutex
	vmBleepOn    bool
	vmBleepGen   uint64
	vmBleepUntil time.Time
	vmBleepLeft  bool // a session ended mid-bleep
	// vmBleepLimit is the longest a bleep lasts; swapped in tests.
	vmBleepLimit = func() time.Duration {
		return time.Duration(GetConfig().VoicemodOptions.BleepMaxSec) * time.Second
	}
)

// parseVoicemodMute reads the microphone state from a mute reply or event:
// toggleMuteMic and getMuteMicStatus carry {"value": bool}; events named
// ...MuteMic...Enabled/DisabledEvent carry it in the name.
func parseVoicemodMute(action string, parts ...json.RawMessage) *bool {
	a := strings.ToLower(action)
	if !strings.Contains(a, "mutemic") {
		return nil
	}
	yes, no := true, false
	switch {
	case strings.HasSuffix(a, "disabledevent"):
		return &no
	case strings.HasSuffix(a, "enabledevent"):
		return &yes
	}
	for _, part := range parts {
		if len(part) == 0 || part[0] != '{' {
			continue
		}
		var v struct {
			Value *bool `json:"value"`
			Muted *bool `json:"muted"`
		}
		if json.Unmarshal(part, &v) != nil {
			continue
		}
		if v.Value != nil {
			return v.Value
		}
		if v.Muted != nil {
			return v.Muted
		}
	}
	return nil
}

func vmBleeping() bool {
	vmBleepMu.Lock()
	defer vmBleepMu.Unlock()
	return vmBleepOn
}

// endBleep forgets a bleep when the session ends, remembering to switch it
// off on the next one.
func endBleep() {
	vmBleepMu.Lock()
	if vmBleepOn {
		vmBleepOn, vmBleepLeft = false, true
		vmBleepGen++
	}
	vmBleepMu.Unlock()
}

func bleepLeftOn() bool {
	vmBleepMu.Lock()
	defer vmBleepMu.Unlock()
	left := vmBleepLeft
	vmBleepLeft = false
	return left
}

// bleepOverdue reports a bleep that outlived its limit, in case its timer's
// command was dropped.
func bleepOverdue() bool {
	vmBleepMu.Lock()
	defer vmBleepMu.Unlock()
	return vmBleepOn && time.Now().After(vmBleepUntil.Add(time.Second))
}

// VoicemodMuted reports the microphone mute state and whether it is known.
func VoicemodMuted() (muted, known bool) {
	vmMu.Lock()
	defer vmMu.Unlock()
	return vmMuted, vmMuteOK
}

func voicemodConnected() bool {
	vmMu.Lock()
	defer vmMu.Unlock()
	return vmLink.State == linkConnected
}

// voicemodMic runs the microphone commands on the session goroutine:
// mute (toggle), muteSet ("1" or "0"), bleepOn (optional length in ms) and
// bleepOff (optional generation, so an old timer cannot end a newer bleep).
func voicemodMic(w *vmWriter, c vmCommand) {
	switch c.action {
	case "mute":
		if _, err := w.send("toggleMuteMic", nil); err == nil {
			vmMu.Lock()
			if vmMuteOK {
				vmMuted = !vmMuted
			}
			vmMu.Unlock()
			NotifyChange()
		}
		_, _ = w.send("getMuteMicStatus", nil)
	case "muteSet":
		want := c.id == "1"
		vmMu.Lock()
		known, muted := vmMuteOK, vmMuted
		vmMu.Unlock()
		if known && muted != want {
			if _, err := w.send("toggleMuteMic", nil); err == nil {
				vmMu.Lock()
				vmMuted = want
				vmMu.Unlock()
				NotifyChange()
			}
		}
		_, _ = w.send("getMuteMicStatus", nil)
	case "bleepOn":
		limit := vmBleepLimit()
		if limit <= 0 {
			limit = 10 * time.Second
		}
		d := limit
		if ms, err := strconv.Atoi(c.id); err == nil && ms > 0 && time.Duration(ms)*time.Millisecond < limit {
			d = time.Duration(ms) * time.Millisecond
		}
		vmBleepMu.Lock()
		was := vmBleepOn
		vmBleepGen++
		gen := vmBleepGen
		vmBleepUntil = time.Now().Add(d)
		vmBleepMu.Unlock()
		if !was {
			if _, err := w.send("setBeepSound", map[string]int{"badLanguage": 1}); err != nil {
				return
			}
		}
		vmBleepMu.Lock()
		if vmBleepGen == gen {
			vmBleepOn = true
		}
		vmBleepMu.Unlock()
		time.AfterFunc(d, func() { VoicemodCommand("bleepOff", strconv.FormatUint(gen, 10)) })
		NotifyChange()
	case "bleepOff":
		vmBleepMu.Lock()
		if !vmBleepOn || (c.id != "" && c.id != strconv.FormatUint(vmBleepGen, 10)) {
			vmBleepMu.Unlock()
			return
		}
		vmBleepOn = false
		vmBleepGen++
		vmBleepMu.Unlock()
		_, _ = w.send("setBeepSound", map[string]int{"badLanguage": 0})
		NotifyChange()
	}
}

// sanitizeMic bounds the microphone options.
func (v *VoicemodOptions) sanitizeMic() {
	v.MuteText = RuneLimit(strings.Join(strings.Fields(v.MuteText), " "), 24)
	if v.BleepMaxSec == 0 {
		v.BleepMaxSec = 10
	}
	v.BleepMaxSec = clampInt(v.BleepMaxSec, 1, 60)
	for _, k := range []*string{&v.MuteHotkey, &v.BleepHotkey} {
		*k = canonicalHotkey(*k)
	}
	if v.BleepHotkey != "" && v.BleepHotkey == v.MuteHotkey {
		v.BleepHotkey = ""
	}
}

// canonicalHotkey writes a shortcut the canonical way, or "" when invalid.
func canonicalHotkey(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	if _, _, canonical, err := ParseHotkey(s); err == nil {
		return canonical
	}
	return ""
}

func init() {
	registerVoicemodRules()
}

func registerVoicemodRules() {
	mustRegister(RegisterRuleTrigger(RuleTrigger{
		ID: TriggerVoicemodMuted, Label: "Voicemod mic muted", Category: "Microphone",
		Description: "While Voicemod's microphone is muted",
		Fields:      []RuleField{},
		Check: func(RuleParams) bool {
			muted, known := VoicemodMuted()
			return known && muted && voicemodConnected()
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionVoicemodMute, Label: "Mute in Voicemod", Category: "Microphone",
		Description: "Undo puts the microphone back the way it was",
		Fields: []RuleField{
			{Key: "state", Label: "To", Type: FieldSelect, Options: []RuleOption{{"on", "Muted"}, {"off", "Unmuted"}, {"toggle", "Toggle"}}, Default: "on"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if !voicemodConnected() {
				return nil, errors.New("Voicemod isn't connected")
			}
			before, known := VoicemodMuted()
			if !known {
				return nil, errors.New("Voicemod hasn't said whether the mic is muted yet")
			}
			after := before
			switch p.String("state") {
			case "off":
				after = false
			case "toggle":
				after = !before
			default:
				after = true
			}
			if before == after {
				return nil, nil
			}
			VoicemodCommand("muteSet", boolDigit(after))
			return func() {
				if now, ok := VoicemodMuted(); ok && now == after {
					VoicemodCommand("muteSet", boolDigit(before))
				}
			}, nil
		},
	}))
	mustRegister(RegisterRuleAction(RuleAction{
		ID: ActionVoicemodBleep, Label: "Bleep in Voicemod", Category: "Microphone",
		Description: "Covers your voice with Voicemod's beep for a moment",
		Fields: []RuleField{
			{Key: "seconds", Label: "For", Type: FieldNumber, Default: 2.0, Min: 1, Max: 60, Step: 1, Hint: "Seconds, up to the longest bleep set on the Voicemod page"},
		},
		Run: func(_ RuleContext, p RuleParams) (func(), error) {
			if !voicemodConnected() {
				return nil, errors.New("Voicemod isn't connected")
			}
			VoicemodCommand("bleepOn", strconv.Itoa(int(p.Number("seconds")*1000)))
			return nil, nil
		},
	}))
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
