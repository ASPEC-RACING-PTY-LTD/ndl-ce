package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// readRefreshFresh is how long a live observation made for one read request
// is reused by other read requests. Any completed write invalidates it, so a
// read issued after a write always observes the host again.
const readRefreshFresh = 2 * time.Second

// readRefreshTimeout bounds a shared observation. It no longer follows the
// first caller's request, so one closed browser tab cannot cancel it for
// every other waiter.
const readRefreshTimeout = 30 * time.Second

type readRequestKey struct{}

// markReadRequests flags GET and HEAD requests so the per-request host
// observations can be shared, and invalidates shared observations after
// every other request.
func (s *Server) markReadRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), readRequestKey{}, true)))
			return
		}
		next.ServeHTTP(w, r)
		if r.Method != http.MethodOptions {
			s.refresh.invalidate()
		}
	})
}

func isReadRequest(ctx context.Context) bool {
	v, _ := ctx.Value(readRequestKey{}).(bool)
	return v
}

// refreshGate shares live host observations between concurrent reads.
//
// Before it, every GET of workloads, storage or networks ran its own full
// observation on the agent (systemctl and lxc-info for every container, pool
// statfs, bridge state). Pages poll several of these every few seconds, so
// observations stacked up and every page waited behind them.
type refreshGate struct {
	mu       sync.Mutex
	epoch    uint64
	done     map[string]refreshDone
	inflight map[string]*refreshCall
}

type refreshDone struct {
	at    time.Time
	epoch uint64
}

type refreshCall struct {
	epoch uint64
	ch    chan struct{}
}

func (g *refreshGate) invalidate() {
	g.mu.Lock()
	g.epoch++
	g.mu.Unlock()
}

// run calls fn, or shares a call of fn for the same key: a read request
// reuses a result that is fresh and from the current epoch, or waits for one
// already in flight. Writes and background work always call fn themselves.
func (g *refreshGate) run(ctx context.Context, key string, fn func(context.Context)) {
	if !isReadRequest(ctx) {
		fn(ctx)
		return
	}
	g.mu.Lock()
	if g.done == nil {
		g.done = map[string]refreshDone{}
		g.inflight = map[string]*refreshCall{}
	}
	epoch := g.epoch
	if d, ok := g.done[key]; ok && d.epoch == epoch && time.Since(d.at) < readRefreshFresh {
		g.mu.Unlock()
		return
	}
	if call, ok := g.inflight[key]; ok && call.epoch == epoch {
		g.mu.Unlock()
		select {
		case <-call.ch:
		case <-ctx.Done():
		}
		return
	}
	call := &refreshCall{epoch: epoch, ch: make(chan struct{})}
	g.inflight[key] = call
	g.mu.Unlock()

	started := time.Now()
	defer func() {
		g.mu.Lock()
		if g.inflight[key] == call {
			delete(g.inflight, key)
		}
		g.done[key] = refreshDone{at: started, epoch: epoch}
		g.mu.Unlock()
		close(call.ch)
	}()
	shared, cancel := context.WithTimeout(context.WithoutCancel(ctx), readRefreshTimeout)
	defer cancel()
	fn(shared)
}
