package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
)

// ociUpdater is implemented by agents that can change a container's
// configuration in place. Older agents only create and run containers.
type ociUpdater interface {
	UpdateOCI(ctx context.Context, spec oci.Spec) (oci.Result, error)
}

// ociNetPlan is how a container is networked.
type ociNetPlan struct {
	Network *appdb.Network
	Mode    string
	Address string // CIDR; empty on a bridge means DHCP
	Gateway string
	DNS     []string
}

// planOCINetwork turns the request's network choice into a plan. A request
// naming a network and no mode gets its own address on that network.
func (s *Server) planOCINetwork(ctx context.Context, p *principal, networkID, mode, ipv4Mode, address, gateway string, dns []string) (ociNetPlan, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	networkID = strings.TrimSpace(networkID)
	if mode == "" {
		if networkID != "" {
			mode = oci.NetworkBridge
		} else {
			mode = oci.NetworkNone
		}
	}
	plan := ociNetPlan{Mode: mode}
	for _, d := range dns {
		if d = strings.TrimSpace(d); d != "" {
			if _, err := netip.ParseAddr(d); err != nil {
				return plan, errBadRequest("DNS server " + d + " is not an IP address")
			}
			plan.DNS = append(plan.DNS, d)
		}
	}
	switch mode {
	case oci.NetworkNone:
		return ociNetPlan{Mode: mode}, nil
	case oci.NetworkHost:
		if !hasRole(p, rbac.Admin) {
			return plan, errForbidden("only admin may put a container on the host network")
		}
		return plan, nil
	case oci.NetworkBridge:
	default:
		return plan, errBadRequest("network_mode must be none, bridge or host")
	}
	if networkID == "" {
		return plan, errBadRequest("choose the network the container joins")
	}
	n, err := s.Store.GetNetwork(ctx, p.User.ClusterID, networkID)
	if err != nil || n == nil {
		return plan, errNotFound("network not found")
	}
	if n.Status != ndnet.StatusAvailable && n.Status != ndnet.StatusWarning {
		return plan, errConflict("an available network is required")
	}
	if strings.TrimSpace(n.BridgeName) == "" {
		return plan, errConflict("the network has no bridge to join")
	}
	plan.Network = n
	address = strings.TrimSpace(address)
	if strings.EqualFold(strings.TrimSpace(ipv4Mode), "dhcp") {
		address = ""
	}
	if strings.EqualFold(strings.TrimSpace(ipv4Mode), "static") && address == "" {
		return plan, errBadRequest("enter the container's IP address")
	}
	if address == "" {
		return plan, nil
	}
	if !strings.Contains(address, "/") {
		pfx, perr := netip.ParsePrefix(strings.TrimSpace(n.IPv4CIDR))
		if perr != nil {
			return plan, errBadRequest("add the prefix length to the IP address, for example " + address + "/24")
		}
		address = address + "/" + strconv.Itoa(pfx.Bits())
	}
	pfx, err := netip.ParsePrefix(address)
	if err != nil || !pfx.Addr().Is4() {
		return plan, errBadRequest("the IP address must be IPv4, for example 192.168.1.50/24")
	}
	plan.Address = pfx.String()
	plan.Gateway = strings.TrimSpace(gateway)
	if plan.Gateway == "" {
		plan.Gateway = strings.TrimSpace(n.Gateway)
	}
	if plan.Gateway != "" {
		if _, err := netip.ParseAddr(plan.Gateway); err != nil {
			return plan, errBadRequest("the gateway must be an IPv4 address")
		}
	}
	return plan, nil
}

// apply writes the plan into a spec.
func (pl ociNetPlan) apply(spec *oci.Spec) {
	spec.NetworkMode = pl.Mode
	spec.IPv4Address = pl.Address
	spec.IPv4Gateway = pl.Gateway
	spec.DNS = pl.DNS
	spec.NetworkID, spec.BridgeName = "", ""
	if pl.Network != nil {
		spec.NetworkID = pl.Network.ID
		spec.BridgeName = pl.Network.BridgeName
	}
}

func (pl ociNetPlan) nic(clusterID, workloadID string) appdb.WorkloadNIC {
	n := appdb.WorkloadNIC{ClusterID: clusterID, WorkloadID: workloadID, MAC: oci.ContainerMAC(workloadID)}
	if pl.Network != nil {
		n.NetworkID = pl.Network.ID
	}
	n.IPv4Mode = "dhcp"
	if pl.Address != "" {
		n.IPv4Mode = "static"
		n.IPv4Address = pl.Address
		n.IPv4Gateway = pl.Gateway
		if pfx, err := netip.ParsePrefix(pl.Address); err == nil {
			n.IPv4 = pfx.Addr().String()
		}
	}
	n.DNS = strings.Join(pl.DNS, ",")
	return n
}

