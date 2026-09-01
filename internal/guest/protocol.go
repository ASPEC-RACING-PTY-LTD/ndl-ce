package guest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	ChannelName = "org.nodal.guest.0"
	JailRoot    = "guest:/"
	Version     = "0.1.18"

	StateOK           = "ok"
	StateNotInstalled = "not_installed"
	StateStale        = "stale"
	StateUnavailable  = "unavailable"

	MethodPing      = "guest.ping"
	MethodInfo      = "guest.info"
	MethodOSInfo    = "guest.osinfo"
	MethodNetwork   = "guest.network"
	MethodMetrics   = "guest.metrics"
	MethodShutdown  = "guest.shutdown"
	MethodFreeze    = "guest.fsfreeze"
	MethodFilesOp   = "guest.files.op"
	MethodFilesPut  = "guest.files.put"
	MethodFilesGet  = "guest.files.get"
	MethodPTYOpen   = "guest.pty.open"
	MethodPTYWrite  = "guest.pty.write"
	MethodPTYRead   = "guest.pty.read"
	MethodPTYResize = "guest.pty.resize"
	MethodPTYClose  = "guest.pty.close"
)

// Request is one NDJSON guest RPC. Methods are typed; there is no shell field.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one NDJSON guest reply.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Info is guest.identity, not host identity.
type Info struct {
	Version  string   `json:"version"`
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	Hostname string   `json:"hostname,omitempty"`
	Features []string `json:"features,omitempty"`
}

// ChannelState is honest qemu-ga or nodal_ga status.
type ChannelState struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Status is observed guest-agent state. Missing is not healthy.
type Status struct {
	WorkloadID string       `json:"workload_id"`
	QEMUGA     ChannelState `json:"qemu_ga"`
	NodalGA    ChannelState `json:"nodal_ga"`
	GuestOS    string       `json:"guest_os,omitempty"`
	GuestArch  string       `json:"guest_arch,omitempty"`
	IPv4       []string     `json:"ipv4,omitempty"`
	ObservedAt time.Time    `json:"observed_at"`
}

// FilesParams is guest.files.op / put / get.
type FilesParams struct {
	Action  string `json:"action,omitempty"`
	Path    string `json:"path,omitempty"`
	Dest    string `json:"dest_path,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	DataB64 string `json:"data_b64,omitempty"`
}

// PTYParams is guest.pty.*.
type PTYParams struct {
	Session string `json:"session,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
	DataB64 string `json:"data_b64,omitempty"`
	EOF     bool   `json:"eof,omitempty"`
}

// ShutdownParams is guest.shutdown.
type ShutdownParams struct {
	Mode string `json:"mode,omitempty"`
}

// Conn is a framed NDJSON guest channel.
type Conn struct {
	rw  io.ReadWriteCloser
	rd  *bufio.Reader
	mu  sync.Mutex
	seq uint64
}

func NewConn(rw io.ReadWriteCloser) *Conn {
	return &Conn{rw: rw, rd: bufio.NewReader(rw)}
}

func (c *Conn) Close() error {
	if c == nil || c.rw == nil {
		return nil
	}
	return c.rw.Close()
}

func (c *Conn) Call(method string, params any) (json.RawMessage, error) {
	raw, err := encodeParams(params)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.seq++
	id := fmt.Sprintf("%d", c.seq)
	req := Request{ID: id, Method: method, Params: raw}
	if err := writeJSONLine(c.rw, req); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	line, err := c.rd.ReadBytes('\n')
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var res Response
	if err := json.Unmarshal(line, &res); err != nil {
		return nil, err
	}
	if res.ID != id {
		return nil, fmt.Errorf("guest reply id mismatch")
	}
	if !res.OK {
		if res.Error == "" {
			return nil, fmt.Errorf("guest method %s failed", method)
		}
		return nil, fmt.Errorf("%s", res.Error)
	}
	return res.Result, nil
}

func encodeParams(params any) (json.RawMessage, error) {
	if params == nil {
		return json.RawMessage(`{}`), nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return raw, nil
	}
	return json.Marshal(params)
}

func writeJSONLine(w io.Writer, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(raw, '\n'))
	return err
}

func okResult(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
