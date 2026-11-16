package modules

import (
	"testing"
	"time"
)

func afkTestOptions() AfkOptions {
	return DefaultConfig().AfkOptions
}

func TestAfkSourcesDecideIdleTime(t *testing.T) {
	now := time.Unix(10000, 0)
	ago := func(sec int) time.Time { return now.Add(-time.Duration(sec) * time.Second) }
	// Sitting still in VRChat for ten minutes while typing in another window.
	at := map[string]time.Time{
		AfkSourceMovement: ago(600), AfkSourceVoice: ago(600), AfkSourceOSC: ago(600),
		AfkSourceWindow: ago(600), AfkSourceInput: ago(2),
	}
	o := afkTestOptions()
	o.AFKTime = 60

	if active, idle := afkStatus(o, now, at, false); active || idle != 2*time.Second {
		t.Errorf("typing elsewhere: active=%v idle=%v, want not AFK, 2s idle", active, idle)
	}

	o.MovementOnly = true
	if active, _ := afkStatus(o, now, at, false); !active {
		t.Error("movement only should ignore the keyboard")
	}
	o.MovementOnly = false

	o.InputMode = AfkInputDesktop
	if active, _ := afkStatus(o, now, at, true); !active {
		t.Error("keyboard set to desktop only still counted in VR")
	}
	if active, _ := afkStatus(o, now, at, false); active {
		t.Error("keyboard set to desktop only ignored on desktop")
	}

	// The reverse: moving in VR with the keyboard untouched is not AFK.
	at[AfkSourceMovement], at[AfkSourceInput] = ago(1), ago(900)
	o.InputMode = AfkInputAlways
	if active, idle := afkStatus(o, now, at, true); active || idle != time.Second {
		t.Errorf("moving in VR: active=%v idle=%v", active, idle)
	}

	none := afkTestOptions()
	none.UseMovement, none.UseVoice, none.UseOSC, none.UseWindow, none.InputMode = false, false, false, false, AfkInputOff
	if active, _ := afkStatus(none, now, at, false); active {
		t.Error("with no sources picked nobody should be detected as AFK")
	}
}

func TestManualAfkStaysOn(t *testing.T) {
	now := time.Unix(10000, 0)
	at := map[string]time.Time{AfkSourceInput: now}
	o := afkTestOptions()
	o.AutoDetect = false
	o.Manual = true
	o.ManualSince = now.Add(-90 * time.Second).UnixMilli()
	active, idle := afkStatus(o, now, at, false)
	if !active || idle != 90*time.Second {
		t.Errorf("manual AFK: active=%v idle=%v", active, idle)
	}

	o.Manual = false
	o.sanitize()
	if o.ManualSince != 0 {
		t.Error("turning manual AFK off should clear its start time")
	}
	o.Manual = true
	o.sanitize()
	if o.ManualSince == 0 {
		t.Error("manual AFK without a start time should start now")
	}
}

func TestRenderAfk(t *testing.T) {
	now := time.Date(2026, 1, 1, 15, 30, 0, 0, time.Local)
	idle := 5*time.Minute + 12*time.Second
	o := afkTestOptions()
	cases := []struct {
		name string
		edit func(*AfkOptions)
		want string
	}{
		{"default", func(*AfkOptions) {}, "💤 AFK 00:05:12"},
		{"no icon", func(o *AfkOptions) { o.Icon = "" }, "AFK 00:05:12"},
		{"template", func(o *AfkOptions) { o.AFKMessage = "away for {time}"; o.TimerStyle = "words" }, "💤 away for 5 minutes"},
		{"since", func(o *AfkOptions) { o.AFKMessage = "away since {since}" }, "💤 away since 3:24 PM"},
		{"timer font", func(o *AfkOptions) { o.TimerStyle = "compact"; o.TimerFont = "superscript" }, "💤 AFK ⁵ᵐ"},
		{"plain text", func(o *AfkOptions) { o.ShowTimer = false; o.PlainMessage = "brb" }, "💤 brb"},
		{"plain fallback", func(o *AfkOptions) { o.ShowTimer = false; o.AFKMessage = "away · {time}" }, "💤 away"},
	}
	for _, c := range cases {
		opts := o
		c.edit(&opts)
		if got := renderAfk(opts, idle, now, false); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatIdleStyles(t *testing.T) {
	cases := []struct {
		style string
		d     time.Duration
		want  string
	}{
		{"full", 3725 * time.Second, "01:02:05"},
		{"short", 125 * time.Second, "2m 05s"},
		{"minutes", 45 * time.Second, "45s"},
		{"compact", 3725 * time.Second, "1h 2m"},
		{"compact", 7200 * time.Second, "2h"},
		{"words", 61 * time.Second, "1 minute"},
		{"words", 7320 * time.Second, "2 hours 2 minutes"},
		{"words", 1 * time.Second, "1 second"},
	}
	for _, c := range cases {
		if got := formatIdle(c.d, c.style); got != c.want {
			t.Errorf("formatIdle(%v, %q) = %q, want %q", c.d, c.style, got, c.want)
		}
	}
}

func TestTickDiffSurvivesWrap(t *testing.T) {
	if got := tickDiff(5, 0xFFFFFFFF-4); got != 10*time.Millisecond {
		t.Errorf("tickDiff across the wrap = %v, want 10ms", got)
	}
}

func TestOSCSources(t *testing.T) {
	cases := map[string]string{
		"/avatar/parameters/VelocityX": AfkSourceMovement,
		"/avatar/parameters/AngularY":  AfkSourceMovement,
		"/avatar/parameters/Voice":     AfkSourceVoice,
		"/avatar/parameters/AngularX":  AfkSourceOSC,
	}
	for addr, want := range cases {
		if got, ok := oscSource(addr); !ok || got != want {
			t.Errorf("oscSource(%q) = %q, %v; want %q", addr, got, ok, want)
		}
	}
	if _, ok := oscSource("/avatar/parameters/Grounded"); ok {
		t.Error("Grounded should not count as activity")
	}
}

func TestAfkPresetsRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range AfkPresets {
		if seen[p.ID] {
			t.Errorf("duplicate preset id %q", p.ID)
		}
		seen[p.ID] = true
		o := afkTestOptions()
		p.apply(&o)
		o.sanitize()
		if got := AfkPresetMatch(o); got != p.ID {
			t.Errorf("preset %q matches %q after applying", p.ID, got)
		}
	}
	if AfkPresetMatch(afkTestOptions()) != "classic" {
		t.Error("the default look should be the classic preset")
	}
	samples := AfkPresetSamples(DefaultConfig())
	if len(samples) != len(AfkPresets)+1 || samples[0].ID != "current" || samples[0].Sample != "💤 AFK 00:05:12" {
		t.Errorf("samples = %+v", samples[:1])
	}
}
