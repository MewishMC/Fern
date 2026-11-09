package modules

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/media/control"
)

// rawMedia is a single sample of the system media session.
type rawMedia struct {
	Available bool
	AppID     string
	Title     string
	Artist    string
	Album     string
	Status    string
	Position  time.Duration
	Duration  time.Duration
	Sampled   time.Time // when this struct was filled
	Updated   time.Time // when the app last reported Position to Windows
}

type smtcRequest struct {
	kind   string // read, control, sessions
	action string
	app    string
	read   chan smtcReadResult
	list   chan smtcListResult
	done   chan error
}

type smtcReadResult struct {
	raw rawMedia
	err error
}

type smtcListResult struct {
	sources []MediaSource
	err     error
}

var (
	smtcMu       sync.Mutex
	smtcManager  *control.GlobalSystemMediaTransportControlsSessionManager
	smtcInitErr  error
	smtcLastInit time.Time
	smtcQueue    = make(chan smtcRequest, 16)
	smtcStarted  bool
)

// awaitAsync blocks until a WinRT async operation finishes and returns its result.
func awaitAsync(op *foundation.IAsyncOperation, signature string) (unsafe.Pointer, error) {
	done := make(chan struct{}, 1)
	iid := ole.NewGUID(winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, signature))
	handler := foundation.NewAsyncOperationCompletedHandler(iid, func(_ *foundation.AsyncOperationCompletedHandler, _ *foundation.IAsyncOperation, _ foundation.AsyncStatus) {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	if err := op.SetCompleted(handler); err != nil {
		handler.Release()
		return nil, err
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		return nil, fmt.Errorf("media: timed out")
	}
	ptr, err := op.GetResults()
	handler.Release()
	return ptr, err
}

// winDateTimeToTime converts a WinRT DateTime (FILETIME: 100ns ticks since
// 1601-01-01 UTC) into a Go time.
func winDateTimeToTime(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	const ticksPerSecond = 10_000_000
	const windowsToUnix = 11_644_473_600
	secs := v/ticksPerSecond - windowsToUnix
	nanos := (v % ticksPerSecond) * 100
	return time.Unix(secs, nanos)
}

// managerLocked returns a cached session manager, creating it on first use.
func managerLocked() (*control.GlobalSystemMediaTransportControlsSessionManager, error) {
	if smtcManager != nil {
		return smtcManager, nil
	}
	// Retry lazily instead of hammering a broken subsystem.
	if smtcInitErr != nil && time.Since(smtcLastInit) < 30*time.Second {
		return nil, smtcInitErr
	}
	smtcLastInit = time.Now()

	if err := ole.RoInitialize(1); err != nil {
		smtcInitErr = err
		return nil, err
	}
	op, err := control.GlobalSystemMediaTransportControlsSessionManagerRequestAsync()
	if err != nil {
		smtcInitErr = err
		return nil, err
	}
	ptr, err := awaitAsync(op, control.SignatureGlobalSystemMediaTransportControlsSessionManager)
	if err != nil {
		smtcInitErr = err
		return nil, err
	}
	if ptr == nil {
		smtcInitErr = fmt.Errorf("media: no manager")
		return nil, smtcInitErr
	}
	smtcManager = (*control.GlobalSystemMediaTransportControlsSessionManager)(ptr)
	smtcInitErr = nil
	return smtcManager, nil
}

// sessionLocked returns the chosen app's session, or the current session when
// none was chosen. Must hold smtcMu.
func sessionLocked(preferred string) (*control.GlobalSystemMediaTransportControlsSession, error) {
	mgr, err := managerLocked()
	if err != nil {
		return nil, err
	}
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		if session := findSessionLocked(mgr, preferred); session != nil {
			return session, nil
		}
		// Do not quietly show another app: the user picked this one.
		return nil, fmt.Errorf("media: %s has no session", preferred)
	}
	session, err := mgr.GetCurrentSession()
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("media: no session")
	}
	return session, nil
}

