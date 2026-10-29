package agentrpc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDialCloudflareCarriesBytesThroughCloudflared(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "cloudflared")
	// Echoes stdin, and records its arguments and Access token.
	script := "#!/bin/sh\necho \"$@ $TUNNEL_SERVICE_TOKEN_ID\" > " + filepath.Join(dir, "args") + "\nexec cat\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPaths, oldAccess := cloudflaredPaths, CloudflareAccess
	defer func() { cloudflaredPaths, CloudflareAccess = oldPaths, oldAccess }()
	cloudflaredPaths = []string{fake}
	CloudflareAccess = func() (string, string) { return "client-id", "client-secret" }

	conn, err := dialAgent(context.Background(), "tcp", "cloudflare:agent.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo through cloudflared: %q %v", buf, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "access tcp --hostname agent.example.com") || !strings.Contains(string(args), "client-id") {
		t.Fatalf("cloudflared was run with %q", args)
	}
	if strings.Contains(string(args), "client-secret") {
		t.Fatal("the Access secret must not be on the command line")
	}
}

func TestDialCloudflareWithoutBinary(t *testing.T) {
	old := cloudflaredPaths
	defer func() { cloudflaredPaths = old }()
	cloudflaredPaths = []string{"/nonexistent/cloudflared"}
	if _, err := dialAgent(context.Background(), "tcp", "cloudflare:agent.example.com"); err != errNoCloudflared {
		t.Fatalf("want errNoCloudflared, got %v", err)
	}
}
