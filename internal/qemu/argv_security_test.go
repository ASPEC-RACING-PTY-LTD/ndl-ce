package qemu

import (
	"strings"
	"testing"
)

const argvSecWorkloadID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func argvSecSpec(disk, format string) Spec {
	return Spec{
		WorkloadID:  argvSecWorkloadID,
		VolumeID:    "vol-argv-sec",
		DiskPath:    disk,
		DiskFormat:  format,
		Machine:     DefaultMachine,
		Accel:       "tcg",
		MemoryBytes: DefaultMemory,
		CPUs:        1,
	}
}

func TestValidateDiskPathRejectsCommaEqualsInjection(t *testing.T) {
	ok := "/var/lib/ndl/storage/p/volumes/vm-disk/" + argvSecWorkloadID + ".qcow2"
	if err := ValidateDiskPath(ok); err != nil {
		t.Fatal(err)
	}
	for _, disk := range []string{
		"/var/lib/ndl/storage/p/d.qcow2,driver=raw",
		"/var/lib/ndl/storage/p/d.qcow2,node-name=evil",
		"/var/lib/ndl/storage/p/d.qcow2=foo",
		"/var/lib/ndl/storage/p/file=../../../etc/passwd",
	} {
		if err := ValidateDiskPath(disk); err == nil {
			t.Fatalf("disk_path %q must be rejected", disk)
		}
		e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
		if _, err := e.compile(argvSecSpec(disk, "qcow2")); err == nil {
			t.Fatalf("compile must reject injected disk_path %q", disk)
		}
	}
}

func TestValidateDiskPathAcceptsZVol(t *testing.T) {
	zvol := "/dev/zvol/tank/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := ValidateDiskPath(zvol); err != nil {
		t.Fatal(err)
	}
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	if _, err := e.compile(argvSecSpec(zvol, "raw")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDiskPath("/dev/sda"); err == nil {
		t.Fatal("generic /dev")
	}
}

func TestValidateDiskPathAcceptsThinLV(t *testing.T) {
	dev := "/dev/ndlvg/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if err := ValidateDiskPath(dev); err != nil {
		t.Fatal(err)
	}
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	if _, err := e.compile(argvSecSpec(dev, "raw")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDiskPath("/dev/sda"); err == nil {
		t.Fatal("generic /dev")
	}
}

func TestValidateDiskPathRejectsOutsideStorageRoot(t *testing.T) {
	for _, disk := range []string{
		"/etc/passwd",
		"/tmp/disk.qcow2",
		"/var/lib/ndl/runtime/disk.qcow2",
		"/var/lib/ndl/storage-extra/p/d.qcow2",
		"/var/lib/ndl/storage/p/../etc/passwd",
		"var/lib/ndl/storage/p/d.qcow2",
		"",
	} {
		if err := ValidateDiskPath(disk); err == nil {
			t.Fatalf("disk_path %q must be rejected", disk)
		}
		e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
		if _, err := e.compile(argvSecSpec(disk, "qcow2")); err == nil {
			t.Fatalf("compile must reject disk_path %q", disk)
		}
	}
}

func TestCompileRejectsNonQcow2RawFormat(t *testing.T) {
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + argvSecWorkloadID + ".qcow2"
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	for _, format := range []string{"vmdk", "qed", "vdi", "vpc", "iso", "qcow"} {
		if _, err := e.compile(argvSecSpec(disk, format)); err == nil {
			t.Fatalf("disk_format %q must be rejected", format)
		}
	}
	for _, format := range []string{"qcow2", "raw"} {
		if _, err := e.compile(argvSecSpec(disk, format)); err != nil {
			t.Fatalf("disk_format %s must be accepted: %v", format, err)
		}
	}
}

func TestValidateFrozenArgvRejectsBashMissingControlUnknownFlagsNonUUID(t *testing.T) {
	id := argvSecWorkloadID
	if err := ValidateFrozenArgv(id, []string{"/bin/bash", "-c", "true"}); err == nil {
		t.Fatal("/bin/bash must be rejected")
	}
	if err := ValidateFrozenArgv(id, []string{BinQEMU, "-smp", "1", "-m", "128"}); err == nil {
		t.Fatal("missing mode=control must be rejected")
	}
	if err := ValidateFrozenArgv(id, []string{BinQEMU, "-incoming", "tcp:0:4444", "-mon", "chardev=qmp0,mode=control"}); err == nil {
		t.Fatal("unknown flag must be rejected")
	}
	if err := ValidateFrozenArgv(id, []string{BinQEMU, "-sandbox", "on", "-mon", "chardev=qmp0,mode=control"}); err == nil {
		t.Fatal("unknown flag -sandbox must be rejected")
	}
	for _, badID := range []string{"", "not-a-uuid", "lab", "../escape", "nodal-vm@x"} {
		ok := []string{BinQEMU, "-name", badID, "-mon", "chardev=qmp0,mode=control"}
		if err := ValidateFrozenArgv(badID, ok); err == nil {
			t.Fatalf("non-UUID id %q must be rejected", badID)
		}
	}
}

func TestValidateFrozenArgvAcceptsCompileArgv(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + argvSecWorkloadID + ".qcow2"
	argv, err := e.compile(argvSecSpec(disk, "qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateFrozenArgv(argvSecWorkloadID, argv); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "mode=control") {
		t.Fatal("compiled argv must include mode=control")
	}
	if strings.Contains(joined, "/bin/bash") {
		t.Fatal("compiled argv must not include /bin/bash")
	}
}
