package modules

// Running a custom line's command or fetching its URL. Each line runs on its
// own goroutine, at most one run at a time, and only while the line is on
// (or when the panel asks for a test run).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

const (
	customMaxOutput = 64 << 10  // bytes read from a command's output
	customMaxBody   = 512 << 10 // bytes read from a URL
	customSettle    = time.Second
)

// customKey identifies the settings a polled value came from, so changing
// the command or URL drops the old value at once.
func customKey(l CustomLine) string {
	switch l.Source {
	case CustomSourceCommand:
		return "command\x00" + l.Command
	case CustomSourceURL:
		return "url\x00" + l.URL + "\x00" + l.JSONPath
	}
	return ""
}

var (
	customWake      = make(chan struct{}, 1)
	customLinesOnce sync.Once
	// customRun does one run of a line; tests swap it.
	customRun = runCustomSource
)

func startCustomLinesWorker() {
	customLinesOnce.Do(func() {
		go func() {
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			for {
				customTick(GetConfig())
				select {
				case <-tick.C:
				case <-customWake:
				}
			}
		}()
	})
}

// RefreshCustomLine runs a command or URL line now, even while it is off,
// so its settings can be tried from the panel.
func RefreshCustomLine(id string) error {
	i := customSlot(id)
	if i < 0 {
		return errors.New("unknown custom line")
	}
	l := GetConfig().CustomLines.Line(i)
	switch {
	case l.Source == CustomSourceCommand && l.Command == "":
		return errors.New("enter a command first")
	case l.Source == CustomSourceURL && l.URL == "":
		return errors.New("enter a URL first")
	case l.Source == CustomSourceAPI:
		return errors.New("this line gets its text from the control API")
	}
	customMu.Lock()
	customPoll[i].force = true
	customPoll[i].next = time.Time{}
	customMu.Unlock()
	select {
	case customWake <- struct{}{}:
	default:
	}
	return nil
}

// customTick starts the runs that are due.
func customTick(cfg Config) {
	now := customNow()
	for i, id := range CustomLineIDs {
		l := cfg.CustomLines.Line(i)
		key := customKey(l)
		if key == "" {
			continue
		}
		customMu.Lock()
		v := &customPoll[i]
		if v.key != key {
			// New settings run after a moment, so a burst of edits runs once.
			*v = customValue{key: key, force: v.force, next: now.Add(customSettle)}
		}
		ready := (l.Source == CustomSourceCommand && l.Command != "") || (l.Source == CustomSourceURL && l.URL != "")
		due := ready && !v.busy && !now.Before(v.next) && (v.force || ModuleEnabled(id, cfg.DisplayOptions))
		if due {
			v.busy, v.force = true, false
		}
		customMu.Unlock()
		if due {
			go customRunOnce(i, l, key)
		}
	}
}

func customRunOnce(i int, l CustomLine, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(l.TimeoutSec)*time.Second)
	text, err := customRun(ctx, l)
	cancel()
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("took longer than %ds", l.TimeoutSec)
	}
	text = RuneLimit(plainLine(text), customMaxText)
	customMu.Lock()
	v := &customPoll[i]
	if v.key == key {
		v.busy = false
		v.runs++
		v.next = customNow().Add(time.Duration(l.IntervalSec) * time.Second)
		msg := ""
		if err != nil {
			msg = RuneLimit(plainLine(err.Error()), 200)
			text = ""
		}
		if v.text != text || v.err != msg || v.at.IsZero() {
			v.at = customNow()
		}
		v.text, v.err = text, msg
	}
	customMu.Unlock()
	NotifyChange()
}

func runCustomSource(ctx context.Context, l CustomLine) (string, error) {
	if l.Source == CustomSourceURL {
		return fetchCustomURL(ctx, l.URL, l.JSONPath)
	}
	return runCustomCommand(ctx, l.Command)
}

// ansiEscape matches terminal colour and cursor codes.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// firstLine is the first line of output with something on it, without
// terminal codes.
func firstLine(out string) string {
	out = strings.TrimPrefix(out, "\uFEFF")
	out = ansiEscape.ReplaceAllString(out, "")
	for _, line := range strings.Split(out, "\n") {
		if line = plainLine(line); line != "" {
			return line
		}
	}
	return ""
}

// limitedBuffer keeps the first max bytes written to it and drops the rest,
// so a chatty command can't fill memory.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

