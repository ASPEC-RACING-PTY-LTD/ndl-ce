package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/guestextras"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

func (s *Server) listWorkloadExtras(w http.ResponseWriter, r *http.Request) {
	if _, err := s.require(w, r, rbac.ComputeRead); err != nil {
		return
	}
	pin := strings.TrimSpace(r.URL.Query().Get("image_pin"))
	writeJSON(w, http.StatusOK, map[string]any{"items": guestextras.Describe(pin)})
}

func (s *Server) setupWorkloadExtras(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if row.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "extras are only supported on system containers")
		return
	}
	var req struct {
		Extras []string `json:"extras"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	op, err := s.startExtensionsJob(p.User.ClusterID, *row, req.Extras, extensionInstall)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.setup-extras", "ok", row.ID)
	out := s.workloadJSON(r.Context(), *row)
	if op != nil {
		out["setup_status"] = "installing"
		out["setup_operation_id"] = op.ID
	}
	writeJSON(w, http.StatusAccepted, out)
}

func extrasAuditResult(warnings []lxc.SetupWarning) string {
	if len(warnings) == 0 {
		return "ok"
	}
	return "warning"
}

// Extension job modes map to agent lifecycle actions.
const (
	extensionInstall   = lxc.ActionGuestSetup
	extensionUpdate    = lxc.ActionGuestExtrasUpdate
	extensionReinstall = lxc.ActionGuestExtrasReinstall
)

// extensionsJobTimeout bounds one background extras run. Docker on a slow
// mirror can take several minutes; the run no longer depends on any request.
const extensionsJobTimeout = 45 * time.Minute

// startExtensionsJob installs, updates or reinstalls extras in the background.
// It is not tied to the HTTP request that asked for it: a browser or proxy
// closing that request (Cloudflare drops requests after 100 seconds) must not
// cancel an install half way, which is how Compose was skipped before.
func (s *Server) startExtensionsJob(clusterID string, row appdb.Workload, selected []string, mode string) (*appdb.Operation, error) {
	extras := guestextras.Resolve(selected)
	if len(extras) == 0 {
		return nil, nil
	}
	if s.Workloads == nil {
		return nil, fmt.Errorf("workload agent is unavailable")
	}
	s.extrasMu.Lock()
	if s.extrasBusy == nil {
		s.extrasBusy = map[string]bool{}
	}
	if s.extrasBusy[row.ID] {
		s.extrasMu.Unlock()
		return nil, fmt.Errorf("extensions are already being changed on this container; wait for that to finish")
	}
	s.extrasBusy[row.ID] = true
	s.extrasMu.Unlock()

	ctx := context.Background()
	stage := map[string]string{extensionInstall: "installing", extensionUpdate: "updating", extensionReinstall: "reinstalling"}[mode]
	msg, _ := json.Marshal(map[string]any{"workload_id": row.ID, "name": row.Name, "extras": extras})
	op := s.startOpKeyed(ctx, clusterID, row.NodeID, "workload.setup-extras", stage, "", string(msg), 10)
	prior := s.extensionMap(ctx, clusterID, row.ID)
	for _, id := range extras {
		x := prior[id]
		x.WorkloadID, x.ClusterID, x.ExtraID = row.ID, clusterID, id
		x.Requested = true
		x.State, x.Message = appdb.ExtensionInstalling, ""
		_ = s.Store.PutWorkloadExtension(ctx, x)
	}
	go func() {
		defer func() {
			s.extrasMu.Lock()
			delete(s.extrasBusy, row.ID)
			s.extrasMu.Unlock()
		}()
		jctx, cancel := context.WithTimeout(ctx, extensionsJobTimeout)
		defer cancel()
		warnings := s.runExtensions(jctx, clusterID, row, extras, mode)
		if len(warnings) > 0 {
			s.finishOp(ctx, op, "failed", extrasWarningMessage(row.Name, warnings), 100)
			return
		}
		s.finishOp(ctx, op, "succeeded", extrasOKMessage(row.Name, extras), 100)
	}()
	return &op, nil
}

func (s *Server) extensionMap(ctx context.Context, clusterID, workloadID string) map[string]appdb.WorkloadExtension {
	out := map[string]appdb.WorkloadExtension{}
	list, _ := s.Store.ListWorkloadExtensions(ctx, clusterID, workloadID)
	for _, x := range list {
		out[x.ExtraID] = x
	}
	return out
}

// runExtensions runs one extras action, starting a stopped container for the
// duration, and records the outcome of every extra.
func (s *Server) runExtensions(ctx context.Context, clusterID string, row appdb.Workload, extras []string, mode string) []lxc.SetupWarning {
	if current, _ := s.Store.GetWorkload(ctx, clusterID, row.ID); current != nil {
		row = *current
	}
	startedForSetup := false
	// Only a container meant to be or seen stopped is started for the run. A
	// just-created container may not be observed running yet, and must not be
	// stopped afterwards.
	if row.DesiredPower == "stopped" || (!row.UnitActive && strings.EqualFold(row.Status, lxc.StatusStopped)) {
		if _, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: "start"}); err == nil {
			startedForSetup = true
		}
	}
	res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: mode, Extras: extras})
	if startedForSetup {
		_, _ = s.Workloads.LifecycleCT(context.WithoutCancel(ctx), lxc.LifecycleRequest{WorkloadID: row.ID, Action: "stop"})
	}
	warnings := append([]lxc.SetupWarning{}, res.SetupWarnings...)
	if err != nil {
		warnings = append(warnings, lxc.SetupWarning{Extra: "setup", Message: sanitizeGuestSetupErr(err)})
	}
	s.recordExtensions(context.WithoutCancel(ctx), clusterID, row.ID, extras, res.Extras, warnings)
	return warnings
}

// recordExtensions stores what the guest reported. An extra the run was asked
// for but that failed, or that the run never reached, is marked failed with
// the reason so it can be retried.
func (s *Server) recordExtensions(ctx context.Context, clusterID, workloadID string, asked []string, found []lxc.ExtraStatus, warnings []lxc.SetupWarning) {
	prior := s.extensionMap(ctx, clusterID, workloadID)
	why := map[string]string{}
	general := ""
	for _, w := range warnings {
		if w.Extra == "setup" || w.Extra == "" {
			general = w.Message
			continue
		}
		why[w.Extra] = w.Message
	}
	wasAsked := map[string]bool{}
	for _, id := range asked {
		wasAsked[id] = true
	}
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.ID] = true
		x, known := prior[f.ID]
		if !known && !f.Installed && !wasAsked[f.ID] {
			continue
		}
		x.WorkloadID, x.ClusterID, x.ExtraID = workloadID, clusterID, f.ID
		x.Requested = x.Requested || wasAsked[f.ID]
		x.Version, x.Package, x.Detail = f.Version, f.Package, f.Detail
		switch {
		case why[f.ID] != "":
			x.State, x.Message = appdb.ExtensionFailed, why[f.ID]
		case f.Installed:
			x.State, x.Message = appdb.ExtensionInstalled, ""
		case wasAsked[f.ID]:
			x.State, x.Message = appdb.ExtensionFailed, firstNonEmpty(general, "not installed after the run")
		default:
			x.State, x.Message = appdb.ExtensionMissing, ""
		}
		_ = s.Store.PutWorkloadExtension(ctx, x)
	}
	for _, id := range asked {
		if seen[id] {
			continue
		}
		x := prior[id]
		x.WorkloadID, x.ClusterID, x.ExtraID, x.Requested = workloadID, clusterID, id, true
		x.State = appdb.ExtensionFailed
		x.Message = firstNonEmpty(why[id], general, "the install did not report back; check the container and retry")
		_ = s.Store.PutWorkloadExtension(ctx, x)
	}
}

func extrasWarningMessage(name string, warnings []lxc.SetupWarning) string {
	ids := make([]string, 0, len(warnings))
	reasons := map[string]string{}
	for _, w := range warnings {
		if w.Extra != "" {
			ids = append(ids, w.Extra)
			reasons[w.Extra] = w.Message
		}
	}
	body, _ := json.Marshal(map[string]any{"name": name, "error": "optional extras failed", "extras": ids, "reasons": reasons})
	return string(body)
}

func extrasOKMessage(name string, extras []string) string {
	body, _ := json.Marshal(map[string]any{"name": name, "extras": extras})
	return string(body)
}

func sanitizeGuestSetupErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}

// extensionJSON is one catalog extra merged with what is known about it on
// the workload.
func extensionJSON(av guestextras.Availability, x *appdb.WorkloadExtension) map[string]any {
	out := map[string]any{
		"id": av.Extra.ID, "name": av.Extra.Name, "description": av.Extra.Description, "category": av.Extra.Category,
		"requires": av.Extra.Requires, "available": av.Available, "state": appdb.ExtensionMissing, "requested": false,
	}
	if av.Reason != "" {
		out["unavailable_reason"] = av.Reason
	}
	if x != nil {
		out["state"], out["requested"] = x.State, x.Requested
		out["version"], out["package"], out["detail"], out["message"] = x.Version, x.Package, x.Detail, x.Message
		out["updated_at"] = x.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func (s *Server) workloadForExtensions(w http.ResponseWriter, r *http.Request, perm string) (*appdb.Workload, *principal, bool) {
	p, err := s.require(w, r, perm)
	if err != nil {
		return nil, nil, false
	}
	row, err := s.Store.GetWorkload(r.Context(), p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return nil, nil, false
	}
	if row.Kind != lxc.KindSystemContainer {
		writeErr(w, http.StatusUnprocessableEntity, "extensions are only supported on system containers")
		return nil, nil, false
	}
	return row, p, true
}

// listWorkloadExtensions reports every extra for a container. With refresh=1
// the guest is asked what is installed now (the container must be running).
func (s *Server) listWorkloadExtensions(w http.ResponseWriter, r *http.Request) {
	row, p, ok := s.workloadForExtensions(w, r, rbac.ComputeRead)
	if !ok {
		return
	}
	clusterID := p.User.ClusterID
	note := ""
	if r.URL.Query().Get("refresh") == "1" && s.Workloads != nil {
		s.extrasMu.Lock()
		busy := s.extrasBusy[row.ID]
		s.extrasMu.Unlock()
		switch {
		case busy:
			note = "a change is running; the status updates when it finishes"
		case row.DesiredPower == "stopped" || strings.EqualFold(row.Status, lxc.StatusStopped):
			note = "start the container to check what is installed"
		default:
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			res, err := s.Workloads.LifecycleCT(ctx, lxc.LifecycleRequest{WorkloadID: row.ID, Action: lxc.ActionGuestExtrasStatus})
			cancel()
			if err != nil {
				note = "could not check the container: " + sanitizeGuestSetupErr(err)
			} else {
				s.recordExtensions(r.Context(), clusterID, row.ID, nil, res.Extras, nil)
			}
		}
	}
	known := s.extensionMap(r.Context(), clusterID, row.ID)
	pin := row.ImagePin
	items := make([]map[string]any, 0, len(guestextras.Catalog()))
	for _, extra := range guestextras.Catalog() {
		var x *appdb.WorkloadExtension
		if v, ok := known[extra.ID]; ok {
			x = &v
		}
		items = append(items, extensionJSON(guestextras.AvailabilityFor(pin, extra.ID), x))
	}
	s.extrasMu.Lock()
	busy := s.extrasBusy[row.ID]
	s.extrasMu.Unlock()
	out := map[string]any{"items": items, "busy": busy, "image_pin": pin}
	if note != "" {
		out["note"] = note
	}
	writeJSON(w, http.StatusOK, out)
}

// changeWorkloadExtensions installs, updates or reinstalls extensions in the
// background. all=true with update or reinstall acts on every installed one.
func (s *Server) changeWorkloadExtensions(w http.ResponseWriter, r *http.Request) {
	row, p, ok := s.workloadForExtensions(w, r, rbac.ComputeModify)
	if !ok {
		return
	}
	clusterID := p.User.ClusterID
	var req struct {
		Action string   `json:"action"`
		Extras []string `json:"extras"`
		All    bool     `json:"all"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	mode := map[string]string{"install": extensionInstall, "update": extensionUpdate, "reinstall": extensionReinstall}[req.Action]
	if mode == "" {
		writeErr(w, http.StatusBadRequest, "action must be install, update or reinstall")
		return
	}
	extras := req.Extras
	if req.All {
		if mode == extensionInstall {
			writeErr(w, http.StatusBadRequest, "choose which extensions to install")
			return
		}
		extras = nil
		for id, x := range s.extensionMap(r.Context(), clusterID, row.ID) {
			if x.State == appdb.ExtensionInstalled || x.Requested {
				extras = append(extras, id)
			}
		}
	}
	for _, id := range extras {
		if len(guestextras.Resolve([]string{id})) == 0 {
			writeErr(w, http.StatusBadRequest, "unknown extension "+id)
			return
		}
	}
	if len(extras) == 0 {
		writeErr(w, http.StatusBadRequest, "no extensions selected")
		return
	}
	op, err := s.startExtensionsJob(clusterID, *row, extras, mode)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.audit(r, clusterID, p.User.ID, "workload.extensions."+req.Action, "ok", row.ID+" "+strings.Join(guestextras.Resolve(extras), ","))
	out := map[string]any{"started": true, "extras": guestextras.Resolve(extras)}
	if op != nil {
		out["operation_id"] = op.ID
	}
	writeJSON(w, http.StatusAccepted, out)
}
