// Package helix holds the policy for the Helix integration: what an
// external AI operator (Helix) may see and do on the machines a SuperAdmin
// grants it. Everything here is enforced by NDL-CE; Helix only ever holds
// a credential, never a role.
package helix

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Capabilities a grant can hold on one machine.
const (
	// CapRead: status, resources, network statistics, Docker containers and
	// their health, and the system service list.
	CapRead = "read"
	// CapLogs: Docker container logs and system service journals. Logs can
	// hold secrets, so they are separate from read.
	CapLogs = "logs"
	// CapWrite: start, stop and restart Docker containers and system
	// services.
	CapWrite = "write"
	// CapExecute: run commands in the machine (system containers only).
	CapExecute = "execute"
	// CapDeploy: deploy the machine's deployment projects and roll them
	// back.
	CapDeploy = "deploy"
	// CapPower: start, stop and restart the machine itself.
	CapPower = "power"
)

// AllCaps lists every capability in display order.
var AllCaps = []string{CapRead, CapLogs, CapWrite, CapExecute, CapDeploy, CapPower}

// Grant is what Helix may do on one machine (an LXC or VM workload).
type Grant struct {
	MachineID string   `json:"machine_id"`
	Caps      []string `json:"caps"`
	// Protected marks critical infrastructure: every change (write,
	// execute, deploy, power) waits for a SuperAdmin's approval.
	Protected bool `json:"protected,omitempty"`
	// Destructive lets destructive operations run without approval on a
	// machine that is not protected. Off by default.
	Destructive bool `json:"destructive,omitempty"`
	// RunAs is the account commands run as. ExecUsers are the other
	// accounts Helix may ask for.
	RunAs     string   `json:"run_as,omitempty"`
	ExecUsers []string `json:"exec_users,omitempty"`
}

// Has reports whether the grant holds cap.
func (g Grant) Has(cap string) bool {
	for _, c := range g.Caps {
		if c == cap {
			return true
		}
	}
	return false
}

// Credential is one Helix API key. Only its hash is stored.
type Credential struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"hash"`
	CreatedAt  time.Time  `json:"created_at"`
	CreatedBy  string     `json:"created_by"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	RevokedBy  string     `json:"revoked_by,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP string     `json:"last_used_ip,omitempty"`
}

// Active reports whether the credential can be used at now.
func (c Credential) Active(now time.Time) bool {
	return c.RevokedAt == nil && (c.ExpiresAt == nil || now.Before(*c.ExpiresAt))
}

// Kill is the emergency stop.
type Kill struct {
	Engaged bool       `json:"engaged"`
	At      *time.Time `json:"at,omitempty"`
	By      string     `json:"by,omitempty"`
	Reason  string     `json:"reason,omitempty"`
}

// Thresholds raise machine.resource_high events.
type Thresholds struct {
	CPUPercent    int `json:"cpu_percent"`
	MemoryPercent int `json:"memory_percent"`
}

// Approval is a held operation waiting for (or given) a SuperAdmin's
// decision. It is bound to the exact request by its fingerprint and can be
// used once.
type Approval struct {
	ID          string     `json:"id"`
	Op          string     `json:"op"`
	MachineID   string     `json:"machine_id"`
	MachineName string     `json:"machine_name,omitempty"`
	Summary     string     `json:"summary"`
	Reasons     []string   `json:"reasons"`
	Fingerprint string     `json:"fingerprint"`
	Status      string     `json:"status"`
	RequestedAt time.Time  `json:"requested_at"`
	RequestedBy string     `json:"requested_by"`
	Note        string     `json:"note,omitempty"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
	UsedAt      *time.Time `json:"used_at,omitempty"`
}

// Approval states.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalDenied   = "denied"
	ApprovalUsed     = "used"
	ApprovalExpired  = "expired"
)

// How long approvals last.
const (
	PendingTTL   = 24 * time.Hour
	ApprovedTTL  = time.Hour
	MaxApprovals = 200
)

