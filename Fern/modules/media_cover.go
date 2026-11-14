package modules

import (
	"fmt"
	"net/http"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go/windows/media/control"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
)

// winrt-go wraps IRandomAccessStreamReference and DataReader but not the stream
// interfaces in between, so those few calls go straight through the vtables.
const (
	iidRandomAccessStream    = "905a0fe1-bc53-11df-8c49-001e4fc686da"
	iidInputStream           = "905a0fe2-bc53-11df-8c49-001e4fc686da"
	iidAsyncInfo             = "00000036-0000-0000-c000-000000000046"
	sigStreamWithContentType = "{cc254827-4b3d-438f-9232-10c76bc7e038}"

	// Vtable slots after IInspectable's six methods.
	slotStreamGetSize       = 6  // IRandomAccessStream.get_Size
	slotInputReadAsync      = 6  // IInputStream.ReadAsync
	slotAsyncInfoStatus     = 7  // IAsyncInfo.get_Status
	slotOpWithProgressGetRe = 10 // IAsyncOperationWithProgress.GetResults

	maxCoverBytes = 4 << 20
)

// MediaCover is the artwork of the track being read, if the app provides one.
type MediaCover struct {
	Data        []byte
	ContentType string
	Key         string // changes whenever the artwork does
}

var (
	coverMu  sync.RWMutex
	cover    MediaCover
	coverFor string // track the stored cover belongs to
)

// CurrentCover returns the artwork for the track shown in the panel.
func CurrentCover() MediaCover {
	coverMu.RLock()
	defer coverMu.RUnlock()
	return cover
}

func setCover(track string, data []byte) {
	coverMu.Lock()
	defer coverMu.Unlock()
	coverFor = track
	if len(data) == 0 {
		cover = MediaCover{}
		return
	}
	cover = MediaCover{
		Data:        data,
		ContentType: http.DetectContentType(data),
		Key:         fmt.Sprintf("%x", time.Now().UnixNano()),
	}
}

func coverTrack() string {
	coverMu.RLock()
	defer coverMu.RUnlock()
	return coverFor
}

// loopUpdateCover reads the thumbnail once per track. Runs on smtcLoop.
func loopUpdateCover(props *control.GlobalSystemMediaTransportControlsSessionMediaProperties, track string) {
	if track == coverTrack() {
		return
	}
	data, err := loopReadThumbnail(props)
	if err != nil {
		data = nil
	}
	setCover(track, data)
}

func loopReadThumbnail(props *control.GlobalSystemMediaTransportControlsSessionMediaProperties) ([]byte, error) {
	ref, err := props.GetThumbnail()
	if err != nil || ref == nil {
		return nil, fmt.Errorf("media: no thumbnail")
	}
	defer ref.Release()

	op, err := ref.OpenReadAsync()
	if err != nil || op == nil {
		return nil, fmt.Errorf("media: open thumbnail: %v", err)
	}
	ptr, err := awaitAsync(op, sigStreamWithContentType)
	op.Release()
	if err != nil || ptr == nil {
		return nil, fmt.Errorf("media: open thumbnail: %v", err)
	}
	stream := (*ole.IUnknown)(ptr)
	defer stream.Release()

	ras, err := queryInterface(stream, iidRandomAccessStream)
	if err != nil {
		return nil, err
	}
	var size uint64
	hr := vcall(ras, slotStreamGetSize, uintptr(unsafe.Pointer(&size)))
	ras.Release()
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	if size == 0 || size > maxCoverBytes {
		return nil, fmt.Errorf("media: thumbnail size %d", size)
	}

	input, err := queryInterface(stream, iidInputStream)
	if err != nil {
		return nil, err
	}
	defer input.Release()

	buf, err := streams.BufferCreate(uint32(size))
	if err != nil {
		return nil, err
	}
	defer buf.Release()
	ibuf, err := queryInterface(&buf.IUnknown, streams.GUIDIBuffer)
	if err != nil {
		return nil, err
	}
	defer ibuf.Release()

	var read *ole.IUnknown
	const inputStreamOptionsNone = 0
	hr = vcall(input, slotInputReadAsync,
		uintptr(unsafe.Pointer(ibuf)), uintptr(size), inputStreamOptionsNone, uintptr(unsafe.Pointer(&read)))
	if hr != 0 || read == nil {
		return nil, fmt.Errorf("media: read thumbnail: %v", ole.NewError(hr))
	}
	defer read.Release()
	if err := waitAsyncInfo(read, 3*time.Second); err != nil {
		return nil, err
	}

	var filled *ole.IUnknown
	if hr := vcall(read, slotOpWithProgressGetRe, uintptr(unsafe.Pointer(&filled))); hr != 0 || filled == nil {
		return nil, fmt.Errorf("media: read thumbnail: %v", ole.NewError(hr))
	}
	defer filled.Release()

	result := (*streams.IBuffer)(unsafe.Pointer(filled))
	n, err := result.GetLength()
	if err != nil || n == 0 {
		return nil, fmt.Errorf("media: empty thumbnail")
	}
	reader, err := streams.DataReaderFromBuffer(result)
	if err != nil {
		return nil, err
	}
	defer reader.Release()
	return reader.ReadBytes(n)
}

// waitAsyncInfo polls an async operation that has progress reporting. Those use
// a different completion handler type than awaitAsync supports, and a thumbnail
// read is short, so polling its status is simpler.
func waitAsyncInfo(op *ole.IUnknown, timeout time.Duration) error {
	info, err := queryInterface(op, iidAsyncInfo)
	if err != nil {
		return err
	}
	defer info.Release()
	const (
		started   = 0
		completed = 1
	)
	deadline := time.Now().Add(timeout)
	for {
		var status int32
		if hr := vcall(info, slotAsyncInfoStatus, uintptr(unsafe.Pointer(&status))); hr != 0 {
			return ole.NewError(hr)
		}
		switch {
		case status == completed:
			return nil
		case status != started:
			return fmt.Errorf("media: thumbnail read failed (status %d)", status)
		case time.Now().After(deadline):
			return fmt.Errorf("media: thumbnail read timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func queryInterface(unk *ole.IUnknown, iid string) (*ole.IUnknown, error) {
	out, err := unk.QueryInterface(ole.NewGUID(iid))
	if err != nil {
		return nil, err
	}
	return (*ole.IUnknown)(unsafe.Pointer(out)), nil
}

// vcall invokes vtable slot n of a COM object and returns its HRESULT.
func vcall(obj *ole.IUnknown, slot int, args ...uintptr) uintptr {
	method := (*[16]uintptr)(unsafe.Pointer(obj.RawVTable))[slot]
	hr, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)...)
	return hr
}
