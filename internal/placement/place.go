package placement

import (
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/inventory"
)

const (
	ModeAutomatic = "automatic"
	ModeNode      = "node"
	ModeGroup     = "group"
)

// Request is a CE placement decision. It is not org governance.
type Request struct {
	Mode                string
	NodeID              string
	GroupID             string
	CPUs                int
	MemoryBytes         int64
	RequireGPU          bool
	RequireStorageClass string
	AffinityNodeID      string
	AntiAffinityNodeID  string
	GroupMembers        map[string][]string
	Pools               []appdb.StoragePool
}

// Candidate is one schedulable node plus observed inventory.
type Candidate struct {
	Node        appdb.Node
	Inventory   *inventory.Inventory
	Maintaining bool
	MemoryFree  int64
	CPUFree     int
}

// Result is the chosen node. Identity is the node UUID.
type Result struct {
	NodeID string
	Reason string
}

// Place selects one node. It never invents a second copy on another host.
func Place(req Request, cands []Candidate) (Result, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = ModeAutomatic
	}
	var eligible []Candidate
	for _, c := range cands {
		if c.Node.RevokedAt != nil {
			continue
		}
		if c.Maintaining {
			continue
		}
		if !classOK(req, c) {
			continue
		}
		if req.RequireGPU && !hasGPU(c) {
			continue
		}
		if req.AntiAffinityNodeID != "" && c.Node.ID == req.AntiAffinityNodeID {
			continue
		}
		if req.CPUs > 0 && c.CPUFree > 0 && c.CPUFree < req.CPUs {
			continue
		}
		if req.MemoryBytes > 0 && c.MemoryFree > 0 && c.MemoryFree < req.MemoryBytes {
			continue
		}
		eligible = append(eligible, c)
	}
	switch mode {
	case ModeNode:
		id := strings.TrimSpace(req.NodeID)
		if id == "" {
			return Result{}, fmt.Errorf("node placement requires node_id")
		}
		for _, c := range eligible {
			if c.Node.ID == id {
				return Result{NodeID: c.Node.ID, Reason: "specific node"}, nil
			}
		}
		return Result{}, fmt.Errorf("requested node is not eligible")
	case ModeGroup:
		id := strings.TrimSpace(req.GroupID)
		if id == "" {
			return Result{}, fmt.Errorf("group placement requires node_group_id")
		}
		members := map[string]struct{}{}
		for _, n := range req.GroupMembers[id] {
			members[n] = struct{}{}
		}
		var inGroup []Candidate
		for _, c := range eligible {
			if _, ok := members[c.Node.ID]; ok {
				inGroup = append(inGroup, c)
			}
		}
		return pick(req, inGroup)
	case ModeAutomatic:
		return pick(req, eligible)
	default:
		return Result{}, fmt.Errorf("placement must be automatic, node, or group")
	}
}

func pick(req Request, cands []Candidate) (Result, error) {
	if len(cands) == 0 {
		return Result{}, fmt.Errorf("no eligible node")
	}
	if req.AffinityNodeID != "" {
		for _, c := range cands {
			if c.Node.ID == req.AffinityNodeID {
				return Result{NodeID: c.Node.ID, Reason: "affinity"}, nil
			}
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if score(c) > score(best) {
			best = c
		}
	}
	return Result{NodeID: best.Node.ID, Reason: "automatic"}, nil
}

func score(c Candidate) int {
	n := 0
	if nodeRole(c.Node) == cluster.RoleControl {
		n += 1
	}
	if hasGPU(c) {
		n += 10
	}
	if c.MemoryFree > 0 {
		n += int(c.MemoryFree / (1 << 20))
	}
	return n
}

func nodeRole(n appdb.Node) string {
	if n.Role == "" {
		return cluster.RoleControl
	}
	return n.Role
}

func hasGPU(c Candidate) bool {
	if c.Inventory == nil {
		return false
	}
	return len(c.Inventory.GPUs) > 0
}

func classOK(req Request, c Candidate) bool {
	class := strings.TrimSpace(req.RequireStorageClass)
	if class == "" {
		return true
	}
	for _, p := range req.Pools {
		if p.NodeID == c.Node.ID && strings.EqualFold(p.BackendType, class) {
			return true
		}
		if p.NodeID == c.Node.ID && strings.EqualFold(p.Name, class) {
			return true
		}
	}
	return false
}