// findSessionLocked walks the session list and returns the chosen app, or nil.
func findSessionLocked(mgr *control.GlobalSystemMediaTransportControlsSessionManager, preferred string) *control.GlobalSystemMediaTransportControlsSession {
	vec, err := mgr.GetSessions()
	if err != nil || vec == nil {
		return nil
	}
	defer vec.Release()
	size, err := vec.GetSize()
	if err != nil {
		return nil
	}
	for i := uint32(0); i < size; i++ {
		ptr, err := vec.GetAt(i)
		if err != nil || ptr == nil {
			continue
		}
		session := (*control.GlobalSystemMediaTransportControlsSession)(ptr)
		appID, _ := session.GetSourceAppUserModelId()
		if matchesApp(appID, preferred) {
			return session
		}
		session.Release()
	}
	return nil
}

func matchesApp(appID, preferred string) bool {
	if strings.EqualFold(strings.TrimSpace(appID), preferred) {
		return true
	}
	return strings.EqualFold(appNameFromID(appID), preferred)
}

// sessionsLocked lists every media session Windows reports. Must hold smtcMu.
func sessionsLocked() ([]MediaSource, error) {
	mgr, err := managerLocked()
	if err != nil {
		return nil, err
	}
	vec, err := mgr.GetSessions()
	if err != nil || vec == nil {
		return nil, err
	}
	defer vec.Release()
	size, err := vec.GetSize()
	if err != nil {
		return nil, err
	}

	out := make([]MediaSource, 0, size)
	seen := make(map[string]bool, size)
	for i := uint32(0); i < size && len(out) < 16; i++ {
		ptr, err := vec.GetAt(i)
		if err != nil || ptr == nil {
			continue
		}
		session := (*control.GlobalSystemMediaTransportControlsSession)(ptr)
		appID, _ := session.GetSourceAppUserModelId()
		src := MediaSource{AppID: appID, App: appNameFromID(appID)}
		if pi, err := session.GetPlaybackInfo(); err == nil && pi != nil {
			status, _ := pi.GetPlaybackStatus()
			src.Playing = status == control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying
			pi.Release()
		}
		if op, err := session.TryGetMediaPropertiesAsync(); err == nil && op != nil {
			if res, err := awaitAsync(op, control.SignatureGlobalSystemMediaTransportControlsSessionMediaProperties); err == nil && res != nil {
				props := (*control.GlobalSystemMediaTransportControlsSessionMediaProperties)(res)
				src.Title, _ = props.GetTitle()
				props.Release()
			}
		}
		session.Release()

		key := strings.ToLower(strings.TrimSpace(appID))
		if key == "" {
			key = strings.ToLower(src.App)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, src)
	}
	return out, nil
}

// readLocked samples the chosen media session. Must hold smtcMu.
func readLocked(preferred string) (rawMedia, error) {
	session, err := sessionLocked(preferred)
	if err != nil {
		return rawMedia{}, err
	}
	defer session.Release()

	out := rawMedia{Available: true, Sampled: time.Now(), Status: "unknown"}
	out.AppID, _ = session.GetSourceAppUserModelId()

	if op, err := session.TryGetMediaPropertiesAsync(); err == nil && op != nil {
		if ptr, err := awaitAsync(op, control.SignatureGlobalSystemMediaTransportControlsSessionMediaProperties); err == nil && ptr != nil {
			props := (*control.GlobalSystemMediaTransportControlsSessionMediaProperties)(ptr)
			out.Title, _ = props.GetTitle()
			out.Artist, _ = props.GetArtist()
			out.Album, _ = props.GetAlbumTitle()
			props.Release()
		}
	}

	if tl, err := session.GetTimelineProperties(); err == nil && tl != nil {
		pos, _ := tl.GetPosition()
		start, _ := tl.GetStartTime()
		end, _ := tl.GetEndTime()
		updated, _ := tl.GetLastUpdatedTime()
		out.Position = time.Duration(pos.Duration) * 100
		out.Updated = winDateTimeToTime(updated.UniversalTime)
		if span := end.Duration - start.Duration; span > 0 {
			out.Duration = time.Duration(span) * 100
		}
		tl.Release()
	}

	if pi, err := session.GetPlaybackInfo(); err == nil && pi != nil {
		status, _ := pi.GetPlaybackStatus()
		switch status {
		case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPlaying:
			out.Status = "playing"
		case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusPaused:
			out.Status = "paused"
		case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusStopped:
			out.Status = "stopped"
		case control.GlobalSystemMediaTransportControlsSessionPlaybackStatusChanging:
			out.Status = "playing"
		default:
			out.Status = "unknown"
		}
		pi.Release()
	}

	if out.Status == "unknown" && out.Title == "" {
		return rawMedia{}, fmt.Errorf("media: empty session")
	}
	return out, nil
}

