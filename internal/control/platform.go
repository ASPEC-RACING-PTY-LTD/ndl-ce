package control

import (
	"context"
	"log"
	"strconv"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/storage"
)

func (o observer) reconcilePlatform(ctx context.Context, clusterID, nodeID string) {
	o.reconcileOperations(ctx, clusterID, nodeID)
	o.sweepOrphanVolumes(ctx, clusterID, nodeID)
	o.emitHealthAlerts(ctx, clusterID, nodeID)
}

func (o observer) reconcileOperations(ctx context.Context, clusterID, nodeID string) {
	ops, err := o.Store.ListOperations(ctx, clusterID, 500)
	if err != nil {
		return
	}
	pools, _ := o.Store.ListStoragePools(ctx, clusterID)
	nets, _ := o.Store.ListNetworks(ctx, clusterID)
	workloads, _ := o.Store.ListWorkloads(ctx, clusterID)
	jobs, _ := o.Store.ListMigrationJobs(ctx, clusterID, 200)
	facts := appdb.OpFacts{
		Pools: pools, Networks: nets, Workloads: workloads, Jobs: jobs, Now: time.Now().UTC(),
	}
	for _, op := range ops {
		dec := appdb.ReconcileOperation(op, facts)
		if !dec.Changed {
			continue
		}
		if err := o.Store.UpsertOperation(ctx, dec.Operation); err != nil {
			log.Printf("operation reconcile persist %s: %v", op.ID, err)
			continue
		}
		o.emit(ctx, clusterID, nodeID, "task.reconciled", map[string]string{
			"operation_id": op.ID, "kind": op.Kind, "from": op.State, "to": dec.Operation.State, "reason": dec.Reason,
		})
	}
}

func (o observer) sweepOrphanVolumes(ctx context.Context, clusterID, nodeID string) {
	vols, err := o.Store.ListVolumes(ctx, clusterID, "")
	if err != nil || len(vols) == 0 {
		return
	}
	disks, _ := o.Store.ListWorkloadDisks(ctx, clusterID, "")
	attached := map[string]bool{}
	for _, d := range disks {
		attached[d.VolumeID] = true
	}
	jobs, _ := o.Store.ListMigrationJobs(ctx, clusterID, 200)
	orphans := appdb.ClassifyOrphanVolumes(appdb.VolumeFacts{
		Volumes: vols, Attached: attached, Jobs: jobs, Now: time.Now().UTC(),
	})
	for _, orphan := range orphans {
		v := orphan.Volume
		o.emit(ctx, clusterID, nodeID, "storage.volume.orphan", map[string]string{
			"volume_id": v.ID, "job_id": orphan.JobID, "reason": orphan.Reason,
			"allocated_bytes": formatInt(v.AllocatedBytes),
		})
		if v.Owner == "" && v.OwnerKind == "" {
			v.Owner = storage.VolumeOwnerName
			v.OwnerKind = storage.VolumeKindMigration
			v.OwnerJobID = orphan.JobID
			_ = o.Store.UpdateVolumeOwner(ctx, v)
		}
		pool, _ := o.Store.GetStoragePool(ctx, clusterID, v.PoolID)
		root := ""
		if pool != nil {
			root = pool.RootPath
		}
		if err := o.Agent.DestroyDirectoryVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: v.ID, PoolID: v.PoolID, RootPath: root, Class: v.Class,
			Format: v.Format, BackendRef: v.BackendRef, Size: v.SizeBytes,
			Owner: storage.VolumeOwnerName, OwnerKind: storage.VolumeKindMigration, JobID: orphan.JobID,
		}, storage.PoolHint{PoolID: v.PoolID, BackendType: v.BackendType, RootPath: root}); err != nil {
			log.Printf("orphan volume destroy %s: %v", v.ID, err)
			continue
		}
		if err := o.Store.DeleteVolume(ctx, clusterID, v.ID); err != nil {
			log.Printf("orphan volume forget %s: %v", v.ID, err)
			continue
		}
		o.emit(ctx, clusterID, nodeID, "storage.volume.removed", map[string]string{
			"volume_id": v.ID, "job_id": orphan.JobID, "reason": orphan.Reason,
		})
	}
}

func (o observer) emitHealthAlerts(ctx context.Context, clusterID, nodeID string) {
	now := time.Now().UTC()
	ops, _ := o.Store.ListOperations(ctx, clusterID, 500)
	stale := appdb.StaleRunningOps(ops, now, appdb.OpStaleAlertAfter)
	if n := len(stale); n > 0 {
		o.emitThrottled(ctx, clusterID, nodeID, "task.stale", map[string]string{
			"count": strconv.Itoa(n), "threshold": appdb.OpStaleAlertAfter.String(),
		})
	}
	vols, _ := o.Store.ListVolumes(ctx, clusterID, "")
	disks, _ := o.Store.ListWorkloadDisks(ctx, clusterID, "")
	attached := map[string]bool{}
	for _, d := range disks {
		attached[d.VolumeID] = true
	}
	jobs, _ := o.Store.ListMigrationJobs(ctx, clusterID, 200)
	orphans := appdb.ClassifyOrphanVolumes(appdb.VolumeFacts{Volumes: vols, Attached: attached, Jobs: jobs, Now: now})
	if n := len(orphans); n > 0 {
		o.emitThrottled(ctx, clusterID, nodeID, "storage.orphan_volumes", map[string]string{"count": strconv.Itoa(n)})
	}
	nets, _ := o.Store.ListNetworks(ctx, clusterID)
	for _, n := range nets {
		if !ndnet.Isolated(n.Kind) {
			continue
		}
		if n.Status == ndnet.StatusWarning || n.Status == ndnet.StatusUnavailable {
			o.emitThrottled(ctx, clusterID, nodeID, "network.degraded", map[string]string{
				"network_id": n.ID, "name": n.Name, "bridge": n.BridgeName, "status": n.Status,
			})
		}
	}
	failed := 0
	for _, j := range jobs {
		if j.State == appdb.OpStateFailed && now.Sub(j.UpdatedAt) < 2*time.Hour {
			failed++
		}
	}
	if failed >= 3 {
		o.emitThrottled(ctx, clusterID, nodeID, "migration.repeated_failure", map[string]string{
			"count": strconv.Itoa(failed), "window": "2h",
		})
	}
}

func formatInt(v *int64) string {
	if v == nil {
		return "0"
	}
	return strconv.FormatInt(*v, 10)
}
