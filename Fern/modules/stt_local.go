package modules

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Local speech to text. Fern bundles no speech model; it talks to a
// whisper server the user runs (whisper.cpp's server, faster-whisper-server,
// Speaches), which all answer the same OpenAI-compatible
// /audio/transcriptions request as the cloud. Optionally Fern starts that
// server itself the first time dictation is used, and stops it on exit.

// STTServerStatus is what the panel shows about the launched server.
type STTServerStatus struct {
	Running  bool   `json:"running"`
	Started  bool   `json:"started"` // Fern started it (rather than finding one already running)
	PID      int    `json:"pid"`
	Ready    bool   `json:"ready"` // its port answers
	Error    string `json:"error"`
	Output   string `json:"output"` // the end of what it printed
	Since    int64  `json:"since"`  // unix ms
	External bool   `json:"external"`
}

// STTTestStatus is the result of the Test connection button.
type STTTestStatus struct {
	Seq     int    `json:"seq"`
	Busy    bool   `json:"busy"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	MS      int    `json:"ms"`
}

var (
	whisperMu     sync.Mutex
	whisperCmd    *exec.Cmd
	whisperSig    string // exe + args it was started with
	whisperState  STTServerStatus
	whisperOutput tailBuffer
	whisperJob    uintptr

	sttTestState STTTestStatus // guarded by sttMu
)

// tailBuffer keeps the last bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 4096 {
		t.buf = append([]byte{}, t.buf[len(t.buf)-4096:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.ToValidUTF8(string(t.buf), "")
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "\r", ""), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return RuneLimit(strings.Join(lines, "\n"), 600)
}

func (t *tailBuffer) reset() {
	t.mu.Lock()
	t.buf = nil
	t.mu.Unlock()
}

// CurrentSTTServer returns the launched server's state.
func CurrentSTTServer() STTServerStatus {
	whisperMu.Lock()
	defer whisperMu.Unlock()
	out := whisperState
	out.Output = whisperOutput.String()
	return out
}

// sttBaseURL is the transcription base URL dictation uses.
func sttBaseURL(cfg Config) string {
	if cfg.STTOptions.BaseURL != "" {
		return cfg.STTOptions.BaseURL
	}
	return cfg.AssistOptions.OpenAIBaseURL
}

// sttRequestFor resolves the transcription server, model and key.
func sttRequestFor(cfg Config) (transcribeRequest, error) {
	o := cfg.STTOptions
	r := transcribeRequest{BaseURL: sttBaseURL(cfg), Model: o.Model, Language: o.Language}
	stored := o.Key
	if stored == "" {
		stored = cfg.AssistOptions.OpenAIKey
	}
	key, err := openSecret(stored)
	if err != nil {
		return r, err
	}
	r.Key = key
	if r.Key == "" && strings.Contains(r.BaseURL, "api.openai.com") {
		return r, errors.New("add an OpenAI API key on the Assist page")
	}
	return r, nil
}

// hostPort is the address a base URL connects to.
func hostPort(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", errors.New("the transcription base URL is not valid")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

func portOpen(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// sttServerWanted reports whether Fern should start a server for dictation.
func sttServerWanted(o STTOptions) bool {
	return o.ServerLaunch && strings.TrimSpace(o.ServerExe) != ""
}

// whisperCommandLine is the Windows command line for the server: the program
// quoted, then the arguments exactly as typed.
func whisperCommandLine(exe, args string) string {
	line := syscall.EscapeArg(exe)
	if args = strings.TrimSpace(args); args != "" {
		line += " " + args
	}
	return line
}

// StartSTTServer starts the user's whisper server when that is turned on
// and it is not already running. It returns quickly; the server loads its
// model in the background.
func StartSTTServer() error {
	cfg := GetConfig()
	o := cfg.STTOptions
	if !sttServerWanted(o) {
		return nil
	}
	sig := o.ServerExe + "\x00" + o.ServerArgs
	whisperMu.Lock()
	defer whisperMu.Unlock()
	if whisperCmd != nil {
		if whisperSig == sig {
			return nil
		}
		stopWhisperLocked() // settings changed: restart with the new ones
	}
	addr, err := hostPort(sttBaseURL(cfg))
	if err != nil {
		whisperState.Error = err.Error()
		return err
	}
	if portOpen(addr, 300*time.Millisecond) {
		// Something already answers there, most likely the server started by
		// hand; use it rather than start a second copy.
		whisperState = STTServerStatus{Running: true, Ready: true, External: true, Since: time.Now().UnixMilli()}
		return nil
	}
	exe := strings.Trim(strings.TrimSpace(o.ServerExe), `"`)
	if info, err := os.Stat(exe); err != nil || info.IsDir() {
		whisperState = STTServerStatus{Error: "can't find the server program " + exe}
		return errors.New(whisperState.Error)
	}
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		CmdLine:       whisperCommandLine(exe, o.ServerArgs),
	}
	whisperOutput.reset()
	cmd.Stdout, cmd.Stderr = &whisperOutput, &whisperOutput
	if err := cmd.Start(); err != nil {
		whisperState = STTServerStatus{Error: "could not start the server: " + err.Error()}
		return errors.New(whisperState.Error)
	}
	killWithFern(cmd.Process.Pid)
	whisperCmd, whisperSig = cmd, sig
	whisperState = STTServerStatus{Running: true, Started: true, PID: cmd.Process.Pid, Since: time.Now().UnixMilli()}
	fmt.Println("stt: started the local whisper server, pid", cmd.Process.Pid)
	go func() {
		err := cmd.Wait()
		whisperMu.Lock()
		defer whisperMu.Unlock()
		if whisperCmd != cmd {
			return // stopped on purpose
		}
		whisperCmd, whisperSig = nil, ""
		msg := "the server stopped"
		if err != nil {
			msg += ": " + err.Error()
		}
		whisperState = STTServerStatus{Error: msg}
	}()
	go watchWhisperReady(cmd, addr)
	return nil
}

