package control

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
)

func testLease(t *testing.T, holder string) (*appdb.Memory, string, *writerLease) {
	t.Helper()
	mem := appdb.NewMemory()
	clusterID := uuid.NewString()
	if err := mem.CreateCluster(context.Background(), appdb.Cluster{ID: clusterID, Name: "local"}); err != nil {
		t.Fatal(err)
	}
	lease := newWriterLease(mem, holder, cluster.CA{Dir: t.TempDir()})
	lease.renew = 20 * time.Millisecond
	return mem, clusterID, lease
}

func TestCleanSIGTERMReleasesOwnedLease(t *testing.T) {
	mem, clusterID, lease := testLease(t, "holder-sigterm")
	if err := lease.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease.startRenewal()

	ctx, stop := shutdownSignals()
	t.Cleanup(stop)
	done := make(chan error, 1)
	go func() {
		<-ctx.Done()
		done <- lease.relinquish(context.Background())
	}()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM must run the graceful shutdown path")
	}
	got, err := mem.GetClusterLease(context.Background(), clusterID)
	if err != nil || got != nil {
		t.Fatalf("owned lease must be gone after SIGTERM: %+v %v", got, err)
	}
}

func TestImmediateRestartAfterCleanShutdown(t *testing.T) {
	mem, _, old := testLease(t, "holder-old")
	if err := old.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	old.startRenewal()
	if err := old.relinquish(context.Background()); err != nil {
		t.Fatal(err)
	}
	replacement := newWriterLease(mem, "holder-new", cluster.CA{Dir: t.TempDir()})
	if err := replacement.acquire(context.Background()); err != nil {
		t.Fatalf("restart after clean shutdown must not wait for TTL: %v", err)
	}
}

func TestCannotReleaseAnotherWriterLease(t *testing.T) {
	mem, clusterID, owner := testLease(t, "writer-a")
	if err := owner.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.startRenewal()
	t.Cleanup(func() { _ = owner.relinquish(context.Background()) })

	other := newWriterLease(mem, "writer-b", cluster.CA{Dir: t.TempDir()})
	if err := other.relinquish(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := mem.GetClusterLease(context.Background(), clusterID)
	if err != nil || got == nil || got.HolderID != "writer-a" {
		t.Fatalf("foreign relinquish must leave the owner lease: %+v %v", got, err)
	}
	if err := other.acquire(context.Background()); err != appdb.ErrLeaseHeld {
		t.Fatalf("second writer after foreign release: %v", err)
	}
}

func TestCrashedWriterLeavesLeaseUntilExpiry(t *testing.T) {
	mem, clusterID, crashed := testLease(t, "writer-crash")
	crashed.ttl = 60 * time.Millisecond
	if err := crashed.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	crashed.startRenewal()
	crashed.stopRenewal()

	standby := newWriterLease(mem, "writer-standby", cluster.CA{Dir: t.TempDir()})
	if err := standby.acquire(context.Background()); err != appdb.ErrLeaseHeld {
		t.Fatalf("crashed writer must keep the lease until expiry: %v", err)
	}
	got, _ := mem.GetClusterLease(context.Background(), clusterID)
	if got == nil || got.HolderID != "writer-crash" {
		t.Fatalf("crash must not drop the lease: %+v", got)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := standby.acquire(context.Background()); err == nil {
			got, _ = mem.GetClusterLease(context.Background(), clusterID)
			if got == nil || got.HolderID != "writer-standby" {
				t.Fatalf("takeover after expiry: %+v", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expired crashed-writer lease must become acquirable")
}

func TestConcurrentSecondWriterRemainsRejected(t *testing.T) {
	mem, _, owner := testLease(t, "writer-a")
	if err := owner.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.startRenewal()
	t.Cleanup(func() { _ = owner.relinquish(context.Background()) })

	errc := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(i int) {
			challenger := newWriterLease(mem, fmt.Sprintf("writer-b-%d", i), cluster.CA{Dir: t.TempDir()})
			errc <- challenger.acquire(context.Background())
		}(i)
	}
	for i := 0; i < 8; i++ {
		if err := <-errc; err != appdb.ErrLeaseHeld {
			t.Fatalf("concurrent writer %d: %v", i, err)
		}
	}
}

func TestRepeatedSystemdStyleRestartCycles(t *testing.T) {
	mem := appdb.NewMemory()
	clusterID := uuid.NewString()
	if err := mem.CreateCluster(context.Background(), appdb.Cluster{ID: clusterID, Name: "local"}); err != nil {
		t.Fatal(err)
	}
	caDir := t.TempDir()
	for i := 0; i < 8; i++ {
		holder := fmt.Sprintf("cycle-%d-%s", i, uuid.NewString())
		lease := newWriterLease(mem, holder, cluster.CA{Dir: caDir})
		if err := lease.acquire(context.Background()); err != nil {
			t.Fatalf("cycle %d acquire: %v", i, err)
		}
		lease.startRenewal()
		if err := lease.relinquish(context.Background()); err != nil {
			t.Fatalf("cycle %d release: %v", i, err)
		}
		got, err := mem.GetClusterLease(context.Background(), clusterID)
		if err != nil || got != nil {
			t.Fatalf("cycle %d leftover lease %+v %v", i, got, err)
		}
	}
}

func TestPackageUpgradeStyleStopStart(t *testing.T) {
	mem, _, outgoing := testLease(t, "pkg-old")
	if err := outgoing.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	outgoing.startRenewal()
	if err := outgoing.relinquish(context.Background()); err != nil {
		t.Fatal(err)
	}

	time.Sleep(200 * time.Millisecond)
	incoming := newWriterLease(mem, "pkg-new", cluster.CA{Dir: t.TempDir()})
	if err := incoming.acquire(context.Background()); err != nil {
		t.Fatalf("package-upgrade replacement must acquire immediately: %v", err)
	}
}

func TestServeHTTPServersStopsOnCancel(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	inst := &httpInstance{
		name: "http",
		srv:  srv,
		serve: func() error {
			return srv.Serve(ln)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- serveHTTPServers(ctx, []*httpInstance{inst}) }()
	url := "http://" + ln.Addr().String() + "/"
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, getErr := http.Get(url)
		if getErr == nil {
			_ = res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("server did not start: %v", getErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("graceful serve stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP shutdown did not complete")
	}
}
