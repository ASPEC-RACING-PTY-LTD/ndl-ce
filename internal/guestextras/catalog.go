package guestextras

import "strings"

const (
	Docker         = "docker"
	Compose        = "compose"
	Git            = "git"
	GitHubCLI      = "gh"
	Curl           = "curl"
	Wget           = "wget"
	Jq             = "jq"
	CACertificates = "ca-certificates"
	Unzip          = "unzip"
	Zip            = "zip"
	// CodeholdCLI is CodeHold's "ch" command line client. It is not a
	// distribution package: the binary is downloaded from the configured
	// CodeHold server (see Download).
	CodeholdCLI = "codehold-cli"
	// Cloudflared is the Cloudflare Tunnel connector, installed from
	// Cloudflare's signed apt repository.
	Cloudflared = "cloudflared"
	Tailscale   = "tailscale"
	Python      = "python3"
	NodeJS      = "nodejs"
	Golang      = "golang"
	BuildTools  = "build-essential"
	Nginx       = "nginx"
	Caddy       = "caddy"
	Postgres    = "postgresql"
	MariaDB     = "mariadb"
	Redis       = "redis"
	Fail2ban    = "fail2ban"
	Htop        = "htop"
	Tmux        = "tmux"
	Vim         = "vim"
	Rsync       = "rsync"
	Restic      = "restic"
	NetTools    = "net-tools"
	Openssh     = "openssh-server"
	Podman      = "podman"
	Ansible     = "ansible"
	Wireguard   = "wireguard-tools"
)

const (
	CategoryContainers  = "containers"
	CategoryDevelopment = "development"
	CategoryUtilities   = "utilities"
	CategoryNetworking  = "networking"
	CategoryWeb         = "web"
	CategoryDatabases   = "databases"
	CategoryTools       = "tools"
)

// Extra is a guest package the operator can preinstall on a new system container.
type Extra struct {
	ID           string
	Name         string
	Description  string
	Category     string
	Requires     []string
	NeedsNesting bool
	// Download marks an extra installed from a download instead of the
	// guest's package manager. Prereqs are the packages that download needs.
	Download bool
	Prereqs  []string
	// Baseline extras go on every new system container without being
	// offered; the UI only shows them when installing one failed.
	Baseline bool
	// DebianOnly extras are packaged for Debian-based images only.
	DebianOnly bool
}

// Availability describes whether an extra can be installed for a guest image.
type Availability struct {
	Extra     Extra
	Available bool
	Reason    string
	Packages  []string
}

// Catalog is the extras the UI may offer. Installation is family-specific.
func Catalog() []Extra {
	return []Extra{
		{ID: Docker, Name: "Docker Engine", Description: "Container runtime", Category: CategoryContainers, NeedsNesting: true},
		{ID: Compose, Name: "Docker Compose", Description: "Compose v2 plugin", Category: CategoryContainers, Requires: []string{Docker}, NeedsNesting: true},
		{ID: Podman, Name: "Podman", Description: "Daemonless container engine", Category: CategoryContainers, NeedsNesting: true},
		{ID: Cloudflared, Name: "Cloudflare Tunnel (cloudflared)", Description: "Publish services through a Cloudflare Tunnel", Category: CategoryNetworking, Download: true, Prereqs: []string{Curl, CACertificates}, DebianOnly: true},
		{ID: Tailscale, Name: "Tailscale", Description: "Join your tailnet (WireGuard mesh VPN)", Category: CategoryNetworking, NeedsNesting: false},
		{ID: Wireguard, Name: "WireGuard tools", Description: "wg and wg-quick", Category: CategoryNetworking},
		{ID: Openssh, Name: "OpenSSH server", Description: "Log in over SSH", Category: CategoryNetworking},
		{ID: Fail2ban, Name: "Fail2ban", Description: "Ban hosts that brute-force logins", Category: CategoryNetworking},
		{ID: Nginx, Name: "Nginx", Description: "Web server and reverse proxy", Category: CategoryWeb},
		{ID: Caddy, Name: "Caddy", Description: "Web server with automatic HTTPS", Category: CategoryWeb},
		{ID: Postgres, Name: "PostgreSQL", Description: "Relational database", Category: CategoryDatabases},
		{ID: MariaDB, Name: "MariaDB", Description: "MySQL-compatible database", Category: CategoryDatabases},
		{ID: Redis, Name: "Redis", Description: "In-memory key-value store", Category: CategoryDatabases},
		{ID: Git, Name: "Git", Description: "Version control", Category: CategoryDevelopment},
		{ID: GitHubCLI, Name: "GitHub CLI", Description: "GitHub command line", Category: CategoryDevelopment},
		{ID: CodeholdCLI, Name: "CodeHold CLI (ch)", Description: "CodeHold Git client", Category: CategoryDevelopment, Download: true, Prereqs: []string{Curl, CACertificates}},
		{ID: Python, Name: "Python 3", Description: "Python with pip and venv", Category: CategoryDevelopment},
		{ID: NodeJS, Name: "Node.js", Description: "JavaScript runtime with npm", Category: CategoryDevelopment},
		{ID: Golang, Name: "Go", Description: "Go toolchain", Category: CategoryDevelopment},
		{ID: BuildTools, Name: "Build tools", Description: "gcc, make and friends", Category: CategoryDevelopment},
		{ID: Ansible, Name: "Ansible", Description: "Configuration management", Category: CategoryDevelopment},
		{ID: Htop, Name: "htop", Description: "Process viewer", Category: CategoryTools},
		{ID: Tmux, Name: "tmux", Description: "Terminal multiplexer", Category: CategoryTools},
		{ID: Vim, Name: "Vim", Description: "Text editor", Category: CategoryTools},
		{ID: Rsync, Name: "rsync", Description: "Fast file sync", Category: CategoryTools},
		{ID: Restic, Name: "restic", Description: "Encrypted backups", Category: CategoryTools},
		{ID: NetTools, Name: "Network tools", Description: "ping, dig, ip, ss and friends", Category: CategoryTools},
		// Baseline: on every new system container, not offered.
		{ID: Curl, Name: "curl", Description: "HTTP client", Category: CategoryUtilities, Baseline: true},
		{ID: Wget, Name: "wget", Description: "File transfer", Category: CategoryUtilities, Baseline: true},
		{ID: Jq, Name: "jq", Description: "JSON processor", Category: CategoryUtilities, Baseline: true},
		{ID: CACertificates, Name: "ca-certificates", Description: "TLS trust store", Category: CategoryUtilities, Baseline: true},
		{ID: Unzip, Name: "unzip", Description: "Extract zip archives", Category: CategoryUtilities, Baseline: true},
		{ID: Zip, Name: "zip", Description: "Create zip archives", Category: CategoryUtilities, Baseline: true},
	}
}

