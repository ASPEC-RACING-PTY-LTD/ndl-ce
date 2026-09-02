package lxc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func (e *Engine) lastAppliedPath(id string) string {
	return filepath.Join(e.workloadsDir(), id, "last-applied.json")
}

func (e *Engine) configPath(id string) string {
	return filepath.Join(e.lxcPath(), id, "config")
}

func (e *Engine) writeApplied(spec Spec, verified bool, sha string) error {
	row := Applied{
		SchemaVersion: LastAppliedSchema,
		Spec:          spec,
		ImageVerified: verified,
		ImageSHA256:   sha,
		AppliedAt:     e.now(),
	}
	b, err := json.MarshalIndent(row, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(e.lastAppliedPath(spec.WorkloadID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return os.WriteFile(e.lastAppliedPath(spec.WorkloadID), append(b, '\n'), 0o640)
}

// RuntimeLXC is the liblxc path used with typed lxc-attach/lxc-console argv.
func (e *Engine) RuntimeLXC() string {
	return e.lxcPath()
}

// LastApplied returns the on-disk last-applied spec for a workload UUID.
func (e *Engine) LastApplied(id string) (Applied, error) {
	return e.readApplied(id)
}

func (e *Engine) readApplied(id string) (Applied, error) {
	b, err := os.ReadFile(e.lastAppliedPath(id))
	if err != nil {
		return Applied{}, err
	}
	var row Applied
	if err := json.Unmarshal(b, &row); err != nil {
		return Applied{}, err
	}
	return row, nil
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now().UTC()
}
