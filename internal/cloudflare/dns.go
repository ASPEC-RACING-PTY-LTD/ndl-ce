package cloudflare

import (
	"fmt"
	"strings"
)

// TunnelSuffix is the CNAME target domain for Cloudflare tunnels.
const TunnelSuffix = ".cfargotunnel.com"

// TunnelTarget is the CNAME content that routes a hostname to a tunnel.
func TunnelTarget(tunnelID string) string { return strings.ToLower(tunnelID) + TunnelSuffix }

// TunnelOfTarget returns the tunnel ID a CNAME points at, or "".
func TunnelOfTarget(content string) string {
	c := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(content)), ".")
	if !strings.HasSuffix(c, TunnelSuffix) {
		return ""
	}
	return strings.TrimSuffix(c, TunnelSuffix)
}

// ZoneFor picks the zone a hostname belongs to (longest matching suffix).
func ZoneFor(hostname string, zones []Zone) (Zone, bool) {
	h := strings.TrimPrefix(strings.ToLower(hostname), "*.")
	var best Zone
	found := false
	for _, z := range zones {
		n := strings.ToLower(z.Name)
		if (h == n || strings.HasSuffix(h, "."+n)) && len(n) > len(best.Name) {
			best, found = z, true
		}
	}
	return best, found
}

// DNS plan actions.
const (
	DNSCreate   = "create"   // no record yet; one will be created
	DNSOK       = "ok"       // already a proxied CNAME to this tunnel
	DNSProxy    = "proxy"    // CNAME to this tunnel but not proxied; it will be proxied
	DNSRetarget = "retarget" // CNAME to another tunnel; only changed when moving the route
	DNSConflict = "conflict" // an unrelated record holds the name; never changed
	// DNSUnavailable means no saved token may read DNS in the zone, so DNS
	// is left alone and only the tunnel route is changed.
	DNSUnavailable = "unavailable"
)

// DNSPlan says what routing hostname to a tunnel would do to DNS.
type DNSPlan struct {
	Action        string     `json:"action"`
	Hostname      string     `json:"hostname"`
	Record        *DNSRecord `json:"record,omitempty"`
	CurrentTunnel string     `json:"current_tunnel,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

// PlanDNS compares the records already at hostname with a CNAME to tunnelID.
// A CNAME cannot share its name with any other record, so any record that is
// not a tunnel CNAME is a conflict and is left alone.
func PlanDNS(hostname, tunnelID string, records []DNSRecord) DNSPlan {
	p := DNSPlan{Hostname: hostname}
	var same []DNSRecord
	for _, r := range records {
		if strings.EqualFold(strings.TrimSuffix(r.Name, "."), hostname) {
			same = append(same, r)
		}
	}
	if len(same) == 0 {
		p.Action = DNSCreate
		return p
	}
	if len(same) == 1 && strings.EqualFold(same[0].Type, "CNAME") {
		r := same[0]
		p.Record = &r
		if t := TunnelOfTarget(r.Content); t != "" {
			if strings.EqualFold(t, tunnelID) {
				if r.Proxied {
					p.Action = DNSOK
				} else {
					p.Action, p.Reason = DNSProxy, "the record points at this tunnel but is not proxied, so it would not work"
				}
				return p
			}
			p.Action, p.CurrentTunnel = DNSRetarget, t
			p.Reason = "this hostname already points at another tunnel; move the route to change it"
			return p
		}
		p.Action = DNSConflict
		p.Reason = fmt.Sprintf("a CNAME to %s already uses this name", r.Content)
		return p
	}
	r := same[0]
	p.Record = &r
	p.Action = DNSConflict
	types := make([]string, 0, len(same))
	for _, x := range same {
		types = append(types, x.Type)
	}
	p.Reason = fmt.Sprintf("existing %s record(s) use this name; remove them in DNS first", strings.Join(types, ", "))
	return p
}

// ManagedComment marks records No-dal created so they can be recognised later.
func ManagedComment(tunnelName string) string {
	return "Managed by No-dal: tunnel " + tunnelName
}

// TunnelCNAME is the record that routes hostname to a tunnel.
func TunnelCNAME(hostname, tunnelID, tunnelName string) DNSRecord {
	return DNSRecord{Name: hostname, Type: "CNAME", Content: TunnelTarget(tunnelID), Proxied: true, TTL: 1, Comment: ManagedComment(tunnelName)}
}
