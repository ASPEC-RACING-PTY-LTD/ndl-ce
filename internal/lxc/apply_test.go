package lxc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClassifyCPUMemoryLiveNameRestart(t *testing.T) {
	prev := Spec{Name: "a", CPUs: 1, MemoryBytes: 1 << 30, MAC: "aa:bb:cc:dd:ee:ff"}
	next := prev
	next.CPUs = 2
	next.MemoryBytes = 2 << 30
	next.Name = "b"
	cls := ClassifyEdit(prev, next, false, false)
	if !RequiresRestart(classesOf(cls, "name")) {
		t.Fatal("name must require restart")
	}
	live := map[string]bool{}
	for _, c := range cls {
		if c.Apply == ApplyLive {
			live[c.Field] = true
		}
	}
	if !live["cpus"] || !live["memory_bytes"] {
		t.Fatalf("cpu/memory should be live: %+v", cls)
	}
	if RequiresStop(cls) {
		t.Fatal("cpu/memory/name must not require stop")
	}
}

func classesOf(cls []ApplyClass, field string) []ApplyClass {
	var out []ApplyClass
	for _, c := range cls {
		if c.Field == field {
			out = append(out, c)
		}
	}
	return out
}

func TestStartAlreadyRunningSkipsRewrite(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.dataDir(), "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		RootfsPath: root, NoStart: true,
	}); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits = map[string]bool{id: true}
	var ran []string
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return []byte("active"), nil
	}
	cfgBefore, err := os.ReadFile(e.configPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for _, line := range ran {
		if strings.Contains(line, "systemctl start") {
			t.Fatalf("already-running start must not systemctl start: %v", ran)
		}
	}
	cfgAfter, err := os.ReadFile(e.configPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if string(cfgBefore) != string(cfgAfter) {
		t.Fatal("already-running start must not rewrite LXC config")
	}
}

func TestApplySpecLiveCgroupArgv(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.dataDir(), "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		RootfsPath: root, CPUs: 1, MemoryBytes: 1 << 30, NoStart: true,
	}); err != nil {
		t.Fatal(err)
	}
	e.LiveUnits = map[string]bool{id: true}
	var ran []string
	e.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	res, err := e.ApplySpec(context.Background(), LifecycleRequest{
		WorkloadID: id, Action: ActionApplySpec, CPUs: 2, MemoryBytes: 2 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRunning {
		t.Fatalf("status %s", res.Status)
	}
	cfg, _ := os.ReadFile(e.configPath(id))
	if !strings.Contains(string(cfg), "lxc.cgroup2.memory.max = 2147483648") {
		t.Fatalf("config missing live memory: %s", cfg)
	}
	joined := strings.Join(ran, "\n")
	if !strings.Contains(joined, BinLXCCgroup) || !strings.Contains(joined, "memory.max") || !strings.Contains(joined, "cpu.max") {
		t.Fatalf("live cgroup argv missing: %v", ran)
	}
	if strings.Contains(joined, "systemctl start") || strings.Contains(joined, "systemctl restart") {
		t.Fatalf("apply-spec must not start/restart: %v", ran)
	}
}

func TestApplySpecPreservesGPUAndNesting(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.dataDir(), "rootfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		RootfsPath: root, GPUDevices: []string{"card0"}, Nesting: &on, TUN: true, NoStart: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ApplySpec(context.Background(), LifecycleRequest{
		WorkloadID: id, CPUs: 4,
	}); err != nil {
		t.Fatal(err)
	}
	applied, err := e.readApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Spec.GPUDevices) != 1 || applied.Spec.GPUDevices[0] != "card0" {
		t.Fatalf("GPU dropped: %+v", applied.Spec.GPUDevices)
	}
	if !SpecWantsNesting(applied.Spec) {
		t.Fatal("nesting dropped")
	}
	if !applied.Spec.TUN {
		t.Fatal("TUN dropped")
	}
	if applied.Spec.CPUs != 4 {
		t.Fatalf("cpus %d", applied.Spec.CPUs)
	}
}

func TestUnlistedBinaryStillRefusedForCgroup(t *testing.T) {
	e := testEngine(t)
	if _, err := e.run(context.Background(), "/usr/bin/lxc-unshare"); err == nil {
		t.Fatal("unlisted binary must be refused")
	}
}
