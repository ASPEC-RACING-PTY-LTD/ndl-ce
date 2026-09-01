package ndnet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ActiveRollback is persisted so the watchdog can restore without the control plane.
type ActiveRollback struct {
	ID                  string    `json:"id"`
	Deadline            time.Time `json:"deadline"`
	ManagementIfIndex   int       `json:"management_ifindex"`
	ManagementIfName    string    `json:"management_ifname"`
	ManagementAddresses []string  `json:"management_addresses,omitempty"`
	SnapshotDir         string    `json:"snapshot_dir"`
	TargetDir           string    `json:"target_dir"`
}

func (e *Engine) rollbackDir() string {
	return filepath.Join(e.stateDir(), "rollback")
}

func (e *Engine) activePath() string {
	return filepath.Join(e.rollbackDir(), "active.json")
}

func (e *Engine) okPath() string {
	return filepath.Join(e.rollbackDir(), "active.ok")
}

func (e *Engine) armRollback(plan Plan, host HostView) (ActiveRollback, error) {
	if err := os.MkdirAll(e.rollbackDir(), 0700); err != nil {
		return ActiveRollback{}, err
	}
	snap := filepath.Join(e.rollbackDir(), plan.NetworkID)
	if err := os.RemoveAll(snap); err != nil {
		return ActiveRollback{}, err
	}
	if err := os.MkdirAll(snap, 0700); err != nil {
		return ActiveRollback{}, err
	}
	if err := snapshotDir(e.networkDir(), snap); err != nil {
		return ActiveRollback{}, err
	}
	active := ActiveRollback{
		ID:                  plan.NetworkID,
		Deadline:            e.now().Add(ProbeWindow),
		ManagementIfIndex:   plan.ManagementIfIndex,
		ManagementIfName:    plan.ManagementIfName,
		ManagementAddresses: append([]string{}, host.ManagementAddresses...),
		SnapshotDir:         snap,
		TargetDir:           e.networkDir(),
	}
	body, err := json.Marshal(active)
	if err != nil {
		return ActiveRollback{}, err
	}
	_ = os.Remove(e.okPath())
	if err := os.WriteFile(e.activePath(), body, 0600); err != nil {
		return ActiveRollback{}, err
	}
	return active, nil
}

func (e *Engine) markRollbackOK() error {
	return os.WriteFile(e.okPath(), []byte("ok\n"), 0600)
}

func (e *Engine) clearRollback() {
	_ = os.Remove(e.activePath())
	_ = os.Remove(e.okPath())
}

func (e *Engine) restoreSnapshot(active ActiveRollback) error {
	if active.SnapshotDir == "" || active.TargetDir == "" {
		return fmt.Errorf("rollback snapshot is incomplete")
	}
	if err := replaceDir(active.SnapshotDir, active.TargetDir); err != nil {
		return err
	}
	return e.reloadNetworkd()
}

func snapshotDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !ownedPersistName(name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0644); err != nil {
			return err
		}
	}
	return nil
}

func replaceDir(snap, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	cur, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	for _, ent := range cur {
		if ent.IsDir() || !ownedPersistName(ent.Name()) {
			continue
		}
		_ = os.Remove(filepath.Join(dest, ent.Name()))
	}
	return snapshotDir(snap, dest)
}

func ownedPersistName(name string) bool {
	return stringsHasPrefixFold(name, "50-ndl-")
}

func stringsHasPrefixFold(name, prefix string) bool {
	if len(name) < len(prefix) {
		return false
	}
	return equalFoldASCII(name[:len(prefix)], prefix)
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// LoadActiveRollback reads the independent watchdog state file.
func LoadActiveRollback(path string) (ActiveRollback, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ActiveRollback{}, err
	}
	var active ActiveRollback
	if err := json.Unmarshal(b, &active); err != nil {
		return ActiveRollback{}, err
	}
	return active, nil
}
