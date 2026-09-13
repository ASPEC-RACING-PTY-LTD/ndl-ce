package agentrpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDestTCPAuthorize(t *testing.T) {
	h := &Handler{AllowDestTCP: true, AllowedUID: 1}
	if err := h.authorize(context.Background()); err == nil {
		t.Fatal("unix path must still require peer creds")
	}
	if err := h.authorize(withDestTCP(context.Background())); err != nil {
		t.Fatalf("dest TCP must be authorized when enabled: %v", err)
	}
	h.AllowDestTCP = false
	if err := h.authorize(withDestTCP(context.Background())); err == nil {
		t.Fatal("dest TCP must stay closed when AllowDestTCP is false")
	}
}

func TestDestTCPListenAddr(t *testing.T) {
	t.Setenv("NODAL_AGENT_TCP_LISTEN", "off")
	if destTCPListenAddr() != "" {
		t.Fatal("off must disable dest TCP")
	}
	t.Setenv("NODAL_AGENT_TCP_LISTEN", "127.0.0.1:19444")
	if destTCPListenAddr() != "127.0.0.1:19444" {
		t.Fatalf("got %q", destTCPListenAddr())
	}
	t.Setenv("NODAL_AGENT_TCP_LISTEN", "")
	dir := t.TempDir()
	t.Setenv("NODAL_DATA_DIR", dir)
	if destTCPListenAddr() != "" {
		t.Fatal("control without join material must not listen dest TCP")
	}
	if err := os.WriteFile(filepath.Join(dir, "node.crt"), []byte("CERT"), 0640); err != nil {
		t.Fatal(err)
	}
	if destTCPListenAddr() != defaultDestTCPListen {
		t.Fatalf("joined worker default %q", destTCPListenAddr())
	}
}
