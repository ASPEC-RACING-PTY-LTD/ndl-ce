package storetrust

import (
	"fmt"
	"strings"

	"github.com/no-dal/ndl-ce/internal/appmanifest"
	"gopkg.in/yaml.v3"
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

// prohibitedKeys matches the Store manifest parser. Analyze can fail them
// without going through ParseYAML so this check is not a rubber stamp.
var prohibitedKeys = []string{
	"run", "script", "bash", "exec", "helper", "postinst", "preinst", "command_script", "host_exec",
}

var dangerousGrants = []string{
	"privileged", "host_exec", "helper", "helper-script", "host_path", "root", "bash", "script", "exec",
}

// Check is one verifier row. Vuln scanning is honest when no scanner is installed.
type Check struct {
	Kind   string
	Status string
	Detail string
}

// Analyze runs static Store trust checks. It does not execute the package.
// raw is the scanned YAML so prohibited keys can fail even when parse already rejected them.
func Analyze(m appmanifest.Manifest, raw []byte) []Check {
	checks := []Check{
		provenanceCheck(m),
		vulnCheck(),
		permissionCheck(m),
		networkCheck(m),
		secretCheck(m),
	}
	if c, ok := prohibitedCheck(raw); ok {
		checks = append(checks, c)
	}
	return append(checks, updateCheck(m))
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
	if len(m.Permissions) == 0 {
		return Check{Kind: CheckPermission, Status: StatusPass, Detail: "No extra permission grants declared. Install maps to stack plus OCI only."}
	}
	var bad []string
	seen := map[string]bool{}
	for _, g := range m.Permissions {
		key := strings.ToLower(strings.TrimSpace(g))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		for _, d := range dangerousGrants {
			if key == d {
				bad = append(bad, g)
				break
			}
		}
	}
	if len(bad) > 0 {
		return Check{Kind: CheckPermission, Status: StatusFail, Detail: "Declared grants are not allowed: " + strings.Join(bad, ", ") + "."}
	}
	return Check{Kind: CheckPermission, Status: StatusPass, Detail: "Declared grants " + strings.Join(m.Permissions, ", ") + ". These are recorded and not executed as host scripts."}
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
	fields := []string{m.Title, m.Summary, m.Deployment.Image, m.Hooks.Backup, m.Hooks.Restore}
	for _, a := range m.AIActions {
		fields = append(fields, a.Title, a.Declaration)
	}
	for _, g := range m.Permissions {
		fields = append(fields, g)
	}
	for _, f := range fields {
		if hit := plaintextSecretHint(f); hit != "" {
			return Check{Kind: CheckSecrets, Status: StatusFail, Detail: "Manifest declares a plaintext secret value (" + hit + "). Secret refs must use the existing secret API."}
		}
	}
	return Check{Kind: CheckSecrets, Status: StatusPass, Detail: "Inspected declared title, summary, image, hooks, permissions, and AI actions. No plaintext secret env is present."}
}

func plaintextSecretHint(s string) string {
	low := strings.ToLower(s)
	for _, key := range []string{"password=", "secret=", "api_key=", "apikey=", "token="} {
		if strings.Contains(low, key) {
			return strings.TrimSuffix(key, "=")
		}
	}
	if strings.Contains(low, "-----begin") && strings.Contains(low, "private") {
		return "private key"
	}
	return ""
}

func prohibitedCheck(raw []byte) (Check, bool) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return Check{}, false
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return Check{Kind: CheckProhibited, Status: StatusFail, Detail: "manifest yaml is invalid"}, true
	}
	if bad := firstProhibitedKey(&root); bad != "" {
		return Check{
			Kind:   CheckProhibited,
			Status: StatusFail,
			Detail: fmt.Sprintf("prohibited key %q; Store packages are declarative and do not run helper scripts", bad),
		}, true
	}
	return Check{Kind: CheckProhibited, Status: StatusPass, Detail: "No prohibited helper-script keys in the scanned YAML."}, true
}

func firstProhibitedKey(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := strings.ToLower(strings.TrimSpace(n.Content[i].Value))
			for _, bad := range prohibitedKeys {
				if key == bad {
					return bad
				}
			}
			if hit := firstProhibitedKey(n.Content[i+1]); hit != "" {
				return hit
			}
		}
		return ""
	}
	for _, c := range n.Content {
		if hit := firstProhibitedKey(c); hit != "" {
			return hit
		}
	}
	return ""
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