// ociVolumePaths checks each mount and finds its volume on the host.
func (s *Server) ociVolumePaths(ctx context.Context, clusterID string, vols []oci.VolumeMount) (map[string]string, error) {
	paths := map[string]string{}
	for _, m := range vols {
		if err := oci.ValidateVolumeMount(m); err != nil {
			return nil, errBadRequest(err.Error())
		}
		vol, err := s.Store.GetVolume(ctx, clusterID, m.VolumeID)
		if err != nil || vol == nil {
			return nil, errNotFound("volume not found")
		}
		if vol.Class != storage.ClassContainerRoot {
			return nil, errConflict("volume is not a container-root")
		}
		if vol.Status != storage.StatusAvailable && vol.Status != storage.StatusWarning {
			return nil, errConflict("storage is unavailable")
		}
		pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
		if err != nil || pool == nil {
			return nil, errUnprocessable("volume pool unavailable")
		}
		if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
			return nil, errConflict("storage is unavailable")
		}
		loc, err := storage.HostVolumePath(pool.BackendType, pool.RootPath, vol.BackendRef)
		if err != nil {
			return nil, errUnprocessable("volume locator is invalid")
		}
		paths[m.VolumeID] = loc
	}
	return paths, nil
}

type ociConfigRequest struct {
	ImagePin    *string            `json:"image_pin"`
	RegistryID  *string            `json:"registry_id"`
	Env         *[]oci.EnvVar      `json:"env"`
	Ports       *[]oci.Port        `json:"ports"`
	Volumes     *[]oci.VolumeMount `json:"volumes"`
	Command     *[]string          `json:"command"`
	Health      *oci.Healthcheck   `json:"health"`
	Privileged  *bool              `json:"privileged"`
	CPUs        *int               `json:"cpus"`
	MemoryBytes *int64             `json:"memory_bytes"`
	NetworkID   *string            `json:"network_id"`
	NetworkMode *string            `json:"network_mode"`
	IPv4Mode    *string            `json:"ipv4_mode"`
	IPv4Address *string            `json:"ipv4_address"`
	IPv4Gateway *string            `json:"ipv4_gateway"`
	DNS         *[]string          `json:"dns"`
}

func strOr(p *string, def string) string {
	if p != nil {
		return *p
	}
	return def
}

