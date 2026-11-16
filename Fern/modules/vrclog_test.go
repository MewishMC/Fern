package modules

import (
	"strings"
	"testing"
	"time"
)

// resetLogState clears everything parseLogLine touches.
func resetLogState(t *testing.T) {
	t.Helper()
	reset := func() {
		logMu.Lock()
		joined, left, activity, order = map[string]LogEvent{}, map[string]LogEvent{}, nil, nil
		selfName, world, inWorld, roomName = "", WorldInfo{}, false, ""
		session = SessionStats{}
		metPlayers, seenPlayers = map[string]bool{}, map[string]bool{}
		logMu.Unlock()
	}
	reset()
	oldNow, oldNet := nowFunc, netTotalFunc
	t.Cleanup(func() { reset(); nowFunc, netTotalFunc = oldNow, oldNet })
}

func feed(lines ...string) {
	logMu.Lock()
	defer logMu.Unlock()
	for _, l := range lines {
		parseLogLine(l)
	}
}

// Lines in the format VRChat writes them (2024-2025 builds; the user ids in
// brackets were added in 2024, older logs have the name only).
const sampleLog = `2025.03.01 20:00:01 Debug      -  [Behaviour] User Authenticated: Fern Tester (usr_11111111-2222-3333-4444-555555555555)
2025.03.01 20:00:10 Debug      -  [Behaviour] Entering Room: The Black Cat
2025.03.01 20:00:10 Debug      -  [Behaviour] Joining wrld_4cf554b4-430c-4f8f-b53e-1f294eed230b:12345~hidden(usr_aaaaaaaa-0000-0000-0000-000000000000)~region(eu)
2025.03.01 20:00:10 Debug      -  [Behaviour] Joining or Creating Room: The Black Cat
2025.03.01 20:00:24 Debug      -  [Behaviour] OnPlayerJoined Fern Tester (usr_11111111-2222-3333-4444-555555555555)
2025.03.01 20:00:24 Debug      -  [Behaviour] OnPlayerJoined Natsumi-sama (usr_bbbbbbbb-0000-0000-0000-000000000000)
2025.03.01 20:00:25 Debug      -  [Behaviour] OnPlayerJoined Some (Body) (usr_cccccccc-0000-0000-0000-000000000000)
2025.03.01 20:00:25 Debug      -  [Behaviour] OnPlayerJoinComplete Some (Body)
2025.03.01 20:05:00 Debug      -  [VRC Camera] Took screenshot to: C:\Users\x\Pictures\VRChat\2025-03\VRChat_2025-03-01_20-05-00.104_1920x1080.png
2025.03.01 20:06:00 Debug      -  [Behaviour] OnPlayerLeftRoom
2025.03.01 20:06:00 Debug      -  [Behaviour] OnPlayerLeft Natsumi-sama (usr_bbbbbbbb-0000-0000-0000-000000000000)`

func TestParseVRChatSession(t *testing.T) {
	resetLogState(t)
	nowFunc = func() time.Time { return time.Date(2025, 3, 1, 20, 6, 30, 0, time.Local) }
	feed(strings.Split(sampleLog, "\n")...)

	w, ok := CurrentWorld()
	if !ok {
		t.Fatal("not in a world")
	}
	if w.Name != "The Black Cat" || w.ID != "wrld_4cf554b4-430c-4f8f-b53e-1f294eed230b" || w.Type != "Friends+" || w.Region != "EU" {
		t.Errorf("world = %+v", w)
	}
	if w.Players != 2 || w.Peak != 3 || w.Loading {
		t.Errorf("players %d, peak %d, loading %v", w.Players, w.Peak, w.Loading)
	}
	if got := JoinLogs(); len(got) != 2 || got[0].Text != "Some (Body)" {
		t.Errorf("joined = %+v", got)
	}
	if got := LeaveLogs(); len(got) != 1 || got[0].Text != "Natsumi-sama" {
		t.Errorf("left = %+v (OnPlayerLeftRoom must not count)", got)
	}
	s := Session()
	if s.Worlds != 1 || s.Players != 2 || s.Peak != 3 {
		t.Errorf("session = %+v (yourself is not a player met)", s)
	}

	var kinds []string
	for _, ev := range ActivityFeed() {
		kinds = append(kinds, ev.Kind+":"+ev.Text)
	}
	want := []string{
		"loaded:Loaded The Black Cat in 14s",
		"join:Fern Tester joined",
		"join:Natsumi-sama joined",
		"join:Some (Body) joined",
		"screenshot:Took a screenshot",
		"leave:Natsumi-sama left",
	}
	if strings.Join(kinds, "|") != strings.Join(want, "|") {
		t.Errorf("activity:\n got %q\nwant %q", kinds, want)
	}
	// The event time comes from the log line, not from when Fern read it.
	if got := ActivityFeed()[4].Time; got != time.Date(2025, 3, 1, 20, 5, 0, 0, time.Local).UnixMilli() {
		t.Errorf("screenshot time = %d", got)
	}
}

