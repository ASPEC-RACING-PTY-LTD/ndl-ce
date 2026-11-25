package appdb

import (
	"context"
	"testing"

	"github.com/no-dal/ndl-ce/internal/lxc"
)

func autostartOf(t *testing.T, m *Memory, cluster, id string) bool {
	t.Helper()
	w, err := m.GetWorkload(context.Background(), cluster, id)
	if err != nil || w == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return w.Autostart
}

// A power change used to write autostart=false along with it.
func TestPartialSpecUpdateKeepsAutostart(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateWorkload(ctx, Workload{ID: "w1", ClusterID: "c", Kind: lxc.KindSystemContainer, Autostart: true, DesiredPower: "running"})
	if err := m.UpdateWorkloadSpec(ctx, Workload{ID: "w1", DesiredPower: "stopped"}); err != nil {
		t.Fatal(err)
	}
	if !autostartOf(t, m, "c", "w1") {
		t.Fatal("a partial update turned autostart off")
	}
	if err := m.UpdateWorkloadSpec(ctx, Workload{ID: "w1", Autostart: false, AutostartSet: true}); err != nil {
		t.Fatal(err)
	}
	if autostartOf(t, m, "c", "w1") {
		t.Fatal("an explicit update must change autostart")
	}
}

func TestReconcileMirrorsContainerBootStart(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	ct := Workload{ID: "ct", Name: "ct", ClusterID: "c", Kind: lxc.KindSystemContainer, Autostart: false}
	vm := Workload{ID: "vm", Name: "vm", ClusterID: "c", Kind: "vm", Autostart: true}
	_ = m.CreateWorkload(ctx, ct)
	_ = m.CreateWorkload(ctx, vm)
	on, off := true, false
	obs := lxc.Observation{Workloads: []lxc.Observed{
		{WorkloadID: "ct", Status: lxc.StatusRunning, Autostart: &on},
		{WorkloadID: "vm", Status: lxc.StatusRunning, Autostart: &off},
	}}
	if _, _, err := ReconcileWorkloads(ctx, m, "c", []Workload{ct, vm}, obs); err != nil {
		t.Fatal(err)
	}
	if !autostartOf(t, m, "c", "ct") {
		t.Fatal("container autostart must follow the host")
	}
	if !autostartOf(t, m, "c", "vm") {
		t.Fatal("a VM's autostart must not be taken from the container observation")
	}
	// Not checked (nil) leaves the row alone.
	ct2, _ := m.GetWorkload(ctx, "c", "ct")
	obs.Workloads[0].Autostart = nil
	if _, _, err := ReconcileWorkloads(ctx, m, "c", []Workload{*ct2}, obs); err != nil {
		t.Fatal(err)
	}
	if !autostartOf(t, m, "c", "ct") {
		t.Fatal("an unchecked observation must not change autostart")
	}
}

func pendingOf(t *testing.T, m *Memory, cluster, id string) bool {
	t.Helper()
	w, err := m.GetWorkload(context.Background(), cluster, id)
	if err != nil || w == nil {
		t.Fatalf("get %s: %v", id, err)
	}
	return w.PendingRestart
}

// A device or spec edit used to clear "restart needed" along with it.
func TestPartialSpecUpdateKeepsPendingRestart(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.CreateWorkload(ctx, Workload{ID: "w1", Name: "w1", ClusterID: "c", Kind: "vm", PendingRestart: true})
	if err := m.UpdateWorkloadSpec(ctx, Workload{ID: "w1", SpecJSON: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if !pendingOf(t, m, "c", "w1") {
		t.Fatal("a partial update cleared pending_restart")
	}
	if err := m.UpdateWorkloadSpec(ctx, Workload{ID: "w1", PendingRestartSet: true}); err != nil {
		t.Fatal(err)
	}
	if pendingOf(t, m, "c", "w1") {
		t.Fatal("an explicit update must clear pending_restart")
	}
}
