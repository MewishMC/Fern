package modules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStartAndStopSTTServer(t *testing.T) {
	ping := `C:\Windows\System32\PING.EXE`
	if _, err := os.Stat(ping); err != nil {
		t.Skip("no ping.exe")
	}
	c := DefaultConfig()
	c.STTOptions.BaseURL = "http://127.0.0.1:1/v1" // nothing listens there
	c.STTOptions.ServerLaunch, c.STTOptions.ServerExe, c.STTOptions.ServerArgs = true, ping, "-n 30 127.0.0.1"
	useTestConfig(t, c)
	t.Cleanup(StopSTTServer)
	if err := StartSTTServer(); err != nil {
		t.Fatal(err)
	}
	st := CurrentSTTServer()
	if !st.Running || !st.Started || st.PID == 0 || st.Ready {
		t.Fatalf("after start: %+v", st)
	}
	proc, err := os.FindProcess(st.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := StartSTTServer(); err != nil || CurrentSTTServer().PID != st.PID {
		t.Error("a second start with the same settings must reuse the running server")
	}
	StopSTTServer()
	if st := CurrentSTTServer(); st.Running {
		t.Errorf("after stop: %+v", st)
	}
	done := make(chan error, 1)
	go func() { _, err := proc.Wait(); done <- err }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the server process is still running after StopSTTServer")
	}
}

func TestHostPort(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:8080/v1":  "127.0.0.1:8080",
		"http://localhost/v1":       "localhost:80",
		"https://api.openai.com/v1": "api.openai.com:443",
		"http://[::1]:9000":         "[::1]:9000",
	} {
		if got, err := hostPort(in); err != nil || got != want {
			t.Errorf("hostPort(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := hostPort("not a url"); err == nil {
		t.Error("expected an error")
	}
}

func TestWhisperCommandLine(t *testing.T) {
	got := whisperCommandLine(`C:\Program Files\whisper\server.exe`, `  -m "models\ggml base.bin" --port 8080 `)
	want := `"C:\Program Files\whisper\server.exe" -m "models\ggml base.bin" --port 8080`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if got := whisperCommandLine(`C:\w\s.exe`, ""); got != `C:\w\s.exe` {
		t.Errorf("no args: %s", got)
	}
}

func TestSTTServerSanitize(t *testing.T) {
	c := DefaultConfig()
	if c.STTOptions.ServerLaunch || c.STTOptions.ServerWaitSec != 60 {
		t.Errorf("defaults: %+v", c.STTOptions)
	}
	c.STTOptions.ServerWaitSec = 100000
	c.STTOptions.ServerExe = "  C:\\a.exe\n"
	c.sanitize()
	if c.STTOptions.ServerWaitSec != 600 || c.STTOptions.ServerExe != "C:\\a.exe" {
		t.Errorf("sanitized: %+v", c.STTOptions)
	}
	if sttServerWanted(STTOptions{ServerLaunch: true}) || !sttServerWanted(STTOptions{ServerLaunch: true, ServerExe: "x"}) {
		t.Error("sttServerWanted")
	}
}

func TestTailBuffer(t *testing.T) {
	var b tailBuffer
	for i := 0; i < 1000; i++ {
		_, _ = b.Write([]byte("line of server output\r\n"))
	}
	if n := strings.Count(b.String(), "\n"); n != 5 {
		t.Errorf("kept %d line breaks, want 6 lines", n)
	}
	if strings.Contains(b.String(), "\r") {
		t.Error("carriage returns kept")
	}
}

func TestRunSTTTest(t *testing.T) {
	var model, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		model, auth = r.FormValue("model"), r.Header.Get("Authorization")
		if _, _, err := r.FormFile("file"); err != nil {
			t.Error("no audio file:", err)
		}
		_, _ = w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	c := DefaultConfig()
	c.STTOptions.BaseURL = srv.URL + "/v1"
	c.STTOptions.Model = "Systran/faster-whisper-small"
	ok, msg, _ := runSTTTest(context.Background(), c)
	if !ok || !strings.Contains(msg, "answered") {
		t.Fatalf("got %v %q", ok, msg)
	}
	if model != "Systran/faster-whisper-small" || auth != "" {
		t.Errorf("model %q auth %q", model, auth)
	}

	c.STTOptions.BaseURL = srv.URL + "/wrong"
	if ok, msg, _ := runSTTTest(context.Background(), c); ok || !strings.Contains(msg, "not found") {
		t.Errorf("wrong path: %v %q", ok, msg)
	}

	// With launching on but the program missing, the test says so.
	c.STTOptions.BaseURL = "http://127.0.0.1:1/v1"
	c.STTOptions.ServerLaunch, c.STTOptions.ServerExe = true, `C:\definitely\not\here\whisper-server.exe`
	useTestConfig(t, c)
	if ok, msg, _ := runSTTTest(context.Background(), GetConfig()); ok || !strings.Contains(msg, "can't find") {
		t.Errorf("missing program: %v %q", ok, msg)
	}
	StopSTTServer()
}
