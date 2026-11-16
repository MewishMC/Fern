package modules

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// Avatar parameters for Spotify. Each one works both ways: the avatar sets it
// to ask for a change, and Fern sends the real state back so a menu toggle
// shows whether the song is liked or shuffle is on. Because Fern's own sends
// can come back, and VRChat re-sends every saved parameter when an avatar
// loads, a value that matches the current state does nothing and values are
// ignored for a moment after an avatar change.

// Kinds of Spotify avatar parameters.
const (
	spotifyParamLike    = "like"
	spotifyParamShuffle = "shuffle"
	spotifyParamRepeat  = "repeat"
	spotifyParamVolume  = "volume"
	spotifyParamDevice  = "device"
	spotifyParamQueue   = "queue"
)

// spotifyAvatarQuiet is how long values are ignored after an avatar change.
const spotifyAvatarQuiet = 3 * time.Second

var (
	spotifyParamOnce  sync.Once
	spotifyParamMap   atomic.Pointer[map[string]string] // parameter name -> kind; nil when off
	spotifyQuietUntil atomic.Int64                      // unix nanoseconds

	spotifyParamMu   sync.Mutex
	spotifySent      = map[string]any{} // kind -> value last sent to the avatar
	spotifyQueueHigh bool               // the queue parameter is held true
	spotifyVolWant   = -1               // latest volume asked for, applied by spotifyVolLoop
	spotifyVolBusy   bool
	spotifyVolInAt   time.Time // when the avatar last moved the volume

	spotifyParamRun = spotifyParamAction // swapped in tests
)

// startSpotifyParams subscribes once; the map decides what is listened to.
func startSpotifyParams() {
	spotifyParamOnce.Do(func() {
		SubscribeOSCPrefix(AvatarParamPrefix, spotifyOnParam)
		OnAvatarChange(func(string) {
			spotifyQuietUntil.Store(time.Now().Add(spotifyAvatarQuiet).UnixNano())
			spotifyParamMu.Lock()
			clear(spotifySent) // the new avatar needs the state again
			spotifyQueueHigh = false
			spotifyParamMu.Unlock()
			time.AfterFunc(spotifyAvatarQuiet, func() { poke(spotifyKickCh) })
		})
	})
}

// spotifyParamNames maps the configured parameter names to their kinds.
func spotifyParamNames(o SpotifyOptions) map[string]string {
	m := map[string]string{}
	if !o.Enabled || !o.Params {
		return m
	}
	for name, kind := range map[string]string{
		o.ParamLike: spotifyParamLike, o.ParamShuffle: spotifyParamShuffle, o.ParamRepeat: spotifyParamRepeat,
		o.ParamVolume: spotifyParamVolume, o.ParamDevice: spotifyParamDevice, o.ParamQueue: spotifyParamQueue,
	} {
		if name != "" {
			m[name] = kind
		}
	}
	return m
}

func refreshSpotifyParamSubs(o SpotifyOptions) {
	m := spotifyParamNames(o)
	if len(m) == 0 {
		spotifyParamMap.Store(nil)
		return
	}
	spotifyParamMap.Store(&m)
}

// spotifyOnParam runs on the OSC listener, so it only looks the name up and
// hands real work to a goroutine.
func spotifyOnParam(msg *osc.Message) {
	m := spotifyParamMap.Load()
	if m == nil || len(msg.Arguments) == 0 {
		return
	}
	kind, ok := (*m)[msg.Address[len(AvatarParamPrefix):]]
	if !ok {
		return
	}
	if time.Now().UnixNano() < spotifyQuietUntil.Load() {
		return
	}
	v := msg.Arguments[0]
	if kind == spotifyParamQueue {
		// Only the moment it turns on counts.
		on := paramTruthy(v)
		spotifyParamMu.Lock()
		rising := on && !spotifyQueueHigh
		spotifyQueueHigh = on
		spotifyParamMu.Unlock()
		if !rising {
			return
		}
	}
	if kind == spotifyParamVolume {
		spotifyVolumeParam(v)
		return
	}
	go spotifyParamRun(kind, v)
}

// paramTruthy reads a bool, int or float parameter as on or off.
func paramTruthy(v any) bool {
	f, ok := oscNumber(v)
	return ok && f >= 0.5
}

