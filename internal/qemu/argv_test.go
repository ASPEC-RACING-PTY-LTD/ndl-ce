package qemu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompilePinsMachineAndVolumeHandle(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	argv, err := e.compile(Spec{
		WorkloadID:  "11111111-1111-1111-1111-111111111111",
		DiskPath:    "/var/lib/ndl/storage/p/volumes/vm-disk/11111111-1111-1111-1111-111111111111.qcow2",
		DiskFormat:  "qcow2",
		Machine:     DefaultMachine,
		Accel:       "tcg",
		MemoryBytes: DefaultMemory,
		CPUs:        1,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != BinQEMU {
		t.Fatal(argv[0])
	}
	if strings.Contains(joined, " -machine q35") || strings.Contains(joined, "q35,") && !strings.Contains(joined, "pc-q35-") {
		t.Fatalf("alias machine: %s", joined)
	}
	if !strings.Contains(joined, DefaultMachine) {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "node-name=disk0") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, GuestAgentName) {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "mode=control") {
		t.Fatal("qmp control monitor required")
	}
	if strings.Contains(joined, "monitor stdio") || strings.Contains(joined, "human") {
		t.Fatal("human monitor is forbidden")
	}
}

func TestCompileRejectsMachineAlias(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	id := "11111111-1111-1111-1111-111111111111"
	if _, err := e.compile(Spec{WorkloadID: id, DiskPath: "/var/lib/ndl/storage/p/d.qcow2", Machine: "q35", Accel: "tcg"}); err == nil {
		t.Fatal("q35 alias must fail")
	}
}

func TestCompileRejectsDiskEscape(t *testing.T) {
	e := &Engine{DataDir: t.TempDir()}
	id := "11111111-1111-1111-1111-111111111111"
	if _, err := e.compile(Spec{WorkloadID: id, DiskPath: "/var/lib/ndl/storage/p/../etc/passwd", Machine: DefaultMachine, Accel: "tcg"}); err == nil {
		t.Fatal("escape must fail")
	}
}

func TestPrepareWritesFrozenArgv(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	id := "22222222-2222-2222-2222-222222222222"
	res, err := e.Prepare(Spec{
		WorkloadID: id,
		VolumeID:   "vol",
		DiskPath:   "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2",
		Accel:      "tcg",
		Machine:    DefaultMachine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Machine != DefaultMachine {
		t.Fatal(res.Machine)
	}
	raw, err := os.ReadFile(e.argvPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), BinQEMU) {
		t.Fatalf("%s", raw)
	}
	applied, err := e.ReadApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Spec.PCIDiskAddr != "0x5" || applied.Spec.PCISerialAddr != "0x6" {
		t.Fatalf("%+v", applied.Spec)
	}
	if _, err := os.Stat(filepath.Join(root, "runtime", "qemu", id)); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAccelHonest(t *testing.T) {
	got := DetectAccel()
	if got != "kvm" && got != "tcg" {
		t.Fatal(got)
	}
	if _, err := os.Stat("/dev/kvm"); err != nil && got != "tcg" {
		t.Fatal("missing /dev/kvm must be tcg")
	}
}
