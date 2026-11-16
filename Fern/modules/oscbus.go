package modules

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hypebeast/go-osc/osc"
)

// The inbound OSC bus. Fern opens one listener for everything VRChat sends
// (see oscin.go) and hands each message to whoever subscribed to it, so new
// features do not each need a port of their own.

// Addresses VRChat uses for avatars.
const (
	AvatarParamPrefix = "/avatar/parameters/"
	AvatarChangeAddr  = "/avatar/change"
)

// OSCHandler receives one inbound OSC message. Handlers run on the listener
// goroutine one after another, so they must return quickly and hand slow work
// to a goroutine of their own. The message is shared between subscribers and
// must not be changed.
type OSCHandler func(msg *osc.Message)

type oscSub struct {
	id     uint64
	key    string
	prefix bool
	fn     OSCHandler
}

var (
	busMu   sync.Mutex
	busSubs atomic.Pointer[[]oscSub] // copy on write, read without a lock
	busNext uint64

	paramMu    sync.RWMutex
	paramCache = map[string]any{}
	avatarID   string
	avatarSubs = map[uint64]func(string){}
)

// SubscribeOSC calls fn for every inbound message whose address is exactly
// address. The returned function removes the subscription.
func SubscribeOSC(address string, fn OSCHandler) (cancel func()) {
	return addOSCSub(address, false, fn)
}

// SubscribeOSCPrefix calls fn for every inbound message whose address starts
// with prefix, e.g. AvatarParamPrefix for every avatar parameter. "" or "/"
// matches everything. The returned function removes the subscription.
func SubscribeOSCPrefix(prefix string, fn OSCHandler) (cancel func()) {
	return addOSCSub(prefix, true, fn)
}

func addOSCSub(key string, prefix bool, fn OSCHandler) func() {
	if fn == nil {
		return func() {}
	}
	busMu.Lock()
	busNext++
	id := busNext
	var next []oscSub
	if cur := busSubs.Load(); cur != nil {
		next = append(next, *cur...)
	}
	next = append(next, oscSub{id: id, key: key, prefix: prefix, fn: fn})
	busSubs.Store(&next)
	busMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			busMu.Lock()
			defer busMu.Unlock()
			cur := busSubs.Load()
			if cur == nil {
				return
			}
			next := make([]oscSub, 0, len(*cur))
			for _, s := range *cur {
				if s.id != id {
					next = append(next, s)
				}
			}
			busSubs.Store(&next)
		})
	}
}

// AvatarParam is the latest value VRChat reported for an avatar parameter:
// bool, int32 or float32 as VRChat sends them. The name is without the
// /avatar/parameters/ prefix. The cache is emptied when the avatar changes,
// since the new avatar has parameters of its own (VRChat sends them all
// straight after /avatar/change).
func AvatarParam(name string) (value any, ok bool) {
	paramMu.RLock()
	defer paramMu.RUnlock()
	value, ok = paramCache[name]
	return value, ok
}

// AvatarParamFloat reads a cached parameter as a number: bools are 0 or 1.
func AvatarParamFloat(name string) (float64, bool) {
	v, ok := AvatarParam(name)
	if !ok {
		return 0, false
	}
	return oscNumber(v)
}

// AvatarParams is a copy of every cached avatar parameter.
func AvatarParams() map[string]any {
	paramMu.RLock()
	defer paramMu.RUnlock()
	out := make(map[string]any, len(paramCache))
	for k, v := range paramCache {
		out[k] = v
	}
	return out
}

// CurrentAvatarID is the avatar ID from VRChat's last /avatar/change, or ""
// before VRChat has sent one.
func CurrentAvatarID() string {
	paramMu.RLock()
	defer paramMu.RUnlock()
	return avatarID
}

// OnAvatarChange calls fn with the new avatar ID on every /avatar/change,
// including a reload of the same avatar (its parameters reset then too). It
// runs on the listener goroutine, after the parameter cache was emptied, and
// must return quickly. The returned function removes the subscription.
func OnAvatarChange(fn func(id string)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	busMu.Lock()
	busNext++
	id := busNext
	busMu.Unlock()
	paramMu.Lock()
	avatarSubs[id] = fn
	paramMu.Unlock()
	return func() {
		paramMu.Lock()
		delete(avatarSubs, id)
		paramMu.Unlock()
	}
}

func oscNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// flattenOSC lists every message of a packet, bundles included.
func flattenOSC(pkt osc.Packet) []*osc.Message {
	switch p := pkt.(type) {
	case *osc.Message:
		return []*osc.Message{p}
	case *osc.Bundle:
		out := append([]*osc.Message{}, p.Messages...)
		for _, b := range p.Bundles {
			out = append(out, flattenOSC(b)...)
		}
		return out
	}
	return nil
}

// receiveOSC handles one datagram from the listener.
func receiveOSC(data []byte) {
	noteOSCPacket()
	var msgs []*osc.Message
	if pkt, err := osc.ParsePacket(string(data)); err == nil {
		msgs = flattenOSC(pkt)
	}
	for _, m := range msgs {
		deliverOSC(m)
	}
	routeOSC(data, msgs)
}

// deliverOSC updates the parameter cache and avatar ID, then runs the
// subscribers that match.
func deliverOSC(msg *osc.Message) {
	if msg == nil {
		return
	}
	if name, ok := strings.CutPrefix(msg.Address, AvatarParamPrefix); ok && name != "" && len(msg.Arguments) > 0 {
		paramMu.Lock()
		paramCache[name] = msg.Arguments[0]
		paramMu.Unlock()
	} else if msg.Address == AvatarChangeAddr && len(msg.Arguments) > 0 {
		if id, ok := msg.Arguments[0].(string); ok {
			paramMu.Lock()
			avatarID = id
			clear(paramCache)
			fns := make([]func(string), 0, len(avatarSubs))
			for _, fn := range avatarSubs {
				fns = append(fns, fn)
			}
			paramMu.Unlock()
			for _, fn := range fns {
				safeCall(func() { fn(id) })
			}
		}
	}
	subs := busSubs.Load()
	if subs == nil {
		return
	}
	for _, s := range *subs {
		if s.prefix {
			if !strings.HasPrefix(msg.Address, s.key) {
				continue
			}
		} else if msg.Address != s.key {
			continue
		}
		safeCall(func() { s.fn(msg) })
	}
}

// safeCall keeps one broken subscriber from taking the listener down.
func safeCall(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("osc: subscriber panicked:", r)
		}
	}()
	fn()
}
