package ndnet

import (
	"context"
	"fmt"
	"net"
	"strings"
)

func parseMAC(v string) (string, error) {
	mac, err := net.ParseMAC(strings.TrimSpace(v))
	if err != nil {
		return "", fmt.Errorf("mac is invalid")
	}
	return mac.String(), nil
}

// RenderBridgePolicy is guest-to-guest nftables in the bridge family.
// It never emits an inet INPUT hook and never matches the management ifindex.
func RenderBridgePolicy(id, action, srcMAC, dstMAC, mgmtIf string) (string, error) {
	src, err := parseMAC(srcMAC)
	if err != nil {
		return "", err
	}
	dst, err := parseMAC(dstMAC)
	if err != nil {
		return "", err
	}
	if src == dst {
		return "", fmt.Errorf("policy source and destination must differ")
	}
	act := strings.ToLower(strings.TrimSpace(action))
	if act != "deny" && act != "drop" && act != "allow" && act != "accept" {
		return "", fmt.Errorf("policy action must be deny or allow")
	}
	verdict := "drop"
	if act == "allow" || act == "accept" {
		verdict = "accept"
	}
	var b strings.Builder
	b.WriteString("table bridge " + PolicyTable + " {\n")
	b.WriteString("  chain forward {\n")
	b.WriteString("    type filter hook forward priority 0; policy accept;\n")
	b.WriteString(fmt.Sprintf("    ether saddr %s ether daddr %s %s comment \"ndl-policy-%s\"\n", src, dst, verdict, id))
	b.WriteString(fmt.Sprintf("    ether saddr %s ether daddr %s %s comment \"ndl-policy-%s-rev\"\n", dst, src, verdict, id))
	b.WriteString("  }\n")
	b.WriteString("}\n")
	rules := b.String()
	if err := RefuseManagementINPUT(rules, mgmtIf); err != nil {
		return "", err
	}
	return rules, nil
}

// RefuseManagementINPUT is the Phase 27 security gate. Guest policy is bridge forward only.
func RefuseManagementINPUT(rules, mgmtIf string) error {
	low := strings.ToLower(rules)
	if strings.Contains(low, "hook input") {
		return fmt.Errorf("%s", MgmtINPUTMsg)
	}
	if strings.Contains(low, "type filter hook input") {
		return fmt.Errorf("%s", MgmtINPUTMsg)
	}
	if mgmtIf != "" && ValidIfName(mgmtIf) {
		if strings.Contains(low, "iifname \""+strings.ToLower(mgmtIf)+"\"") || strings.Contains(low, "iifname "+strings.ToLower(mgmtIf)) {
			return fmt.Errorf("%s", MgmtINPUTMsg)
		}
	}
	if strings.Contains(low, "chain input") && strings.Contains(low, "policy drop") {
		return fmt.Errorf("%s", MgmtINPUTMsg)
	}
	return nil
}

func (e *Engine) applyPolicy(ctx context.Context, op AdvancedOp) (AdvancedResult, error) {
	host, err := e.host()
	if err != nil {
		return AdvancedResult{}, err
	}
	mgmt := managementName(host)
	rules, err := RenderBridgePolicy(op.ObjectID, op.PolicyAction, op.SrcMAC, op.DstMAC, mgmt)
	if err != nil {
		return AdvancedResult{}, err
	}
	res := AdvancedResult{
		Action: ActionPolicyApply, ObjectID: op.ObjectID, NFT: rules, Status: StatusAvailable,
		ManagementIfName: mgmt, ManagementIfIndex: host.ManagementIfIndex,
	}
	if e.SkipHostCmds {
		return res, nil
	}
	path, err := e.writeNFTNamed("ndl-policy.nft", rules)
	if err != nil {
		return AdvancedResult{}, err
	}
	if err := e.run(ctx, NFTBin, "-c", "-f", path); err != nil {
		return AdvancedResult{}, err
	}
	_ = e.run(ctx, NFTBin, "delete", "table", "bridge", PolicyTable)
	if err := e.run(ctx, NFTBin, "-f", path); err != nil {
		return AdvancedResult{}, err
	}
	return res, nil
}
