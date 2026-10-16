package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	_ "time/tzdata" // the schedule names a zone; do not depend on host zoneinfo

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// Docker pruning removes images that no container uses and the build cache
// in every machine with Docker (the host and system containers). Images used
// by any container, running or stopped, and all volumes are kept. It runs
// daily at a configured local time (06:00 Australia/Melbourne by default)
// and on demand.

const (
	dockerPruneDefaultAt = "06:00"
	dockerPruneDefaultTZ = "Australia/Melbourne"
)

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type dockerPruneRPC interface {
	DockerPrune(ctx context.Context, machineID string) (docker.PruneResult, error)
}

// dockerPruneRun is the summary of one pass, stored as last_result.
type dockerPruneRun struct {
	StartedAt     time.Time            `json:"started_at"`
	FinishedAt    time.Time            `json:"finished_at"`
	Trigger       string               `json:"trigger"`
	Machines      []dockerPruneMachine `json:"machines"`
	ImagesDeleted int                  `json:"images_deleted"`
	BytesFreed    int64                `json:"bytes_freed"`
	Failed        int                  `json:"failed"`
}

type dockerPruneMachine struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ImagesDeleted int    `json:"images_deleted"`
	BytesFreed    int64  `json:"bytes_freed"`
	Error         string `json:"error,omitempty"`
}

var dockerPruneBusy atomic.Bool

func (s *Server) dockerPruneSettings(ctx context.Context, clusterID string) appdb.DockerPruneSettings {
	st, _ := s.Store.GetDockerPruneSettings(ctx, clusterID)
	if st == nil {
		return appdb.DockerPruneSettings{ClusterID: clusterID, Enabled: true, RunAt: dockerPruneDefaultAt, Timezone: dockerPruneDefaultTZ}
	}
	if st.RunAt == "" {
		st.RunAt = dockerPruneDefaultAt
	}
	if st.Timezone == "" {
		st.Timezone = dockerPruneDefaultTZ
	}
	return *st
}

// dockerPruneDue returns the most recent scheduled time at or before now.
func dockerPruneDue(st appdb.DockerPruneSettings, now time.Time) time.Time {
	loc, err := time.LoadLocation(st.Timezone)
	if err != nil {
		loc = time.UTC
	}
	var h, m int
	_, _ = fmt.Sscanf(st.RunAt, "%d:%d", &h, &m)
	local := now.In(loc)
	due := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	if due.After(now) {
		due = due.AddDate(0, 0, -1)
	}
	return due
}

func dockerPruneJSON(st appdb.DockerPruneSettings, now time.Time) map[string]any {
	out := map[string]any{
		"enabled": st.Enabled, "run_at": st.RunAt, "timezone": st.Timezone,
		"next_run_at": dockerPruneDue(st, now).AddDate(0, 0, 1).UTC().Format(time.RFC3339),
		"running":     dockerPruneBusy.Load(),
	}
	if st.LastRunAt != nil {
		out["last_run_at"] = st.LastRunAt.UTC().Format(time.RFC3339)
	}
	if st.LastResult != "" {
		out["last_result"] = json.RawMessage(st.LastResult)
	}
	return out
}

// RunDockerPrune runs the daily prune in the background.
func (s *Server) RunDockerPrune(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.tickDockerPrune(ctx)
	}
}

func (s *Server) tickDockerPrune(ctx context.Context) {
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return
	}
	st := s.dockerPruneSettings(ctx, cluster.ID)
	if !st.Enabled {
		return
	}
	now := s.now()
	due := dockerPruneDue(st, now)
	if st.LastRunAt == nil {
		// First start: arm the schedule rather than prune straight away.
		st.LastRunAt = &now
		_ = s.Store.PutDockerPruneSettings(ctx, st)
		return
	}
	if !st.LastRunAt.Before(due) {
		return
	}
	s.runDockerPrune(ctx, cluster.ID, "schedule")
}

