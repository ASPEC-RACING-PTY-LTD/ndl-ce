package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// The CodeHold CLI ("ch") is the guestextras.CodeholdCLI extra: new system
// containers get it unless turned off (globally here, or per container at
// create), and a bulk rollout installs or updates it in existing ones. The
// install itself is the extras path (lxc/guest_codehold.go). Nothing here
// signs anyone in; each user runs "ch auth login" themselves.

const (
	chCLIDefaultConcurrency = 5
	chCLIMaxConcurrency     = 20
	chCLIDefaultTimeout     = 10 * time.Minute
)

type codeholdCLIConfig struct {
	URL            string `json:"url"`
	AutoInstall    bool   `json:"auto_install"`
	Concurrency    int    `json:"concurrency"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func (s *Server) codeholdCLIConfig(ctx context.Context, clusterID string) codeholdCLIConfig {
	cfg := codeholdCLIConfig{AutoInstall: true}
	if st, _ := s.Store.GetCodeholdCLISettings(ctx, clusterID); st != nil {
		cfg = codeholdCLIConfig{URL: st.URL, AutoInstall: st.AutoInstall, Concurrency: st.Concurrency, TimeoutSeconds: st.TimeoutSeconds}
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = chCLIDefaultConcurrency
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = int(chCLIDefaultTimeout / time.Second)
	}
	return cfg
}

// codeholdCLIAccess is the hook for a CodeHold behind Cloudflare Access. It
// returns no token today, so downloads go without Access headers. To turn it
// on, return a service token's client ID and secret from secret storage, for
// example s.chSecret(ctx, clusterID, chSecretCFID) and chSecretCFSecret (the
// sealed Access token the runners integration already keeps). The agent puts
// them in a 0600 header file inside the container for the one curl call
// (curl -H @file) and removes it; they are never on a command line, in a log
// or in operation messages.
func (s *Server) codeholdCLIAccess(context.Context, string) (id, secret string) {
	return "", ""
}

// codeholdCLISource is passed with an extras run that includes the CLI.
func (s *Server) codeholdCLISource(ctx context.Context, clusterID string, extras []string) *lxc.CodeholdSource {
	if !slices.Contains(extras, guestextras.CodeholdCLI) {
		return nil
	}
	// With no CodeHold address set the install reports "no CodeHold address
	// is configured" for that container.
	src := &lxc.CodeholdSource{URL: s.codeholdCLIConfig(ctx, clusterID).URL}
	src.AccessClientID, src.AccessClientSecret = s.codeholdCLIAccess(ctx, clusterID)
	return src
}

// withDefaultExtras adds the extras new containers get by default. optIn nil
// follows the setting; false keeps the CLI out of this container.
func (s *Server) withDefaultExtras(ctx context.Context, clusterID, pin string, extras []string, optIn *bool) []string {
	out := slices.DeleteFunc(slices.Clone(extras), func(id string) bool {
		return id == guestextras.CodeholdCLI && optIn != nil && !*optIn
	})
	cfg := s.codeholdCLIConfig(ctx, clusterID)
	// There is no built-in CodeHold server: until an address is set, new
	// containers do not get the CLI by default.
	want := optIn != nil && *optIn || optIn == nil && cfg.AutoInstall && cfg.URL != ""
	if want && !slices.Contains(out, guestextras.CodeholdCLI) && guestextras.AvailabilityFor(pin, guestextras.CodeholdCLI).Available {
		out = append(out, guestextras.CodeholdCLI)
	}
	return out
}

func (s *Server) getCodeholdCLI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	s.chCLIMu.Lock()
	last := s.chCLIRun.snapshot()
	s.chCLIMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": s.codeholdCLIConfig(r.Context(), p.User.ClusterID),
		"rollout":  last,
	})
}

func (s *Server) putCodeholdCLISettings(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	var req codeholdCLIConfig
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.URL = strings.TrimRight(strings.TrimSpace(req.URL), "/")
	if req.URL != "" {
		if _, err := lxc.CHDownloadURL(req.URL, "amd64"); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Concurrency < 0 || req.Concurrency > chCLIMaxConcurrency {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("concurrency must be between 1 and %d", chCLIMaxConcurrency))
		return
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > 3600 {
		writeErr(w, http.StatusBadRequest, "timeout must be at most an hour")
		return
	}
	if err := s.Store.PutCodeholdCLISettings(r.Context(), appdb.CodeholdCLISettings{
		ClusterID: p.User.ClusterID, URL: req.URL, AutoInstall: req.AutoInstall,
		Concurrency: req.Concurrency, TimeoutSeconds: req.TimeoutSeconds, UpdatedBy: p.User.ID,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save the settings")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "codehold-cli.settings", "ok", fmt.Sprintf("auto_install=%t", req.AutoInstall))
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.codeholdCLIConfig(r.Context(), p.User.ClusterID)})
}

// Rollout outcomes per container.
const (
	cliInstalled   = "installed"
	cliUpdated     = "updated"
	cliCurrent     = "current"
	cliFailed      = "failed"
	cliNotRunning  = "not_running"
	cliUnsupported = "unsupported"
)

type cliTarget struct {
	WorkloadID string `json:"workload_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	// Installed and Version are what was found before the run (a dry run
	// reports only these and Plan).
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Done      bool   `json:"done"`
}

