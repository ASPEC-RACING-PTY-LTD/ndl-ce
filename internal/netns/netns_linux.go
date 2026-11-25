//go:build linux

// Package netns runs code inside another process's network namespace
// without disturbing the rest of the process.
//
// Switching a thread's network namespace is per thread, but /proc/net (a
// link to /proc/self/net) shows the namespace of the process's main
// thread. If the main thread is the one switched, every other goroutine
// that reads /proc/net/route, /proc/net/dev and so on reads the
// container's tables instead of the host's. The agent once rebuilt the
// host's management bridge from such a reading and dropped its default
// gateway. So Do never switches the main thread.
package netns

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// onMainThread reports whether the calling (locked) goroutine runs on the
// process's main thread; tests replace it.
var onMainThread = func() bool { return unix.Gettid() == unix.Getpid() }

// Do runs fn on an OS thread switched into the network namespace of
// process pid, and switches it back. The thread is never the process's
// main thread. If it cannot be switched back it stays locked, so Go ends
// it with its goroutine instead of reusing it.
func Do(pid int, fn func() error) error {
	if pid <= 0 {
		return fmt.Errorf("no process to enter")
	}
	target, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", pid))
	if err != nil {
		return err
	}
	defer target.Close()
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if onMainThread() {
			// Hold the main thread so the goroutine below gets another
			// one, and let the main thread go once that is done.
			errc <- onOtherThread(target, fn)
			runtime.UnlockOSThread()
			return
		}
		res, restored := switched(target, fn)
		if restored {
			runtime.UnlockOSThread()
		}
		errc <- res
	}()
	return <-errc
}

// onOtherThread runs fn on a new locked goroutine; the caller holds the
// main thread, so this one cannot be scheduled on it.
func onOtherThread(target *os.File, fn func() error) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		if onMainThread() {
			errc <- fmt.Errorf("refusing to switch the main thread's network namespace")
			runtime.UnlockOSThread()
			return
		}
		res, restored := switched(target, fn)
		if restored {
			runtime.UnlockOSThread()
		}
		errc <- res
	}()
	return <-errc
}

// switched runs fn with the calling locked thread in target's namespace.
// restored is false when the thread could not be switched back.
func switched(target *os.File, fn func() error) (res error, restored bool) {
	self, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		return err, true
	}
	defer self.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		return fmt.Errorf("enter network namespace: %w", err), true
	}
	res = fn()
	if err := unix.Setns(int(self.Fd()), unix.CLONE_NEWNET); err != nil {
		return res, false
	}
	return res, true
}
