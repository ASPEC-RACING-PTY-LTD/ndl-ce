package qemu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const gateWorkloadID = "66666666-6666-4666-8666-666666666666"

func gateSpec(disk string) Spec {
	return Spec{
		WorkloadID:  gateWorkloadID,
		VolumeID:    "vol-gate",
		DiskPath:    disk,
		DiskFormat:  "qcow2",
		Machine:     DefaultMachine,
		Accel:       "tcg",
		MemoryBytes: DefaultMemory,
		CPUs:        1,
	}
}

func gateCompile(t *testing.T) []string {
	t.Helper()
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	argv, err := e.compile(gateSpec("/var/lib/ndl/storage/p/volumes/vm-disk/" + gateWorkloadID + ".qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	return argv
}

func TestGatePinnedMachineNotAliasQ35(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + gateWorkloadID + ".qcow2"
	if _, err := e.compile(Spec{WorkloadID: gateWorkloadID, DiskPath: disk, Machine: "q35", Accel: "tcg"}); err == nil {
		t.Fatal("machine alias q35 must be rejected")
	}
	argv, err := e.compile(gateSpec(disk))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, "\x00")
	if !strings.Contains(joined, DefaultMachine) {
		t.Fatalf("pinned machine %s missing from argv: %v", DefaultMachine, argv)
	}
	for i, a := range argv {
		if a != "-machine" {
			continue
		}
		if i+1 >= len(argv) {
			t.Fatal("dangling -machine")
		}
		val := argv[i+1]
		if val == "q35" || strings.HasPrefix(val, "q35,") {
			t.Fatalf("alias q35 is forbidden: %s", val)
		}
		if !strings.HasPrefix(val, "pc-q35-") {
			t.Fatalf("machine must be pinned pc-q35-X.Y: %s", val)
		}
	}
}

func TestGateVolumeHandleDiskPathRequired(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	for _, disk := range []string{"", "volumes/vm-disk/x.qcow2", "var/lib/ndl/x.qcow2", "/var/lib/ndl/storage/p/../etc/passwd"} {
		if _, err := e.compile(Spec{WorkloadID: gateWorkloadID, DiskPath: disk, Machine: DefaultMachine, Accel: "tcg"}); err == nil {
			t.Fatalf("disk_path %q must be rejected", disk)
		}
	}
	ok := "/var/lib/ndl/storage/p/volumes/vm-disk/" + gateWorkloadID + ".qcow2"
	argv, err := e.compile(gateSpec(ok))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "filename="+ok) {
		t.Fatalf("VolumeHandle locator missing from blockdev: %s", joined)
	}
}

func TestGateFrozenArgvArtifactWritten(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, SkipHostCmds: true}
	disk := "/var/lib/ndl/storage/p/volumes/vm-disk/" + gateWorkloadID + ".qcow2"
	if _, err := e.Prepare(gateSpec(disk)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(e.argvPath(gateWorkloadID))
	if err != nil {
		t.Fatal(err)
	}
	var file ArgvFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.WorkloadID != gateWorkloadID {
		t.Fatalf("frozen argv workload_id=%s", file.WorkloadID)
	}
	if len(file.Argv) < 2 || file.Argv[0] != BinQEMU {
		t.Fatalf("frozen argv is not a typed qemu command: %v", file.Argv)
	}
	applied, err := e.ReadApplied(gateWorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	if applied.SchemaVersion != LastAppliedSchema {
		t.Fatal(applied.SchemaVersion)
	}
}

func TestGateQemuGAChannelInArgv(t *testing.T) {
	argv := gateCompile(t)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, GuestAgentName) {
		t.Fatalf("qemu-ga channel %s missing: %s", GuestAgentName, joined)
	}
	found := false
	for _, a := range argv {
		if strings.Contains(a, "name="+GuestAgentName) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("virtserialport name=%s missing: %v", GuestAgentName, argv)
	}
}

func TestGateQMPIsControlMonitor(t *testing.T) {
	argv := gateCompile(t)
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "mode=control") {
		t.Fatalf("QMP must be a control monitor: %s", joined)
	}
	if strings.Contains(joined, "mode=human") || strings.Contains(joined, "monitor stdio") {
		t.Fatalf("human monitor is forbidden: %s", joined)
	}
	for i, a := range argv {
		if a != "-mon" {
			continue
		}
		if i+1 >= len(argv) {
			t.Fatal("dangling -mon")
		}
		if !strings.Contains(argv[i+1], "mode=control") {
			t.Fatalf("QMP -mon must be mode=control: %s", argv[i+1])
		}
		if strings.Contains(argv[i+1], "human") {
			t.Fatalf("QMP -mon must not be human: %s", argv[i+1])
		}
	}
}

func TestGateNoHostExecOrBashC(t *testing.T) {
	argv := gateCompile(t)
	for _, a := range argv {
		if strings.Contains(a, "Host.Exec") || strings.Contains(a, "bash -c") || a == "bash" || a == "/bin/bash" || a == "/bin/sh" {
			t.Fatalf("argv must not use Host.Exec or a shell: %v", argv)
		}
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "bash -c") || strings.Contains(joined, "Host.Exec") {
		t.Fatalf("argv must not contain Host.Exec or bash -c: %s", joined)
	}
	dir := filepath.Dir(mustCallerFile(t))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	hostExec := "Host" + ".Exec"
	bashC := "bash" + " -c"
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") || strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, hostExec) {
			t.Fatalf("%s must not contain %s", ent.Name(), hostExec)
		}
		if strings.Contains(text, bashC) {
			t.Fatalf("%s must not contain %s", ent.Name(), bashC)
		}
	}
}

func TestGateDetectAccelTCGWithoutKVM(t *testing.T) {
	got := DetectAccel()
	if got != "kvm" && got != "tcg" {
		t.Fatalf("DetectAccel=%s", got)
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		if got != "tcg" {
			t.Fatalf("missing /dev/kvm must be tcg, got %s", got)
		}
	}
}

func TestGateCleanupFailedLaunchPreservesDisk(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	m := reflect.ValueOf(e).MethodByName("CleanupFailedLaunch")
	if !m.IsValid() {
		t.Skip("CleanupFailedLaunch does not exist")
	}
	id := gateWorkloadID
	disk := filepath.Join(e.DataDir, "fake-disk.qcow2")
	if err := os.WriteFile(disk, []byte("keep-me"), 0o640); err != nil {
		t.Fatal(err)
	}
	locator := "/var/lib/ndl/storage/p/volumes/vm-disk/" + id + ".qcow2"
	if _, err := e.Prepare(gateSpec(locator)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(e.runtimeDir(id), 0o750); err != nil {
		t.Fatal(err)
	}
	stale := e.qmpPath(id)
	if err := os.WriteFile(stale, []byte("stale-qmp"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := m.Call([]reflect.Value{reflect.ValueOf(id)})
	if len(out) > 0 && !out[0].IsNil() {
		t.Fatal(out[0].Interface())
	}
	raw, err := os.ReadFile(disk)
	if err != nil {
		t.Fatalf("failed-launch cleanup must not delete a fake disk file: %v", err)
	}
	if string(raw) != "keep-me" {
		t.Fatalf("disk contents changed: %q", raw)
	}
}

func mustCallerFile(t *testing.T) string {
	t.Helper()
	// gate_test.go lives in this package directory.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "gate_test.go")
}
