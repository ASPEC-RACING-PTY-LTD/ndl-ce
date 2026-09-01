package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) nodeLogs(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.EventsRead)
	if err != nil {
		return
	}
	node, _, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	unit := r.URL.Query().Get("unit")
	if unit == "" {
		unit = journald.UnitAgent
	}
	s.writeLogs(w, r, unit)
}

func (s *Server) workloadLogs(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.EventsRead)
	if err != nil {
		return
	}
	wl, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	unit := "nodal-vm@" + wl.ID + ".service"
	switch wl.Kind {
	case "vm":
		unit = "nodal-vm@" + wl.ID + ".service"
	case oci.KindOCI:
		unit = oci.UnitName(wl.ID)
	default:
		unit = "nodal-ct@" + wl.ID + ".service"
	}
	s.writeLogs(w, r, unit)
}

func (s *Server) writeLogs(w http.ResponseWriter, r *http.Request, unit string) {
	if _, err := journald.AllowUnit(unit); err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if s.Logs == nil {
		writeJSON(w, http.StatusOK, journald.Result{Status: journald.StatusUnavailable, Unit: unit, Lines: []string{}, Message: "agent logs unavailable"})
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lines = n
		}
	}
	var since time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = t
		}
	}
	res, err := s.Logs.GetLogs(r.Context(), unit, lines, since)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if res.Lines == nil {
		res.Lines = []string{}
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) nodeSMART(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.NodeRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if inv == nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "collecting", "items": []any{}})
		return
	}
	parsed, ok := decodeInv(inv)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "items": []any{}})
		return
	}
	if redactViewer(p) {
		parsed = inventory.RedactForViewer(parsed)
	}
	items := make([]map[string]any, 0, len(parsed.BlockDevices))
	for _, d := range parsed.BlockDevices {
		st := string(d.SMARTStatus)
		if st == "" {
			st = string(inventory.StatusNotReported)
		}
		items = append(items, map[string]any{
			"name":         d.Name,
			"smart_status": st,
		})
	}
	status := "available"
	if inv.Stale {
		status = "stale"
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "stale": inv.Stale, "items": items})
}

func (s *Server) nodeCapacity(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.MetricsRead)
	if err != nil {
		return
	}
	node, inv, err := s.cachedNode(r, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if node == nil || node.ID != r.PathValue("id") {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if s.Observer == nil || (inv != nil && inv.Stale) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "stale", "message": "Stale"})
		return
	}
	to := s.now()
	from := to.Add(-7 * 24 * time.Hour)
	res, err := s.Observer.GetMetrics(r.Context(), from, to)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "unavailable", "message": "Unavailable"})
		return
	}
	var series *metrics.Series
	for i := range res.Series {
		if res.Series[i].Name == metrics.MetricStorageAvailBytes {
			series = &res.Series[i]
			break
		}
	}
	if series == nil || len(series.Points) < 4 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "collecting", "message": "Collecting"})
		return
	}
	forecast := forecastHours(series.Points)
	out := map[string]any{
		"status":     series.Status,
		"samples":    len(series.Points),
		"last_bytes": series.Points[len(series.Points)-1].Value,
	}
	if forecast == nil {
		out["message"] = "not depleting"
	} else {
		out["hours_to_zero"] = *forecast
	}
	writeJSON(w, http.StatusOK, out)
}

