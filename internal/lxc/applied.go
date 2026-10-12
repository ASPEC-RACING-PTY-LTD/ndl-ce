package lxc

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/guestextras"
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

// InfoPID returns the guest init PID and IPv4 from lxc-info.
func (e *Engine) InfoPID(ctx context.Context, id string) (int, string, error) {
	return e.lxcInfo(ctx, id)
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

// ListAppliedIDs returns workload UUIDs that have a last-applied artifact.
func (e *Engine) ListAppliedIDs() []string {
	entries, err := os.ReadDir(e.workloadsDir())
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		id := ent.Name()
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		if _, err := e.readApplied(id); err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

// RewriteRuntimeConfig rewrites the on-disk LXC config from last-applied.
// It does not start, stop, or recreate the container.
func (e *Engine) RewriteRuntimeConfig(id string) error {
	applied, err := e.readApplied(id)
	if err != nil {
		return nil
	}
	spec, err := normalizeSpec(applied.Spec)
	if err != nil {
		return err
	}
	if err := e.writeConfig(spec); err != nil {
		return err
	}
	if applied.Spec.Nesting == nil || specChanged(applied.Spec, spec) {
		return e.writeApplied(spec, applied.ImageVerified, applied.ImageSHA256)
	}
	return nil
}

// ReconcileRuntimeConfigs rewrites every applied system-container config
// from last-applied plus current host overrides, and reapplies guest files
// (network, locale, nano) into the mounted rootfs. Idempotent. Running
// guests are not stopped; rewritten LXC keys load on the next start, and
// guest files appear immediately in a mounted rootfs.
//
// Each running guest is reconciled under its own deadline and a few at a
// time, so one guest without a network cannot hold up the others. A guest
// whose network probes fail is recorded and shown as a workload warning.
func (e *Engine) ReconcileRuntimeConfigs() {
	ids := e.ListAppliedIDs()
	sem := make(chan struct{}, reconcileParallel)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			e.reconcileOne(id)
		}(id)
	}
	wg.Wait()
}

const (
	reconcileParallel = 4
	reconcileTimeout  = 60 * time.Second
)

func (e *Engine) reconcileOne(id string) {
	_ = e.writeAppliedConfig(id)
	ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
	defer cancel()
	pid, _, err := e.lxcInfo(ctx, id)
	if err != nil || pid <= 0 {
		e.guestNet.Delete(id)
		return
	}
	if err := e.bootstrapGuest(ctx, id, true); err != nil {
		log.Printf("container %s: guest reconcile: %v", id, err)
	}
	if applied, aerr := e.readApplied(id); aerr == nil {
		_ = e.applyLiveCgroups(context.Background(), id, applied.Spec)
	}
}

// noteGuestNet records the outcome of the last guest network probe.
func (e *Engine) noteGuestNet(id string, err error) {
	if err == nil {
		e.guestNet.Delete(id)
		return
	}
	e.guestNet.Store(id, "guest network check failed: "+err.Error()+"; the container may not reach its gateway, DNS or the internet")
}

// GuestNetWarning returns the last failed guest network probe, if any.
func (e *Engine) GuestNetWarning(id string) string {
	if v, ok := e.guestNet.Load(id); ok {
		return v.(string)
	}
	return ""
}

// RecheckGuestNet re-probes guests that last failed a network check and
// clears the warning once they can resolve or connect out again. It never
// rewrites guest files.
func (e *Engine) RecheckGuestNet() {
	var ids []string
	e.guestNet.Range(func(k, _ any) bool {
		ids = append(ids, k.(string))
		return true
	})
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(context.Background(), reconcileTimeout)
		pid, _, err := e.lxcInfo(ctx, id)
		switch {
		case err != nil || pid <= 0:
			e.guestNet.Delete(id)
		case e.guestNetReachable(ctx, id):
			e.guestNet.Delete(id)
			log.Printf("container %s: guest network check passes again", id)
		}
		cancel()
	}
}

func (e *Engine) guestNetReachable(ctx context.Context, id string) bool {
	applied, err := e.readApplied(id)
	if err != nil {
		return false
	}
	if guestextras.Family(applied.Spec.ImagePin) == "alpine" {
		return e.guestAlpineReachable(ctx, id)
	}
	return e.guestDNSOK(ctx, id) || e.guestTCP443OK(ctx, id)
}
