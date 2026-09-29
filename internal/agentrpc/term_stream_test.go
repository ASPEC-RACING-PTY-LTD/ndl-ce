package agentrpc

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
)

// floodTerm emits output continuously, the way apt and docker compose do.
type floodTerm struct {
	done chan struct{}
	once sync.Once
}

func newFloodTerm() *floodTerm {
	return &floodTerm{done: make(chan struct{})}
}

func (f *floodTerm) Read(p []byte) (int, error) {
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-f.done:
		return 0, io.EOF
	case <-timer.C:
		n := copy(p, []byte("out"))
		return n, nil
	}
}

func (f *floodTerm) Write(p []byte) (int, error) { return len(p), nil }
func (f *floodTerm) Resize(uint16, uint16) error { return nil }
func (f *floodTerm) CWD() (string, bool)         { return "/root", true }
func (f *floodTerm) Pong() error                 { return nil }
func (f *floodTerm) Done() <-chan struct{}       { return f.done }
func (f *floodTerm) Close()                      { f.once.Do(func() { close(f.done) }) }

// overlapStream fails if two Sends run at once. connect StreamingHandlerConn
// Send is not safe for concurrent use; overlapping writes corrupt the HTTP/2
// stream and the control plane then closes the browser socket.
type overlapStream struct {
	ctx      context.Context
	mu       sync.Mutex
	in       int
	overlaps int
}

func (s *overlapStream) Send(*agentv1.TermFrame) error {
	s.mu.Lock()
	s.in++
	if s.in > 1 {
		s.overlaps++
	}
	s.mu.Unlock()
	time.Sleep(5 * time.Millisecond)
	s.mu.Lock()
	s.in--
	s.mu.Unlock()
	return nil
}

func (s *overlapStream) Receive() (*agentv1.TermFrame, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s *overlapStream) Overlaps() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overlaps
}

func TestTerminalOutputDoesNotRaceCWDOnTheAgentStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	sess := newFloodTerm()
	defer sess.Close()
	stream := &overlapStream{ctx: ctx}
	meta := &agentv1.TermFrame{TargetKind: "workload", TargetId: "ct", JailRoot: "/jail", Cwd: "/"}
	_ = serveTerm(ctx, stream, sess, meta, "/jail", time.Millisecond)
	if n := stream.Overlaps(); n > 0 {
		t.Fatalf("terminal output overlapped cwd or pong sends %d times; that drops the connection during apt and docker compose", n)
	}
}
