package modules

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Discord's local RPC runs over a named pipe, \\.\pipe\discord-ipc-N, with
// N from 0 to 9 (several Discord builds can run side by side). Each frame is a
// little-endian uint32 opcode, a little-endian uint32 length and that many
// bytes of JSON.
const (
	ipcOpHandshake = 0
	ipcOpFrame     = 1
	ipcOpClose     = 2
	ipcOpPing      = 3
	ipcOpPong      = 4

	ipcMaxFrame = 4 << 20
)

var errFrameTooLarge = errors.New("discord: frame too large")

// encodeFrame writes one frame with v marshalled as its body.
func encodeFrame(w io.Writer, op uint32, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(buf[0:4], op)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(body)))
	copy(buf[8:], body)
	_, err = w.Write(buf)
	return err
}

// decodeFrame reads one whole frame.
func decodeFrame(r io.Reader) (uint32, []byte, error) {
	var head [8]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, err
	}
	op := binary.LittleEndian.Uint32(head[0:4])
	n := binary.LittleEndian.Uint32(head[4:8])
	if n > ipcMaxFrame {
		return 0, nil, errFrameTooLarge
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return op, body, nil
}

// rpcMessage is a command, response or event on the RPC channel.
type rpcMessage struct {
	Cmd   string          `json:"cmd"`
	Nonce string          `json:"nonce,omitempty"`
	Evt   string          `json:"evt,omitempty"`
	Args  any             `json:"args,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// rpcError is the data of an ERROR response or event.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (m rpcMessage) err() error {
	if m.Evt != "ERROR" {
		return nil
	}
	var e rpcError
	_ = json.Unmarshal(m.Data, &e)
	return fmt.Errorf("discord error %d: %s", e.Code, e.Message)
}

// ipcTransport is the pipe, or a fake in tests.
type ipcTransport interface {
	io.ReadWriteCloser
	Available() (int, error)
}

// dialDiscord opens the first Discord pipe that answers.
func dialDiscord() (ipcTransport, error) {
	var last error = errors.New("discord is not running")
	for i := 0; i < 10; i++ {
		p, err := openPipe(fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i))
		if err == nil {
			return p, nil
		}
		last = err
	}
	return nil, last
}