// runDockerPrune prunes every machine with a running Docker daemon, one at
// a time, and records the result.
func (s *Server) runDockerPrune(ctx context.Context, clusterID, trigger string) (dockerPruneRun, error) {
	if !dockerPruneBusy.CompareAndSwap(false, true) {
		return dockerPruneRun{}, errConflict("a Docker prune is already running")
	}
	defer dockerPruneBusy.Store(false)
	run := dockerPruneRun{StartedAt: s.now(), Trigger: trigger, Machines: []dockerPruneMachine{}}
	rpc, ok := s.dockerRPC().(dockerPruneRPC)
	if !ok {
		return run, errUnavailable("the agent cannot prune Docker")
	}
	inv, err := s.dockerInventory(ctx, clusterID, true)
	if err != nil {
		return run, err
	}
	for _, m := range inv.Machines {
		if !m.DaemonOK {
			continue
		}
		mctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		res, err := rpc.DockerPrune(mctx, m.ID)
		cancel()
		row := dockerPruneMachine{ID: m.ID, Name: m.Name}
		if err != nil {
			row.Error = err.Error()
			run.Failed++
		} else {
			row.ImagesDeleted = res.ImagesDeleted
			row.BytesFreed = res.ImageBytes + res.BuildCacheByte
			run.ImagesDeleted += row.ImagesDeleted
			run.BytesFreed += row.BytesFreed
		}
		run.Machines = append(run.Machines, row)
	}
	run.FinishedAt = s.now()
	st := s.dockerPruneSettings(ctx, clusterID)
	st.LastRunAt = &run.StartedAt
	raw, _ := json.Marshal(run)
	st.LastResult = string(raw)
	_ = s.Store.PutDockerPruneSettings(context.WithoutCancel(ctx), st)
	log.Printf("docker prune (%s): %d machine(s), %d image(s), %d bytes freed, %d failed", trigger, len(run.Machines), run.ImagesDeleted, run.BytesFreed, run.Failed)
	return run, nil
}

func (s *Server) getDockerPrune(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, dockerPruneJSON(s.dockerPruneSettings(r.Context(), p.User.ClusterID), s.now()))
}

func (s *Server) putDockerPruneSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
		return
	}
	var in struct {
		Enabled  *bool  `json:"enabled"`
		RunAt    string `json:"run_at"`
		Timezone string `json:"timezone"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	st := s.dockerPruneSettings(r.Context(), p.User.ClusterID)
	if in.Enabled != nil {
		st.Enabled = *in.Enabled
	}
	if in.RunAt = strings.TrimSpace(in.RunAt); in.RunAt != "" {
		if !hhmm.MatchString(in.RunAt) {
			writeErr(w, http.StatusBadRequest, "run_at must be HH:MM (24-hour)")
			return
		}
		st.RunAt = in.RunAt
	}
	if in.Timezone = strings.TrimSpace(in.Timezone); in.Timezone != "" {
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			writeErr(w, http.StatusBadRequest, "unknown time zone")
			return
		}
		st.Timezone = in.Timezone
	}
	if st.LastRunAt == nil {
		now := s.now()
		st.LastRunAt = &now
	}
	st.UpdatedBy = p.User.Username
	if err := s.Store.PutDockerPruneSettings(r.Context(), st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.prune.settings", "ok", fmt.Sprintf("enabled=%t at=%s %s", st.Enabled, st.RunAt, st.Timezone))
	writeJSON(w, http.StatusOK, dockerPruneJSON(st, s.now()))
}

// postDockerPrune prunes now. It runs in the background unless ?wait=true.
func (s *Server) postDockerPrune(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireDocker(w, r, rbac.ComputeLifecycle)
	if !ok {
		return
	}
	if dockerPruneBusy.Load() {
		writeErr(w, http.StatusConflict, "a Docker prune is already running")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "docker.prune.run", "started", "")
	if r.URL.Query().Get("wait") == "true" {
		run, err := s.runDockerPrune(context.WithoutCancel(r.Context()), p.User.ClusterID, "manual")
		if err != nil {
			writeErr(w, statusFor(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, run)
		return
	}
	clusterID := p.User.ClusterID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		_, _ = s.runDockerPrune(ctx, clusterID, "manual")
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}