// Config is the whole integration, one document per cluster.
type Config struct {
	Enabled      bool         `json:"enabled"`
	Kill         Kill         `json:"kill"`
	Credentials  []Credential `json:"credentials"`
	AllowedIPs   []string     `json:"allowed_ips"`
	Grants       []Grant      `json:"grants"`
	Approvals    []Approval   `json:"approvals"`
	GlobalEvents bool         `json:"global_events"`
	Thresholds   Thresholds   `json:"thresholds"`
	// Initialised is set the first time the config is saved, so defaults
	// apply only to a new integration.
	Initialised bool `json:"initialised"`
}

// Defaults fills in a new or partial config.
func (c *Config) Defaults() {
	if !c.Initialised {
		c.GlobalEvents = true
		c.Initialised = true
	}
	if c.Thresholds.CPUPercent == 0 {
		c.Thresholds.CPUPercent = 90
	}
	if c.Thresholds.MemoryPercent == 0 {
		c.Thresholds.MemoryPercent = 90
	}
	if c.Credentials == nil {
		c.Credentials = []Credential{}
	}
	if c.AllowedIPs == nil {
		c.AllowedIPs = []string{}
	}
	if c.Grants == nil {
		c.Grants = []Grant{}
	}
	if c.Approvals == nil {
		c.Approvals = []Approval{}
	}
}

// Grant returns the grant for machine id.
func (c Config) Grant(id string) (Grant, bool) {
	for _, g := range c.Grants {
		if g.MachineID == id {
			return g, true
		}
	}
	return Grant{}, false
}

// IPAllowed reports whether ip may use the API. An empty list allows any.
func (c Config) IPAllowed(ip string) bool {
	if len(c.AllowedIPs) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, entry := range c.AllowedIPs {
		if p, err := netip.ParsePrefix(entry); err == nil && p.Contains(addr) {
			return true
		}
		if a, err := netip.ParseAddr(entry); err == nil && a.Unmap() == addr {
			return true
		}
	}
	return false
}

var userRE = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

// ValidUser reports whether u is a plain account name.
func ValidUser(u string) bool { return userRE.MatchString(u) }

// CleanAllowedIPs validates and normalises the allowlist.
func CleanAllowedIPs(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if p, err := netip.ParsePrefix(v); err == nil {
			v = p.Masked().String()
		} else if a, err := netip.ParseAddr(v); err == nil {
			v = a.Unmap().String()
		} else {
			return nil, fmt.Errorf("%q is not an IP address or CIDR range", raw)
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > 32 {
		return nil, fmt.Errorf("at most 32 addresses or ranges")
	}
	return out, nil
}

// CleanGrant validates one grant. isVM is true for virtual machines, which
// have no command execution, Docker or service control through NDL-CE.
func CleanGrant(g Grant, isVM bool) (Grant, error) {
	caps := []string{}
	seen := map[string]bool{}
	for _, c := range g.Caps {
		c = strings.TrimSpace(c)
		known := false
		for _, k := range AllCaps {
			if c == k {
				known = true
			}
		}
		if !known {
			return g, fmt.Errorf("%q is not a capability", c)
		}
		if isVM && (c == CapExecute || c == CapWrite || c == CapDeploy || c == CapLogs) {
			return g, fmt.Errorf("virtual machines support only read and power for Helix")
		}
		if !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	// Every other capability needs read: Helix must see what it changes.
	if len(caps) > 0 && !seen[CapRead] {
		caps = append([]string{CapRead}, caps...)
	}
	order := map[string]int{}
	for i, k := range AllCaps {
		order[k] = i
	}
	sort.SliceStable(caps, func(i, j int) bool { return order[caps[i]] < order[caps[j]] })
	g.Caps = caps
	g.RunAs = strings.TrimSpace(g.RunAs)
	if g.RunAs == "" {
		g.RunAs = "root"
	}
	if !ValidUser(g.RunAs) {
		return g, fmt.Errorf("%q is not a valid account name", g.RunAs)
	}
	users := []string{}
	for _, u := range g.ExecUsers {
		u = strings.TrimSpace(u)
		if u == "" || u == g.RunAs {
			continue
		}
		if !ValidUser(u) {
			return g, fmt.Errorf("%q is not a valid account name", u)
		}
		users = append(users, u)
	}
	g.ExecUsers = users
	if g.Protected {
		g.Destructive = false
	}
	return g, nil
}
