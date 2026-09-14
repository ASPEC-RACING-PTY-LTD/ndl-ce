package control

import (
	"context"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
)

const (
	writerLeaseTTL   = 30 * time.Second
	writerLeaseRenew = 10 * time.Second
)

// writerLease is the in-process writer lock. HolderID is the ownership
// proof: hostname, pid, and a random token. Only that holder may
// renew or relinquish the row.
type writerLease struct {
	store  appdb.Store
	holder string
	ca     cluster.CA
	ttl    time.Duration
	renew  time.Duration

	mu        sync.Mutex
	clusterID string
	stopRenew chan struct{}
	renewDone chan struct{}
	renewing  bool
}

func newWriterLease(store appdb.Store, holder string, ca cluster.CA) *writerLease {
	return &writerLease{
		store:  store,
		holder: holder,
		ca:     ca,
		ttl:    writerLeaseTTL,
		renew:  writerLeaseRenew,
	}
}

func (w *writerLease) acquire(ctx context.Context) error {
	c, err := w.store.GetCluster(ctx)
	if err != nil {
		return err
	}
	if c == nil {
		return nil
	}
	w.setClusterID(c.ID)
	return w.store.AcquireLease(ctx, c.ID, w.holder, time.Now().UTC().Add(w.ttl))
}

func (w *writerLease) startRenewal() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.renewing {
		return
	}
	w.stopRenew = make(chan struct{})
	w.renewDone = make(chan struct{})
	w.renewing = true
	stop := w.stopRenew
	done := w.renewDone
	go w.renewLoop(stop, done)
}

func (w *writerLease) renewLoop(stop <-chan struct{}, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(w.renew)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			w.renewOnce(context.Background())
		}
	}
}

func (w *writerLease) renewOnce(ctx context.Context) {
	c, err := w.store.GetCluster(ctx)
	if err != nil || c == nil {
		return
	}
	w.setClusterID(c.ID)
	_ = w.store.AcquireLease(ctx, c.ID, w.holder, time.Now().UTC().Add(w.ttl))
	_ = w.ca.Ensure(time.Now().UTC())
}

func (w *writerLease) stopRenewal() {
	w.mu.Lock()
	if !w.renewing {
		w.mu.Unlock()
		return
	}
	stop := w.stopRenew
	done := w.renewDone
	w.renewing = false
	w.mu.Unlock()
	select {
	case <-stop:
	default:
		close(stop)
	}
	if done != nil {
		<-done
	}
}

// relinquish stops renewal, then deletes the lease only if this
// process still owns it. A foreign holder is left untouched.
func (w *writerLease) relinquish(ctx context.Context) error {
	w.stopRenewal()
	id := w.getClusterID()
	if id == "" {
		c, err := w.store.GetCluster(ctx)
		if err != nil {
			return err
		}
		if c == nil {
			return nil
		}
		id = c.ID
	}
	return w.store.ReleaseLease(ctx, id, w.holder)
}

func (w *writerLease) setClusterID(id string) {
	w.mu.Lock()
	w.clusterID = id
	w.mu.Unlock()
}

func (w *writerLease) getClusterID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clusterID
}
