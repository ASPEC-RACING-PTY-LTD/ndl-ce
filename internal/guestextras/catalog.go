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
)

const (
	CategoryContainers  = "containers"
	CategoryDevelopment = "development"
	CategoryUtilities   = "utilities"
)

// Extra is a guest package the operator can preinstall on a new system container.
type Extra struct {
	ID           string
	Name         string
	Description  string
	Category     string
	Requires     []string
	NeedsNesting bool
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
		{ID: Git, Name: "Git", Description: "Version control", Category: CategoryDevelopment},
		{ID: GitHubCLI, Name: "GitHub CLI", Description: "GitHub command line", Category: CategoryDevelopment},
		{ID: Curl, Name: "curl", Description: "HTTP client", Category: CategoryUtilities},
		{ID: Wget, Name: "wget", Description: "File transfer", Category: CategoryUtilities},
		{ID: Jq, Name: "jq", Description: "JSON processor", Category: CategoryUtilities},
		{ID: CACertificates, Name: "ca-certificates", Description: "TLS trust store", Category: CategoryUtilities},
		{ID: Unzip, Name: "unzip", Description: "Extract zip archives", Category: CategoryUtilities},
		{ID: Zip, Name: "zip", Description: "Create zip archives", Category: CategoryUtilities},
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
		return []string{"docker-compose-v2"}
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
	default:
		return nil
	}
}
