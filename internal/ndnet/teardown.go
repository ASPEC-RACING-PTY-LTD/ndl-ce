package ndnet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Teardown for advanced network objects. Each remove deletes only the
// No-dal-owned networkd files the matching add wrote, removes the runtime
// link, and reloads networkd. Removing a VLAN or bond on the management path
// needs the same typed confirmation and rollback watchdog as creating it.

const (
	ActionVLANRemove    = "vlan-remove"
	ActionBondRemove    = "bond-remove"
	ActionOverlayRemove = "overlay-remove"
	ActionPolicyClear   = "policy-clear"
)

// ownedFilesWithPrefix lists networkd files written for one object.
func (e *Engine) ownedFilesWithPrefix(id string, suffixes ...string) []string {
	entries, err := os.ReadDir(e.networkDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !ownedPersistName(name) {
			continue
		}
		for _, suffix := range suffixes {
			if strings.HasPrefix(strings.ToLower(name), persistName(id, suffix)) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

func (e *Engine) removeAdvanced(ctx context.Context, op AdvancedOp, action, ifname string, guard []string, suffixes ...string) (AdvancedResult, error) {
	id := strings.ToLower(strings.TrimSpace(op.ObjectID))
	if id == "" {
		return AdvancedResult{}, fmt.Errorf("object_id is required")
	}
	res := AdvancedResult{Action: action, ObjectID: op.ObjectID, Locator: ifname}
	if len(guard) > 0 {
		host, err := e.host()
		if err != nil {
			return AdvancedResult{}, err
		}
		res.ManagementIfName = managementName(host)
		res.ManagementIfIndex = host.ManagementIfIndex
		if touchesManagement(host, guard...) {
			armed, err := e.maybeArm(ctx, true, op.ConfirmIfName, firstNonEmptyIf(guard...), host, op.ObjectID)
			if err != nil {
				return AdvancedResult{}, err
			}
			res.RollbackArmed = armed
		}
	}
	for _, name := range e.ownedFilesWithPrefix(id, suffixes...) {
		if err := os.Remove(filepath.Join(e.networkDir(), name)); err != nil && !os.IsNotExist(err) {
			return AdvancedResult{}, err
		}
		res.Files = append(res.Files, File{RelPath: name})
	}
	if !e.SkipHostCmds {
		if ifname != "" && ValidIfName(ifname) {
			// The link may already be gone; networkd does not delete netdevs on reload.
			_ = e.run(ctx, ipBin(), "link", "delete", "dev", ifname)
		}
		if err := e.reloadNetworkd(); err != nil {
			if res.RollbackArmed {
				_ = e.RestoreActive()
				res.RolledBack = true
			}
			return AdvancedResult{}, err
		}
	}
	if res.RollbackArmed {
		after, _ := e.host()
		if err := e.probeManagement(after, Plan{ManagementIfName: res.ManagementIfName, ManagementIfIndex: res.ManagementIfIndex}); err != nil {
			_ = e.RestoreActive()
			res.RolledBack = true
			return res, err
		}
	}
	res.Status = StatusUnavailable
	res.Reason = "removed"
	return res, nil
}

func (e *Engine) removeVLAN(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	vlanIf, err := VLANName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	if op.Mode == VLANAccess && op.AccessIfName != "" && ValidIfName(op.AccessIfName) && ParseVID(op.VID) == nil && !e.SkipHostCmds {
		_ = e.run(ctx, BridgeBin, "vlan", "del", "dev", op.AccessIfName, "vid", fmt.Sprintf("%d", op.VID))
	}
	return e.removeAdvanced(ctx, op, ActionVLANRemove, vlanIf, []string{op.ParentIfName, op.AccessIfName}, "-vlan.", "-parent.")
}

func (e *Engine) removeBond(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	bondIf, err := BondName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	guard := append([]string{bondIf}, op.Members...)
	return e.removeAdvanced(ctx, op, ActionBondRemove, bondIf, guard, "-bond.", "-m")
}

func (e *Engine) removeOverlay(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	ifname, err := OverlayName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	return e.removeAdvanced(ctx, op, ActionOverlayRemove, ifname, nil, "-vxlan.")
}

// clearPolicies drops the bridge policy table when no policy remains.
func (e *Engine) clearPolicies(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	res := AdvancedResult{Action: ActionPolicyClear, ObjectID: op.ObjectID, Status: StatusUnavailable, Reason: "removed"}
	if e.SkipHostCmds {
		return res, nil
	}
	_ = e.run(ctx, NFTBin, "delete", "table", "bridge", PolicyTable)
	_ = os.Remove(filepath.Join(e.stateDir(), "nft", "ndl-policy.nft"))
	return res, nil
}
