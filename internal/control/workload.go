package control

import (
	"context"
	"log"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func (o observer) reconcileWorkloads(ctx context.Context, clusterID, nodeID string) {
	items, err := o.Store.ListWorkloads(ctx, clusterID)
	if err != nil || len(items) == 0 {
		return
	}
	obs, err := o.Agent.GetWorkloads(ctx, appdb.WorkloadHints(items))
	if err != nil {
		return
	}
	unavail, recovered, err := appdb.ReconcileWorkloads(ctx, o.Store, clusterID, items, obs)
	if err != nil {
		log.Printf("workload reconcile: %v", err)
		return
	}
	for _, id := range unavail {
		o.emit(ctx, clusterID, nodeID, "workload.unavailable", map[string]string{"workload_id": id})
	}
	for _, id := range recovered {
		o.emit(ctx, clusterID, nodeID, "workload.recovered", map[string]string{"workload_id": id})
	}
}
