package ndnet

import (
	"os"
	"path/filepath"
	"strings"
)

// The LAN-bridge's .network is rebuilt from what the host shows at the
// time, and its Gateway= line is written only while the default route is
// seen on the bridge or its uplink. A rebuild that runs while the route is
// missing, or while it is misread, would drop Gateway=, and the networkd
// reload that follows deletes the management default route: the host goes
// offline and stays offline across reboots. So a rebuild never drops a
// gateway the persisted file has unless the host plainly routes by default
// through another interface it has.

// keepManagementGateway carries Gateway= (and DNS= when the rebuild has
// none) over from the persisted .network into plan's, when the rebuild
// lost them. It reports whether it changed the plan.
func (e *Engine) keepManagementGateway(plan *Plan, host HostView) bool {
	if plan.Kind != KindLANBridge || movedDefaultRoute(host, plan.BridgeName, plan.UplinkIfName) {
		return false
	}
	name := persistName(plan.NetworkID, ".network")
	for i := range plan.Files {
		if filepath.Base(plan.Files[i].RelPath) != name {
			continue
		}
		body := plan.Files[i].Body
		if !staticNetwork(body) || len(iniValues(body, "Gateway")) > 0 {
			return false
		}
		old, err := os.ReadFile(filepath.Join(e.networkDir(), name))
		if err != nil {
			return false
		}
		gws := iniValues(string(old), "Gateway")
		if len(gws) == 0 {
			return false
		}
		var add []string
		for _, gw := range gws {
			add = append(add, "Gateway="+gw)
		}
		if len(iniValues(body, "DNS")) == 0 {
			for _, ns := range iniValues(string(old), "DNS") {
				add = append(add, "DNS="+ns)
			}
		}
		plan.Files[i].Body = strings.TrimRight(body, "\n") + "\n" + strings.Join(add, "\n") + "\n"
		plan.Warnings = append(plan.Warnings, "kept the management gateway "+strings.Join(gws, ", ")+": the host shows no default route on "+plan.BridgeName+" right now")
		return true
	}
	return false
}

// movedDefaultRoute reports a default route through a real host interface
// other than the bridge and its uplink: the operator moved management
// elsewhere, so the bridge may stop carrying a gateway. A missing default
// route, or one through an interface the host does not have (a reading
// taken from another network namespace), is not a move.
func movedDefaultRoute(host HostView, bridge, uplink string) bool {
	dev := strings.TrimSpace(host.DefaultRouteIf)
	if dev == "" || sameIface(dev, bridge) || sameIface(dev, uplink) {
		return false
	}
	_, ok := lookup(host, dev)
	return ok
}

// persistedGateways is the Gateway= list of the network's persisted
// .network file.
func (e *Engine) persistedGateways(networkID string) []string {
	raw, err := os.ReadFile(filepath.Join(e.networkDir(), persistName(networkID, ".network")))
	if err != nil {
		return nil
	}
	return iniValues(string(raw), "Gateway")
}

func staticNetwork(body string) bool {
	for _, v := range iniValues(body, "DHCP") {
		if !strings.EqualFold(v, "no") {
			return false
		}
	}
	return len(iniValues(body, "Address")) > 0
}

// iniValues lists the values of key= lines in a systemd unit body.
func iniValues(body, key string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}
