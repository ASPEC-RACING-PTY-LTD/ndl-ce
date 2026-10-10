package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// OCI container groups are stored as stacks: a stack is a group, and each
// member links one OCI workload. Grouping never creates, changes or deletes
// a container; it only records which group the container belongs to.

// addOCIGroupMember puts an existing OCI container into a group, moving it out
// of any group it was in.
func (s *Server) addOCIGroupMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	ctx := r.Context()
	group, err := s.Store.GetStack(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || group == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	var req struct {
		WorkloadID string `json:"workload_id"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.WorkloadID) == "" {
		writeErr(w, http.StatusBadRequest, "workload_id is required")
		return
	}
	wl, err := s.Store.GetWorkload(ctx, p.User.ClusterID, strings.TrimSpace(req.WorkloadID))
	if err != nil || wl == nil {
		writeErr(w, http.StatusNotFound, "container not found")
		return
	}
	if wl.Kind != oci.KindOCI {
		writeErr(w, http.StatusConflict, "only OCI containers can be grouped")
		return
	}
	stacks, err := s.Store.ListStacks(ctx, p.User.ClusterID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	taken := map[string]bool{}
	for _, st := range stacks {
		members, _ := s.Store.ListStackMembers(ctx, p.User.ClusterID, st.ID)
		for _, m := range members {
			if st.ID == group.ID {
				taken[m.ServiceName] = true
			}
			if m.WorkloadID != wl.ID {
				continue
			}
			if st.ID == group.ID {
				writeJSON(w, http.StatusOK, s.groupOut(ctx, p.User.ClusterID, *group))
				return
			}
			if err := s.Store.DeleteStackMember(ctx, p.User.ClusterID, m.ID); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	service := wl.Name
	for i := 2; taken[service]; i++ {
		service = wl.Name + "-" + strconv.Itoa(i)
	}
	md := memberDesired{ServiceName: service, Name: wl.Name, ImagePin: wl.ImagePin}
	var spec oci.Spec
	if json.Unmarshal(wl.SpecJSON, &spec) == nil {
		md.Env, md.Ports, md.Volumes, md.Command, md.Health = spec.Env, spec.Ports, spec.Volumes, spec.Command, spec.Health
		md.NetworkID, md.RegistryID = spec.NetworkID, spec.RegistryID
		md.CPUs, md.MemoryBytes, md.Privileged = spec.Resources.CPUs, spec.Resources.MemoryBytes, spec.Privileged
	}
	body, _ := json.Marshal(md)
	members, _ := s.Store.ListStackMembers(ctx, p.User.ClusterID, group.ID)
	if err := s.Store.CreateStackMember(ctx, appdb.StackMember{
		ID: uuid.NewString(), ClusterID: p.User.ClusterID, StackID: group.ID, ServiceName: service,
		WorkloadID: wl.ID, DesiredJSON: body, Status: memberStatusFromWorkload(*wl),
		SortOrder: len(members), CreatedAt: s.now(),
	}); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.refreshGroupStatus(ctx, p.User.ClusterID, group.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "oci.group.member.add", "ok", group.ID+" "+wl.ID)
	writeJSON(w, http.StatusOK, s.groupOut(ctx, p.User.ClusterID, *group))
}

// removeOCIGroupMember takes a container out of its group. The container keeps running.
func (s *Server) removeOCIGroupMember(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	ctx := r.Context()
	group, err := s.Store.GetStack(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || group == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	mem, err := s.Store.GetStackMember(ctx, p.User.ClusterID, r.PathValue("memberId"))
	if err != nil || mem == nil || mem.StackID != group.ID {
		writeErr(w, http.StatusNotFound, "group member not found")
		return
	}
	if err := s.Store.DeleteStackMember(ctx, p.User.ClusterID, mem.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.refreshGroupStatus(ctx, p.User.ClusterID, group.ID)
	s.audit(r, p.User.ClusterID, p.User.ID, "oci.group.member.remove", "ok", group.ID+" "+mem.WorkloadID)
	writeJSON(w, http.StatusOK, s.groupOut(ctx, p.User.ClusterID, *group))
}

// groupPower starts, stops or restarts every container in a group.
func (s *Server) groupPower(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeLifecycle)
	if err != nil {
		return
	}
	ctx := r.Context()
	group, err := s.Store.GetStack(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || group == nil {
		writeErr(w, http.StatusNotFound, "group not found")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "start" && action != "stop" && action != "restart" {
		writeErr(w, http.StatusBadRequest, "action must be start, stop or restart")
		return
	}
	members, _ := s.Store.ListStackMembers(ctx, p.User.ClusterID, group.ID)
	type result struct {
		WorkloadID string `json:"workload_id"`
		Name       string `json:"name"`
		OK         bool   `json:"ok"`
		Error      string `json:"error,omitempty"`
	}
	out := []result{}
	failed := 0
	for _, m := range members {
		if m.WorkloadID == "" {
			continue
		}
		wl, _ := s.Store.GetWorkload(ctx, p.User.ClusterID, m.WorkloadID)
		if wl == nil {
			continue
		}
		code, body := s.delegate(r, http.MethodPost, "/workloads/"+wl.ID+"/"+action, nil, map[string]string{"id": wl.ID}, "", s.lifecycleWorkload(action))
		res := result{WorkloadID: wl.ID, Name: wl.Name, OK: code < 300}
		if !res.OK {
			failed++
			res.Error, _ = body["error"].(string)
		}
		out = append(out, res)
	}
	s.refreshGroupStatus(ctx, p.User.ClusterID, group.ID)
	outcome := "ok"
	if failed > 0 {
		outcome = "partial"
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "oci.group."+action, outcome, group.ID)
	writeJSON(w, http.StatusOK, map[string]any{"id": group.ID, "action": action, "results": out, "failed": failed})
}

func (s *Server) refreshGroupStatus(ctx context.Context, clusterID, groupID string) {
	members, _ := s.Store.ListStackMembers(ctx, clusterID, groupID)
	s.refreshMemberStatuses(ctx, clusterID, members)
	members, _ = s.Store.ListStackMembers(ctx, clusterID, groupID)
	_ = s.Store.UpdateStack(ctx, appdb.Stack{ID: groupID, ClusterID: clusterID, Status: deriveStackStatus(members)})
}

func (s *Server) groupOut(ctx context.Context, clusterID string, group appdb.Stack) map[string]any {
	if fresh, _ := s.Store.GetStack(ctx, clusterID, group.ID); fresh != nil {
		group = *fresh
	}
	members, _ := s.Store.ListStackMembers(ctx, clusterID, group.ID)
	if members == nil {
		members = []appdb.StackMember{}
	}
	return s.stackJSON(ctx, group, members)
}
