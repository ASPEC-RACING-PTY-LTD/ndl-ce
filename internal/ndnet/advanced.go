package ndnet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	ActionVLANAdd     = "vlan-add"
	ActionBondAdd     = "bond-add"
	ActionPolicyApply = "policy-apply"
	ActionOverlayPrep = "overlay-prep"

	BondActiveBackup = "active-backup"
	BondLACP         = "802.3ad"
	VLANAccess       = "access"
	VLANTrunk        = "trunk"

	BridgeBin = "/usr/sbin/bridge"
	NFTBin    = "/usr/sbin/nft"

	PolicyTable    = "ndl-policy"
	OverlayPrepMsg = "VXLAN overlay is local prep. Multi-node mesh is Phase 30."
	MgmtINPUTMsg   = "network policies cannot drop management INPUT"
)

// AdvancedOp is a typed VLAN, bond, policy, or overlay action. There is no argv field.
type AdvancedOp struct {
	Action        string   `json:"action"`
	ObjectID      string   `json:"object_id"`
	NetworkID     string   `json:"network_id,omitempty"`
	Name          string   `json:"name,omitempty"`
	VID           int      `json:"vlan_id,omitempty"`
	ParentIfName  string   `json:"parent_ifname,omitempty"`
	Mode          string   `json:"mode,omitempty"`
	AccessIfName  string   `json:"access_ifname,omitempty"`
	Members       []string `json:"members,omitempty"`
	SrcMAC        string   `json:"src_mac,omitempty"`
	DstMAC        string   `json:"dst_mac,omitempty"`
	PolicyAction  string   `json:"policy_action,omitempty"`
	OverlayVNI    uint32   `json:"overlay_vni,omitempty"`
	ConfirmIfName string   `json:"confirm_ifname,omitempty"`
	ArmRollback   bool     `json:"arm_rollback,omitempty"`
	BridgeName    string   `json:"bridge_name,omitempty"`
}

// AdvancedResult is the honest apply outcome.
type AdvancedResult struct {
	Action            string   `json:"action"`
	ObjectID          string   `json:"object_id"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	Locator           string   `json:"locator,omitempty"`
	VID               int      `json:"vlan_id,omitempty"`
	Mode              string   `json:"mode,omitempty"`
	Argv              []string `json:"argv,omitempty"`
	Files             []File   `json:"files,omitempty"`
	NFT               string   `json:"nft,omitempty"`
	RollbackArmed     bool     `json:"rollback_armed"`
	RolledBack        bool     `json:"rolled_back"`
	ManagementIfName  string   `json:"management_ifname,omitempty"`
	ManagementIfIndex int      `json:"management_ifindex,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// ApplyAdvanced executes one typed Phase 27 action.
func (e *Engine) ApplyAdvanced(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	switch strings.ToLower(strings.TrimSpace(op.Action)) {
	case ActionVLANAdd:
		return e.applyVLAN(ctx, op)
	case ActionBondAdd:
		return e.applyBond(ctx, op)
	case ActionPolicyApply:
		return e.applyPolicy(ctx, op)
	case ActionOverlayPrep:
		return e.applyOverlay(ctx, op)
	default:
		return AdvancedResult{}, fmt.Errorf("advanced network action is unsupported")
	}
}

func (e *Engine) writeOwned(files []File) error {
	if err := os.MkdirAll(e.networkDir(), 0755); err != nil {
		return err
	}
	for _, file := range files {
		base := filepath.Base(file.RelPath)
		if !ownedPersistName(base) {
			return fmt.Errorf("refusing to write unmanaged networkd file")
		}
		if err := os.WriteFile(filepath.Join(e.networkDir(), base), []byte(file.Body), 0644); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) maybeArm(ctx context.Context, dangerous bool, confirm, typed string, host HostView, id string) (armed bool, err error) {
	if !dangerous {
		return false, nil
	}
	if !ValidIfName(confirm) || !sameIface(confirm, typed) {
		return false, fmt.Errorf("typed interface confirmation is required: type %s", typed)
	}
	plan := Plan{NetworkID: id, ManagementIfIndex: host.ManagementIfIndex, ManagementIfName: managementName(host)}
	if _, err := e.armRollback(plan, host); err != nil {
		return false, err
	}
	if err := e.startWatchdog(ctx); err != nil {
		e.clearRollback()
		return false, err
	}
	return true, nil
}

func touchesManagement(host HostView, names ...string) bool {
	mgmt := managementName(host)
	for _, name := range names {
		if name == "" {
			continue
		}
		if sameIface(name, mgmt) || sameIface(name, host.DefaultRouteIf) || matchesManagementIndex(host, name) {
			return true
		}
		if iface, ok := lookup(host, name); ok && containsAny(iface.Addresses, host.ManagementAddresses) {
			return true
		}
	}
	return false
}

func refuseShellArgv(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("argv is not typed")
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "bash") || strings.Contains(joined, "/bin/sh") {
		return fmt.Errorf("shell is not a typed network action")
	}
	if argv[0] != BridgeBin && argv[0] != NFTBin && argv[0] != "/usr/bin/networkctl" {
		return fmt.Errorf("network argv is not typed")
	}
	return nil
}
