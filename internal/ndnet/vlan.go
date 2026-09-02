package ndnet

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// VLANName is the Linux locator for a VLAN UUID. UUID remains identity.
func VLANName(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("vlan id must be a UUID")
	}
	hex := strings.ReplaceAll(parsed.String(), "-", "")
	return "ndlv" + hex[:8], nil
}

// ParseVID accepts 1-4094. VID is a locator tag, not identity.
func ParseVID(vid int) error {
	if vid < 1 || vid > 4094 {
		return fmt.Errorf("vlan id must be 1-4094")
	}
	return nil
}

// VLANAccessArgv sets an access port PVID. Parent and port are locators.
func VLANAccessArgv(dev string, vid int) ([]string, error) {
	if !ValidIfName(dev) {
		return nil, fmt.Errorf("access interface name is not valid")
	}
	if err := ParseVID(vid); err != nil {
		return nil, err
	}
	argv := []string{BridgeBin, "vlan", "add", "dev", dev, "vid", fmt.Sprintf("%d", vid), "pvid", "untagged"}
	if err := refuseShellArgv(argv); err != nil {
		return nil, err
	}
	return argv, nil
}

func vlanFiles(op AdvancedOp, vlanIf, parent string) []File {
	id := strings.ToLower(strings.TrimSpace(op.ObjectID))
	netdev := "[NetDev]\nName=" + vlanIf + "\nKind=vlan\n\n[VLAN]\nId=" + fmt.Sprintf("%d", op.VID) + "\n"
	vlanNet := "[Match]\nName=" + vlanIf + "\n\n[Network]\n"
	if op.BridgeName != "" && ValidIfName(op.BridgeName) {
		vlanNet += "Bridge=" + op.BridgeName + "\n"
	}
	parentNet := "[Match]\nName=" + parent + "\n\n[Network]\nVLAN=" + vlanIf + "\n"
	return []File{
		{RelPath: persistName(id, "-vlan.netdev"), Body: netdev},
		{RelPath: persistName(id, "-vlan.network"), Body: vlanNet},
		{RelPath: persistName(id, "-parent.network"), Body: parentNet},
	}
}

func (e *Engine) applyVLAN(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	if err := ParseVID(op.VID); err != nil {
		return AdvancedResult{}, err
	}
	vlanIf, err := VLANName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	parent := strings.TrimSpace(op.ParentIfName)
	if parent == "" {
		parent = strings.TrimSpace(op.BridgeName)
	}
	if !ValidIfName(parent) {
		return AdvancedResult{}, fmt.Errorf("vlan parent interface is required")
	}
	mode := strings.TrimSpace(op.Mode)
	if mode == "" {
		mode = VLANAccess
	}
	if mode != VLANAccess && mode != VLANTrunk {
		return AdvancedResult{}, fmt.Errorf("vlan mode must be access or trunk")
	}
	host, err := e.host()
	if err != nil {
		return AdvancedResult{}, err
	}
	res := AdvancedResult{
		Action: ActionVLANAdd, ObjectID: op.ObjectID, Locator: vlanIf, VID: op.VID, Mode: mode,
		ManagementIfName: managementName(host), ManagementIfIndex: host.ManagementIfIndex,
	}
	if touchesManagement(host, parent, op.AccessIfName) {
		res.Warnings = append(res.Warnings, "VLAN on the management path requires typed confirm and the rollback watchdog")
		armed, err := e.maybeArm(ctx, true, op.ConfirmIfName, firstNonEmptyIf(parent, op.AccessIfName), host, op.ObjectID)
		if err != nil {
			return AdvancedResult{}, err
		}
		res.RollbackArmed = armed
	}
	res.Files = vlanFiles(op, vlanIf, parent)
	if err := e.writeOwned(res.Files); err != nil {
		if res.RollbackArmed {
			_ = e.RestoreActive()
			res.RolledBack = true
		}
		return AdvancedResult{}, err
	}
	if mode == VLANAccess && op.AccessIfName != "" {
		argv, err := VLANAccessArgv(op.AccessIfName, op.VID)
		if err != nil {
			return AdvancedResult{}, err
		}
		res.Argv = argv
		if !e.SkipHostCmds {
			if err := e.run(ctx, argv[0], argv[1:]...); err != nil {
				if res.RollbackArmed {
					_ = e.RestoreActive()
					res.RolledBack = true
				}
				return AdvancedResult{}, err
			}
		}
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

func firstNonEmptyIf(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