func TestSeenAgainAcrossInstances(t *testing.T) {
	resetLogState(t)
	feed(
		"2025.03.01 20:00:00 Debug      -  [Behaviour] User Authenticated: Me (usr_1)",
		"2025.03.01 20:00:01 Debug      -  [Behaviour] Joining wrld_a:1~region(us)",
		"2025.03.01 20:00:02 Debug      -  [Behaviour] OnPlayerJoined Me (usr_1)",
		"2025.03.01 20:00:03 Debug      -  [Behaviour] OnPlayerJoined Friend (usr_2)",
		"2025.03.01 20:10:00 Debug      -  [Behaviour] OnPlayerLeft Friend (usr_2)",
		"2025.03.01 20:10:01 Debug      -  [Behaviour] OnLeftRoom",
		"2025.03.01 20:10:05 Debug      -  [Behaviour] Joining wrld_b:2~private(usr_9)~canRequestInvite~region(jp)",
		"2025.03.01 20:10:20 Debug      -  [Behaviour] Entering Room: Second World",
		"2025.03.01 20:10:21 Debug      -  [Behaviour] OnPlayerJoined Me (usr_1)",
		"2025.03.01 20:10:22 Debug      -  [Behaviour] OnPlayerJoined Friend (usr_2)",
		"2025.03.01 20:10:23 Debug      -  [Behaviour] OnPlayerJoined Stranger (usr_3)",
	)
	w, _ := CurrentWorld()
	if w.Name != "Second World" || w.Type != "Invite+" || w.Region != "JP" || w.Players != 3 {
		t.Errorf("world = %+v", w)
	}
	again := map[string]bool{}
	for _, ev := range JoinLogs() {
		again[ev.Text] = ev.Again
	}
	if !again["Friend"] || again["Stranger"] || again["Me"] {
		t.Errorf("seen again = %v", again)
	}
	if s := Session(); s.Worlds != 2 || s.Players != 2 || s.Peak != 3 {
		t.Errorf("session = %+v", s)
	}
}

func TestOldLogFormatWithoutUserIDs(t *testing.T) {
	resetLogState(t)
	feed(
		"2021.12.12 11:47:22 Log        -  [Behaviour] OnPlayerJoined Natsumi-sama",
		"2021.12.12 11:47:23 Log        -  [Behaviour] OnPlayerJoined: something else",
		"2021.12.12 11:53:14 Log        -  [Behaviour] OnPlayerLeftRoom",
	)
	if got := JoinLogs(); len(got) != 1 || got[0].Text != "Natsumi-sama" {
		t.Errorf("joined = %+v", got)
	}
	if got := LeaveLogs(); len(got) != 0 {
		t.Errorf("left = %+v", got)
	}
}

func TestParseInstance(t *testing.T) {
	cases := []struct{ loc, id, access, region string }{
		{"wrld_x:12345", "wrld_x", "Public", "US West"},
		{"wrld_x:12345~region(use)", "wrld_x", "Public", "US East"},
		{"wrld_x:1~hidden(usr_a)~region(eu)", "wrld_x", "Friends+", "EU"},
		{"wrld_x:1~friends(usr_a)~region(jp)", "wrld_x", "Friends", "JP"},
		{"wrld_x:1~private(usr_a)~region(eu)", "wrld_x", "Invite", "EU"},
		{"wrld_x:1~private(usr_a)~canRequestInvite~region(us)", "wrld_x", "Invite+", "US West"},
		{"wrld_x:1~group(grp_a)~groupAccessType(public)~region(eu)", "wrld_x", "Group Public", "EU"},
		{"wrld_x:1~group(grp_a)~groupAccessType(plus)", "wrld_x", "Group+", "US West"},
		{"wrld_x:1~group(grp_a)~groupAccessType(members)~region(jp)", "wrld_x", "Group", "JP"},
	}
	for _, c := range cases {
		id, access, region := parseInstance(c.loc)
		if id != c.id || access != c.access || region != c.region {
			t.Errorf("%s: got %s %s %s", c.loc, id, access, region)
		}
	}
}

