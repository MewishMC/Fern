package modules

import (
	"testing"
	"time"
)

func TestScheduleSanitize(t *testing.T) {
	o := ScheduleOptions{Messages: []ScheduledMessage{
		{ID: "x", Text: " hello\nthere ", Kind: "weird", At: "7:00", EveryMin: 0, Days: []string{"sun", "mon", "xyz", "mon"}, ShowSec: 1},
		{ID: "x", Kind: ScheduleEvery, EveryMin: 5000, ShowSec: 999999},
	}}
	for i := 0; i < 25; i++ {
		o.Messages = append(o.Messages, ScheduledMessage{})
	}
	o.sanitize()
	if len(o.Messages) != MaxScheduled || o.Separator != " · " {
		t.Fatalf("len %d sep %q", len(o.Messages), o.Separator)
	}
	a, b := o.Messages[0], o.Messages[1]
	if a.Text != "hello there" || a.Kind != ScheduleAt || a.At != "00:00" || a.EveryMin != 30 || a.ShowSec != 3 {
		t.Errorf("first %+v", a)
	}
	if len(a.Days) != 2 || a.Days[0] != "mon" || a.Days[1] != "sun" {
		t.Errorf("days %v", a.Days)
	}
	if b.ID == a.ID || b.EveryMin != 1440 || b.ShowSec != 3600 {
		t.Errorf("second %+v", b)
	}
}

func TestScheduleSlot(t *testing.T) {
	mon := time.Date(2026, 11, 16, 9, 30, 20, 0, time.Local) // a Monday
	at := ScheduledMessage{Kind: ScheduleAt, At: "09:30"}
	if scheduleSlot(at, mon) != "2026-11-16T09:30" || scheduleSlot(at, mon.Add(time.Minute)) != "" {
		t.Error("at 09:30 every day")
	}
	at.Days = []string{"tue"}
	if scheduleSlot(at, mon) != "" || scheduleSlot(at, mon.AddDate(0, 0, 1)) == "" {
		t.Error("at 09:30 on Tuesdays")
	}

	every := ScheduledMessage{Kind: ScheduleEvery, EveryMin: 20, From: "09:10", To: "10:00"}
	due := func(h, m int) bool {
		return scheduleSlot(every, time.Date(2026, 11, 16, h, m, 5, 0, time.Local)) != ""
	}
	if !due(9, 10) || !due(9, 30) || !due(9, 50) || due(9, 20) || due(10, 10) || due(8, 50) {
		t.Error("every 20 minutes from 09:10 until 10:00")
	}
	every.From, every.To, every.EveryMin = "00:00", "00:00", 45 // all day, from midnight
	if !due(0, 0) || !due(0, 45) || !due(1, 30) || due(1, 0) {
		t.Error("every 45 minutes all day")
	}
	every.From, every.To, every.EveryMin = "23:00", "01:00", 30 // past midnight
	if !due(23, 30) || !due(0, 0) || !due(0, 30) || due(1, 0) || due(22, 30) {
		t.Error("window past midnight")
	}
}

func TestScheduleTickShowsOncePerSlot(t *testing.T) {
	c := DefaultConfig()
	c.DisplayOptions.Schedule = true
	c.ScheduleOptions.Messages = []ScheduledMessage{
		{ID: "a", Enabled: true, Text: "Stretch at {time}!", Kind: ScheduleAt, At: "10:00", ShowSec: 60},
		{ID: "b", Enabled: true, Text: "Water", Kind: ScheduleEvery, EveryMin: 1, ShowSec: 30},
		{ID: "c", Enabled: false, Text: "Off", Kind: ScheduleEvery, EveryMin: 1},
		{ID: "d", Enabled: true, Text: "Big news", Kind: ScheduleAt, At: "10:00", ShowSec: 10, Takeover: true},
	}
	c.sanitize()
	useConfig(t, c)
	var held []string
	oldHold := scheduleHold
	scheduleHold = func(text string, d time.Duration) { held = append(held, text+" "+d.String()) }
	reset := func() {
		scheduleMu.Lock()
		scheduleLast, scheduleSent, scheduleShown = map[string]string{}, map[string]time.Time{}, nil
		scheduleMu.Unlock()
	}
	reset()
	t.Cleanup(func() { scheduleHold, scheduleNow = oldHold, time.Now; reset() })

	o := c.ScheduleOptions
	now := timerT0 // 10:00:00
	scheduleTick(o, now)
	scheduleTick(o, now.Add(20*time.Second)) // same minute: nothing new
	if got := scheduleText(o, now.Add(25*time.Second)); got != "Water · Stretch at 10:00!" {
		t.Errorf("line %q", got)
	}
	if len(held) != 1 || held[0] != "Big news 10s" {
		t.Errorf("takeover %v", held)
	}
	now = now.Add(time.Minute)
	scheduleTick(o, now)
	if got := scheduleText(o, now.Add(10*time.Second)); got != "Water" {
		t.Errorf("a minute later, water again and the stretch gone: %q", got)
	}
	clock := now.Add(10 * time.Second)
	scheduleNow = func() time.Time { return clock }
	st := CurrentSchedule(o)
	if st[0].LastMS != timerT0.UnixMilli() || st[0].Showing || !st[1].Showing || st[2].LastMS != 0 {
		t.Errorf("status %+v", st)
	}
	if err := ShowScheduledNow("c"); err != nil || scheduleText(o, clock) != "Off · Water" {
		t.Errorf("show now: %v, line %q", err, scheduleText(o, clock))
	}
	if err := ShowScheduledNow("nope"); err == nil {
		t.Error("unknown message accepted")
	}
}
