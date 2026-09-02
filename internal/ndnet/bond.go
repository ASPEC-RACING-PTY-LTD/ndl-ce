package ndnet

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// BondName is the Linux locator for a bond UUID.
func BondName(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("bond id must be a UUID")
	}
	hex := strings.ReplaceAll(parsed.String(), "-", "")
	return "ndlb" + hex[:8], nil
}

func parseBondMode(mode string) (string, error) {
	switch strings.TrimSpace(mode) {
	case "", BondActiveBackup:
		return BondActiveBackup, nil
	case BondLACP, "lacp":
		return BondLACP, nil
	default:
		return "", fmt.Errorf("bond mode must be active-backup or 802.3ad")
	}
}

func bondFiles(id, bondIf, mode string, members []string) []File {
	body := "[NetDev]\nName=" + bondIf + "\nKind=bond\n\n[Bond]\nMode=" + mode + "\nMIIMonitorSec=1s\n"
	if mode == BondActiveBackup && len(members) > 0 {
		body += "PrimarySlave=" + members[0] + "\n"
	}
	files := []File{{RelPath: persistName(id, "-bond.netdev"), Body: body}}
	files = append(files, File{
		RelPath: persistName(id, "-bond.network"),
		Body:    "[Match]\nName=" + bondIf + "\n\n[Network]\nBindCarrier=" + strings.Join(members, " ") + "\n",
	})
	for i, m := range members {
		files = append(files, File{
			RelPath: persistName(id, fmt.Sprintf("-m%d.network", i)),
			Body:    "[Match]\nName=" + m + "\n\n[Network]\nBond=" + bondIf + "\n",
		})
	}
	return files
}

func (e *Engine) applyBond(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	mode, err := parseBondMode(op.Mode)
	if err != nil {
		return AdvancedResult{}, err
	}
	bondIf, err := BondName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	if len(op.Members) < 1 {
		return AdvancedResult{}, fmt.Errorf("bond requires at least one member interface")
	}
	for _, m := range op.Members {
		if !ValidIfName(m) {
			return AdvancedResult{}, fmt.Errorf("bond member interface name is not valid")
		}
		if m == bondIf {
			return AdvancedResult{}, fmt.Errorf("bond member cannot be the bond locator")
		}
	}
	host, err := e.host()
	if err != nil {
		return AdvancedResult{}, err
	}
	res := AdvancedResult{
		Action: ActionBondAdd, ObjectID: op.ObjectID, Locator: bondIf, Mode: mode,
		ManagementIfName: managementName(host), ManagementIfIndex: host.ManagementIfIndex,
	}
	if touchesManagement(host, op.Members...) {
		res.Warnings = append(res.Warnings, "bonding the management NIC requires typed confirm and the rollback watchdog")
		armed, err := e.maybeArm(ctx, true, op.ConfirmIfName, op.Members[0], host, op.ObjectID)
		if err != nil {
			return AdvancedResult{}, err
		}
		res.RollbackArmed = armed
	}
	res.Files = bondFiles(strings.ToLower(op.ObjectID), bondIf, mode, op.Members)
	if err := e.writeOwned(res.Files); err != nil {
		if res.RollbackArmed {
			_ = e.RestoreActive()
			res.RolledBack = true
		}
		return AdvancedResult{}, err
	}
	if err := e.reloadNetworkd(); err != nil && !e.SkipHostCmds {
		if res.RollbackArmed {
			_ = e.RestoreActive()
			res.RolledBack = true
		}
		return AdvancedResult{}, err
	}
	if res.RollbackArmed {
		after, _ := e.host()
		if err := e.probeManagement(after, Plan{ManagementIfName: res.ManagementIfName, ManagementIfIndex: res.ManagementIfIndex}); err != nil {
			_ = e.RestoreActive()
			res.Status = StatusUnavailable
			res.Reason = "probe failed; rolled back"
			res.RolledBack = true
			return res, err
		}
	}
	res.Status = StatusAvailable
	return res, nil
}