// spotifyParamAction does what one parameter value asks, unless the state
// already matches.
func spotifyParamAction(kind string, v any) {
	p := spotifySnapshot()
	var err error
	switch kind {
	case spotifyParamLike:
		want := paramTruthy(v)
		if p.likedKnown && p.liked == want {
			return
		}
		_, err = SpotifySetLiked(map[bool]string{true: spotifyOn, false: spotifyOff}[want])
	case spotifyParamShuffle:
		want := paramTruthy(v)
		if p.active && p.shuffle == want {
			return
		}
		_, err = SpotifySetShuffle(map[bool]string{true: spotifyOn, false: spotifyOff}[want])
	case spotifyParamRepeat:
		want := repeatFromParam(v)
		if p.active && p.repeat == want {
			return
		}
		_, err = SpotifySetRepeat(want)
	case spotifyParamDevice:
		f, ok := oscNumber(v)
		n := int(math.Round(f))
		list := SpotifyDevices()
		if !ok || n < 1 {
			return
		}
		if n > len(list) {
			err = fmt.Errorf("there is no Spotify device number %d", n)
			break
		}
		if list[n-1].ID == p.device.ID {
			return
		}
		_, err = SpotifyTransfer(list[n-1].ID)
	case spotifyParamQueue:
		var added string
		if added, err = SpotifyQueue(GetConfig().SpotifyOptions.QueueText); err == nil {
			spotifyNotify("Queued "+added, false)
		}
	}
	if err != nil {
		spotifyNotify(err.Error(), true)
	}
}

// repeatFromParam reads 0, 1 and 2 (or a bool) as off, playlist and track.
func repeatFromParam(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return spotifyRepeatContext
		}
		return spotifyRepeatOff
	}
	f, _ := oscNumber(v)
	switch int(math.Round(f)) {
	case 1:
		return spotifyRepeatContext
	case 2:
		return spotifyRepeatTrack
	}
	return spotifyRepeatOff
}

func repeatToParam(r string) int {
	switch r {
	case spotifyRepeatContext:
		return 1
	case spotifyRepeatTrack:
		return 2
	}
	return 0
}

// volumeFromParam reads a float 0..1, or an int as percent.
func volumeFromParam(v any) int {
	switch x := v.(type) {
	case int32:
		return clampInt(int(x), 0, 100)
	case int:
		return clampInt(x, 0, 100)
	}
	f, _ := oscNumber(v)
	return clampInt(int(math.Round(f*100)), 0, 100)
}

// spotifyVolumeParam keeps only the newest volume while a radial menu slides,
// so a drag becomes a few requests instead of dozens.
func spotifyVolumeParam(v any) {
	want := volumeFromParam(v)
	spotifyParamMu.Lock()
	spotifyVolWant = want
	spotifyVolInAt = time.Now()
	start := !spotifyVolBusy
	spotifyVolBusy = true
	spotifyParamMu.Unlock()
	if start {
		go spotifyVolLoop()
	}
}

func spotifyVolLoop() {
	for {
		time.Sleep(250 * time.Millisecond)
		spotifyParamMu.Lock()
		want := spotifyVolWant
		spotifyVolWant = -1
		if want < 0 {
			spotifyVolBusy = false
			spotifyParamMu.Unlock()
			return
		}
		spotifyParamMu.Unlock()
		p := spotifySnapshot()
		if p.active && p.device.Volume >= 0 && abs(p.device.Volume-want) < 2 {
			continue
		}
		if _, err := SpotifySetVolume(spotifySet, want); err != nil {
			spotifyNotify(err.Error(), true)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// spotifySyncParams sends the state the avatar does not have yet.
func spotifySyncParams(o SpotifyOptions) {
	if time.Now().UnixNano() < spotifyQuietUntil.Load() {
		return
	}
	p := spotifySnapshot()
	if !p.active {
		return
	}
	want := map[string]any{}
	if o.ParamLike != "" && p.likedKnown {
		want[spotifyParamLike] = p.liked
	}
	if o.ParamShuffle != "" {
		want[spotifyParamShuffle] = p.shuffle
	}
	if o.ParamRepeat != "" {
		want[spotifyParamRepeat] = repeatToParam(p.repeat)
	}
	spotifyParamMu.Lock()
	sliding := time.Since(spotifyVolInAt) < 2*time.Second
	spotifyParamMu.Unlock()
	// While the avatar's radial is moving, sending the volume back would fight it.
	if o.ParamVolume != "" && p.device.Volume >= 0 && !sliding {
		want[spotifyParamVolume] = float64(p.device.Volume) / 100
	}
	names := map[string]string{
		spotifyParamLike: o.ParamLike, spotifyParamShuffle: o.ParamShuffle,
		spotifyParamRepeat: o.ParamRepeat, spotifyParamVolume: o.ParamVolume,
	}
	spotifyParamMu.Lock()
	var send []string
	for kind, v := range want {
		if prev, ok := spotifySent[kind]; !ok || prev != v {
			spotifySent[kind] = v
			send = append(send, kind)
		}
	}
	spotifyParamMu.Unlock()
	for _, kind := range send {
		_ = SendAvatarParam(names[kind], want[kind])
	}
}
