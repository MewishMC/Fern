package modules

import (
	"testing"
	"time"
)

func TestCalendarDiff(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		a, b             string
		y, mo, d, h, min int
	}{
		{"2024-02-14 00:00", "2025-04-20 13:30", 1, 2, 6, 13, 30},
		{"2024-01-31 00:00", "2024-03-01 00:00", 0, 1, 1, 0, 0},
		{"2024-03-10 22:00", "2024-03-11 01:15", 0, 0, 0, 3, 15},
		{"2020-02-29 00:00", "2021-02-28 00:00", 1, 0, 0, 0, 0},   // Feb 29 has its anniversary on the 28th
		{"2025-04-20 13:30", "2024-02-14 00:00", 1, 2, 6, 13, 30}, // either way round
	}
	for _, c := range cases {
		y, mo, d, h, mi := calendarDiff(at(c.a), at(c.b))
		if y != c.y || mo != c.mo || d != c.d || h != c.h || mi != c.min {
			t.Errorf("%s → %s: got %dy %dm %dd %dh %dmin", c.a, c.b, y, mo, d, h, mi)
		}
	}
}

func TestFormatSpan(t *testing.T) {
	a := time.Date(2024, 2, 14, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		b     time.Time
		units int
		want  string
	}{
		{time.Date(2025, 4, 20, 13, 30, 0, 0, time.UTC), 2, "1y 2m"},
		{time.Date(2025, 4, 20, 13, 30, 0, 0, time.UTC), 4, "1y 2m 6d 13h"},
		{time.Date(2025, 2, 17, 0, 0, 0, 0, time.UTC), 2, "1y"}, // zero months are left out
		{time.Date(2024, 2, 17, 5, 0, 0, 0, time.UTC), 2, "3d 5h"},
		{time.Date(2024, 2, 14, 0, 12, 0, 0, time.UTC), 2, "12min"},
		{a, 2, "0min"},
		{time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC), 1, "1m"}, // a date in the future counts down
	}
	for _, c := range cases {
		if got := formatSpan(a, c.b, c.units); got != c.want {
			t.Errorf("%v (%d units): got %q, want %q", c.b, c.units, got, c.want)
		}
	}
}

func TestFormatClockLine(t *testing.T) {
	now := time.Date(2026, 1, 15, 14, 5, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.TimeOptions.Zone = "Europe/Berlin"
	if got := formatClockLine(cfg, now); got != "3:05 PM" {
		t.Errorf("zone: got %q", got)
	}
	cfg.TimeOptions.ShowZone = true
	cfg.TimeOptions.Use24h = true
	cfg.TimeOptions.Zone2 = "America/New_York"
	if got := formatClockLine(cfg, now); got != "15:05 CET · 09:05 EST" {
		t.Errorf("two clocks: got %q", got)
	}
	cfg.TimeOptions.Label2 = "NY"
	cfg.TimeOptions.Counters = []TimeCounter{
		{Label: "together", Date: "2024-11-01"},
		{Label: "half typed", Date: "2025-1"},
		{Label: "trip in", Date: "2026-01-20T09:00"},
	}
	if got := formatClockLine(cfg, now); got != "15:05 CET · NY 09:05 · together 1y 2m · trip in 4d 17h" {
		t.Errorf("counters: got %q", got)
	}
	cfg.TimeOptions.CounterLine = true
	cfg.TimeOptions.Counters = cfg.TimeOptions.Counters[:1]
	if got := formatClockLine(cfg, now); got != "15:05 CET · NY 09:05\ntogether 1y 2m" {
		t.Errorf("counter line: got %q", got)
	}
}

func TestSanitizeTime(t *testing.T) {
	c := DefaultConfig()
	c.TimeOptions.Zone = " Asia/Tokyo "
	c.TimeOptions.Zone2 = "Mars/Olympus_Mons"
	c.TimeOptions.CounterUnits = 9
	for i := 0; i < 12; i++ {
		c.TimeOptions.Counters = append(c.TimeOptions.Counters, TimeCounter{Label: "  a  b ", Date: "2024-0"})
	}
	c.sanitize()
	o := c.TimeOptions
	if o.Zone != "Asia/Tokyo" || o.Zone2 != "" || o.CounterUnits != 4 {
		t.Errorf("got zone %q, zone2 %q, units %d", o.Zone, o.Zone2, o.CounterUnits)
	}
	if len(o.Counters) != maxTimeCounter || o.Counters[0] != (TimeCounter{Label: "a b", Date: "2024-0"}) {
		t.Errorf("counters: %d, first %+v", len(o.Counters), o.Counters[0])
	}
}
