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
	NetworkID           string
	Priority            int
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
	// HealthOK is observed node health. The zero value means unknown.
	// Unknown stays eligible so HTTP can wire this later. When any
	// candidate is marked healthy, candidates with HealthOK false are skipped.
	HealthOK bool
	// Networks lists network IDs present on this node. Used when Request.NetworkID is set.
	Networks []string
	// Priority is an optional node-level tie-break. Lower numbers win. Zero means unset.
	Priority int
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
	healthActive := healthObserved(cands)
	var eligible []Candidate
	for _, c := range cands {
		if c.Node.RevokedAt != nil {
			continue
		}
		if healthActive && !c.HealthOK {
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
		if !networkOK(req, c) {
			continue
		}
		if !fitsCPU(req, c) {
			continue
		}
		if !fitsMemory(req, c) {
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
		if better(req, c, best) {
			best = c
		}
	}
	return Result{NodeID: best.Node.ID, Reason: "automatic"}, nil
}

func better(req Request, c, best Candidate) bool {
	sc, sb := score(c), score(best)
	if sc != sb {
		return sc > sb
	}
	pc, pb := priorityOf(req, c), priorityOf(req, best)
	if pc == pb {
		return false
	}
	return pc < pb
}

// priorityOf returns the tie-break priority. Candidate.Priority wins when set.
// Otherwise Request.Priority is used. Lower numbers win. Zero means unset.
func priorityOf(req Request, c Candidate) int {
	if c.Priority != 0 {
		return c.Priority
	}
	return req.Priority
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

func networkOK(req Request, c Candidate) bool {
	want := strings.TrimSpace(req.NetworkID)
	if want == "" {
		return true
	}
	for _, id := range c.Networks {
		if id == want {
			return true
		}
	}
	return false
}

// fitsCPU reports whether the candidate can satisfy Request.CPUs.
// CPUFree <= 0 means unknown (HTTP may not populate it yet) and stays eligible.
func fitsCPU(req Request, c Candidate) bool {
	if req.CPUs <= 0 {
		return true
	}
	if c.CPUFree <= 0 {
		return true
	}
	return c.CPUFree >= req.CPUs
}

// fitsMemory reports whether the candidate can satisfy Request.MemoryBytes.
// MemoryFree <= 0 means unknown and stays eligible.
func fitsMemory(req Request, c Candidate) bool {
	if req.MemoryBytes <= 0 {
		return true
	}
	if c.MemoryFree <= 0 {
		return true
	}
	return c.MemoryFree >= req.MemoryBytes
}

func healthObserved(cands []Candidate) bool {
	for _, c := range cands {
		if c.HealthOK {
			return true
		}
	}
	return false
}
