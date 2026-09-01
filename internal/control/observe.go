package control

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/httpapi"
	"github.com/no-dal/ndl-ce/internal/inventory"
)

type observer struct {
	Store   appdb.Store
	Agent   agentrpc.Client
	Hub     *httpapi.EventHub
	Period  time.Duration
	Nightly func(context.Context)
	Alerts  func(context.Context)
}

func (o observer) run(ctx context.Context) {
	period := o.Period
	if period <= 0 {
		period = 15 * time.Second
	}
	t := time.NewTicker(period)
	defer t.Stop()
	var fails int
	var down bool
	tick := func() {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		cluster, err := o.Store.GetCluster(cctx)
		if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
			return
		}
		node, err := o.Store.GetNode(cctx, cluster.ID)
		if err != nil || node == nil {
			return
		}
		prev, _ := o.Store.GetInventory(cctx, node.ID)
		inv, err := o.Agent.Observe(cctx)
		if err != nil {
			fails++
			if fails >= 2 {
				_ = o.Store.MarkInventoryStale(cctx, node.ID)
				if !down {
					down = true
					o.emit(cctx, cluster.ID, node.ID, "agent.disconnected", map[string]string{
						"detail": "agent observe failed",
					})
				}
			}
			return
		}
		if down || (prev != nil && prev.Stale) {
			o.emit(cctx, cluster.ID, node.ID, "agent.reconnected", map[string]string{})
		}
		down = false
		fails = 0
		if prev != nil {
			var old inventory.Inventory
			if json.Unmarshal(prev.Payload, &old) == nil && !sparseInventory(old) && sparseInventory(inv) {
				_ = o.Store.MarkInventoryStale(cctx, node.ID)
				return
			}
		}
		payload, err := json.Marshal(inv)
		if err != nil {
			return
		}
		_ = o.Store.UpsertInventory(cctx, appdb.HardwareInventory{
			NodeID:     node.ID,
			ClusterID:  cluster.ID,
			Payload:    payload,
			ObservedAt: inv.ObservedAt,
			Stale:      false,
		})
		o.reconcileStorage(cctx, cluster.ID, node.ID)
		o.reconcileNetworks(cctx, cluster.ID, node.ID)
		o.reconcileWorkloads(cctx, cluster.ID, node.ID)
		if o.Nightly != nil {
			go o.Nightly(context.Background())
		}
		if o.Alerts != nil {
			go o.Alerts(context.Background())
		}
		changed := prev == nil || inventoryFingerprint(prev.Payload) != inventoryFingerprint(payload)
		if !changed {
			return
		}
		_ = o.Store.InsertObservation(cctx, appdb.NodeObservation{
			ID:         uuid.NewString(),
			ClusterID:  cluster.ID,
			NodeID:     node.ID,
			Kind:       "inventory",
			ObservedAt: inv.ObservedAt,
		})
		o.emit(cctx, cluster.ID, node.ID, "inventory.updated", map[string]string{
			"schema_version": inv.SchemaVersion,
		})
		done := 100
		_ = o.Store.UpsertOperation(cctx, appdb.Operation{
			ID:             uuid.NewString(),
			ClusterID:      cluster.ID,
			NodeID:         node.ID,
			Kind:           "inventory.refresh",
			State:          "succeeded",
			IdempotencyKey: "inventory.refresh",
			Progress:       &done,
			Stage:          "collected",
			Message:        "host inventory observed",
			UpdatedAt:      time.Now().UTC(),
		})
	}
	tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

func (o observer) emit(ctx context.Context, clusterID, nodeID, typ string, payload map[string]string) {
	body, _ := json.Marshal(payload)
	e := appdb.Event{
		ID:        uuid.NewString(),
		ClusterID: clusterID,
		NodeID:    nodeID,
		Type:      typ,
		Payload:   body,
		CreatedAt: time.Now().UTC(),
	}
	if err := o.Store.InsertEvent(ctx, e); err != nil {
		log.Printf("event insert: %v", err)
		return
	}
	if o.Hub != nil {
		o.Hub.Publish(e)
	}
}

func inventoryFingerprint(raw []byte) string {
	var inv inventory.Inventory
	if json.Unmarshal(raw, &inv) != nil {
		return string(raw)
	}
	inv.ObservedAt = time.Time{}
	inv.Stale = false
	inv.Temperatures = nil
	inv.Memory.AvailableBytes = nil
	inv.Memory.UsedBytes = nil
	b, err := json.Marshal(inv)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func sparseInventory(inv inventory.Inventory) bool {
	return inv.CPU.Status != inventory.StatusAvailable &&
		inv.Memory.Status != inventory.StatusAvailable &&
		len(inv.BlockDevices) == 0 &&
		len(inv.NICs) == 0
}
