package control

import (
	"context"
	"log"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func (o observer) reconcileStorage(ctx context.Context, clusterID, nodeID string) {
	pools, err := o.Store.ListStoragePools(ctx, clusterID)
	if err != nil || len(pools) == 0 {
		return
	}
	obs, err := o.Agent.GetStorage(ctx, appdb.PoolHints(pools))
	if err != nil {
		return
	}
	unavail, recovered, err := appdb.ReconcileStorage(ctx, o.Store, clusterID, pools, obs)
	if err != nil {
		log.Printf("storage reconcile: %v", err)
		return
	}
	for _, id := range unavail {
		o.emit(ctx, clusterID, nodeID, "storage.pool.unavailable", map[string]string{"pool_id": id})
	}
	for _, id := range recovered {
		o.emit(ctx, clusterID, nodeID, "storage.pool.recovered", map[string]string{"pool_id": id})
	}
}