const (
	createNoWindow        = 0x08000000
	jobLimitKillOnClose   = 0x2000
	jobExtendedLimitClass = 9
	processSetQuota       = 0x0100
	processTerminate      = 0x0001
)

var (
	kernel32Custom          = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject     = kernel32Custom.NewProc("CreateJobObjectW")
	procSetInformationJob   = kernel32Custom.NewProc("SetInformationJobObject")
	procAssignProcessToJob  = kernel32Custom.NewProc("AssignProcessToJobObject")
	procGetOEMCP            = kernel32Custom.NewProc("GetOEMCP")
	procMultiByteToWideChar = kernel32Custom.NewProc("MultiByteToWideChar")
)

type jobBasicLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimits struct {
	Basic                 jobBasicLimits
	IoInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// killTreeJob puts pid in a job object that ends every process in it, the
// command's own children included, once the returned function closes it.
func killTreeJob(pid int) func() {
	job, _, _ := procCreateJobObject.Call(0, 0)
	if job == 0 {
		return func() {}
	}
	info := jobExtendedLimits{}
	info.Basic.LimitFlags = jobLimitKillOnClose
	_, _, _ = procSetInformationJob.Call(job, jobExtendedLimitClass, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid)); err == nil {
		_, _, _ = procAssignProcessToJob.Call(job, uintptr(h))
		_ = syscall.CloseHandle(h)
	}
	return func() { _ = syscall.CloseHandle(syscall.Handle(job)) }
}

// runCustomCommand runs command through cmd.exe without a console window
// and returns the first line it prints. Whatever is still running when it
// ends or times out is stopped.
func runCustomCommand(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("no command")
	}
	cmd := exec.CommandContext(ctx, "cmd.exe")
	// cmd.exe parses its own command line, so it is passed through as
	// typed: /s strips the outer quotes and keeps any inside.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
		CmdLine:       `cmd.exe /d /s /c "` + command + `"`,
	}
	cmd.WaitDelay = 2 * time.Second
	stdout := &limitedBuffer{max: customMaxOutput}
	stderr := &limitedBuffer{max: 4 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("could not start: %w", err)
	}
	closeJob := killTreeJob(cmd.Process.Pid)
	err := cmd.Wait()
	closeJob()
	line := firstLine(decodeConsole(stdout.buf.Bytes()))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if why := firstLine(decodeConsole(stderr.buf.Bytes())); why != "" {
				return "", fmt.Errorf("exit code %d: %s", exit.ExitCode(), why)
			}
			return "", fmt.Errorf("exit code %d", exit.ExitCode())
		}
		return "", err
	}
	return line, nil
}

// decodeConsole turns a program's output into text. Programs that don't
// write UTF-8 use the console's OEM code page.
func decodeConsole(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	cp, _, _ := procGetOEMCP.Call()
	n, _, _ := procMultiByteToWideChar.Call(cp, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return strings.ToValidUTF8(string(b), "")
	}
	wide := make([]uint16, n)
	procMultiByteToWideChar.Call(cp, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&wide[0])), n)
	return syscall.UTF16ToString(wide)
}

// customClient fetches custom line URLs. Each request has its own deadline
// from the line's timeout.
var customClient = &http.Client{}

// fetchCustomURL reads target and returns the value at path in its JSON
// reply, or with no path the first line of the reply.
func fetchCustomURL(ctx context.Context, target, path string) (string, error) {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("the URL must start with http:// or https://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", errors.New("that URL can't be used")
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "application/json, text/plain;q=0.9, */*;q=0.5")
	resp, err := customClient.Do(req)
	if err != nil {
		// The error repeats the URL, which may carry a key.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, customMaxBody))
	if err != nil {
		return "", errors.New("the reply broke off")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", httpStatusError(resp.StatusCode)
	}
	return customReplyText(body, path)
}

// customReplyText picks the line's text out of a reply body.
func customReplyText(body []byte, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		var s string
		if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '"' && json.Unmarshal(trimmed, &s) == nil {
			return firstLine(s), nil
		}
		return firstLine(string(body)), nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", errors.New("the reply is not JSON")
	}
	v, err := jsonPathLookup(doc, path)
	if err != nil {
		return "", err
	}
	return jsonText(v), nil
}

// jsonText is a JSON value as chatbox text.
func jsonText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return firstLine(t)
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