// controlLocked asks the chosen media app to do something. Must hold smtcMu.
func controlLocked(action, preferred string) error {
	session, err := sessionLocked(preferred)
	if err != nil {
		return err
	}
	defer session.Release()

	var op *foundation.IAsyncOperation
	switch action {
	case "playpause":
		op, err = session.TryTogglePlayPauseAsync()
	case "play":
		op, err = session.TryPlayAsync()
	case "pause":
		op, err = session.TryPauseAsync()
	case "next":
		op, err = session.TrySkipNextAsync()
	case "prev":
		op, err = session.TrySkipPreviousAsync()
	default:
		return fmt.Errorf("media: unknown action %q", action)
	}
	if err != nil {
		return err
	}
	if op == nil {
		return fmt.Errorf("media: action not supported")
	}
	// These operations return IAsyncOperation<bool>.
	_, err = awaitAsync(op, winrt.SignatureBool)
	return err
}

// smtcLoop owns all WinRT calls so COM stays on one goroutine.
func smtcLoop() {
	_ = ole.RoInitialize(1)
	for req := range smtcQueue {
		smtcMu.Lock()
		switch req.kind {
		case "control":
			err := controlLocked(req.action, req.app)
			smtcMu.Unlock()
			req.done <- err
		case "sessions":
			sources, err := sessionsLocked()
			smtcMu.Unlock()
			req.list <- smtcListResult{sources: sources, err: err}
		default:
			raw, err := readLocked(req.app)
			smtcMu.Unlock()
			req.read <- smtcReadResult{raw: raw, err: err}
		}
	}
}

func startSMTCLoop() {
	if smtcStarted {
		return
	}
	smtcStarted = true
	go smtcLoop()
}

func smtcRead(app string) (rawMedia, error) {
	req := smtcRequest{kind: "read", app: app, read: make(chan smtcReadResult, 1)}
	select {
	case smtcQueue <- req:
	case <-time.After(2 * time.Second):
		return rawMedia{}, fmt.Errorf("media: busy")
	}
	select {
	case res := <-req.read:
		return res.raw, res.err
	case <-time.After(4 * time.Second):
		return rawMedia{}, fmt.Errorf("media: timeout")
	}
}

// smtcSessions lists the media sessions Windows is exposing.
func smtcSessions() ([]MediaSource, error) {
	req := smtcRequest{kind: "sessions", list: make(chan smtcListResult, 1)}
	select {
	case smtcQueue <- req:
	case <-time.After(2 * time.Second):
		return nil, fmt.Errorf("media: busy")
	}
	select {
	case res := <-req.list:
		return res.sources, res.err
	case <-time.After(6 * time.Second):
		return nil, fmt.Errorf("media: timeout")
	}
}

// MediaControl sends a transport command to the chosen media session.
func MediaControl(action string) error {
	preferred := strings.TrimSpace(GetConfig().MediaOptions.PreferredApp)
	req := smtcRequest{kind: "control", action: action, app: preferred, done: make(chan error, 1)}
	select {
	case smtcQueue <- req:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("media: busy")
	}
	select {
	case err := <-req.done:
		return err
	case <-time.After(4 * time.Second):
		return fmt.Errorf("media: timeout")
	}
}
