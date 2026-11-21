package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/helix"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

// RunHelix watches the machines Helix may read and turns what it sees into
// events: unhealthy Docker containers, resource pressure and machines that
// stopped when they should run. It also forwards platform alerts, expires
// approvals and prunes the feed.
func (s *Server) RunHelix(ctx context.Context) {
	var hub chan appdb.Event
	if s.Hub != nil {
		hub = s.Hub.subscribe()
		defer s.Hub.unsubscribe(hub)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	prune := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-hub:
			if !ok {
				hub = nil
				continue
			}
			s.helixHubEvent(ctx, e)
		case <-t.C:
			s.helixObserve(ctx)
			if time.Since(prune) > time.Hour {
				prune = time.Now()
				s.helixHousekeeping(ctx)
			}
		}
	}
}

// helixHubEvent forwards platform events not already covered by the
// automation bus.
func (s *Server) helixHubEvent(ctx context.Context, e appdb.Event) {
	switch {
	case e.Type == "alert.firing":
		s.helixEmit(ctx, e.ClusterID, "alert.firing", "warning", "", rawToMap(e.Payload))
	case e.Type == "migration.failed":
		s.helixEmit(ctx, e.ClusterID, "migration.failed", "warning", "", rawToMap(e.Payload))
	case e.Type == "platform.recovered":
		s.helixEmit(ctx, e.ClusterID, "platform.recovered", "info", "", rawToMap(e.Payload))
	}
}

func (s *Server) helixObserve(ctx context.Context) {
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return
	}
	cfg, err := s.helixLoad(ctx, cluster.ID)
	if err != nil || !cfg.Enabled || cfg.Kill.Engaged || len(cfg.Grants) == 0 {
		return
	}
	rt := s.helixRT()
	readable := map[string]bool{}
	for _, g := range cfg.Grants {
		if g.Has(helix.CapRead) {
			readable[g.MachineID] = true
		}
	}
	// Machines that should run but do not.
	for id := range readable {
		wl, err := s.Store.GetWorkload(ctx, cluster.ID, id)
		if err != nil || wl == nil {
			continue
		}
		down := wl.DesiredPower == "running" && wl.Status != lxc.StatusRunning && wl.Status != "starting"
		if s.helixEdge(&rt.down, id, down) {
			typ, sev := "machine.down", "critical"
			if !down {
				typ, sev = "machine.up", "info"
			}
			s.helixEmit(ctx, cluster.ID, typ, sev, id, map[string]any{"name": wl.Name, "status": wl.Status, "reason": wl.Reason})
		}
	}
	// Unhealthy Docker containers.
	if s.dockerEnabled(ctx, cluster.ID) {
		if inv, err := s.dockerInventory(ctx, cluster.ID, false); err == nil {
			seen := map[string]bool{}
			for _, c := range inv.Containers {
				if !readable[c.MachineID] {
					continue
				}
				bad := c.Health == docker.HealthCritical || c.Health == docker.HealthDegraded
				seen[c.Ref] = true
				if s.helixEdge(&rt.unhealthy, c.Ref, bad) {
					typ, sev := "docker.container_unhealthy", "warning"
					if !bad {
						typ, sev = "docker.container_recovered", "info"
					}
					s.helixEmit(ctx, cluster.ID, typ, sev, c.MachineID, map[string]any{
						"container_id": c.EngineID, "name": c.Name, "project": c.Project, "health": c.Health,
						"reason": c.HealthReason, "state": c.State, "restart_count": c.RestartCount, "exit_code": c.ExitCode,
					})
				}
			}
			rt.obsMu.Lock()
			for ref := range rt.unhealthy {
				if !seen[ref] {
					delete(rt.unhealthy, ref)
				}
			}
			rt.obsMu.Unlock()
		}
	}
	// Resource pressure from the last few minutes of metrics.
	if s.Observer != nil {
		to := s.now()
		res, err := s.Observer.GetMetrics(ctx, to.Add(-5*time.Minute), to)
		if err != nil {
			return
		}
		for id := range readable {
			latest := helixLatest(filterWorkloadMetrics(res, id), id)
			for _, m := range []struct {
				key, name string
				limit     int
			}{{"cpu_percent", "cpu", cfg.Thresholds.CPUPercent}, {"memory_percent", "memory", cfg.Thresholds.MemoryPercent}} {
				v, ok := latest[m.key].(float64)
				if !ok {
					continue
				}
				high := v >= float64(m.limit)
				if s.helixEdge(&rt.high, id+":"+m.name, high) {
					typ, sev := "machine.resource_high", "warning"
					if !high {
						typ, sev = "machine.resource_recovered", "info"
					}
					s.helixEmit(ctx, cluster.ID, typ, sev, id, map[string]any{"resource": m.name, "percent": v, "threshold": m.limit})
				}
			}
		}
	}
}

// helixEdge records a state and reports whether it changed. A problem
// present at the first look is reported once; a healthy first look is not.
func (s *Server) helixEdge(m *map[string]bool, key string, on bool) bool {
	rt := s.helixRT()
	rt.obsMu.Lock()
	defer rt.obsMu.Unlock()
	was, known := (*m)[key]
	(*m)[key] = on
	if !known {
		return on
	}
	return was != on
}

func (s *Server) helixHousekeeping(ctx context.Context) {
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return
	}
	_ = s.Store.PruneHelixEvents(ctx, cluster.ID, s.now().Add(-helixEventRetention))
	cfg, err := s.helixLoad(ctx, cluster.ID)
	if err != nil {
		return
	}
	now := s.now()
	stale := false
	for _, a := range cfg.Approvals {
		if (a.Status == helix.ApprovalPending || a.Status == helix.ApprovalApproved) && now.After(a.ExpiresAt) {
			stale = true
		}
	}
	if stale {
		_, _ = s.helixUpdate(ctx, cluster.ID, helixActorName, func(c *helix.Config) error {
			helixExpireApprovals(c, now)
			return nil
		})
	}
}

func rawToMap(raw []byte) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
