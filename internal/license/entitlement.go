package license

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DocumentVersion = "ndl-ee-entitlement-v1"
	EditionEE       = "ee"
	StatusExpired   = "expired"
	DefaultGrace    = 14 * 24 * time.Hour
	ImportConfirm   = "import-license"
)

// Document is the signed entitlement payload returned by the licensing API.
type Document struct {
	Version          string   `json:"version"`
	KeyID            string   `json:"key_id"`
	InstallationID   string   `json:"installation_id"`
	ClusterID        string   `json:"cluster_id"`
	Organization     string   `json:"organization"`
	SubscriptionID   string   `json:"subscription_id"`
	Edition          string   `json:"edition"`
	Capabilities     []string `json:"capabilities"`
	IssuedAt         string   `json:"issued_at"`
	ExpiresAt        string   `json:"expires_at"`
	GraceUntil       string   `json:"grace_until"`
	UpdateChannel    string   `json:"update_channel"`
	CECompatMin      string   `json:"ce_compat_min"`
	CECompatMax      string   `json:"ce_compat_max"`
	ArtifactVersion  string   `json:"artifact_version"`
	Accepted         bool     `json:"accepted"`
	Entitled         bool     `json:"entitled"`
	WorkloadsStopped bool     `json:"workloads_stopped"`
	Nonce            string   `json:"nonce,omitempty"`
	Signature        string   `json:"signature,omitempty"`
}

type ActivationRequest struct {
	Edition        string `json:"edition"`
	ClusterID      string `json:"cluster_id,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	CEVersion      string `json:"ce_version,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
}

type Snapshot struct {
	Edition          string   `json:"edition"`
	Status           string   `json:"status"`
	Reason           string   `json:"reason"`
	HasKey           bool     `json:"has_key"`
	KeySuffix        string   `json:"key_suffix,omitempty"`
	WorkloadsStopped bool     `json:"workloads_stopped"`
	EEBlobs          bool     `json:"ee_blobs"`
	EERuntime        bool     `json:"ee_runtime"`
	ContactsAPI      bool     `json:"contacts_api"`
	Degraded         bool     `json:"degraded"`
	Organization     string   `json:"organization,omitempty"`
	InstallationID   string   `json:"installation_id,omitempty"`
	SubscriptionID   string   `json:"subscription_id,omitempty"`
	UpdateChannel    string   `json:"update_channel,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
	GraceUntil       string   `json:"grace_until,omitempty"`
	LastChecked      string   `json:"last_checked,omitempty"`
	Signed           bool     `json:"signed"`
}

func (d Document) expiresAt() time.Time  { return parseTime(d.ExpiresAt) }
func (d Document) graceUntil() time.Time { return parseTime(d.GraceUntil) }

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func CanonicalJSON(d Document) ([]byte, error) {
	copyDoc := d
	copyDoc.Signature = ""
	caps := append([]string{}, copyDoc.Capabilities...)
	sort.Strings(caps)
	copyDoc.Capabilities = caps
	return json.Marshal(copyDoc)
}

func FilterKnown(caps []string) []string {
	out := make([]string, 0, len(caps))
	seen := map[string]struct{}{}
	for _, c := range caps {
		c = strings.TrimSpace(c)
		if !knownCapability(c) {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func HasCapability(caps []string, id string) bool {
	for _, c := range caps {
		if c == id {
			return true
		}
	}
	return false
}

func (d Document) ValidateShape() error {
	if d.Version != DocumentVersion {
		return fmt.Errorf("unsupported entitlement version")
	}
	if strings.TrimSpace(d.KeyID) == "" {
		return fmt.Errorf("key_id is required")
	}
	if d.WorkloadsStopped {
		return fmt.Errorf("entitlement must not stop workloads")
	}
	if d.Edition != EditionEE && d.Edition != EditionCE {
		return fmt.Errorf("edition must be ce or ee")
	}
	return nil
}

func ParseDocument(raw []byte) (Document, bool) {
	var d Document
	if err := json.Unmarshal(raw, &d); err != nil {
		return Document{}, false
	}
	return d, true
}
