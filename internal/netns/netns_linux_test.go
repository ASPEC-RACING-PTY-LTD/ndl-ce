//go:build linux

package netns

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

// enter skips the test when this process may not switch namespaces.
func enter(t *testing.T) {
	t.Helper()
	if err := Do(os.Getpid(), func() error { return nil }); err != nil {
		t.Skipf("cannot switch network namespace here: %v", err)
	}
}

func TestDoNeverRunsOnTheMainThread(t *testing.T) {
	enter(t)
	var wg sync.WaitGroup
	var onMain atomic.Int32
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Do(os.Getpid(), func() error {
				if unix.Gettid() == unix.Getpid() {
					onMain.Add(1)
				}
				return nil
			})
		}()
	}
	wg.Wait()
	if n := onMain.Load(); n != 0 {
		t.Fatalf("%d runs switched the main thread", n)
	}
}

// When the goroutine Do starts lands on the main thread, the work moves to
// another thread.
func TestDoMovesOffTheMainThread(t *testing.T) {
	enter(t)
	var calls atomic.Int32
	var outer atomic.Int64
	old := onMainThread
	onMainThread = func() bool {
		if calls.Add(1) == 1 {
			outer.Store(int64(unix.Gettid()))
			return true
		}
		return false
	}
	defer func() { onMainThread = old }()
	var ran int
	err := Do(os.Getpid(), func() error {
		ran = unix.Gettid()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if ran == 0 || int64(ran) == outer.Load() {
		t.Fatalf("work ran on the thread taken for the main thread (tid %d)", ran)
	}
}

func TestDoRefusesWhenEveryThreadLooksLikeTheMainThread(t *testing.T) {
	old := onMainThread
	onMainThread = func() bool { return true }
	defer func() { onMainThread = old }()
	ran := false
	err := Do(os.Getpid(), func() error { ran = true; return nil })
	if err == nil || ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

func TestDoReturnsFnError(t *testing.T) {
	enter(t)
	want := errors.New("boom")
	if err := Do(os.Getpid(), func() error { return want }); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
}

func TestDoRejectsNoProcess(t *testing.T) {
	if err := Do(0, func() error { return nil }); err == nil {
		t.Fatal("pid 0 must be refused")
	}
}
