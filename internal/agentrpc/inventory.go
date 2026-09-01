package agentrpc

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/metrics"
)

// GetInventory returns the last observation without rescanning when cached.
func (h *Handler) GetInventory(ctx context.Context, _ *connect.Request[agentv1.GetInventoryRequest]) (*connect.Response[agentv1.GetInventoryResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	inv := h.cachedOrRefresh()
	return connect.NewResponse(&agentv1.GetInventoryResponse{
		ObservedAt:    inv.ObservedAt.UTC().Format(timeRFC3339),
		SchemaVersion: inv.SchemaVersion,
		InventoryJson: mustJSON(inv),
	}), nil
}

// GetMetrics reads agent SQLite samples. The control plane does not store them.
func (h *Handler) GetMetrics(ctx context.Context, req *connect.Request[agentv1.GetMetricsRequest]) (*connect.Response[agentv1.GetMetricsResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	if h.Metrics == nil {
		body, _ := json.Marshal(metrics.QueryResult{Status: metrics.StatusUnavailable, Series: []metrics.Series{}})
		return connect.NewResponse(&agentv1.GetMetricsResponse{
			Status:     string(metrics.StatusUnavailable),
			SeriesJson: body,
		}), nil
	}
	from, to := parseMetricWindow(req.Msg.GetFrom(), req.Msg.GetTo())
	res, err := h.Metrics.Query(metrics.KnownNames, from, to)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	body, err := json.Marshal(res)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.GetMetricsResponse{
		Status:     string(res.Status),
		SeriesJson: body,
	}), nil
}

// RefreshLoop keeps a cached observation so API reads do not rescan the host.
func (h *Handler) RefreshLoop(period time.Duration) {
	if period <= 0 {
		period = 30 * time.Second
	}
	h.refresh()
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		h.refresh()
	}
}

func parseMetricWindow(fromS, toS string) (time.Time, time.Time) {
	var from, to time.Time
	if fromS != "" {
		if t, err := time.Parse(time.RFC3339, fromS); err == nil {
			from = t
		}
	}
	if toS != "" {
		if t, err := time.Parse(time.RFC3339, toS); err == nil {
			to = t
		}
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	return from, to
}
