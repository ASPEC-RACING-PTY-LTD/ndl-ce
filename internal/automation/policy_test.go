package automation

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func TestParseRejectsHostExecShapedKeys(t *testing.T) {
	if _, err := ParseYAML([]byte("kind: storage_pressure\nrun: bash\n")); err == nil || !strings.Contains(err.Error(), "run") {
		t.Fatalf("run: %v", err)
	}
	if _, err := ParseYAML([]byte("kind: storage_pressure\nhost_exec: true\n")); err == nil {
		t.Fatal("host_exec")
	}
	if _, err := ParseYAML([]byte("kind: storage_pressure\nshell: /bin/sh\n")); err == nil {
		t.Fatal("shell")
	}
	spec, err := ParseYAML([]byte("kind: storage_pressure\nthreshold_percent: 85\naction: enqueue_migrate_low_priority\n"))
	if err != nil || spec.ThresholdPercent != 85 || spec.Action != ActionEnqueueMigrate {
		t.Fatalf("%+v %v", spec, err)
	}
	if _, err := ParseJSONMap("storage_pressure", "host.exec", 85, false); err == nil {
		t.Fatal("unknown action")
	}
}

func TestUsedPercentAndLowPrioritySelect(t *testing.T) {
	usable, alloc := int64(100), int64(90)
	pct, ok := UsedPercent(&usable, &alloc)
	if !ok || pct != 90 {
		t.Fatalf("%d %v", pct, ok)
	}
	if _, ok := UsedPercent(nil, &alloc); ok {
		t.Fatal("unavailable")
	}
	pool := "pool-1"
	vols := []appdb.Volume{{ID: "vol-hi", PoolID: pool}, {ID: "vol-lo", PoolID: pool}}
	disks := []appdb.WorkloadDisk{{WorkloadID: "vm-hi", VolumeID: "vol-hi"}, {WorkloadID: "vm-lo", VolumeID: "vol-lo"}}
	wls := []appdb.Workload{
		{ID: "vm-hi", Name: "important", Kind: "vm"},
		{ID: "vm-lo", Name: "batch", Kind: "vm"},
		{ID: "ct-1", Name: "ct", Kind: "system-container"},
	}
	place := map[string]appdb.WorkloadPlacement{
		"vm-hi": {WorkloadID: "vm-hi", Priority: 90},
		"vm-lo": {WorkloadID: "vm-lo", Priority: 10},
	}
	got := SelectLowPriority(pool, wls, disks, vols, place)
	if len(got) != 2 || got[0].WorkloadID != "vm-lo" || got[0].Priority != 10 {
		t.Fatalf("%+v", got)
	}
}