type cliRollout struct {
	ID             string         `json:"id"`
	OperationID    string         `json:"operation_id,omitempty"`
	DryRun         bool           `json:"dry_run"`
	Update         bool           `json:"update"`
	IncludeStopped bool           `json:"include_stopped"`
	Concurrency    int            `json:"concurrency"`
	TimeoutSeconds int            `json:"timeout_seconds"`
	URL            string         `json:"url"`
	StartedBy      string         `json:"started_by"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     *time.Time     `json:"finished_at,omitempty"`
	Running        bool           `json:"running"`
	Targets        []*cliTarget   `json:"targets"`
	Skipped        []*cliTarget   `json:"skipped"`
	Summary        map[string]int `json:"summary"`
}

func (r *cliRollout) snapshot() *cliRollout {
	if r == nil {
		return nil
	}
	out := *r
	out.Targets = make([]*cliTarget, len(r.Targets))
	for i, t := range r.Targets {
		c := *t
		out.Targets[i] = &c
	}
	out.Skipped = slices.Clone(r.Skipped)
	out.Summary = map[string]int{}
	for k, v := range r.Summary {
		out.Summary[k] = v
	}
	return &out
}

type cliRolloutRequest struct {
	DryRun         bool     `json:"dry_run"`
	Update         bool     `json:"update"`
	IncludeStopped bool     `json:"include_stopped"`
	Concurrency    int      `json:"concurrency"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	WorkloadIDs    []string `json:"workload_ids"`
}

func workloadRunning(row appdb.Workload) bool {
	if row.DesiredPower == "stopped" {
		return false
	}
	return row.UnitActive || strings.EqualFold(row.Status, lxc.StatusRunning)
}

