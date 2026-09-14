package qemu

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQMPNegotiateAndStatus(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "qmp.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(`{"QMP":{"version":{"qemu":{"major":10}}}}` + "\n"))
		buf := make([]byte, 256)
		n, _ := c.Read(buf)
		var req map[string]any
		_ = json.Unmarshal(buf[:n], &req)
		if req["execute"] != "qmp_capabilities" {
			errCh <- err
			return
		}
		_, _ = c.Write([]byte(`{"return":{}}` + "\n"))
		n, _ = c.Read(buf)
		_ = json.Unmarshal(buf[:n], &req)
		if req["execute"] != "query-status" {
			errCh <- err
			return
		}
		_, _ = c.Write([]byte(`{"return":{"status":"running"}}` + "\n"))
		errCh <- nil
	}()
	e := &Engine{DataDir: dir}
	// point qmp path by using runtime layout
	id := "qmp"
	if err := os.MkdirAll(e.runtimeDir(id), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(e.qmpPath(id)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(sock, e.qmpPath(id)); err != nil {
		// Windows or if symlink fails, listen directly on engine path
		_ = ln.Close()
		ln2, err := net.Listen("unix", e.qmpPath(id))
		if err != nil {
			t.Skip(err)
		}
		defer ln2.Close()
		go func() {
			c, err := ln2.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			_, _ = c.Write([]byte(`{"QMP":{"version":{}}}` + "\n"))
			buf := make([]byte, 256)
			_, _ = c.Read(buf)
			_, _ = c.Write([]byte(`{"return":{}}` + "\n"))
			_, _ = c.Read(buf)
			_, _ = c.Write([]byte(`{"return":{"status":"running"}}` + "\n"))
		}()
		if err := e.ReconnectQMP(id); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := e.ReconnectQMP(id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
	}
}
