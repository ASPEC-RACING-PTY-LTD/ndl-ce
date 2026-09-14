package ndnet

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// OverlayName is the Linux locator for a VXLAN prep UUID.
func OverlayName(id string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil {
		return "", fmt.Errorf("overlay id must be a UUID")
	}
	hex := strings.ReplaceAll(parsed.String(), "-", "")
	return "ndlo" + hex[:8], nil
}

// ParseOverlayVNI accepts 1-16777215. VNI 0 is reserved and cannot apply.
func ParseOverlayVNI(vni uint32) error {
	if vni == 0 || vni > 16777215 {
		return fmt.Errorf("overlay vni is invalid")
	}
	return nil
}

func overlayFiles(id, ifname string, vni uint32) []File {
	netdev := "[NetDev]\nName=" + ifname + "\nKind=vxlan\n\n[VXLAN]\nVNI=" + fmt.Sprintf("%d", vni) + "\nIndependent=true\n"
	network := "[Match]\nName=" + ifname + "\n\n[Network]\nLinkLocalAddressing=no\n"
	return []File{
		{RelPath: persistName(id, "-vxlan.netdev"), Body: netdev},
		{RelPath: persistName(id, "-vxlan.network"), Body: network},
	}
}

func (e *Engine) applyOverlay(_ context.Context, op AdvancedOp) (AdvancedResult, error) {
	if err := ParseOverlayVNI(op.OverlayVNI); err != nil {
		return AdvancedResult{}, err
	}
	ifname, err := OverlayName(op.ObjectID)
	if err != nil {
		return AdvancedResult{}, err
	}
	files := overlayFiles(strings.ToLower(op.ObjectID), ifname, op.OverlayVNI)
	if err := e.writeOwned(files); err != nil {
		return AdvancedResult{}, err
	}
	return AdvancedResult{
		Action: ActionOverlayPrep, ObjectID: op.ObjectID, Locator: ifname,
		Status: StatusAvailable, Files: files, Warnings: []string{OverlayPrepMsg},
		Reason: OverlayPrepMsg,
	}, nil
}
