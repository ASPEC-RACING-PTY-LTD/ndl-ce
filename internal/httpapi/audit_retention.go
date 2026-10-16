package httpapi

import (
	"context"
	"log"
	"time"
)

// The audit log always keeps at least auditKeep. Once the oldest event is a
// week past that, events older than auditKeep are pruned, so the log is
// trimmed week by week and holds between 30 and 37 days.
const (
	auditKeep       = 30 * 24 * time.Hour
	auditPruneEvery = 7 * 24 * time.Hour
	auditCheckTick  = time.Hour
)

// RunAuditRetention prunes the audit log in the background.
func (s *Server) RunAuditRetention(ctx context.Context) {
	t := time.NewTicker(auditCheckTick)
	defer t.Stop()
	for {
		s.pruneAudit(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) pruneAudit(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil {
		return
	}
	oldest, err := s.Store.OldestAuditEvent(ctx, cluster.ID)
	if err != nil || oldest.IsZero() {
		return
	}
	now := s.now()
	if now.Sub(oldest) < auditKeep+auditPruneEvery {
		return
	}
	n, err := s.Store.ClearAuditEvents(ctx, cluster.ID, now.Add(-auditKeep))
	if err != nil {
		log.Printf("audit retention: %v", err)
		return
	}
	log.Printf("audit retention: pruned %d event(s) older than 30 days", n)
}