// startCodeholdCLIRollout checks (dry run) or installs or updates the CLI in
// every system container, a few at a time. Containers that are not running
// are listed and skipped unless include_stopped is set; those are started for
// the run and stopped again.
func (s *Server) startCodeholdCLIRollout(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeRead)
	if err != nil {
		return
	}
	var req cliRolloutRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// A dry run only looks; installing needs compute.modify.
	if !req.DryRun && !rbac.Authorize(p.Grants, rbac.ComputeModify) {
		writeErr(w, http.StatusForbidden, "forbidden")
		return
	}
	if s.Workloads == nil {
		writeErr(w, http.StatusServiceUnavailable, "workload agent is unavailable")
		return
	}
	clusterID := p.User.ClusterID
	cfg := s.codeholdCLIConfig(r.Context(), clusterID)
	if cfg.URL == "" {
		writeErr(w, http.StatusBadRequest, "set the CodeHold address first")
		return
	}
	if req.Concurrency <= 0 {
		req.Concurrency = cfg.Concurrency
	}
	if req.Concurrency > chCLIMaxConcurrency {
		req.Concurrency = chCLIMaxConcurrency
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = cfg.TimeoutSeconds
	}
	rows, err := s.Store.ListWorkloads(r.Context(), clusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not list containers")
		return
	}
	run := &cliRollout{
		ID: uuid.NewString(), DryRun: req.DryRun, Update: req.Update, IncludeStopped: req.IncludeStopped,
		Concurrency: req.Concurrency, TimeoutSeconds: req.TimeoutSeconds, URL: cfg.URL,
		StartedBy: p.User.ID, StartedAt: time.Now().UTC(), Running: true, Summary: map[string]int{},
	}
	known := map[string]bool{}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	byID := map[string]appdb.Workload{}
	for _, row := range rows {
		if row.Kind != lxc.KindSystemContainer {
			continue
		}
		known[row.ID] = true
		if len(req.WorkloadIDs) > 0 && !slices.Contains(req.WorkloadIDs, row.ID) {
			continue
		}
		byID[row.ID] = row
		t := &cliTarget{WorkloadID: row.ID, Name: row.Name, Status: firstNonEmpty(row.Status, "unknown")}
		if x, ok := s.extensionMap(r.Context(), clusterID, row.ID)[guestextras.CodeholdCLI]; ok && x.State == appdb.ExtensionInstalled {
			t.Installed, t.Version = true, x.Version
		}
		switch {
		case !guestextras.AvailabilityFor(row.ImagePin, guestextras.CodeholdCLI).Available:
			t.Outcome, t.Done = cliUnsupported, true
			t.Reason = guestextras.AvailabilityFor(row.ImagePin, guestextras.CodeholdCLI).Reason
			run.Skipped = append(run.Skipped, t)
		case !workloadRunning(row) && !req.IncludeStopped:
			t.Outcome, t.Done = cliNotRunning, true
			t.Reason = "not running; include stopped containers to start it for the install and stop it again"
			if row.DesiredPower == "stopped" && (row.UnitActive || strings.EqualFold(row.Status, lxc.StatusRunning)) {
				t.Reason = "running, but set to stopped in No-dal; including stopped containers installs ch and then stops it"
			}
			run.Skipped = append(run.Skipped, t)
		default:
			run.Targets = append(run.Targets, t)
		}
	}
	for _, id := range req.WorkloadIDs {
		if !known[id] {
			writeErr(w, http.StatusBadRequest, "unknown system container "+id)
			return
		}
	}
	for _, t := range run.Skipped {
		run.Summary[t.Outcome]++
	}

	s.chCLIMu.Lock()
	if s.chCLIRun != nil && s.chCLIRun.Running {
		s.chCLIMu.Unlock()
		writeErr(w, http.StatusConflict, "a CodeHold CLI rollout is already running")
		return
	}
	s.chCLIRun = run
	s.chCLIMu.Unlock()

	if !req.DryRun {
		op := s.startOpKeyed(r.Context(), clusterID, "", "codehold-cli.rollout", map[bool]string{true: "updating", false: "installing"}[req.Update], "", cliOpMessage(run), 0)
		run.OperationID = op.ID
		s.audit(r, clusterID, p.User.ID, "codehold-cli.rollout", "ok", fmt.Sprintf("targets=%d update=%t include_stopped=%t", len(run.Targets), req.Update, req.IncludeStopped))
		go func() {
			s.runCodeholdCLIRollout(context.Background(), clusterID, run, byID)
			s.chCLIMu.Lock()
			snap := run.snapshot()
			s.chCLIMu.Unlock()
			state := "succeeded"
			if snap.Summary[cliFailed] > 0 {
				state = "failed"
			}
			s.finishOp(context.Background(), op, state, cliOpMessage(snap), 100)
			s.finishCodeholdCLIRollout(run)
		}()
	} else {
		go func() {
			s.runCodeholdCLIRollout(context.Background(), clusterID, run, byID)
			s.finishCodeholdCLIRollout(run)
		}()
	}
	s.chCLIMu.Lock()
	snap := run.snapshot()
	s.chCLIMu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]any{"rollout": snap})
}

func (s *Server) getCodeholdCLIRollout(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.ComputeRead); err != nil {
		return
	}
	s.chCLIMu.Lock()
	snap := s.chCLIRun.snapshot()
	s.chCLIMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"rollout": snap})
}

