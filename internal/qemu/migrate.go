package qemu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StripIncoming removes dest-only -incoming defer from a frozen argv so
// the remaining vector can be compared to the source ABI.
func StripIncoming(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		if argv[i] == "-incoming" {
			if i+1 < len(argv) {
				i++
			}
			continue
		}
		out = append(out, argv[i])
	}
	return out
}

// SameABI reports whether dest is the source argv plus optional incoming defer.
func SameABI(source, dest []string) bool {
	stripped := StripIncoming(dest)
	if len(stripped) != len(source) {
		return false
	}
	for i := range source {
		if source[i] != stripped[i] {
			return false
		}
	}
	return true
}

// CPUHost reports a recorded -cpu host that will not live-migrate.
func CPUHost(argv []string) bool {
	for i, a := range argv {
		if a == "-cpu" && i+1 < len(argv) && argv[i+1] == "host" {
			return true
		}
	}
	return false
}

// MigrateReadiness is honest about live migrate. Offline is always the
// fallback when live is blocked. cpu host is a recorded single-node default.
func MigrateReadiness(argv []string) (ready bool, blockers []string) {
	if CPUHost(argv) {
		return false, []string{"cpu host does not live-migrate"}
	}
	return true, nil
}

// IncomingURI is the typed unix locator dest listens on after -incoming defer.
func (e *Engine) IncomingURI(id string) string {
	return "unix:" + e.incomingPath(id)
}

func (e *Engine) incomingPath(id string) string {
	return e.runtimeDir(id) + "/incoming.sock"
}

// PrepareIncoming freezes dest argv as the source ABI plus -incoming defer.
func (e *Engine) PrepareIncoming(spec Spec) (Result, error) {
	spec.IncomingDefer = true
	argv, err := e.compile(spec)
	if err != nil {
		return Result{}, err
	}
	if err := e.writeFrozen(spec, argv); err != nil {
		return Result{}, err
	}
	return Result{WorkloadID: spec.WorkloadID, Status: StatusStopped, Machine: spec.Machine, Accel: spec.Accel, Argv: argv, Reason: "incoming defer"}, nil
}

func validateMigrateURI(uri string) error {
	if uri == "" || !strings.HasPrefix(uri, "unix:") {
		return fmt.Errorf("migrate uri must be a unix locator")
	}
	rest := strings.TrimPrefix(uri, "unix:")
	if strings.Contains(rest, "..") || strings.ContainsAny(rest, "\n\r\x00; ") {
		return fmt.Errorf("migrate uri is not a clean locator")
	}
	if strings.HasPrefix(rest, "tcp:") || strings.Contains(uri, "tcp:0") {
		return fmt.Errorf("tcp migrate listen is refused")
	}
	return nil
}

// LiveMigrate issues QMP migrate on the source. On failure the source unit
// is left running. Dest is not started as a second copy here.
func (e *Engine) LiveMigrate(ctx context.Context, id, uri string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if err := validateMigrateURI(uri); err != nil {
		return err
	}
	if e.FailLiveMigrate {
		return fmt.Errorf("live migrate failed; source remains running")
	}
	if e.SkipHostCmds {
		return fmt.Errorf("host commands skipped; live migrate was not issued; source remains running")
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return fmt.Errorf("live migrate requires a live QMP session: %w", err)
	}
	defer q.Close()
	if err := q.migrate(uri); err != nil {
		return fmt.Errorf("live migrate failed; source remains running: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := q.queryMigrate()
		if err != nil {
			return fmt.Errorf("live migrate failed; source remains running: %w", err)
		}
		switch st {
		case "completed":
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("live migrate %s; source remains running", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = q.migrateCancel()
	return fmt.Errorf("live migrate timed out; source remains running")
}

// CancelLiveMigrate aborts an in-flight QMP migrate. The source stays running.
func (e *Engine) CancelLiveMigrate(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if e.SkipHostCmds {
		return nil
	}
	q, err := e.dialQMP(id, 3*time.Second)
	if err != nil {
		return fmt.Errorf("migrate cancel requires a live QMP session: %w", err)
	}
	defer q.Close()
	return q.migrateCancel()
}

// AbortIncoming stops a dest that was waiting for migrate. It does not
// touch the source unit.
func (e *Engine) AbortIncoming(ctx context.Context, id string) error {
	if err := ValidateWorkloadID(id); err != nil {
		return err
	}
	if e.LiveUnits != nil {
		delete(e.LiveUnits, id)
	}
	if e.SkipHostCmds {
		return e.CleanupFailedLaunch(id)
	}
	_ = e.ForceStop(ctx, id)
	return e.CleanupFailedLaunch(id)
}

func (q *qmpConn) migrate(uri string) error {
	_, err := q.exec("migrate", map[string]any{"uri": uri})
	return err
}

func (q *qmpConn) migrateCancel() error {
	_, err := q.exec("migrate_cancel", nil)
	return err
}

func (q *qmpConn) queryMigrate() (string, error) {
	raw, err := q.exec("query-migrate", nil)
	if err != nil {
		return "", err
	}
	var st struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return "", err
	}
	return st.Status, nil
}
