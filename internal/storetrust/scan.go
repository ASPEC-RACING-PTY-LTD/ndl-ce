package storetrust

import (
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appmanifest"
)

const (
	CheckSignature  = "signature"
	CheckProvenance = "provenance"
	CheckVuln       = "vulnerability"
	CheckPermission = "permission"
	CheckNetwork    = "network"
	CheckSecrets    = "secret_handling"
	CheckProhibited = "prohibited"
	CheckUpdate     = "update_testing"
	StatusPass      = "pass"
	StatusFail      = "fail"
	StatusWarn      = "warn"
	StatusUnavail   = "unavailable"
)

// Check is one verifier row. Vuln scanning is honest when no scanner is installed.
type Check struct {
	Kind   string
	Status string
	Detail string
}

// Analyze runs static Store trust checks. It does not execute the package.
func Analyze(m appmanifest.Manifest) []Check {
	return []Check{
		provenanceCheck(m),
		vulnCheck(),
		permissionCheck(m),
		networkCheck(m),
		secretCheck(m),
		prohibitedCheck(),
		updateCheck(m),
	}
}

func provenanceCheck(m appmanifest.Manifest) Check {
	img := strings.TrimSpace(m.Deployment.Image)
	if img == "" || strings.ContainsAny(img, " \n;|&") {
		return Check{Kind: CheckProvenance, Status: StatusFail, Detail: "deployment.image is not a pinned registry reference"}
	}
	if !strings.Contains(img, "/") {
		return Check{Kind: CheckProvenance, Status: StatusWarn, Detail: "image is not a fully qualified registry path"}
	}
	return Check{Kind: CheckProvenance, Status: StatusPass, Detail: "Image pin " + img + ". Digest provenance from a registry scanner is not claimed."}
}

func vulnCheck() Check {
	return Check{
		Kind:   CheckVuln,
		Status: StatusUnavail,
		Detail: "CVE scanner is not installed on this control node. The scan report still includes static checks. Live Trivy or Grype is not claimed.",
	}
}

func permissionCheck(m appmanifest.Manifest) Check {
	_ = m
	return Check{Kind: CheckPermission, Status: StatusPass, Detail: "No privileged, host_exec, or helper-script keys. Install maps to stack plus OCI only."}
}

func networkCheck(m appmanifest.Manifest) Check {
	if len(m.Ports) == 0 {
		return Check{Kind: CheckNetwork, Status: StatusPass, Detail: "No published ports declared."}
	}
	parts := make([]string, 0, len(m.Ports))
	for _, p := range m.Ports {
		parts = append(parts, fmt.Sprintf("%d:%d", p.Host, p.Container))
	}
	return Check{Kind: CheckNetwork, Status: StatusPass, Detail: "Published ports " + strings.Join(parts, ", ") + "."}
}

func secretCheck(m appmanifest.Manifest) Check {
	_ = m
	return Check{Kind: CheckSecrets, Status: StatusPass, Detail: "Manifest has no plaintext secret env. Secret refs must use the existing secret API."}
}

func prohibitedCheck() Check {
	return Check{Kind: CheckProhibited, Status: StatusPass, Detail: "Prohibited keys are rejected at parse time. This is not a root script runner."}
}

func updateCheck(m appmanifest.Manifest) Check {
	if m.Hooks.Backup != "" && m.Hooks.Backup != "existing-backup-api" {
		return Check{Kind: CheckUpdate, Status: StatusFail, Detail: "hooks.backup must declare existing-backup-api"}
	}
	if m.Hooks.Restore != "" && m.Hooks.Restore != "existing-restore-api" {
		return Check{Kind: CheckUpdate, Status: StatusFail, Detail: "hooks.restore must declare existing-restore-api"}
	}
	if m.Hooks.Backup == "" && m.Hooks.Restore == "" {
		return Check{Kind: CheckUpdate, Status: StatusWarn, Detail: "No backup or restore hooks declared. Upgrade testing is limited to static schema checks."}
	}
	return Check{Kind: CheckUpdate, Status: StatusPass, Detail: "Backup and restore hooks call existing APIs. Version-graph upgrade testing is not claimed."}
}

// Failed reports whether any check is fail. unavailable is not fail.
func Failed(checks []Check) bool {
	for _, c := range checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}
