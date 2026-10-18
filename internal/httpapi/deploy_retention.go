package httpapi

import (
	"context"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
)

// tickDeployRetention expires old deployments and their logs once an hour.
func (s *Server) tickDeployRetention(ctx context.Context, clusterID string) {
	rt := s.deployRT()
	if now := s.now(); now.Sub(rt.lastRetention) < time.Hour {
		return
	} else {
		rt.lastRetention = now
	}
	s.expireDeployments(ctx, clusterID)
}

// expireDeployments applies the retention settings. Logs are whole files:
// a deployment is kept with its complete log or removed with it, never cut.
// Running deployments and each project's newest deployment are always kept.
func (s *Server) expireDeployments(ctx context.Context, clusterID string) []appdb.Deployment {
	settings := s.deploySettings(ctx, clusterID)
	deps, err := s.Store.ListDeployments(ctx, clusterID, appdb.DeploymentFilter{})
	if err != nil {
		return nil
	}
	drop := expiredDeployments(deps, settings, s.now())
	if len(drop) == 0 {
		return nil
	}
	s.deleteDeploymentLogs(ctx, drop)
	ids := make([]string, len(drop))
	for i, d := range drop {
		ids[i] = d.ID
	}
	_ = s.Store.DeleteDeployments(ctx, clusterID, ids)
	return drop
}

// expiredDeployments picks what retention removes from deps, newest first.
func expiredDeployments(deps []appdb.Deployment, st appdb.DeploySettings, now time.Time) []appdb.Deployment {
	cutoff := time.Time{}
	if st.RetentionDays > 0 {
		cutoff = now.Add(-time.Duration(st.RetentionDays) * 24 * time.Hour)
	}
	limit := int64(st.MaxLogGB * (1 << 30))
	perProject := map[string]int{}
	var kept int64
	var drop []appdb.Deployment
	for _, d := range deps {
		if appdb.DeployActive(d.Status) {
			continue
		}
		perProject[d.ProjectID]++
		newest := perProject[d.ProjectID] == 1
		expired := (!cutoff.IsZero() && d.QueuedAt.Before(cutoff)) ||
			(st.KeepPerProject > 0 && perProject[d.ProjectID] > st.KeepPerProject) ||
			(limit > 0 && kept+d.Detail.LogBytes > limit)
		if expired && !newest {
			drop = append(drop, d)
			continue
		}
		kept += d.Detail.LogBytes
	}
	return drop
}
