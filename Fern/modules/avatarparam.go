package modules

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/hypebeast/go-osc/osc"
)

// OSCDestinations lists host:port of the main output and of every enabled
// extra one, skipping repeats so nobody gets a message twice. The chatbox and
// avatar parameters both go to these.
func OSCDestinations(opts OSCOptions) []string {
	seen := map[string]bool{}
	var out []string
	add := func(host string, port int) {
		key := fmt.Sprintf("%s:%d", host, port)
		if !seen[strings.ToLower(key)] {
			seen[strings.ToLower(key)] = true
			out = append(out, key)
		}
	}
	add(opts.Host, opts.Port)
	for _, t := range opts.Extra {
		if t.Enabled {
			add(t.Host, t.Port)
		}
	}
	return out
}

// avatarSend delivers one avatar parameter message to every output. A
// variable so tests can capture the messages.
var avatarSend = func(o OSCOptions, msg *osc.Message) error {
	var firstErr error
	for _, d := range OSCDestinations(o) {
		i := strings.LastIndex(d, ":")
		var port int
		fmt.Sscanf(d[i+1:], "%d", &port)
		if err := osc.NewClient(d[:i], port).Send(msg); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

var errNoParamName = errors.New("avatar parameter has no name")

// avatarParamName accepts a bare name or a full /avatar/parameters/ address.
func avatarParamName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, AvatarParamPrefix)
	return strings.TrimSpace(name)
}

// avatarParamValue turns a Go value into the OSC type VRChat expects: bool,
// int32 clamped to 0..255 (VRChat's Int range) or float32 clamped to -1..1.
func avatarParamValue(value any) (any, error) {
	clampInt := func(n int64) int32 { return int32(max(0, min(255, n))) }
	clampFloat := func(f float64) float32 {
		if math.IsNaN(f) {
			return 0
		}
		return float32(max(-1, min(1, f)))
	}
	switch v := value.(type) {
	case bool:
		return v, nil
	case int:
		return clampInt(int64(v)), nil
	case int8:
		return clampInt(int64(v)), nil
	case int16:
		return clampInt(int64(v)), nil
	case int32:
		return clampInt(int64(v)), nil
	case int64:
		return clampInt(v), nil
	case uint8:
		return clampInt(int64(v)), nil
	case uint16:
		return clampInt(int64(v)), nil
	case uint32:
		return clampInt(int64(v)), nil
	case float32:
		return clampFloat(float64(v)), nil
	case float64:
		return clampFloat(v), nil
	}
	return nil, fmt.Errorf("avatar parameter value %T is not a bool, int or float", value)
}

// sendAvatarParamTo sends one avatar parameter to the outputs in o.
func sendAvatarParamTo(o OSCOptions, name string, value any) error {
	name = avatarParamName(name)
	if name == "" {
		return errNoParamName
	}
	v, err := avatarParamValue(value)
	if err != nil {
		return err
	}
	return avatarSend(o, osc.NewMessage(AvatarParamPrefix+name, v))
}

// SendAvatarParam sets an avatar parameter in VRChat over OSC, sent to the
// main output and every enabled extra output. name is the parameter name
// (a full /avatar/parameters/ address works too). value is a bool, any Go
// integer (sent as Int, clamped to 0..255) or float (sent as Float, clamped
// to -1..1).
func SendAvatarParam(name string, value any) error {
	return sendAvatarParamTo(GetConfig().OSCOptions, name, value)
}

// SendAvatarBool sets a Bool avatar parameter.
func SendAvatarBool(name string, v bool) error { return SendAvatarParam(name, v) }

// SendAvatarInt sets an Int avatar parameter; v is clamped to 0..255.
func SendAvatarInt(name string, v int) error { return SendAvatarParam(name, v) }

// SendAvatarFloat sets a Float avatar parameter; v is clamped to -1..1.
func SendAvatarFloat(name string, v float64) error { return SendAvatarParam(name, v) }
