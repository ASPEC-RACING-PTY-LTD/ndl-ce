package appdb

import (
	"context"
	"time"

	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// Network is desired existence plus observed availability.
// BridgeName and UplinkIfName are locators. ID is the UUID.
type Network struct {
	ID                string
	ClusterID         string
	NodeID            string
	Name              string
	Kind              string
	Status            string
	Reason            string
	Danger            string
	BridgeName        string
	UplinkIfName      string
	IPv4CIDR          string
	Gateway           string
	DHCP              bool
	DNS               bool
	NAT               bool
	PersistKind       string
	Warnings          []string
	ManagementIfIndex *int
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Address is a desired address on a network (typically the isolated gateway).
type Address struct {
	ID        string
	ClusterID string
	NetworkID string
	Family    string
	CIDR      string
	Role      string
	CreatedAt time.Time
}

// DHCPReservation is a static mapping on an isolated network.
type DHCPReservation struct {
	ID        string
	ClusterID string
	NetworkID string
	MAC       string
	IPv4      string
	Hostname  string
	CreatedAt time.Time
}

func NetworkHints(items []Network) []ndnet.Hint {
	out := make([]ndnet.Hint, 0, len(items))
	for _, n := range items {
		out = append(out, ndnet.Hint{
			NetworkID: n.ID, Kind: n.Kind, BridgeName: n.BridgeName, UplinkIfName: n.UplinkIfName,
		})
	}
	return out
}

// ReconcileNetworks updates availability. It never deletes desired rows.
func ReconcileNetworks(ctx context.Context, st Store, clusterID string, desired []Network, obs ndnet.Observation) (unavailable, recovered []string, err error) {
	seen := map[string]ndnet.ObservedNetwork{}
	for _, item := range obs.Networks {
		seen[item.NetworkID] = item
	}
	for _, n := range desired {
		cur, ok := seen[n.ID]
		next := n
		if !ok {
			next.Status = ndnet.StatusUnavailable
			next.Reason = "network was not observed"
		} else {
			next.Status = cur.Status
			next.Reason = cur.Reason
			next.Warnings = cur.Warnings
			if cur.ManagementIfIndex > 0 {
				idx := cur.ManagementIfIndex
				next.ManagementIfIndex = &idx
			}
		}
		if n.Status != ndnet.StatusUnavailable && next.Status == ndnet.StatusUnavailable {
			unavailable = append(unavailable, n.ID)
		}
		if n.Status == ndnet.StatusUnavailable && next.Status != ndnet.StatusUnavailable {
			recovered = append(recovered, n.ID)
		}
		if err := st.UpdateNetworkObserved(ctx, next); err != nil {
			return unavailable, recovered, err
		}
	}
	return unavailable, recovered, nil
}