func forecastHours(points []metrics.Point) *float64 {
	if len(points) < 4 {
		return nil
	}
	n := float64(len(points))
	var sumX, sumY, sumXY, sumXX float64
	t0 := points[0].Time
	for _, p := range points {
		x := p.Time.Sub(t0).Hours()
		y := p.Value
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	den := n*sumXX - sumX*sumX
	if den == 0 {
		return nil
	}
	slope := (n*sumXY - sumX*sumY) / den
	if slope >= 0 {
		return nil
	}
	last := points[len(points)-1]
	hours := last.Value / (-slope)
	if hours < 0 {
		return nil
	}
	return &hours
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.EventsRead)
	if err != nil {
		return
	}
	from, to := parseWindow(r)
	if r.URL.Query().Get("from") == "" && r.URL.Query().Get("minutes") == "" {
		from = to.Add(-24 * time.Hour)
	}
	events, err := s.Store.ListEvents(r.Context(), p.User.ClusterID, 200)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	ops, _ := s.Store.ListOperations(r.Context(), p.User.ClusterID, 50)
	var audit []appdb.AuditEvent
	if rbac.Authorize(p.Grants, rbac.AuditRead) || rbac.Authorize(p.Grants, rbac.All) {
		audit, _ = s.Store.ListAuditEvents(r.Context(), p.User.ClusterID, 200)
	}
	items := make([]map[string]any, 0, len(events)+len(ops)+len(audit))
	for _, e := range events {
		if !inWindow(e.CreatedAt, from, to) {
			continue
		}
		items = append(items, map[string]any{
			"kind": "event", "id": e.ID, "title": e.Type, "created_at": e.CreatedAt.UTC().Format(time.RFC3339),
			"payload": json.RawMessage(orEmptyJSON(e.Payload)),
		})
	}
	for _, op := range ops {
		if !inWindow(op.UpdatedAt, from, to) {
			continue
		}
		items = append(items, map[string]any{
			"kind": "task", "id": op.ID, "title": op.Kind, "state": op.State,
			"created_at": op.UpdatedAt.UTC().Format(time.RFC3339), "message": op.Message,
		})
	}
	for _, a := range audit {
		if !inWindow(a.CreatedAt, from, to) {
			continue
		}
		items = append(items, map[string]any{
			"kind": "audit", "id": a.ID, "title": a.Action, "result": a.Result,
			"created_at": a.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "from": from.UTC().Format(time.RFC3339), "to": to.UTC().Format(time.RFC3339)})
}

func inWindow(t, from, to time.Time) bool {
	if t.IsZero() {
		return false
	}
	if !from.IsZero() && t.Before(from) {
		return false
	}
	if !to.IsZero() && t.After(to) {
		return false
	}
	return true
}

func orEmptyJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AlertRead)
	if err != nil {
		return
	}
	rules, err := s.Store.ListAlertRules(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		items = append(items, alertJSON(rule))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createAlert(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AlertManage)
	if err != nil {
		return
	}
	var req struct {
		Name       string  `json:"name"`
		Metric     string  `json:"metric"`
		Op         string  `json:"op"`
		Threshold  float64 `json:"threshold"`
		ForMinutes int     `json:"for_minutes"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if !metrics.Alertable(req.Metric) {
		writeErr(w, http.StatusUnprocessableEntity, "metric is not alertable")
		return
	}
	if req.Op != appdb.AlertOpGT && req.Op != appdb.AlertOpLT {
		writeErr(w, http.StatusBadRequest, "op must be gt or lt")
		return
	}
	if req.ForMinutes <= 0 {
		req.ForMinutes = 1
	}
	rule := appdb.AlertRule{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: req.Name, Metric: req.Metric,
		Op: req.Op, Threshold: req.Threshold, ForMinutes: req.ForMinutes, Enabled: true, CreatedAt: s.now(),
	}
	if err := s.Store.CreateAlertRule(r.Context(), rule); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "alert.create", "ok", rule.ID)
	writeJSON(w, http.StatusCreated, alertJSON(rule))
}

func alertJSON(r appdb.AlertRule) map[string]any {
	out := map[string]any{
		"id": r.ID, "name": r.Name, "metric": r.Metric, "op": r.Op, "threshold": r.Threshold,
		"for_minutes": r.ForMinutes, "enabled": r.Enabled, "created_at": r.CreatedAt.UTC().Format(time.RFC3339),
	}
	if r.LastFiredAt != nil {
		out["last_fired_at"] = r.LastFiredAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AlertRead)
	if err != nil {
		return
	}
	chs, err := s.Store.ListNotificationChannels(r.Context(), p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, len(chs))
	for _, ch := range chs {
		url, _, _ := s.Store.NotificationSecrets(r.Context(), p.User.ClusterID, ch.ID)
		items = append(items, channelJSON(ch, url != ""))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.AlertManage)
	if err != nil {
		return
	}
	var req struct {
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		URL          string `json:"url"`
		SMTPHost     string `json:"smtp_host"`
		SMTPPort     int    `json:"smtp_port"`
		SMTPFrom     string `json:"smtp_from"`
		SMTPUsername string `json:"smtp_username"`
		SMTPPassword string `json:"smtp_password"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	ch := appdb.NotificationChannel{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, Name: req.Name, Kind: req.Kind,
		SMTPHost: req.SMTPHost, SMTPPort: req.SMTPPort, SMTPFrom: req.SMTPFrom, SMTPUsername: req.SMTPUsername,
		Status: appdb.NotifyNotConfigured, CreatedAt: s.now(),
	}
	webhookURL := ""
	switch req.Kind {
	case appdb.NotifyWebhook:
		if err := validateWebhookURL(req.URL); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "webhook url is invalid")
			return
		}
		webhookURL = req.URL
		ch.Status = appdb.NotifyConfigured
	case appdb.NotifySMTP:
		if strings.TrimSpace(req.SMTPHost) == "" {
			ch.Status = appdb.NotifyNotConfigured
		} else {
			ch.Status = appdb.NotifyConfigured
		}
	default:
		writeErr(w, http.StatusBadRequest, "kind must be webhook or smtp")
		return
	}
	if err := s.Store.CreateNotificationChannel(r.Context(), ch, webhookURL, req.SMTPPassword); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "alert.channel.create", "ok", ch.ID)
	writeJSON(w, http.StatusCreated, channelJSON(ch, webhookURL != ""))
}

