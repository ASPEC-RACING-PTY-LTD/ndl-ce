package ndnet

import "time"

// Network kinds. Distro-neutral desired types.
const (
	KindIsolated    = "isolated"
	KindIsolatedNAT = "isolated-nat"
	KindLANBridge   = "lan-bridge"
)

// Danger classes returned by Classify.
const (
	DangerSafe      = "safe"
	DangerWarning   = "warning"
	DangerDangerous = "dangerous"
)

// Status values. Missing host state is unavailable, not empty.
const (
	StatusAvailable   = "available"
	StatusUnavailable = "unavailable"
	StatusChecking    = "checking"
	StatusWarning     = "warning"
)

// PersistNetworkd is the Debian 13 persistence implementation name.
// It is a locator kind, never a desired identity.
const PersistNetworkd = "systemd-networkd"

// DefaultIPv4 is the first-run isolated subnet.
const DefaultIPv4 = "10.64.0.0/24"

// ProbeWindow is the independent rollback watchdog duration.
const ProbeWindow = 120 * time.Second

// Reservation is a static DHCP mapping on an isolated bridge.
type Reservation struct {
	ID       string `json:"id"`
	MAC      string `json:"mac"`
	IPv4     string `json:"ipv4"`
	Hostname string `json:"hostname,omitempty"`
}

// Spec is a distro-neutral desired network. Bridge and uplink names are locators.
type Spec struct {
	NetworkID     string
	Name          string
	Kind          string
	IPv4CIDR      string
	DHCP          bool
	DNS           bool
	UplinkIfName  string
	ConfirmIfName string
	Reservations  []Reservation
	ArmRollback   bool
}

// Hint is enough observed locator state to re-check a network.
type Hint struct {
	NetworkID    string `json:"network_id"`
	Kind         string `json:"kind"`
	BridgeName   string `json:"bridge_name"`
	UplinkIfName string `json:"uplink_ifname,omitempty"`
}

// Iface is one observed host interface. Tests inject these via HostView.
type Iface struct {
	Name      string
	IfIndex   int
	Addresses []string
	Kind      string
	Master    string
	Up        bool
}

// HostView is the fake-netlink snapshot used for danger classification.
type HostView struct {
	Ifaces              []Iface
	DefaultRouteIf      string
	ManagementIfIndex   int
	ManagementIfName    string
	ManagementAddresses []string
}

// Classification is the danger result for a spec against a host view.
type Classification struct {
	Danger           string
	Reason           string
	RequiresConfirm  bool
	TypedIfName      string
	ManagementIfName string
	ManagementIndex  int
	SingleNIC        bool
}

// File is a generated persistence artifact. The path is a locator.
type File struct {
	RelPath string
	Body    string
}

// Plan is the fully generated apply payload. No user shell strings.
type Plan struct {
	NetworkID          string
	Name               string
	Kind               string
	BridgeName         string
	UplinkIfName       string
	IPv4CIDR           string
	Gateway            string
	DHCPStart          string
	DHCPEnd            string
	DHCP               bool
	DNS                bool
	NAT                bool
	Files              []File
	Dnsmasq            string
	NFT                string
	Class              Classification
	ManagementIfIndex  int
	ManagementIfName   string
	Warnings           []string
}

// Preview is the dry-run API/agent result.
type Preview struct {
	NetworkID         string   `json:"network_id"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	BridgeName        string   `json:"bridge_name"`
	UplinkIfName      string   `json:"uplink_ifname,omitempty"`
	IPv4CIDR          string   `json:"ipv4_cidr,omitempty"`
	Gateway           string   `json:"gateway,omitempty"`
	Danger            string   `json:"danger"`
	DangerReason      string   `json:"danger_reason"`
	RequiresConfirm   bool     `json:"requires_confirm"`
	TypedIfName       string   `json:"typed_ifname,omitempty"`
	DHCP              bool     `json:"dhcp"`
	DNS               bool     `json:"dns"`
	NAT               bool     `json:"nat"`
	Files             []File   `json:"files"`
	ManagementIfIndex int      `json:"management_ifindex"`
	ManagementIfName  string   `json:"management_ifname"`
	Warnings          []string `json:"warnings,omitempty"`
	DryRun            bool     `json:"dry_run"`
}

// ApplyResult is the agent apply outcome.
type ApplyResult struct {
	NetworkID         string   `json:"network_id"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	BridgeName        string   `json:"bridge_name"`
	UplinkIfName      string   `json:"uplink_ifname,omitempty"`
	IPv4CIDR          string   `json:"ipv4_cidr,omitempty"`
	Gateway           string   `json:"gateway,omitempty"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	DHCP              bool     `json:"dhcp"`
	DNS               bool     `json:"dns"`
	NAT               bool     `json:"nat"`
	ManagementIfIndex int      `json:"management_ifindex"`
	ManagementIfName  string   `json:"management_ifname"`
	RollbackArmed     bool     `json:"rollback_armed"`
	RolledBack        bool     `json:"rolled_back"`
	Warnings          []string `json:"warnings,omitempty"`
}

// ObservedNetwork is host-observed state for one desired network.
type ObservedNetwork struct {
	NetworkID         string   `json:"network_id"`
	Kind              string   `json:"kind"`
	BridgeName        string   `json:"bridge_name"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	DHCPRunning       bool     `json:"dhcp_running"`
	ManagementIfIndex int      `json:"management_ifindex"`
	Warnings          []string `json:"warnings,omitempty"`
}

// Observation is the agent network scrape.
type Observation struct {
	Networks          []ObservedNetwork `json:"networks"`
	ManagementIfIndex int               `json:"management_ifindex"`
	ManagementIfName  string            `json:"management_ifname"`
}

// Address is a desired address row (gateway on isolated nets).
type Address struct {
	ID        string
	NetworkID string
	Family    string
	CIDR      string
	Role      string
}
