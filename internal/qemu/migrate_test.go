package qemu

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
)

func TestCompileIncomingIsSourceABIPlusDefer(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + argvSecWorkloadID + ".qcow2"
	src, err := e.compile(argvSecSpec(disk, "qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	destSpec := argvSecSpec(disk, "qcow2")
	destSpec.IncomingDefer = true
	dest, err := e.compile(destSpec)
	if err != nil {
		t.Fatal(err)
	}
	if !SameABI(src, dest) {
		t.Fatalf("dest must match source ABI plus incoming defer\nsrc=%q\ndest=%q", src, dest)
	}
	if !strings.Contains(strings.Join(dest, " "), "-incoming "+IncomingDefer) {
		t.Fatal("dest must wait with incoming defer")
	}
	if strings.Contains(strings.Join(src, " "), "-incoming") {
		t.Fatal("source must not listen for incoming")
	}
}

func TestCPUHostDoesNotLiveMigrate(t *testing.T) {
	ready, blockers := MigrateReadiness([]string{BinQEMU, "-cpu", "host", "-mon", "chardev=qmp0,mode=control"})
	if ready || len(blockers) == 0 {
		t.Fatal("cpu host must block live migrate")
	}
	ready, blockers = MigrateReadiness([]string{BinQEMU, "-cpu", "qemu64", "-mon", "chardev=qmp0,mode=control"})
	if !ready || len(blockers) != 0 {
		t.Fatalf("qemu64 must be live-migratable: ready=%v blockers=%v", ready, blockers)
	}
}

func TestLiveMigrateFailureLeavesSourceRunning(t *testing.T) {
	id := argvSecWorkloadID
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{id: true}, FailLiveMigrate: true}
	err := e.LiveMigrate(t.Context(), id, e.IncomingURI(id))
	if err == nil {
		t.Fatal("injected live migrate failure")
	}
	if !strings.Contains(err.Error(), "source remains running") {
		t.Fatalf("error must say source remains running: %v", err)
	}
	if !e.LiveUnits[id] {
		t.Fatal("failed live migrate must not stop the source unit")
	}
}

func TestLiveMigrateSkipHostCmdsStopsSourceOnSuccess(t *testing.T) {
	id := argvSecWorkloadID
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{id: true}}
	if err := e.LiveMigrate(t.Context(), id, e.IncomingURI(id)); err != nil {
		t.Fatal(err)
	}
	if e.LiveUnits[id] {
		t.Fatal("successful fake live migrate stops the source unit")
	}
}

func TestAbortIncomingDoesNotRequireSource(t *testing.T) {
	id := argvSecWorkloadID
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true, LiveUnits: map[string]bool{id: true}}
	if err := e.AbortIncoming(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

func TestQMPMigrateCompleted(t *testing.T) {
	id := argvSecWorkloadID
	e := &Engine{DataDir: t.TempDir()}
	if err := os.MkdirAll(e.runtimeDir(id), 0o750); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", e.qmpPath(id))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write([]byte(`{"QMP":{"version":{}}}` + "\n"))
		buf := make([]byte, 512)
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte(`{"return":{}}` + "\n"))
		n, _ := c.Read(buf)
		var req map[string]any
		_ = json.Unmarshal(buf[:n], &req)
		if req["execute"] != "migrate" {
			return
		}
		_, _ = c.Write([]byte(`{"return":{}}` + "\n"))
		_, _ = c.Read(buf)
		_, _ = c.Write([]byte(`{"return":{"status":"completed"}}` + "\n"))
	}()
	if err := e.LiveMigrate(t.Context(), id, e.IncomingURI(id)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMigrateURIRejectsTCP(t *testing.T) {
	if err := validateMigrateURI("tcp:0:4444"); err == nil {
		t.Fatal("tcp migrate must be refused")
	}
	e := &Engine{DataDir: t.TempDir()}
	if err := validateMigrateURI(e.IncomingURI(argvSecWorkloadID)); err != nil {
		t.Fatal(err)
	}
}
