package agentrpc

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/qemu"
)

type sockSession struct {
	conn net.Conn
	done chan struct{}
}

func startVMConsole(ctx context.Context, req termRequest) (termSession, error) {
	if err := qemu.ValidateWorkloadID(req.TargetID); err != nil {
		return nil, err
	}
	p := filepath.Clean(strings.TrimSpace(req.JailRoot))
	prefix := filepath.Join("/var/lib/ndl/runtime/qemu", req.TargetID) + string(filepath.Separator)
	if !strings.HasPrefix(p, prefix) || strings.Contains(p, "..") {
		return nil, fmt.Errorf("console socket is invalid")
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", p)
	if err != nil {
		return nil, fmt.Errorf("console socket: %w", err)
	}
	s := &sockSession{conn: conn, done: make(chan struct{})}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	return s, nil
}

func (s *sockSession) Read(p []byte) (int, error) {
	return s.conn.Read(p)
}

func (s *sockSession) Write(p []byte) (int, error) {
	return s.conn.Write(p)
}

func (s *sockSession) Resize(uint16, uint16) error { return nil }

func (s *sockSession) CWD() (string, bool) { return "", false }

func (s *sockSession) Pong() error { return nil }

func (s *sockSession) Done() <-chan struct{} {
	return s.done
}

func (s *sockSession) Close() {
	_ = s.conn.Close()
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}
