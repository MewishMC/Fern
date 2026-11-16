package modules

import (
	"bytes"
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// The session archive keeps more than the panel's short lists, so the whole
// session can be saved as a file: messages sent from the Live page, what the
// chatbox showed, and VRChat's joins, leaves, screenshots and world loads.
// It lives in memory only and starts empty with every launch.

// sessionArchiveLimit bounds each list; a long session drops its oldest.
const sessionArchiveLimit = 5000

// What can be exported.
const (
	ExportMessages = "messages" // sent from the Live page
	ExportChatbox  = "chatbox"  // every chatbox message that went out
	ExportActivity = "activity" // VRChat joins, leaves, screenshots and worlds
	ExportAll      = "all"
)

// Export file formats.
const (
	ExportTXT = "txt"
	ExportCSV = "csv"
)

// Kinds of ExportEntry besides the VRChat event kinds.
const (
	exportKindMessage = "message"
	exportKindChatbox = "chatbox"
)

var (
	archiveMu       sync.Mutex
	sentArchive     []LogEvent
	chatboxArchive  []LogEvent
	activityArchive []LogEvent
)

func archiveAppend(list *[]LogEvent, ev LogEvent) {
	archiveMu.Lock()
	defer archiveMu.Unlock()
	*list = append(*list, ev)
	if over := len(*list) - sessionArchiveLimit; over > 0 {
		*list = append((*list)[:0], (*list)[over:]...)
	}
}

// noteSentMessage archives a message sent from the Live page.
func noteSentMessage(text string, at time.Time) {
	archiveAppend(&sentArchive, LogEvent{Time: at.UnixMilli(), Text: text, Kind: exportKindMessage})
}

// clearSentArchive forgets the Live page messages, with the history.
func clearSentArchive() {
	archiveMu.Lock()
	sentArchive = nil
	archiveMu.Unlock()
}

func archiveChatbox(ev LogEvent) {
	ev.Kind = exportKindChatbox
	archiveAppend(&chatboxArchive, ev)
}

func archiveActivity(ev LogEvent) { archiveAppend(&activityArchive, ev) }

// ExportEntry is one line of an export.
type ExportEntry struct {
	Time int64  `json:"time"` // unix ms
	Kind string `json:"kind"` // message, chatbox, join, leave, screenshot or loaded
	Text string `json:"text"`
}

// SessionExport lists what to export, oldest first. Entries at the same time
// keep the order they happened in.
func SessionExport(what string) ([]ExportEntry, error) {
	archiveMu.Lock()
	var lists [][]LogEvent
	switch what {
	case ExportMessages:
		lists = [][]LogEvent{sentArchive}
	case ExportChatbox:
		lists = [][]LogEvent{chatboxArchive}
	case ExportActivity:
		lists = [][]LogEvent{activityArchive}
	case ExportAll:
		lists = [][]LogEvent{sentArchive, chatboxArchive, activityArchive}
	default:
		archiveMu.Unlock()
		return nil, fmt.Errorf("unknown export %q", what)
	}
	var out []ExportEntry
	for _, list := range lists {
		for _, ev := range list {
			text := ev.Text
			if ev.Again {
				text += " (seen again)"
			}
			out = append(out, ExportEntry{Time: ev.Time, Kind: ev.Kind, Text: text})
		}
	}
	archiveMu.Unlock()
	slices.SortStableFunc(out, func(a, b ExportEntry) int { return cmp.Compare(a.Time, b.Time) })
	return out, nil
}

// exportTitles head a text export.
var exportTitles = map[string]string{
	ExportMessages: "Messages sent from Fern",
	ExportChatbox:  "Chatbox messages sent by Fern",
	ExportActivity: "VRChat activity",
	ExportAll:      "Fern session",
}

// exportKindLabels name the kinds in a text export of everything.
var exportKindLabels = map[string]string{
	exportKindMessage: "sent",
	exportKindChatbox: "chatbox",
	EventJoin:         "joined",
	EventLeave:        "left",
	EventScreenshot:   "photo",
	EventLoaded:       "world",
}

// RenderExport turns entries into a file. loc is the time zone the times are
// written in; now is when the export was made.
func RenderExport(what, format string, entries []ExportEntry, now time.Time, loc *time.Location) ([]byte, error) {
	if loc == nil {
		loc = time.Local
	}
	switch format {
	case ExportTXT:
		return renderExportTXT(what, entries, now, loc), nil
	case ExportCSV:
		return renderExportCSV(entries, loc)
	}
	return nil, fmt.Errorf("unknown format %q", format)
}

func renderExportTXT(what string, entries []ExportEntry, now time.Time, loc *time.Location) []byte {
	var b bytes.Buffer
	title := exportTitles[what]
	if title == "" {
		title = "Fern export"
	}
	fmt.Fprintf(&b, "%s, saved %s\r\n", title, now.In(loc).Format("2 Jan 2006 15:04"))
	if len(entries) == 0 {
		b.WriteString("\r\nNothing yet this session.\r\n")
		return b.Bytes()
	}
	day := ""
	for _, e := range entries {
		at := time.UnixMilli(e.Time).In(loc)
		if d := at.Format("Monday 2 January 2006"); d != day {
			day = d
			fmt.Fprintf(&b, "\r\n%s\r\n", d)
		}
		text := strings.ReplaceAll(e.Text, "\n", " ")
		if what == ExportAll {
			label := exportKindLabels[e.Kind]
			if label == "" {
				label = e.Kind
			}
			fmt.Fprintf(&b, "[%s] %-7s %s\r\n", at.Format("15:04:05"), label, text)
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\r\n", at.Format("15:04:05"), text)
	}
	return b.Bytes()
}

const utf8BOM = "\xef\xbb\xbf"

func renderExportCSV(entries []ExportEntry, loc *time.Location) ([]byte, error) {
	var b bytes.Buffer
	// A byte order mark, so Excel opens the file as UTF-8 and emoji survive.
	b.WriteString(utf8BOM)
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	_ = w.Write([]string{"time", "kind", "text"})
	for _, e := range entries {
		at := time.UnixMilli(e.Time).In(loc).Format("2006-01-02 15:04:05")
		_ = w.Write([]string{at, e.Kind, csvSafe(e.Text)})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// csvSafe keeps a spreadsheet from running a message as a formula: a chatbox
// message is whatever someone typed, and "=HYPERLINK(...)" in a cell is live.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ExportFileName is the file name an export is offered under.
func ExportFileName(what, format string, now time.Time) string {
	return "fern-" + what + "-" + now.Format("2006-01-02-1504") + "." + format
}

// ExportSession renders one export. It is the whole job of the panel's
// download link.
func ExportSession(what, format string, now time.Time) (data []byte, name string, err error) {
	if format != ExportTXT && format != ExportCSV {
		return nil, "", errors.New("unknown format")
	}
	entries, err := SessionExport(what)
	if err != nil {
		return nil, "", err
	}
	data, err = RenderExport(what, format, entries, now, time.Local)
	if err != nil {
		return nil, "", err
	}
	return data, ExportFileName(what, format, now), nil
}
