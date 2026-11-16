package modules

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"strings"
	"testing"
	"time"
)

// useEmptyArchive starts a test with an empty session archive.
func useEmptyArchive(t *testing.T) {
	t.Helper()
	archiveMu.Lock()
	saved := [3][]LogEvent{sentArchive, chatboxArchive, activityArchive}
	sentArchive, chatboxArchive, activityArchive = nil, nil, nil
	archiveMu.Unlock()
	t.Cleanup(func() {
		archiveMu.Lock()
		sentArchive, chatboxArchive, activityArchive = saved[0], saved[1], saved[2]
		archiveMu.Unlock()
	})
}

func TestSessionExportMergesInOrder(t *testing.T) {
	useEmptyArchive(t)
	base := time.Date(2026, 3, 1, 20, 0, 0, 0, time.UTC)
	noteSentMessage("hello", base.Add(2*time.Second))
	archiveChatbox(LogEvent{Time: base.Add(3 * time.Second).UnixMilli(), Text: "hello"})
	archiveActivity(LogEvent{Time: base.UnixMilli(), Text: "Loaded The Black Cat in 12s", Kind: EventLoaded})
	archiveActivity(LogEvent{Time: base.Add(time.Second).UnixMilli(), Text: "Kit joined", Kind: EventJoin, Again: true})

	all, err := SessionExport(ExportAll)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range all {
		kinds = append(kinds, e.Kind)
	}
	if want := []string{EventLoaded, EventJoin, exportKindMessage, exportKindChatbox}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds %q, want %q", kinds, want)
	}
	if all[1].Text != "Kit joined (seen again)" {
		t.Errorf("join text %q", all[1].Text)
	}

	msgs, _ := SessionExport(ExportMessages)
	if len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Errorf("messages %+v", msgs)
	}
	if _, err := SessionExport("passwords"); err == nil {
		t.Error("an unknown export should fail")
	}
}

func TestSessionArchiveIsBounded(t *testing.T) {
	useEmptyArchive(t)
	for i := 0; i < sessionArchiveLimit+25; i++ {
		archiveActivity(LogEvent{Time: int64(i), Text: "x"})
	}
	got, _ := SessionExport(ExportActivity)
	if len(got) != sessionArchiveLimit || got[0].Time != 25 {
		t.Errorf("kept %d entries starting at %d", len(got), got[0].Time)
	}
}

func TestRenderExportTXT(t *testing.T) {
	loc := time.FixedZone("test", 0)
	at := time.Date(2026, 3, 1, 23, 59, 30, 0, loc)
	entries := []ExportEntry{
		{Time: at.UnixMilli(), Kind: exportKindMessage, Text: "good night"},
		{Time: at.Add(time.Minute).UnixMilli(), Kind: EventLeave, Text: "Kit left"},
	}
	out, err := RenderExport(ExportAll, ExportTXT, entries, at.Add(time.Hour), loc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"Fern session, saved 2 Mar 2026 00:59\r\n",
		"\r\nSunday 1 March 2026\r\n[23:59:30] sent    good night\r\n",
		"\r\nMonday 2 March 2026\r\n[00:00:30] left    Kit left\r\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}

	out, _ = RenderExport(ExportMessages, ExportTXT, entries[:1], at, loc)
	if !strings.Contains(string(out), "[23:59:30] good night\r\n") {
		t.Errorf("single list should leave out the kind:\n%s", out)
	}
	out, _ = RenderExport(ExportActivity, ExportTXT, nil, at, loc)
	if !strings.Contains(string(out), "Nothing yet") {
		t.Errorf("empty export:\n%s", out)
	}
}

func TestRenderExportCSV(t *testing.T) {
	loc := time.FixedZone("test", 3600)
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, loc)
	entries := []ExportEntry{
		{Time: at.UnixMilli(), Kind: exportKindMessage, Text: `she said "hi", then ✨`},
		{Time: at.UnixMilli(), Kind: exportKindMessage, Text: "=HYPERLINK(\"x\")"},
		{Time: at.UnixMilli(), Kind: exportKindChatbox, Text: "two\nlines"},
	}
	out, err := RenderExport(ExportAll, ExportCSV, entries, at, loc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte(utf8BOM)) {
		t.Error("csv should start with a byte order mark")
	}
	rows, err := csv.NewReader(bytes.NewReader(out[len(utf8BOM):])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"time", "kind", "text"},
		{"2026-03-01 12:00:00", "message", `she said "hi", then ✨`},
		{"2026-03-01 12:00:00", "message", `'=HYPERLINK("x")`},
		{"2026-03-01 12:00:00", "chatbox", "two\nlines"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("got %q\nwant %q", rows, want)
	}
	if _, err := RenderExport(ExportAll, "xlsx", entries, at, loc); err == nil {
		t.Error("an unknown format should fail")
	}
}

func TestExportSession(t *testing.T) {
	useEmptyArchive(t)
	now := time.Date(2026, 3, 1, 9, 5, 0, 0, time.Local)
	data, name, err := ExportSession(ExportChatbox, ExportCSV, now)
	if err != nil {
		t.Fatal(err)
	}
	if name != "fern-chatbox-2026-03-01-0905.csv" {
		t.Errorf("file name %q", name)
	}
	if !strings.Contains(string(data), "time,kind,text") {
		t.Errorf("csv without a header: %q", data)
	}
	if _, _, err := ExportSession(ExportChatbox, "../evil", now); err == nil {
		t.Error("an unknown format should fail")
	}
	if _, _, err := ExportSession("../evil", ExportTXT, now); err == nil {
		t.Error("an unknown export should fail")
	}
}

func TestSentMessagesFollowTheHistorySwitch(t *testing.T) {
	useEmptyArchive(t)
	useTestConfig(t, DefaultConfig())
	AddChatHistory("first")
	AddChatHistory("first")
	if got, _ := SessionExport(ExportMessages); len(got) != 2 {
		t.Errorf("each send should be archived, got %+v", got)
	}
	ClearChatHistory()
	if got, _ := SessionExport(ExportMessages); len(got) != 0 {
		t.Errorf("clearing the history should clear the archive, got %+v", got)
	}
	off := DefaultConfig()
	off.ChatOptions.KeepHistory = false
	SetConfig(off)
	AddChatHistory("secret")
	if got, _ := SessionExport(ExportMessages); len(got) != 0 {
		t.Errorf("archived %+v with history off", got)
	}
}

func TestActivityFeedFillsTheArchive(t *testing.T) {
	useEmptyArchive(t)
	logMu.Lock()
	saved := activity
	addActivityAt(time.UnixMilli(5), EventScreenshot, "Took a screenshot")
	activity = saved
	logMu.Unlock()
	got, _ := SessionExport(ExportActivity)
	if len(got) != 1 || got[0].Kind != EventScreenshot {
		t.Errorf("archive %+v", got)
	}
}
