package control

import (
	"context"
	"log"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

func (o observer) reconcileNetworks(ctx context.Context, clusterID, nodeID string) {
	items, err := o.Store.ListNetworks(ctx, clusterID)
	if err != nil || len(items) == 0 {
		return
	}
	obs, err := o.Agent.GetNetworks(ctx, appdb.NetworkHints(items))
	if err != nil {
		return
	}
	unavail, recovered, err := appdb.ReconcileNetworks(ctx, o.Store, clusterID, items, obs)
	if err != nil {
		log.Printf("network reconcile: %v", err)
		return
	}
	for _, id := range unavail {
		o.emit(ctx, clusterID, nodeID, "network.unavailable", map[string]string{"network_id": id})
	}
	for _, id := range recovered {
		o.emit(ctx, clusterID, nodeID, "network.recovered", map[string]string{"network_id": id})
	}
}
