package guest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

const defaultDialTimeout = 800 * time.Millisecond

// Dial connects to a nodal guest unix socket.
func Dial(ctx context.Context, path string) (*Conn, error) {
	if path == "" {
		return nil, fmt.Errorf("guest socket path is required")
	}
	d := net.Dialer{Timeout: defaultDialTimeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	return NewConn(conn), nil
}

func (c *Conn) CallTimeout(method string, params any, d time.Duration) (json.RawMessage, error) {
	if nc, ok := c.rw.(net.Conn); ok && d > 0 {
		_ = nc.SetDeadline(time.Now().Add(d))
		defer func() { _ = nc.SetDeadline(time.Time{}) }()
	}
	return c.Call(method, params)
}

// Probe reports honest nodal_ga state for one socket.
func Probe(ctx context.Context, path string) ChannelState {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ChannelState{State: StateUnavailable, Reason: "guest channel socket is missing"}
		}
		return ChannelState{State: StateUnavailable, Reason: err.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()
	c, err := Dial(ctx, path)
	if err != nil {
		return ChannelState{State: StateNotInstalled, Reason: "nodal guest is not connected"}
	}
	defer c.Close()
	raw, err := c.CallTimeout(MethodInfo, nil, defaultDialTimeout)
	if err != nil {
		return ChannelState{State: StateNotInstalled, Reason: err.Error()}
	}
	var info Info
	_ = json.Unmarshal(raw, &info)
	st := ChannelState{State: StateOK, Version: info.Version}
	if st.Version == "" {
		st.Version = Version
	}
	return st
}

// ProbeQGA pings qemu-ga over its unix chardev. Missing is not healthy.
func ProbeQGA(ctx context.Context, path string) ChannelState {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return ChannelState{State: StateUnavailable, Reason: "qemu-ga socket is missing"}
		}
		return ChannelState{State: StateUnavailable, Reason: err.Error()}
	}
	d := net.Dialer{Timeout: defaultDialTimeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return ChannelState{State: StateNotInstalled, Reason: "qemu-guest-agent is not connected"}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(defaultDialTimeout))
	if _, err := conn.Write([]byte(`{"execute":"guest-ping"}` + "\n")); err != nil {
		return ChannelState{State: StateNotInstalled, Reason: err.Error()}
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return ChannelState{State: StateNotInstalled, Reason: "qemu-guest-agent did not reply"}
	}
	return ChannelState{State: StateOK, Version: "qemu-ga"}
}

// FilesOp runs a typed files action inside the guest jail.
func (c *Conn) FilesOp(action, rel, dest string, mode uint32) (json.RawMessage, error) {
	return c.Call(MethodFilesOp, FilesParams{Action: action, Path: rel, Dest: dest, Mode: mode})
}

// FilesPut writes one file as base64. Max 8 MiB.
func (c *Conn) FilesPut(rel string, mode uint32, data []byte) (json.RawMessage, error) {
	return c.Call(MethodFilesPut, FilesParams{
		Path: rel, Mode: mode, DataB64: base64.StdEncoding.EncodeToString(data),
	})
}

// FilesGet reads one file.
func (c *Conn) FilesGet(rel string) ([]byte, string, error) {
	raw, err := c.Call(MethodFilesGet, FilesParams{Path: rel})
	if err != nil {
		return nil, "", err
	}
	var out struct {
		DataB64 string `json:"data_b64"`
		SHA     string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	data, err := base64.StdEncoding.DecodeString(out.DataB64)
	if err != nil {
		return nil, "", err
	}
	return data, out.SHA, nil
}

// OpenPTY starts a guest PTY session.
func (c *Conn) OpenPTY(cwd string, cols, rows uint16) (string, error) {
	raw, err := c.Call(MethodPTYOpen, PTYParams{CWD: cwd, Cols: cols, Rows: rows})
	if err != nil {
		return "", err
	}
	var out struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if out.Session == "" {
		return "", fmt.Errorf("guest pty session is missing")
	}
	return out.Session, nil
}

func (c *Conn) PTYWrite(session string, data []byte) error {
	_, err := c.Call(MethodPTYWrite, PTYParams{
		Session: session, DataB64: base64.StdEncoding.EncodeToString(data),
	})
	return err
}

func (c *Conn) PTYRead(session string) ([]byte, bool, error) {
	raw, err := c.Call(MethodPTYRead, PTYParams{Session: session})
	if err != nil {
		return nil, false, err
	}
	var out PTYParams
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, err
	}
	data, err := base64.StdEncoding.DecodeString(out.DataB64)
	if err != nil {
		return nil, false, err
	}
	return data, out.EOF, nil
}

func (c *Conn) PTYResize(session string, cols, rows uint16) error {
	_, err := c.Call(MethodPTYResize, PTYParams{Session: session, Cols: cols, Rows: rows})
	return err
}

func (c *Conn) PTYClose(session string) error {
	_, err := c.Call(MethodPTYClose, PTYParams{Session: session})
	return err
}

func (c *Conn) Info() (Info, error) {
	raw, err := c.Call(MethodInfo, nil)
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(raw, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}