// updateOCIConfig changes an existing OCI container: image, environment,
// ports, volumes, network and limits. Fields left out keep their value. A
// running container restarts on the new configuration.
func (s *Server) updateOCIConfig(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ComputeModify)
	if err != nil {
		return
	}
	ctx := r.Context()
	row, err := s.Store.GetWorkload(ctx, p.User.ClusterID, r.PathValue("id"))
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "workload not found")
		return
	}
	if row.Kind != oci.KindOCI {
		writeErr(w, http.StatusConflict, "only OCI containers are configured here")
		return
	}
	var req ociConfigRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.guardLocalApply(w, r, p.User.ClusterID, firstNonEmpty(row.DesiredNodeID, row.NodeID), "update") {
		return
	}
	var spec oci.Spec
	if len(row.SpecJSON) > 0 {
		if err := json.Unmarshal(row.SpecJSON, &spec); err != nil {
			writeErr(w, http.StatusConflict, "the container's stored configuration is unreadable")
			return
		}
	}
	spec.WorkloadID, spec.Name = row.ID, row.Name
	if spec.ImagePin == "" {
		spec.ImagePin = row.ImagePin
	}
	if req.ImagePin != nil {
		spec.ImagePin = strings.TrimSpace(*req.ImagePin)
		if err := oci.ValidateImageRef(spec.ImagePin); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.Env != nil {
		spec.Env = *req.Env
	}
	if req.Ports != nil {
		spec.Ports = *req.Ports
	}
	if req.Command != nil {
		spec.Command = *req.Command
	}
	if req.Health != nil {
		spec.Health = req.Health
		if req.Health.HTTPPath == "" && req.Health.Port == 0 {
			spec.Health = nil
		}
	}
	if req.Privileged != nil {
		if *req.Privileged && !hasRole(p, rbac.Admin) {
			s.audit(r, p.User.ClusterID, p.User.ID, "workload.oci.privileged", "denied", row.ID)
			writeErr(w, http.StatusForbidden, "only admin may make a container privileged")
			return
		}
		spec.Privileged = *req.Privileged
	}
	if req.CPUs != nil && *req.CPUs > 0 {
		spec.Resources.CPUs = *req.CPUs
	}
	if req.MemoryBytes != nil && *req.MemoryBytes > 0 {
		spec.Resources.MemoryBytes = *req.MemoryBytes
	}
	if req.Volumes != nil {
		spec.Volumes = *req.Volumes
	}
	paths, err := s.ociVolumePaths(ctx, p.User.ClusterID, spec.Volumes)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	spec.VolumePaths = paths
	if req.RegistryID != nil {
		spec.RegistryID = strings.TrimSpace(*req.RegistryID)
	}
	spec.RegistryURL, spec.PullUsername, spec.PullPassword = "", "", ""
	if spec.RegistryID != "" {
		reg, err := s.Store.GetRegistry(ctx, p.User.ClusterID, spec.RegistryID)
		if err != nil || reg == nil {
			writeErr(w, http.StatusNotFound, "registry not found")
			return
		}
		spec.RegistryURL = reg.URL
		spec.PullUsername, spec.PullPassword, _ = s.Store.RegistrySecrets(ctx, p.User.ClusterID, spec.RegistryID)
	}
	netTouched := req.NetworkID != nil || req.NetworkMode != nil || req.IPv4Mode != nil || req.IPv4Address != nil || req.IPv4Gateway != nil || req.DNS != nil
	var plan *ociNetPlan
	if netTouched {
		mode := strOr(req.NetworkMode, spec.NetworkMode)
		if req.NetworkMode == nil && req.NetworkID != nil {
			// Naming a network joins it; clearing the network disconnects.
			mode = oci.NetworkBridge
			if strings.TrimSpace(*req.NetworkID) == "" {
				mode = oci.NetworkNone
			}
		}
		dns := spec.DNS
		if req.DNS != nil {
			dns = *req.DNS
		}
		pl, err := s.planOCINetwork(ctx, p, strOr(req.NetworkID, spec.NetworkID), mode,
			strOr(req.IPv4Mode, ""), strOr(req.IPv4Address, spec.IPv4Address), strOr(req.IPv4Gateway, spec.IPv4Gateway), dns)
		if err != nil {
			writeErr(w, statusFor(err), err.Error())
			return
		}
		pl.apply(&spec)
		plan = &pl
	}
	if err := oci.ValidateSpec(spec); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updater, ok := s.ociRPC().(ociUpdater)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "this node's agent cannot change OCI containers; update No-dal on the node")
		return
	}
	res, err := updater.UpdateOCI(ctx, spec)
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "workload.oci.update", "failed", err.Error())
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	stored := oci.Redact(spec)
	stored.PullUsername, stored.PullPassword = "", ""
	specJSON, _ := json.Marshal(stored)
	applied, _ := json.Marshal(map[string]any{
		"schema_version": oci.LastAppliedSchema, "spec": stored,
		"image_digest": res.ImageDigest, "health": res.Health,
	})
	if err := s.Store.UpdateWorkloadSpec(ctx, appdb.Workload{
		ID: row.ID, CPUs: spec.Resources.CPUs, MemoryBytes: spec.Resources.MemoryBytes,
		SpecJSON: specJSON, AppliedJSON: applied, Autostart: row.Autostart, PendingRestart: row.PendingRestart,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.UpdateWorkloadImage(ctx, p.User.ClusterID, row.ID, spec.ImagePin, spec.Privileged); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.syncOCIDisks(ctx, p.User.ClusterID, row.ID, spec.Volumes)
	if plan != nil && plan.Network != nil {
		nic := plan.nic(p.User.ClusterID, row.ID)
		if nics, _ := s.Store.ListWorkloadNICs(ctx, p.User.ClusterID, row.ID); len(nics) > 0 {
			nic.ID = nics[0].ID
			_ = s.Store.UpdateWorkloadNIC(ctx, nic)
		} else {
			nic.ID = uuid.NewString()
			nic.CreatedAt = s.now()
			_ = s.Store.CreateWorkloadNIC(ctx, nic)
		}
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "workload.oci.update", "ok", row.ID)
	updated, _ := s.Store.GetWorkload(ctx, p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	out := s.workloadJSON(ctx, *updated)
	out["applied_status"] = res.Status
	out["applied_message"] = res.Health.Message
	writeJSON(w, http.StatusOK, out)
}

// syncOCIDisks keeps the container's data-volume records in line with its
// mounts, so a detached volume is free to reuse or delete.
func (s *Server) syncOCIDisks(ctx context.Context, clusterID, workloadID string, vols []oci.VolumeMount) {
	want := map[string]bool{}
	for _, v := range vols {
		want[v.VolumeID] = true
	}
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, workloadID)
	have := map[string]bool{}
	for _, d := range disks {
		if d.Role != "data" {
			continue
		}
		have[d.VolumeID] = true
		if !want[d.VolumeID] {
			_ = s.Store.DeleteWorkloadDisk(ctx, clusterID, workloadID, d.VolumeID)
		}
	}
	for id := range want {
		if !have[id] {
			_ = s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
				ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: workloadID,
				VolumeID: id, Role: "data", Format: storage.FormatDirectory, CreatedAt: s.now(),
			})
		}
	}
}
