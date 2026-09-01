package appdb

import (
	"context"
	"encoding/json"

	"github.com/no-dal/ndl-ce/internal/storage"
)

// ReconcileStorage updates observed pool/volume/library status.
// It never deletes rows.
func ReconcileStorage(ctx context.Context, st Store, clusterID string, pools []StoragePool, obs storage.Observation) (unavailable, recovered []string, err error) {
	byID := map[string]storage.ObservedPool{}
	for _, p := range obs.Pools {
		byID[p.PoolID] = p
	}
	vols := map[string]storage.ObservedVolume{}
	for _, v := range obs.Volumes {
		vols[v.VolumeID] = v
	}
	libs := map[string]storage.ObservedLibrary{}
	for _, item := range obs.Library {
		libs[item.ItemID] = item
	}
	for _, pool := range pools {
		seen, ok := byID[pool.ID]
		next := pool
		prev := pool.Status
		if !ok {
			next.Status = storage.StatusUnavailable
			next.Reason = "pool was not observed"
			next.UsableBytes, next.AllocatedBytes, next.ProvisionedBytes, next.TotalBytes = nil, nil, nil, nil
		} else {
			next.Status = seen.Status
			next.Reason = seen.Reason
			next.Warnings = seen.Warnings
			next.WarningText = seen.WarningText
			caps, _ := json.Marshal(seen.Capabilities)
			next.Capabilities = caps
			next.UsableBytes = seen.Capacity.UsableBytes
			next.AllocatedBytes = seen.Capacity.AllocatedBytes
			next.ProvisionedBytes = seen.Capacity.ProvisionedBytes
			next.TotalBytes = seen.Capacity.TotalBytes
		}
		if err := st.UpdateStoragePoolObserved(ctx, next); err != nil {
			return unavailable, recovered, err
		}
		if prev != next.Status && next.Status == storage.StatusUnavailable {
			unavailable = append(unavailable, pool.ID)
		}
		if prev == storage.StatusUnavailable && next.Status != storage.StatusUnavailable && next.Status != "" {
			recovered = append(recovered, pool.ID)
		}
		rows, err := st.ListVolumes(ctx, clusterID, pool.ID)
		if err != nil {
			return unavailable, recovered, err
		}
		for _, v := range rows {
			if seen, ok := vols[v.ID]; ok && next.Status != storage.StatusUnavailable {
				v.Status = seen.Status
				v.XattrState = seen.XattrState
				alloc := seen.Allocated
				v.AllocatedBytes = &alloc
			} else {
				v.Status = storage.StatusUnavailable
			}
			if err := st.UpdateVolumeObserved(ctx, v); err != nil {
				return unavailable, recovered, err
			}
		}
		items, err := st.ListLibraryItems(ctx, clusterID, pool.ID)
		if err != nil {
			return unavailable, recovered, err
		}
		for _, item := range items {
			if seen, ok := libs[item.ID]; ok && next.Status != storage.StatusUnavailable {
				item.Status = seen.Status
			} else {
				item.Status = storage.StatusUnavailable
			}
			if err := st.UpdateLibraryObserved(ctx, item); err != nil {
				return unavailable, recovered, err
			}
		}
	}
	return unavailable, recovered, nil
}