func TestLoadSizeMeasuredLive(t *testing.T) {
	resetLogState(t)
	start := time.Date(2025, 3, 1, 21, 0, 0, 0, time.Local)
	now := start
	nowFunc = func() time.Time { return now }
	total := uint64(5_000_000)
	netTotalFunc = func() (uint64, bool) { return total, true }
	feed("2025.03.01 21:00:00 Debug      -  [Behaviour] Joining wrld_a:1~region(eu)")
	now, total = start.Add(9*time.Second), 230_000_000
	feed("2025.03.01 21:00:00 Debug      -  [Behaviour] Entering Room: Big World",
		"2025.03.01 21:00:09 Debug      -  [Behaviour] OnPlayerJoined Me (usr_1)")
	feed2 := ActivityFeed()
	if len(feed2) == 0 || feed2[0].Text != "Loaded Big World in 9s · 225 MB" {
		t.Errorf("activity = %+v", feed2)
	}
}

func TestLatestLogEvent(t *testing.T) {
	now := time.Unix(10_000, 0)
	events := []LogEvent{
		{Time: now.Add(-50 * time.Second).UnixMilli(), Text: "Loaded X in 3s", Kind: EventLoaded},
		{Time: now.Add(-40 * time.Second).UnixMilli(), Text: "Amy joined", Kind: EventJoin, Again: true},
		{Time: now.Add(-5 * time.Second).UnixMilli(), Text: "Took a screenshot", Kind: EventScreenshot},
	}
	o := LogsOptions{ShowJoin: true, ShowLeave: true, ShowScreenshot: true, ShowLoaded: true, MarkSeenAgain: true}
	if got := latestLogEvent(o, events, now); got != "Took a screenshot" {
		t.Errorf("latest = %q", got)
	}
	o.ShowScreenshot = false
	if got := latestLogEvent(o, events, now); got != "Amy joined again" {
		t.Errorf("without screenshots = %q", got)
	}
	o.HideAfter = 30
	if got := latestLogEvent(o, events, now); got != "" {
		t.Errorf("expired event shown: %q", got)
	}
	o.HideAfter, o.MarkSeenAgain, o.Label = 0, false, "vrc"
	if got := latestLogEvent(o, events, now); got != "vrc Amy joined" {
		t.Errorf("unmarked = %q", got)
	}
}

func TestFormatWorld(t *testing.T) {
	now := time.Unix(100_000, 0)
	w := WorldInfo{Name: "The Black Cat", Type: "Friends+", Region: "EU", Players: 12, Peak: 15, JoinedAt: now.Add(-75 * time.Minute).UnixMilli()}
	s := SessionStats{Worlds: 3, Players: 25}
	o := WorldOptions{ShowName: true, ShowCount: true, ShowType: true, ShowRegion: true}
	if got, want := formatWorld(o, w, s, now), "The Black Cat · 12 players · Friends+ EU"; got != want {
		t.Errorf("default = %q, want %q", got, want)
	}
	o = WorldOptions{Label: "🌎", ShowName: true, MaxChars: 9, ShowCount: true, ShowPeak: true, ShowTime: true, ShowStats: true}
	if got, want := formatWorld(o, w, s, now), "🌎 The Black · 12 players (peak 15) · 1h 15m · 3 worlds · 25 met"; got != want {
		t.Errorf("full = %q, want %q", got, want)
	}
	w.Players, w.Name = 1, ""
	if got, want := formatWorld(WorldOptions{ShowName: true, ShowCount: true}, w, s, now), "Loading world · 1 player"; got != want {
		t.Errorf("loading = %q, want %q", got, want)
	}
}
