package agentrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/guest"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

const guestJailRoot = guest.JailRoot

func (h *Handler) execVMGuest(ctx context.Context, msg *agentv1.VMGuest) (*connect.Response[agentv1.ExecuteResponse], error) {
	id := strings.TrimSpace(msg.GetWorkloadId())
	if err := qemu.ValidateWorkloadID(id); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	switch strings.ToLower(strings.TrimSpace(msg.GetAction())) {
	case "", "status":
		st := h.guestStatus(ctx, id)
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: st.NodalGA.State, ResultJson: mustJSON(st)}), nil
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown guest action %q", msg.GetAction()))
	}
}

func (h *Handler) guestStatus(ctx context.Context, id string) guest.Status {
	now := time.Now().UTC()
	st := guest.Status{
		WorkloadID: id,
		QEMUGA:     guest.ChannelState{State: guest.StateUnavailable, Reason: "vm is stopped"},
		NodalGA:    guest.ChannelState{State: guest.StateUnavailable, Reason: "vm is stopped"},
		ObservedAt: now,
	}
	running := false
	if h.QEMU != nil {
		obs := h.QEMU.Observe(ctx, id)
		running = obs.UnitActive || obs.Status == qemu.StatusRunning
		if !running && obs.Status != qemu.StatusStopped && obs.Status != qemu.StatusUnavailable {
			running = false
		}
	}
	qga := h.qgaSocket(id)
	nga := h.guestSocket(id)
	if !running {
		if h.GuestSocketFn != nil {
			ngaSt := guest.Probe(ctx, nga)
			st.NodalGA = ngaSt
			if ngaSt.State == guest.StateOK {
				st.QEMUGA = guest.ChannelState{State: guest.StateNotInstalled, Reason: "fixture guest channel only"}
				h.fillGuestIdentity(ctx, nga, &st)
			}
		}
		return st
	}
	st.QEMUGA = guest.ProbeQGA(ctx, qga)
	st.NodalGA = guest.Probe(ctx, nga)
	if st.NodalGA.State == guest.StateOK {
		h.fillGuestIdentity(ctx, nga, &st)
	}
	return st
}

func (h *Handler) fillGuestIdentity(ctx context.Context, sock string, st *guest.Status) {
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	c, err := guest.Dial(ctx, sock)
	if err != nil {
		return
	}
	defer c.Close()
	info, err := c.Info()
	if err == nil {
		st.GuestOS = info.OS
		st.GuestArch = info.Arch
		if st.NodalGA.Version == "" {
			st.NodalGA.Version = info.Version
		}
	}
	raw, err := c.Call(guest.MethodNetwork, nil)
	if err != nil {
		return
	}
	var net struct {
		IPv4 []string `json:"ipv4"`
	}
	if json.Unmarshal(raw, &net) == nil {
		st.IPv4 = net.IPv4
	}
}

func (h *Handler) guestSocket(id string) string {
	if h.GuestSocketFn != nil {
		return h.GuestSocketFn(id)
	}
	if h.QEMU != nil {
		return h.QEMU.GuestPath(id)
	}
	p, _ := (&qemu.Engine{}).GuestSocket(id)
	return p
}

func (h *Handler) qgaSocket(id string) string {
	if h.QGASocketFn != nil {
		return h.QGASocketFn(id)
	}
	if h.QEMU != nil {
		return h.QEMU.QGAPath(id)
	}
	return "/var/lib/ndl/runtime/qemu/" + id + "/qga.sock"
}

func (h *Handler) guestFilesOp(ctx context.Context, id, action, rel, dest string, mode uint32) ([]byte, error) {
	c, err := h.dialGuest(ctx, id)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.FilesOp(action, rel, dest, mode)
}

func (h *Handler) guestFilesPut(ctx context.Context, id, rel string, mode uint32, data []byte) ([]byte, error) {
	c, err := h.dialGuest(ctx, id)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.FilesPut(rel, mode, data)
}

func (h *Handler) guestFilesGet(ctx context.Context, id, rel string) ([]byte, string, error) {
	c, err := h.dialGuest(ctx, id)
	if err != nil {
		return nil, "", err
	}
	defer c.Close()
	return c.FilesGet(rel)
}

func (h *Handler) dialGuest(ctx context.Context, id string) (*guest.Conn, error) {
	if err := qemu.ValidateWorkloadID(id); err != nil {
		return nil, err
	}
	c, err := guest.Dial(ctx, h.guestSocket(id))
	if err != nil {
		return nil, fmt.Errorf("nodal guest is not connected: %w", err)
	}
	return c, nil
}

func isGuestJail(root string) bool {
	r := strings.TrimSpace(root)
	return r == guestJailRoot || r == "guest" || strings.HasPrefix(r, "guest:")
}

type guestPTY struct {
	conn    *guest.Conn
	session string
	cwd     string
	done    chan struct{}
	pending []byte
}

func startGuestPTY(ctx context.Context, h *Handler, req termRequest) (termSession, error) {
	c, err := h.dialGuest(ctx, req.TargetID)
	if err != nil {
		return nil, err
	}
	sess, err := c.OpenPTY(req.CWD, 80, 24)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = "/"
	}
	s := &guestPTY{conn: c, session: sess, cwd: cwd, done: make(chan struct{})}
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	return s, nil
}

func (s *guestPTY) Read(p []byte) (int, error) {
	if len(s.pending) > 0 {
		n := copy(p, s.pending)
		s.pending = s.pending[n:]
		return n, nil
	}
	data, eof, err := s.conn.PTYRead(s.session)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		if eof {
			return 0, io.EOF
		}
		time.Sleep(20 * time.Millisecond)
		return 0, nil
	}
	n := copy(p, data)
	if n < len(data) {
		s.pending = append([]byte(nil), data[n:]...)
	}
	return n, nil
}

func (s *guestPTY) Write(p []byte) (int, error) {
	if err := s.conn.PTYWrite(s.session, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *guestPTY) Resize(rows, cols uint16) error {
	return s.conn.PTYResize(s.session, cols, rows)
}

func (s *guestPTY) CWD() (string, bool) {
	if strings.TrimSpace(s.cwd) == "" {
		return "/", true
	}
	return s.cwd, true
}

func (s *guestPTY) Pong() error { return nil }

func (s *guestPTY) Done() <-chan struct{} { return s.done }

func (s *guestPTY) Close() {
	if s.conn != nil {
		_ = s.conn.PTYClose(s.session)
		_ = s.conn.Close()
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}