func watchWhisperReady(cmd *exec.Cmd, addr string) {
	for i := 0; i < 1200; i++ { // up to ten minutes for a big model
		if portOpen(addr, 300*time.Millisecond) {
			whisperMu.Lock()
			if whisperCmd == cmd {
				whisperState.Ready = true
			}
			whisperMu.Unlock()
			return
		}
		whisperMu.Lock()
		gone := whisperCmd != cmd
		whisperMu.Unlock()
		if gone {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// StopSTTServer stops the server Fern started.
func StopSTTServer() {
	whisperMu.Lock()
	defer whisperMu.Unlock()
	stopWhisperLocked()
	whisperState = STTServerStatus{}
}

func stopWhisperLocked() {
	if whisperCmd != nil && whisperCmd.Process != nil {
		_ = whisperCmd.Process.Kill()
		fmt.Println("stt: stopped the local whisper server")
	}
	whisperCmd, whisperSig = nil, ""
}

// waitSTTServer waits until the launched server's port answers, up to the
// configured time. Without a launched server it returns at once.
func waitSTTServer(ctx context.Context, cfg Config) error {
	if !sttServerWanted(cfg.STTOptions) {
		return nil
	}
	if err := StartSTTServer(); err != nil {
		return err
	}
	addr, err := hostPort(sttBaseURL(cfg))
	if err != nil {
		return err
	}
	deadline := time.Now().Add(time.Duration(cfg.STTOptions.ServerWaitSec) * time.Second)
	for {
		if portOpen(addr, 300*time.Millisecond) {
			return nil
		}
		st := CurrentSTTServer()
		if !st.Running {
			msg := "the local whisper server is not running"
			if st.Error != "" {
				msg = st.Error
			}
			if st.Output != "" {
				msg += " (" + lastLine(st.Output) + ")"
			}
			return errors.New(msg)
		}
		if time.Now().After(deadline) {
			return errors.New("the local whisper server is still starting; try again in a moment")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return RuneLimit(strings.TrimSpace(lines[len(lines)-1]), 160)
}

// TestSTTConnection sends half a second of silence to the transcription
// server and reports whether it answered like an OpenAI-compatible one. It
// runs in the background; the result is in CurrentSTT().Test.
func TestSTTConnection() {
	sttMu.Lock()
	if sttTestState.Busy {
		sttMu.Unlock()
		return
	}
	sttTestState.Busy = true
	sttMu.Unlock()
	go func() {
		ok, msg, ms := runSTTTest(context.Background(), GetConfig())
		sttMu.Lock()
		sttTestState = STTTestStatus{Seq: sttTestState.Seq + 1, OK: ok, Message: msg, MS: ms}
		sttMu.Unlock()
	}()
}

func runSTTTest(ctx context.Context, cfg Config) (ok bool, msg string, ms int) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.STTOptions.ServerWaitSec)*time.Second+time.Duration(cfg.AssistOptions.TimeoutSec)*time.Second+15*time.Second)
	defer cancel()
	if err := waitSTTServer(ctx, cfg); err != nil {
		return false, err.Error(), 0
	}
	r, err := sttRequestFor(cfg)
	if err != nil {
		return false, err.Error(), 0
	}
	r.WAV = encodeWAV(make([]int16, sttSampleRate/2), sttSampleRate)
	start := time.Now()
	text, _, err := transcribe(ctx, aiClient, r)
	ms = int(time.Since(start).Milliseconds())
	if err != nil {
		return false, err.Error(), ms
	}
	msg = fmt.Sprintf("%s answered in %.1fs", r.BaseURL, float64(ms)/1000)
	if text != "" {
		msg += " (heard “" + RuneLimit(text, 40) + "” in silence)"
	}
	return true, msg, ms
}

/* ---------------- job object: the server ends with Fern ---------------- */

var (
	procCreateJobObject = kernel32DLL.NewProc("CreateJobObjectW")
	procSetJobInfo      = kernel32DLL.NewProc("SetInformationJobObject")
	procAssignJob       = kernel32DLL.NewProc("AssignProcessToJobObject")
	procOpenProcessJob  = kernel32DLL.NewProc("OpenProcess")
	procCloseHandleJob  = kernel32DLL.NewProc("CloseHandle")
)

var kernel32DLL = syscall.NewLazyDLL("kernel32.dll")

// jobObjectExtendedLimitInformation is JOBOBJECT_EXTENDED_LIMIT_INFORMATION
// on 64-bit Windows.
type jobObjectExtendedLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoInfo                  [6]uint64
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

// killWithFern puts the process in a job that Windows closes, and so kills,
// when Fern exits for any reason. Failing that, the server outlives a crash
// but StopSTTServer still stops it.
func killWithFern(pid int) {
	if whisperJob == 0 {
		h, _, _ := procCreateJobObject.Call(0, 0)
		if h == 0 {
			return
		}
		const (
			jobObjectExtendedLimitInformationClass = 9
			jobObjectLimitKillOnJobClose           = 0x2000
		)
		info := jobObjectExtendedLimitInformation{LimitFlags: jobObjectLimitKillOnJobClose}
		r, _, _ := procSetJobInfo.Call(h, jobObjectExtendedLimitInformationClass, uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
		if r == 0 {
			procCloseHandleJob.Call(h)
			return
		}
		whisperJob = h
	}
	const processSetQuota, processTerminate = 0x0100, 0x0001
	ph, _, _ := procOpenProcessJob.Call(processSetQuota|processTerminate, 0, uintptr(pid))
	if ph == 0 {
		return
	}
	defer procCloseHandleJob.Call(ph)
	procAssignJob.Call(whisperJob, ph)
}