// Family is the guest OS family encoded in an official image pin.
func Family(pin string) string {
	pin = strings.ToLower(strings.TrimSpace(pin))
	if pin == "" {
		return ""
	}
	return strings.Split(pin, "/")[0]
}

func byID(id string) (Extra, bool) {
	id = strings.TrimSpace(strings.ToLower(id))
	for _, extra := range Catalog() {
		if extra.ID == id {
			return extra, true
		}
	}
	return Extra{}, false
}

// BaselineIDs are the extras every new system container gets.
func BaselineIDs() []string {
	var out []string
	for _, e := range Catalog() {
		if e.Baseline {
			out = append(out, e.ID)
		}
	}
	return out
}

// WithBaseline is selected plus the baseline extras.
func WithBaseline(selected []string) []string {
	return Resolve(append(BaselineIDs(), selected...))
}

// IsBaseline reports whether id is a baseline extra.
func IsBaseline(id string) bool {
	e, ok := byID(id)
	return ok && e.Baseline
}

// Resolve expands dependencies (Compose requires Docker) and drops unknowns.
func Resolve(selected []string) []string {
	seen := map[string]struct{}{}
	var out []string
	var add func(string)
	add = func(id string) {
		extra, ok := byID(id)
		if !ok {
			return
		}
		if _, exists := seen[extra.ID]; exists {
			return
		}
		for _, req := range extra.Requires {
			add(req)
		}
		if _, exists := seen[extra.ID]; exists {
			return
		}
		seen[extra.ID] = struct{}{}
		out = append(out, extra.ID)
	}
	for _, id := range selected {
		add(id)
	}
	return out
}

// NeedsNesting reports whether any selected extra requires LXC nesting.
func NeedsNesting(selected []string) bool {
	for _, id := range Resolve(selected) {
		if extra, ok := byID(id); ok && extra.NeedsNesting {
			return true
		}
	}
	return false
}

// AvailabilityFor reports whether extraID can be installed on pin.
func AvailabilityFor(pin, extraID string) Availability {
	extra, ok := byID(extraID)
	if !ok {
		return Availability{Reason: "Unknown extra"}
	}
	av := Availability{Extra: extra, Available: true}
	family := Family(pin)
	if extra.DebianOnly && family != "debian" && pin != "" {
		av.Available = false
		av.Reason = extra.Name + " is only packaged for Debian-based images"
		return av
	}
	if extra.Download {
		return downloadAvailability(av, pin, family)
	}
	switch family {
	case "debian":
		av.Packages = debianPackages(extra.ID)
	case "alpine":
		av.Packages = alpinePackages(extra.ID)
	default:
		if pin == "" {
			av.Available = false
			av.Reason = "Select a guest operating system first"
			return av
		}
		av.Available = false
		av.Reason = "This extra is not packaged for " + family
		return av
	}
	if len(av.Packages) == 0 {
		av.Available = false
		av.Reason = extra.Name + " is not in this image's default repositories"
	}
	return av
}