func channelJSON(c appdb.NotificationChannel, webhookConfigured bool) map[string]any {
	return map[string]any{
		"id":                 c.ID,
		"name":               c.Name,
		"kind":               c.Kind,
		"status":             c.Status,
		"webhook_configured": webhookConfigured,
		"smtp_host":          c.SMTPHost,
		"smtp_port":          c.SMTPPort,
		"created_at":         c.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errInvalidWebhook
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errInvalidWebhook
	}
	return nil
}

var errInvalidWebhook = errors.New("invalid webhook")

func (s *Server) TickAlerts(ctx context.Context) {
	if !s.alertBusy.CompareAndSwap(false, true) {
		return
	}
	defer s.alertBusy.Store(false)
	if s.Observer == nil {
		return
	}
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return
	}
	rules, err := s.Store.ListAlertRules(ctx, cluster.ID)
	if err != nil {
		return
	}
	to := s.now()
	from := to.Add(-time.Hour)
	res, err := s.Observer.GetMetrics(ctx, from, to)
	if err != nil {
		return
	}
	byName := map[string]metrics.Series{}
	for _, ser := range res.Series {
		byName[ser.Name] = ser
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		ser, ok := byName[rule.Metric]
		if !ok || len(ser.Points) == 0 {
			continue
		}
		last := ser.Points[len(ser.Points)-1]
		if !thresholdBreached(rule.Op, last.Value, rule.Threshold) {
			continue
		}
		if rule.LastFiredAt != nil && to.Sub(*rule.LastFiredAt) < 15*time.Minute {
			continue
		}
		payload, _ := json.Marshal(map[string]any{
			"rule_id": rule.ID, "name": rule.Name, "metric": rule.Metric, "op": rule.Op,
			"threshold": rule.Threshold, "value": last.Value,
		})
		e := appdb.Event{
			ID: uuid.NewString(), ClusterID: cluster.ID, Type: "alert.firing", Payload: payload, CreatedAt: to,
		}
		_ = s.Store.InsertEvent(ctx, e)
		if s.Hub != nil {
			s.Hub.Publish(e)
		}
		_ = s.Store.UpdateAlertRuleFired(ctx, cluster.ID, rule.ID, to)
		s.notify(ctx, cluster.ID, payload)
	}
}

func thresholdBreached(op string, value, threshold float64) bool {
	switch op {
	case appdb.AlertOpGT:
		return value > threshold
	case appdb.AlertOpLT:
		return value < threshold
	default:
		return false
	}
}

func (s *Server) notify(ctx context.Context, clusterID string, payload []byte) {
	chs, err := s.Store.ListNotificationChannels(ctx, clusterID)
	if err != nil {
		return
	}
	cli := s.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 10 * time.Second}
	}
	for _, ch := range chs {
		switch ch.Kind {
		case appdb.NotifyWebhook:
			url, _, err := s.Store.NotificationSecrets(ctx, clusterID, ch.ID)
			if err != nil || url == "" {
				continue
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
			if err != nil {
				continue
			}
			req.Header.Set("Content-Type", "application/json")
			res, err := cli.Do(req)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		case appdb.NotifySMTP:
			if strings.TrimSpace(ch.SMTPHost) == "" || ch.Status == appdb.NotifyNotConfigured {
				continue
			}
			// Local SMTP delivery is optional. Cloud tests leave host empty so this stays not_configured.
		}
	}
}
