package modules

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextFilterDefaultsAndSanitize(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{}`), &c); err != nil {
		t.Fatal(err)
	}
	c.sanitize()
	got := c.TextFilter
	if got.Replace == nil || got.Censor == nil || got.Mask != "*" {
		t.Fatalf("old config without the section: %+v", got)
	}
	if def := DefaultConfig().TextFilter; !def.Typed || !def.Dictated || !def.Speech || def.Mask != "*" {
		t.Fatalf("defaults: %+v", def)
	}

	o := TextFilterOptions{Mask: "  #x", Censor: []string{" bad ", "BAD", "", "worse\nword"}}
	for i := 0; i < MaxWordReplacements+5; i++ {
		o.Replace = append(o.Replace, WordReplacement{On: true, From: "a\tb", To: strings.Repeat("x", 300)})
	}
	o.sanitize()
	if o.Mask != "#" {
		t.Errorf("mask = %q, want the first character", o.Mask)
	}
	if len(o.Replace) != MaxWordReplacements {
		t.Errorf("replacements = %d, want %d", len(o.Replace), MaxWordReplacements)
	}
	if o.Replace[0].From != "a b" || len([]rune(o.Replace[0].To)) != maxFilterReplace {
		t.Errorf("replacement not cleaned: %+v", o.Replace[0])
	}
	if want := []string{"bad", "worse word"}; strings.Join(o.Censor, "|") != strings.Join(want, "|") {
		t.Errorf("censor = %q, want %q (trimmed, deduplicated ignoring case, empty dropped)", o.Censor, want)
	}
	o.Mask = "\x01"
	o.sanitize()
	if o.Mask != "*" {
		t.Errorf("a control character mask = %q, want the default", o.Mask)
	}
}

func TestTextFilterApply(t *testing.T) {
	o := TextFilterOptions{
		Replace: []WordReplacement{
			{On: true, From: "brb", To: "be right back"},
			{On: true, From: "/(\\d+)\\s*min\\b/", To: "$1 minutes"},
			{On: false, From: "hello", To: "NOPE"},
			{On: true, From: "c++", To: "C plus plus"},
			{On: true, From: "", To: "never"},
			{On: true, From: "/(/", To: "broken"},
			{On: true, From: "good  night", To: "gn"},
		},
		Censor: []string{"darn", "/fr[ei]ck/"},
		Mask:   "*",
	}
	cases := []struct{ in, want string }{
		{"brb", "be right back"},
		{"Brb, back in 5 min", "Be right back, back in 5 minutes"},
		{"BRB", "BE RIGHT BACK"},
		{"brbx and xbrb stay", "brbx and xbrb stay"},
		{"hello there", "hello there"},
		{"I like c++ a lot", "I like C plus plus a lot"},
		{"good night all", "gn all"},
		{"darn it, DARN!", "**** it, ****!"},
		{"what the freck", "what the *****"},
		{"darnit is fine", "darnit is fine"},
	}
	for _, tc := range cases {
		if got := o.apply(tc.in, false); got != tc.want {
			t.Errorf("apply(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := o.apply("oh darn it", true); got != "oh it" {
		t.Errorf("speech leaves censored words out: got %q", got)
	}
	// A replacement cannot bring back a censored word.
	o.Replace = append(o.Replace, WordReplacement{On: true, From: "dang", To: "darn"})
	if got := o.apply("dang", false); got != "****" {
		t.Errorf("replacement then censor = %q", got)
	}
	o.Mask = "•"
	if got := o.apply("darn", false); got != "••••" {
		t.Errorf("custom mask = %q", got)
	}
}

func TestFilterTextSurfaces(t *testing.T) {
	c := DefaultConfig()
	c.TextFilter.Censor = []string{"darn"}
	c.TextFilter.Dictated = false
	useTestConfig(t, c)
	if got := FilterText(SurfaceTyped, "darn"); got != "****" {
		t.Errorf("typed = %q", got)
	}
	if got := FilterText(SurfaceDictated, "darn"); got != "darn" {
		t.Errorf("dictated is off but got %q", got)
	}
	if got := FilterText(SurfaceSpeech, "oh darn"); got != "oh" {
		t.Errorf("speech = %q", got)
	}
	if got := FilterText("other", "darn"); got != "darn" {
		t.Errorf("unknown surface = %q", got)
	}
}

func TestTextFilterErrors(t *testing.T) {
	o := TextFilterOptions{
		Replace: []WordReplacement{{From: "fine"}, {From: "/(/"}},
		Censor:  []string{"/ok/", "/[/"},
	}
	errs := TextFilterErrors(o)
	if len(errs) != 2 || errs["replace.1"] == "" || errs["censor.1"] == "" {
		t.Fatalf("errors = %v", errs)
	}
}
