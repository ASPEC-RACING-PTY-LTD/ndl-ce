//go:build linux

package lxc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/google/uuid"
)

var nvidiaEncodeDevices = []string{
	"/dev/dri/by-path/pci-0000:01:00.0-render",
	"/dev/nvidia1",
	"/dev/nvidiactl",
	"/dev/nvidia-uvm",
	"/dev/nvidia-uvm-tools",
	"/dev/nvidia-modeset",
}

// An NVIDIA encode claim must regenerate every driver node plus the device
// baseline, on the first write and on every later rewrite from last-applied.
func TestNVIDIAEncodeConfigSurvivesRegeneration(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", id)
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "viewdock", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: root, BridgeName: "ndldeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyGPUDevices(id, nvidiaEncodeDevices); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(e.configPath(id))
	if err := e.RewriteRuntimeConfig(id); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(e.configPath(id))
	if string(first) != string(again) {
		t.Fatalf("regenerated config drifted:\n%s\n---\n%s", first, again)
	}
	cfg := string(again)
	for _, dev := range nvidiaEncodeDevices {
		want := "lxc.mount.entry = " + dev + " " + strings.TrimPrefix(dev, "/") + " none bind,optional,create=file\n"
		if !strings.Contains(cfg, want) {
			t.Fatalf("missing mount for %s:\n%s", dev, cfg)
		}
	}
	for _, rule := range []string{"c 195:1 rwm", "c 195:255 rwm", "c 1:3 rwm", "c 136:* rwm"} {
		if !strings.Contains(cfg, "lxc.cgroup2.devices.allow = "+rule+"\n") {
			t.Fatalf("missing rule %s:\n%s", rule, cfg)
		}
	}
	if !strings.Contains(cfg, "lxc.cgroup2.devices.deny = a\n") || strings.Contains(cfg, "195:*") || strings.Contains(cfg, "nvidia-caps") {
		t.Fatalf("policy must stay default-deny and per-GPU:\n%s", cfg)
	}
	if strings.Contains(cfg, "c 195:0 rwm") {
		t.Fatalf("another GPU's node must stay denied:\n%s", cfg)
	}
}

func TestGPUDeviceNodesAddCanonicalNodeForByPath(t *testing.T) {
	link := "/dev/ndl-test-" + uuid.NewString()[:8] + "-render"
	if err := os.Symlink("zero", link); err != nil {
		t.Skipf("needs a writable /dev: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	cfg := RenderConfig(Spec{
		WorkloadID: uuid.NewString(), Name: "ct", RootfsPath: "/vol/root",
		GPUDevices: []string{link, "/dev/null"},
	})
	for _, dev := range []string{link, "/dev/zero", "/dev/null"} {
		if !strings.Contains(cfg, "lxc.mount.entry = "+dev+" ") {
			t.Fatalf("missing mount for %s:\n%s", dev, cfg)
		}
	}
	if strings.Count(cfg, "lxc.cgroup2.devices.allow = c 1:5 rwm\n") != 1 {
		t.Fatalf("canonical node rule must appear once:\n%s", cfg)
	}
}

func diagEngine(t *testing.T, devices []string, pid string) (*Engine, string) {
	t.Helper()
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", id)
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: root, BridgeName: "ndldeadbeef",
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyGPUDevices(id, devices); err != nil {
		t.Fatal(err)
	}
	if pid != "" {
		e.SkipHostCmds = false
		e.Run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name == BinLXCInfo {
				return []byte("State: RUNNING\nPID: " + pid + "\n"), nil
			}
			return nil, nil
		}
	}
	return e, id
}

func TestDiagnoseGPUStoppedContainer(t *testing.T) {
	e, id := diagEngine(t, []string{"/dev/null", "/dev/nvidia-uvm-tools"}, "")
	d, err := e.DiagnoseGPU(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Running || d.RestartRequired || !d.ConfigPresent || !d.ConfigCurrent {
		t.Fatalf("diagnosis %+v", d)
	}
	null := d.Nodes[0]
	if null.HostRule != "c 1:3 rwm" || !null.Mounted || !null.Allowed || null.Guest != GuestNodeNotRunning {
		t.Fatalf("null %+v", null)
	}
	uvm := d.Nodes[1]
	if uvm.HostRule != "" || !uvm.Optional {
		t.Fatalf("optional missing host node %+v", uvm)
	}
	if !strings.Contains(strings.Join(d.Issues, "\n"), "/dev/nvidia-uvm-tools is not present on the host") {
		t.Fatalf("issues %v", d.Issues)
	}
	if d.DevicesMissing {
		t.Fatal("a missing optional node is not a missing GPU")
	}
}

// A removed card leaves its claim's nodes absent on the host. The config
// still renders (optional mounts, no wildcard), so the guest starts without
// the GPU, and the diagnosis names the missing devices.
func TestDiagnoseGPURemovedCard(t *testing.T) {
	if _, err := os.Stat("/dev/nvidia1"); err == nil {
		t.Skip("host has /dev/nvidia1")
	}
	e, id := diagEngine(t, nvidiaEncodeDevices, "")
	cfg, err := os.ReadFile(e.configPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "lxc.cgroup2.devices.deny = a\n") || !strings.Contains(string(cfg), "lxc.cgroup2.devices.allow = c 1:3 rwm\n") {
		t.Fatalf("baseline must survive a removed GPU:\n%s", cfg)
	}
	d, err := e.DiagnoseGPU(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !d.DevicesMissing || !d.ConfigCurrent {
		t.Fatalf("diagnosis %+v", d)
	}
	issues := strings.Join(d.Issues, "\n")
	for _, want := range []string{"/dev/nvidia1 is missing on the host", "unassign it from this workload"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("missing issue %q in %v", want, d.Issues)
		}
	}
}

func TestDiagnoseGPUDetectsStaleConfigAndMissingGuestNode(t *testing.T) {
	proc := t.TempDir()
	old := procRoot
	procRoot = proc
	t.Cleanup(func() { procRoot = old })
	guestDev := filepath.Join(proc, "4242", "root", "dev")
	if err := os.MkdirAll(guestDev, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mknod(filepath.Join(guestDev, "null"), syscall.S_IFCHR|0o600, 1<<8|3); err != nil {
		t.Skipf("mknod needs CAP_MKNOD: %v", err)
	}
	e, id := diagEngine(t, []string{"/dev/null", "/dev/zero"}, "4242")
	cfgPath := e.configPath(id)
	raw, _ := os.ReadFile(cfgPath)
	stale := strings.ReplaceAll(string(raw), "lxc.cgroup2.devices.allow = c 1:5 rwm\n", "")
	if err := os.WriteFile(cfgPath, []byte(stale), 0o640); err != nil {
		t.Fatal(err)
	}
	d, err := e.DiagnoseGPU(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Running || d.PID != 4242 || d.ConfigCurrent || !d.RestartRequired {
		t.Fatalf("diagnosis %+v", d)
	}
	if d.Nodes[0].Guest != GuestNodePresent || d.Nodes[1].Guest != GuestNodeMissing || d.Nodes[1].Allowed {
		t.Fatalf("nodes %+v", d.Nodes)
	}
	issues := strings.Join(d.Issues, "\n")
	for _, want := range []string{"/dev/zero has no device permission", "/dev/zero is missing inside the running container", "differs from the saved assignment"} {
		if !strings.Contains(issues, want) {
			t.Fatalf("missing issue %q in %v", want, d.Issues)
		}
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != stale {
		t.Fatal("diagnosis must not rewrite the config")
	}
}
