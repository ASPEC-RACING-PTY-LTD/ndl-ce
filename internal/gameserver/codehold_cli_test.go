package gameserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodeholdCLIPrefersDownloadThenHost(t *testing.T) {
	root := t.TempDir()
	host := filepath.Join(root, "host-ch")
	if err := os.WriteFile(host, []byte("\x7fELF host"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := NewRuntime(root)
	rt.HostCH = host
	// No CodeHold address: the host's ch is passed through.
	if got := rt.codeholdCLI(context.Background()); got != host {
		t.Fatalf("got %q, want the host ch", got)
	}
	// A kept download wins over the host's ch.
	cached := filepath.Join(root, "tools", "ch")
	_ = os.MkdirAll(filepath.Dir(cached), 0o755)
	if err := os.WriteFile(cached, []byte("\x7fELF kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := rt.codeholdCLI(context.Background()); got != cached {
		t.Fatalf("got %q, want the downloaded ch", got)
	}
	// Something that is not an executable is never mounted.
	_ = os.WriteFile(cached, []byte("<html>"), 0o644)
	_ = os.WriteFile(host, []byte("#!/bin/sh"), 0o755)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(cached, old, old)
	if got := rt.codeholdCLI(context.Background()); got != "" {
		t.Fatalf("got %q, want none", got)
	}
}

func TestStartMountsCodeholdCLI(t *testing.T) {
	root := t.TempDir()
	host := filepath.Join(root, "host-ch")
	_ = os.WriteFile(host, []byte("\x7fELF"), 0o755)
	rt := NewRuntime(root)
	rt.HostCH = host
	var run string
	rt.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "run" {
			run = strings.Join(args, " ")
		}
		return []byte("cid"), nil
	}
	if _, err := rt.Start(context.Background(), RunSpec{ID: "abc", Image: "debian:bookworm-slim", Startup: "true"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run, "-v "+host+":/usr/local/bin/ch:ro") {
		t.Fatalf("ch is not mounted read-only: %s", run)
	}
}