// Describe returns catalog rows for an image pin, including availability.
// downloadAvailability covers extras fetched over HTTPS: they need a
// supported family and CPU type, and Packages lists the prerequisites.
func downloadAvailability(av Availability, pin, family string) Availability {
	if family != "debian" && family != "alpine" {
		av.Available = false
		if pin == "" {
			av.Reason = "Select a guest operating system first"
		} else {
			av.Reason = av.Extra.Name + " is not supported on " + family
		}
		return av
	}
	if a := Arch(pin); a != "" && a != "amd64" && a != "arm64" {
		av.Available = false
		av.Reason = av.Extra.Name + " is not built for " + a
		return av
	}
	for _, id := range av.Extra.Prereqs {
		if family == "alpine" {
			av.Packages = append(av.Packages, alpinePackages(id)...)
		} else {
			av.Packages = append(av.Packages, debianPackages(id)...)
		}
	}
	return av
}

// Arch is the CPU type in an image pin (family/release/arch/variant), or ""
// when the pin does not name one.
func Arch(pin string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(pin)), "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

func Describe(pin string) []map[string]any {
	out := make([]map[string]any, 0, len(Catalog()))
	for _, extra := range Catalog() {
		av := AvailabilityFor(pin, extra.ID)
		item := map[string]any{
			"id":            extra.ID,
			"name":          extra.Name,
			"description":   extra.Description,
			"category":      extra.Category,
			"requires":      extra.Requires,
			"needs_nesting": extra.NeedsNesting,
			"available":     av.Available,
			"download":      extra.Download,
			"baseline":      extra.Baseline,
		}
		if av.Reason != "" {
			item["unavailable_reason"] = av.Reason
		}
		out = append(out, item)
	}
	return out
}

func debianPackages(id string) []string {
	switch id {
	case Docker:
		return []string{"docker.io"}
	case Compose:
		// Debian 13 (trixie) ships Compose v2 as docker-compose.
		// Older suites / backports still use docker-compose-v2.
		// Install tries these one at a time and verifies `docker compose version`.
		return []string{"docker-compose-v2", "docker-compose"}
	case Git:
		return []string{"git"}
	case GitHubCLI:
		return nil
	case Curl:
		return []string{"curl"}
	case Wget:
		return []string{"wget"}
	case Jq:
		return []string{"jq"}
	case CACertificates:
		return []string{"ca-certificates"}
	case Unzip:
		return []string{"unzip"}
	case Zip:
		return []string{"zip"}
	case Tailscale:
		return []string{"tailscale"}
	case Wireguard:
		return []string{"wireguard-tools"}
	case Openssh:
		return []string{"openssh-server"}
	case Fail2ban:
		return []string{"fail2ban"}
	case Nginx:
		return []string{"nginx"}
	case Caddy:
		return []string{"caddy"}
	case Postgres:
		return []string{"postgresql"}
	case MariaDB:
		return []string{"mariadb-server"}
	case Redis:
		return []string{"redis-server"}
	case Python:
		return []string{"python3", "python3-pip", "python3-venv"}
	case NodeJS:
		return []string{"nodejs", "npm"}
	case Golang:
		return []string{"golang-go"}
	case BuildTools:
		return []string{"build-essential"}
	case Ansible:
		return []string{"ansible"}
	case Htop:
		return []string{"htop"}
	case Tmux:
		return []string{"tmux"}
	case Vim:
		return []string{"vim"}
	case Rsync:
		return []string{"rsync"}
	case Restic:
		return []string{"restic"}
	case NetTools:
		return []string{"iputils-ping", "dnsutils", "iproute2", "net-tools"}
	case Podman:
		return []string{"podman"}
	default:
		return nil
	}
}

func alpinePackages(id string) []string {
	switch id {
	case Docker:
		return []string{"docker"}
	case Compose:
		return []string{"docker-cli-compose"}
	case Git:
		return []string{"git"}
	case GitHubCLI:
		return []string{"github-cli"}
	case Curl:
		return []string{"curl"}
	case Wget:
		return []string{"wget"}
	case Jq:
		return []string{"jq"}
	case CACertificates:
		return []string{"ca-certificates"}
	case Unzip:
		return []string{"unzip"}
	case Zip:
		return []string{"zip"}
	case Tailscale:
		return []string{"tailscale"}
	case Wireguard:
		return []string{"wireguard-tools"}
	case Openssh:
		return []string{"openssh"}
	case Fail2ban:
		return []string{"fail2ban"}
	case Nginx:
		return []string{"nginx"}
	case Caddy:
		return []string{"caddy"}
	case Postgres:
		return []string{"postgresql"}
	case MariaDB:
		return []string{"mariadb", "mariadb-client"}
	case Redis:
		return []string{"redis"}
	case Python:
		return []string{"python3", "py3-pip"}
	case NodeJS:
		return []string{"nodejs", "npm"}
	case Golang:
		return []string{"go"}
	case BuildTools:
		return []string{"build-base"}
	case Ansible:
		return []string{"ansible"}
	case Htop:
		return []string{"htop"}
	case Tmux:
		return []string{"tmux"}
	case Vim:
		return []string{"vim"}
	case Rsync:
		return []string{"rsync"}
	case Restic:
		return []string{"restic"}
	case NetTools:
		return []string{"iputils", "bind-tools", "iproute2", "net-tools"}
	case Podman:
		return []string{"podman"}
	default:
		return nil
	}
}
