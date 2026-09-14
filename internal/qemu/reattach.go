package qemu

import (
	"context"
	"os"
	"path/filepath"
)

// ListAppliedIDs returns workload UUIDs that have a last-applied artifact.
func (e *Engine) ListAppliedIDs() []string {
	entries, err := os.ReadDir(filepath.Join(e.dataDir(), "workloads"))
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		if err := ValidateWorkloadID(id); err != nil {
			continue
		}
		if _, err := e.ReadApplied(id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

// ReattachApplied reconnects QMP for applied VMs whose units are still
// running. It does not start or stop QEMU. Agent restart must rediscover.
func (e *Engine) ReattachApplied(ctx context.Context) []error {
	var errs []error
	for _, id := range e.ListAppliedIDs() {
		if ctx.Err() != nil {
			return append(errs, ctx.Err())
		}
		if !e.AlreadyRunning(ctx, id) {
			continue
		}
		if err := e.ReconnectQMP(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
