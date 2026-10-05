package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readCtx() context.Context {
	return context.WithValue(context.Background(), readRequestKey{}, true)
}

func TestRefreshGateSharesConcurrentReads(t *testing.T) {
	var g refreshGate
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	fn := func(context.Context) {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.run(readCtx(), "k", fn)
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent reads must share one observation, got %d", got)
	}
	g.run(readCtx(), "k", fn)
	if got := calls.Load(); got != 1 {
		t.Fatalf("a fresh observation must be reused, got %d", got)
	}
}

func TestRefreshGateWriteInvalidates(t *testing.T) {
	var g refreshGate
	var calls atomic.Int32
	fn := func(context.Context) { calls.Add(1) }
	g.run(readCtx(), "k", fn)
	g.invalidate()
	g.run(readCtx(), "k", fn)
	if got := calls.Load(); got != 2 {
		t.Fatalf("a read after a write must observe again, got %d", got)
	}
}

func TestRefreshGateNonReadAlwaysRuns(t *testing.T) {
	var g refreshGate
	var calls atomic.Int32
	fn := func(context.Context) { calls.Add(1) }
	g.run(readCtx(), "k", fn)
	g.run(context.Background(), "k", fn)
	g.run(context.Background(), "k", fn)
	if got := calls.Load(); got != 3 {
		t.Fatalf("writes must not reuse a read observation, got %d", got)
	}
}

func TestRefreshGateSharedRunOutlivesFirstCaller(t *testing.T) {
	var g refreshGate
	ctx, cancel := context.WithCancel(readCtx())
	cancel()
	var sawErr error
	g.run(ctx, "k", func(c context.Context) { sawErr = c.Err() })
	if sawErr != nil {
		t.Fatalf("shared observation must not inherit the caller's cancellation: %v", sawErr)
	}
}

func TestMarkReadRequestsInvalidatesAfterWrites(t *testing.T) {
	s := &Server{}
	var reads []bool
	h := s.markReadRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads = append(reads, isReadRequest(r.Context()))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/workloads", nil))
	before := s.refresh.epoch
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/workloads/x/start", nil))
	if s.refresh.epoch == before {
		t.Fatal("a write must invalidate shared observations")
	}
	if len(reads) != 2 || !reads[0] || reads[1] {
		t.Fatalf("only GET is a read request: %v", reads)
	}
}