func (s *Server) runCodeholdCLIRollout(ctx context.Context, clusterID string, run *cliRollout, rows map[string]appdb.Workload) {
	sem := make(chan struct{}, run.Concurrency)
	var wg sync.WaitGroup
	for _, t := range run.Targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t *cliTarget) {
			defer func() { <-sem; wg.Done() }()
			tctx, cancel := context.WithTimeout(ctx, time.Duration(run.TimeoutSeconds)*time.Second)
			defer cancel()
			var res cliTarget
			if run.DryRun {
				res = s.checkCodeholdCLI(tctx, clusterID, rows[t.WorkloadID], *t, run.Update)
			} else {
				res = s.installCodeholdCLI(tctx, clusterID, rows[t.WorkloadID], *t, run.Update)
			}
			if tctx.Err() == context.DeadlineExceeded && res.Outcome == cliFailed {
				res.Reason = fmt.Sprintf("timed out after %ds", run.TimeoutSeconds)
			}
			res.Done = true
			s.chCLIMu.Lock()
			*t = res
			if t.Outcome != "" {
				run.Summary[t.Outcome]++
			}
			s.chCLIMu.Unlock()
		}(t)
	}
	wg.Wait()
}

func (s *Server) finishCodeholdCLIRollout(run *cliRollout) {
	s.chCLIMu.Lock()
	now := time.Now().UTC()
	run.FinishedAt, run.Running = &now, false
	s.chCLIMu.Unlock()
}

// checkCodeholdCLI asks one container whether ch is installed, changing
// nothing. A container that is not running reports what was last recorded.
func (s *Server) checkCodeholdCLI(ctx context.Context, clusterID string, row appdb.Workload, t cliTarget, update bool) cliTarget {
	if workloadRunning(row) {
		res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: lxc.ActionGuestExtrasStatus, Extras: []string{guestextras.CodeholdCLI}})
		if err != nil {
			t.Plan, t.Reason = "unknown", "could not check the container: "+sanitizeGuestSetupErr(err)
			return t
		}
		t.Installed, t.Version = false, ""
		for _, x := range res.Extras {
			if x.ID == guestextras.CodeholdCLI && x.Installed {
				t.Installed, t.Version = true, x.Version
			}
		}
		s.recordExtensions(context.WithoutCancel(ctx), clusterID, row.ID, nil, res.Extras, nil)
	} else {
		t.Reason = "not running: it will be started for the install and stopped again"
	}
	switch {
	case !t.Installed:
		t.Plan = "install"
	case update:
		t.Plan = "update"
	default:
		t.Plan = "skip"
	}
	return t
}

func (s *Server) installCodeholdCLI(ctx context.Context, clusterID string, row appdb.Workload, t cliTarget, update bool) cliTarget {
	s.extrasMu.Lock()
	if s.extrasBusy == nil {
		s.extrasBusy = map[string]bool{}
	}
	if s.extrasBusy[row.ID] {
		s.extrasMu.Unlock()
		t.Outcome, t.Reason = cliFailed, "extensions are already being changed on this container"
		return t
	}
	s.extrasBusy[row.ID] = true
	s.extrasMu.Unlock()
	defer func() {
		s.extrasMu.Lock()
		delete(s.extrasBusy, row.ID)
		s.extrasMu.Unlock()
	}()
	mode := extensionInstall
	if update {
		mode = extensionUpdate
	}
	changes, warnings := s.runExtensions(ctx, clusterID, row, []string{guestextras.CodeholdCLI}, mode)
	for _, w := range warnings {
		if w.Extra == guestextras.CodeholdCLI || w.Extra == "setup" || w.Extra == "" {
			t.Outcome, t.Reason = cliFailed, w.Message
			return t
		}
	}
	for _, c := range changes {
		if c.Extra == guestextras.CodeholdCLI {
			t.Outcome, t.Version = c.Outcome, c.Version
			return t
		}
	}
	t.Outcome, t.Reason = cliFailed, "the install did not report back"
	return t
}

// cliOpMessage is the operation record of a rollout: counts and the failures
// with their reasons. It holds no secrets.
func cliOpMessage(run *cliRollout) string {
	failed := map[string]string{}
	for _, t := range run.Targets {
		if t.Outcome == cliFailed {
			failed[t.Name] = t.Reason
		}
	}
	body, _ := json.Marshal(map[string]any{"targets": len(run.Targets), "skipped": len(run.Skipped), "summary": run.Summary, "failed": failed})
	return string(body)
}
