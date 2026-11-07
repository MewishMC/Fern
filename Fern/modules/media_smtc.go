package modules

import (
	"fmt"
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
	control bool
	action  string
	read    chan smtcReadResult
	done    chan error
}

type smtcReadResult struct {
	raw rawMedia
	err error
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

func currentSessionLocked() (*control.GlobalSystemMediaTransportControlsSession, error) {
	mgr, err := managerLocked()
	if err != nil {
		return nil, err
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

// readLocked samples the current media session. Must hold smtcMu.
func readLocked() (rawMedia, error) {
	session, err := currentSessionLocked()
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

// controlLocked asks the media app to do something. Must hold smtcMu.
func controlLocked(action string) error {
	session, err := currentSessionLocked()
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
		if req.control {
			err := controlLocked(req.action)
			smtcMu.Unlock()
			req.done <- err
			continue
		}
		raw, err := readLocked()
		smtcMu.Unlock()
		req.read <- smtcReadResult{raw: raw, err: err}
	}
}

func startSMTCLoop() {
	if smtcStarted {
		return
	}
	smtcStarted = true
	go smtcLoop()
}

func smtcRead() (rawMedia, error) {
	req := smtcRequest{read: make(chan smtcReadResult, 1)}
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

// MediaControl sends a transport command to the active media session.
func MediaControl(action string) error {
	req := smtcRequest{control: true, action: action, done: make(chan error, 1)}
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
