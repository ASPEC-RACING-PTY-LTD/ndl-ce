package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/transport"
	"golang.org/x/net/http2"
)

const (
	qemuProtoName      = "qemu-proto"
	qemuProtoDiskBytes = 1 << 30
)

// QemuRPC is the privileged agent surface for the lab QEMU prototype.
type QemuRPC interface {
	StartQemuProto(ctx context.Context, spec qemu.Spec) (qemu.Result, error)
	StopQemuProto(ctx context.Context, id string) (qemu.Result, error)
	KillQemuProto(ctx context.Context, id string) (qemu.Result, error)
	StatusQemuProto(ctx context.Context, id string) (qemu.Observed, error)
}

type labQemuStartRequest struct {
	WorkloadID string `json:"workload_id"`
	PoolID     string `json:"pool_id"`
	VolumeID   string `json:"volume_id"`
	SizeBytes  int64  `json:"size_bytes"`
	Autostart  bool   `json:"autostart"`
}

type qemuProtoApplied struct {
	SchemaVersion string `json:"schema_version"`
	VolumeID      string `json:"volume_id"`
	DiskPath      string `json:"disk_path"`
	DiskFormat    string `json:"disk_format"`
	Machine       string `json:"machine"`
	Accel         string `json:"accel"`
	Autostart     bool   `json:"autostart"`
	MemoryBytes   int64  `json:"memory_bytes"`
	CPUs          int    `json:"cpus"`
}

type qemuUnavailable struct{}

func (qemuUnavailable) StartQemuProto(context.Context, qemu.Spec) (qemu.Result, error) {
	return qemu.Result{}, errUnavailable("qemu proto agent is unavailable")
}

func (qemuUnavailable) StopQemuProto(context.Context, string) (qemu.Result, error) {
	return qemu.Result{}, errUnavailable("qemu proto agent is unavailable")
}

func (qemuUnavailable) KillQemuProto(context.Context, string) (qemu.Result, error) {
	return qemu.Result{}, errUnavailable("qemu proto agent is unavailable")
}

func (qemuUnavailable) StatusQemuProto(context.Context, string) (qemu.Observed, error) {
	return qemu.Observed{}, errUnavailable("qemu proto agent is unavailable")
}

type qemuProtoAdapter struct {
	socket string
}

// AdaptQEMU returns the agent client when it implements QemuRPC.
// Otherwise it uses a compile-friendly adapter that calls the typed Execute
// methods the agent already exposes, so HTTP tests can still fake Server.QEMU.
func AdaptQEMU(client any) QemuRPC {
	if q, ok := client.(QemuRPC); ok {
		return q
	}
	if c, ok := client.(agentrpc.Client); ok {
		return qemuProtoAdapter{socket: c.Socket}
	}
	return qemuUnavailable{}
}

func (a qemuProtoAdapter) StartQemuProto(ctx context.Context, spec qemu.Spec) (qemu.Result, error) {
	res, err := a.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStart{QemuProtoStart: &agentv1.QemuProtoStart{
			WorkloadId: spec.WorkloadID, VolumeId: spec.VolumeID, DiskPath: spec.DiskPath,
			DiskFormat: spec.DiskFormat, Cpus: int32(spec.CPUs), MemoryBytes: spec.MemoryBytes,
			Machine: spec.Machine, Accel: spec.Accel, Autostart: spec.Autostart,
		}},
	}))
	if err != nil {
		return qemu.Result{}, err
	}
	var out qemu.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Result{}, err
	}
	return out, nil
}

func (a qemuProtoAdapter) StopQemuProto(ctx context.Context, id string) (qemu.Result, error) {
	return a.stopQemu(ctx, id, false)
}

func (a qemuProtoAdapter) KillQemuProto(ctx context.Context, id string) (qemu.Result, error) {
	return a.stopQemu(ctx, id, true)
}

func (a qemuProtoAdapter) stopQemu(ctx context.Context, id string, force bool) (qemu.Result, error) {
	res, err := a.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStop{QemuProtoStop: &agentv1.QemuProtoStop{
			WorkloadId: id,
			Force:      force,
		}},
	}))
	if err != nil {
		return qemu.Result{}, err
	}
	var out qemu.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Result{}, err
	}
	return out, nil
}

func (a qemuProtoAdapter) StatusQemuProto(ctx context.Context, id string) (qemu.Observed, error) {
	res, err := a.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_QemuProtoStatus{QemuProtoStatus: &agentv1.QemuProtoStatus{
			WorkloadId: id,
		}},
	}))
	if err != nil {
		return qemu.Observed{}, err
	}
	var out qemu.Observed
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return qemu.Observed{}, err
	}
	return out, nil
}

func (a qemuProtoAdapter) rpc() agentv1connect.AgentServiceClient {
	path := a.socket
	if path == "" {
		path = transport.AgentSocket
	}
	httpClient := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
	return agentv1connect.NewAgentServiceClient(httpClient, "http://local")
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, action string) (*principal, error) {
	p, err := s.principal(r)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "not authenticated")
		return nil, err
	}
	if !hasRole(p, rbac.Admin) {
		s.audit(r, p.User.ClusterID, p.User.ID, action, "denied", "lab qemu-proto is admin only")
		writeErr(w, http.StatusForbidden, "forbidden")
		return nil, errors.New("forbidden")
	}
	return p, nil
}

func (s *Server) labQemuProtoStart(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAdmin(w, r, "lab.qemu-proto.start")
	if err != nil {
		return
	}
	var req labQemuStartRequest
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.PoolID = strings.TrimSpace(req.PoolID)
	req.VolumeID = strings.TrimSpace(req.VolumeID)
	node, err := s.Store.GetNode(r.Context(), p.User.ClusterID)
	if err != nil || node == nil {
		writeErr(w, http.StatusFailedDependency, "local node is not enrolled")
		return
	}
	if s.QEMU == nil || s.Storage == nil {
		writeErr(w, http.StatusBadGateway, "qemu proto agent is unavailable")
		return
	}
	existing, _ := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, qemuProtoName)
	created := existing == nil
	pool, vol, diskPath, err := s.prepareQemuDisk(r.Context(), p.User.ClusterID, node.ID, req, existing)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	id := uuid.NewString()
	if existing != nil {
		id = existing.ID
	} else if req.WorkloadID != "" {
		if err := qemu.ValidateWorkloadID(req.WorkloadID); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		id = req.WorkloadID
	}
	spec := qemu.Spec{
		WorkloadID:  id,
		VolumeID:    vol.ID,
		DiskPath:    diskPath,
		DiskFormat:  firstNonEmpty(vol.Format, storage.FormatQCOW2),
		MemoryBytes: qemu.DefaultMemory,
		CPUs:        1,
		Machine:     qemu.DefaultMachine,
		Autostart:   req.Autostart,
	}
	op := s.startOp(r.Context(), p.User.ClusterID, node.ID, "lab.qemu-proto.start", "starting", 40)
	res, err := s.QEMU.StartQemuProto(r.Context(), spec)
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		s.audit(r, p.User.ClusterID, p.User.ID, "lab.qemu-proto.start", "denied", err.Error())
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	applied := qemuProtoApplied{
		SchemaVersion: qemu.LastAppliedSchema,
		VolumeID:      vol.ID,
		DiskPath:      diskPath,
		DiskFormat:    spec.DiskFormat,
		Machine:       firstNonEmpty(res.Machine, spec.Machine),
		Accel:         honestAccel(res.Accel),
		Autostart:     req.Autostart,
		MemoryBytes:   spec.MemoryBytes,
		CPUs:          spec.CPUs,
	}
	status := firstNonEmpty(res.Status, qemu.StatusRunning)
	if existing == nil {
		devices, _ := json.Marshal(applied)
		row := appdb.Workload{
			ID: id, ClusterID: p.User.ClusterID, NodeID: node.ID,
			OwnerNodeID: node.ID, DesiredNodeID: node.ID, Name: qemuProtoName, Kind: qemu.KindVM,
			Status: status, DesiredPower: "running", CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes,
			Devices: devices, MigrateBlockers: json.RawMessage(`["QEMU live migrate is not implemented"]`),
		}
		if err := s.Store.CreateWorkload(r.Context(), row); err != nil {
			if existing, _ = s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, qemuProtoName); existing == nil {
				s.finishOp(r.Context(), op, "failed", err.Error(), 0)
				writeErr(w, http.StatusConflict, "could not record workload")
				return
			}
		} else {
			existing = &row
		}
	} else {
		_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: existing.ID, DesiredPower: "running", CPUs: spec.CPUs, MemoryBytes: spec.MemoryBytes})
		_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{ID: existing.ID, Status: status, Reason: "last-applied recorded; unit not observed"})
		if updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, existing.ID); updated != nil {
			existing = updated
		}
	}
	if existing != nil {
		s.ensureQemuDisk(r.Context(), p.User.ClusterID, existing.ID, vol.ID)
		_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{
			ID: existing.ID, Status: status, Reason: firstNonEmpty(res.Reason, "started"), UnitActive: res.UnitActive,
		})
		if updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, existing.ID); updated != nil {
			existing = updated
		}
	}
	_ = pool
	s.finishOp(r.Context(), op, "succeeded", "started", 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "lab.qemu-proto.start", "ok", id)
	s.emitEvent(r.Context(), p.User.ClusterID, node.ID, "lab.qemu-proto.started", map[string]string{"workload_id": id, "kind": qemu.KindVM})
	if existing == nil {
		writeErr(w, http.StatusInternalServerError, "workload missing after start")
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	obs := qemu.Observed{WorkloadID: id, Status: status, Reason: res.Reason, UnitActive: res.UnitActive, RunningAs: res.RunningAs, Machine: applied.Machine, Accel: applied.Accel}
	writeJSON(w, code, s.qemuProtoJSON(*existing, applied, &obs))
}

func (s *Server) labQemuProtoStatus(w http.ResponseWriter, r *http.Request) {
	p, err := s.requireAdmin(w, r, "lab.qemu-proto.status")
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, qemuProtoName)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "qemu-proto is not started")
		return
	}
	applied := decodeQemuApplied(row.Devices)
	var obs *qemu.Observed
	if s.QEMU != nil {
		got, oerr := s.QEMU.StatusQemuProto(r.Context(), row.ID)
		if oerr == nil {
			obs = &got
			_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{
				ID: row.ID, Status: firstNonEmpty(got.Status, row.Status), Reason: got.Reason, UnitActive: got.UnitActive,
			})
			if updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID); updated != nil {
				row = updated
			}
			if got.Machine != "" {
				applied.Machine = got.Machine
			}
			if got.Accel != "" {
				applied.Accel = honestAccel(got.Accel)
			}
		}
	}
	writeJSON(w, http.StatusOK, s.qemuProtoJSON(*row, applied, obs))
}

func (s *Server) labQemuProtoStop(w http.ResponseWriter, r *http.Request) {
	s.labQemuProtoHalt(w, r, "stop")
}

func (s *Server) labQemuProtoKill(w http.ResponseWriter, r *http.Request) {
	s.labQemuProtoHalt(w, r, "kill")
}

func (s *Server) labQemuProtoHalt(w http.ResponseWriter, r *http.Request, action string) {
	p, err := s.requireAdmin(w, r, "lab.qemu-proto."+action)
	if err != nil {
		return
	}
	row, err := s.Store.GetWorkloadByName(r.Context(), p.User.ClusterID, qemuProtoName)
	if err != nil || row == nil {
		writeErr(w, http.StatusNotFound, "qemu-proto is not started")
		return
	}
	if s.QEMU == nil {
		writeErr(w, http.StatusBadGateway, "qemu proto agent is unavailable")
		return
	}
	op := s.startOp(r.Context(), p.User.ClusterID, row.NodeID, "lab.qemu-proto."+action, action, 40)
	var res qemu.Result
	if action == "kill" {
		res, err = s.QEMU.KillQemuProto(r.Context(), row.ID)
	} else {
		res, err = s.QEMU.StopQemuProto(r.Context(), row.ID)
	}
	if err != nil {
		s.finishOp(r.Context(), op, "failed", err.Error(), 0)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	status := firstNonEmpty(res.Status, qemu.StatusStopped)
	_ = s.Store.UpdateWorkloadSpec(r.Context(), appdb.Workload{ID: row.ID, DesiredPower: "stopped"})
	_ = s.Store.UpdateWorkloadObserved(r.Context(), appdb.Workload{ID: row.ID, Status: status, Reason: firstNonEmpty(res.Reason, action), UnitActive: res.UnitActive})
	s.finishOp(r.Context(), op, "succeeded", action, 100)
	s.audit(r, p.User.ClusterID, p.User.ID, "lab.qemu-proto."+action, "ok", row.ID)
	updated, _ := s.Store.GetWorkload(r.Context(), p.User.ClusterID, row.ID)
	if updated == nil {
		updated = row
	}
	applied := decodeQemuApplied(updated.Devices)
	if res.Accel != "" {
		applied.Accel = honestAccel(res.Accel)
	}
	if res.Machine != "" {
		applied.Machine = res.Machine
	}
	obs := qemu.Observed{WorkloadID: row.ID, Status: status, Reason: res.Reason, UnitActive: res.UnitActive, RunningAs: res.RunningAs, Machine: applied.Machine, Accel: applied.Accel}
	writeJSON(w, http.StatusOK, s.qemuProtoJSON(*updated, applied, &obs))
}

func (s *Server) prepareQemuDisk(ctx context.Context, clusterID, nodeID string, req labQemuStartRequest, existing *appdb.Workload) (*appdb.StoragePool, *appdb.Volume, string, error) {
	volumeID := req.VolumeID
	if volumeID == "" && existing != nil {
		disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, existing.ID)
		if len(disks) > 0 {
			volumeID = disks[0].VolumeID
		}
	}
	if volumeID != "" {
		if vol, _ := s.Store.GetVolume(ctx, clusterID, volumeID); vol != nil {
			pool, err := s.Store.GetStoragePool(ctx, clusterID, vol.PoolID)
			if err != nil || pool == nil {
				return nil, nil, "", errConflict("storage pool is not found")
			}
			diskPath, err := storage.JoinUnder(pool.RootPath, vol.BackendRef)
			if err != nil {
				return nil, nil, "", errConflict("volume locator is invalid")
			}
			return pool, vol, diskPath, nil
		}
	}
	pool, err := s.pickQemuPool(ctx, clusterID, req.PoolID)
	if err != nil {
		return nil, nil, "", err
	}
	if volumeID == "" {
		volumeID = uuid.NewString()
	}
	size := req.SizeBytes
	if size < 1 {
		size = qemuProtoDiskBytes
	}
	hint := appdb.PoolHints([]appdb.StoragePool{*pool})[0]
	res, err := s.Storage.CreateDirectoryVolume(ctx, storage.CreateVolumeRequest{
		VolumeID: volumeID, PoolID: pool.ID, RootPath: pool.RootPath,
		Class: storage.ClassVMDisk, Size: size, Format: storage.FormatQCOW2,
	}, hint)
	if err != nil && !errors.Is(err, storage.ErrDuplicate) {
		return nil, nil, "", err
	}
	if existingVol, _ := s.Store.GetVolume(ctx, clusterID, volumeID); existingVol != nil {
		diskPath, jerr := storage.JoinUnder(pool.RootPath, existingVol.BackendRef)
		if jerr != nil {
			return nil, nil, "", errConflict("volume locator is invalid")
		}
		return pool, existingVol, diskPath, nil
	}
	backend := res.Handle.BackendRef
	if backend == "" {
		backend = path.Join("volumes", storage.ClassVMDisk, volumeID+".qcow2")
	}
	row := appdb.Volume{
		ID: volumeID, ClusterID: clusterID, NodeID: nodeID, PoolID: pool.ID,
		Class: storage.ClassVMDisk, Kind: firstNonEmpty(res.Handle.Kind, storage.KindBlock),
		Format: firstNonEmpty(res.Handle.Format, storage.FormatQCOW2), SizeBytes: size,
		Status: storage.StatusAvailable, BackendType: firstNonEmpty(res.Handle.BackendType, storage.BackendDirectory),
		BackendRef: backend,
	}
	if err := s.Store.CreateVolume(ctx, row); err != nil {
		if existingVol, _ := s.Store.GetVolume(ctx, clusterID, volumeID); existingVol != nil {
			row = *existingVol
		} else {
			return nil, nil, "", err
		}
	}
	diskPath, err := storage.JoinUnder(pool.RootPath, row.BackendRef)
	if err != nil {
		return nil, nil, "", errConflict("volume locator is invalid")
	}
	return pool, &row, diskPath, nil
}

func (s *Server) pickQemuPool(ctx context.Context, clusterID, poolID string) (*appdb.StoragePool, error) {
	if poolID != "" {
		pool, err := s.Store.GetStoragePool(ctx, clusterID, poolID)
		if err != nil || pool == nil {
			return nil, errNotFound("storage pool is not found")
		}
		if pool.Status != storage.StatusAvailable && pool.Status != storage.StatusWarning {
			return nil, errConflict("an available storage pool is required")
		}
		return pool, nil
	}
	pools, err := s.Store.ListStoragePools(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	for i := range pools {
		if pools[i].Status == storage.StatusAvailable || pools[i].Status == storage.StatusWarning {
			cp := pools[i]
			return &cp, nil
		}
	}
	return nil, errConflict("an available storage pool is required")
}

func (s *Server) ensureQemuDisk(ctx context.Context, clusterID, workloadID, volumeID string) {
	disks, _ := s.Store.ListWorkloadDisks(ctx, clusterID, workloadID)
	for _, d := range disks {
		if d.VolumeID == volumeID {
			return
		}
	}
	_ = s.Store.CreateWorkloadDisk(ctx, appdb.WorkloadDisk{
		ID: uuid.NewString(), ClusterID: clusterID, WorkloadID: workloadID, VolumeID: volumeID, Role: "root",
	})
}

func (s *Server) qemuProtoJSON(w appdb.Workload, applied qemuProtoApplied, obs *qemu.Observed) map[string]any {
	unit := "nodal-vm@" + w.ID + ".service"
	unitStatus := "Unavailable"
	observeStatus := "Collecting"
	runningAs := ""
	qmp := ""
	serial := ""
	vnc := ""
	qga := ""
	if obs != nil {
		observeStatus = firstNonEmpty(obs.Status, observeStatus)
		runningAs = obs.RunningAs
		qmp = obs.QMP
		serial = obs.SerialSocket
		vnc = obs.VNCSocket
		qga = obs.QGASocket
		switch {
		case obs.UnitActive:
			unitStatus = "active"
		case obs.Status == qemu.StatusFailed:
			unitStatus = "failed"
		case obs.Status == qemu.StatusStopped:
			unitStatus = "inactive"
		case obs.Status == qemu.StatusUnavailable:
			unitStatus = "Unavailable"
		}
		if obs.Status != "" {
			w.Status = obs.Status
		}
		if obs.Reason != "" {
			w.Reason = obs.Reason
		}
	}
	return map[string]any{
		"id": w.ID, "name": w.Name, "kind": w.Kind, "status": w.Status,
		"reason": w.Reason, "desired_power": w.DesiredPower,
		"volume_id": applied.VolumeID, "disk_path": applied.DiskPath,
		"disk_format": applied.DiskFormat, "machine": applied.Machine,
		"accel": applied.Accel, "autostart": applied.Autostart,
		"cpus": w.CPUs, "memory_bytes": w.MemoryBytes,
		"unit": unit, "unit_status": unitStatus, "observe_status": observeStatus,
		"unit_active": obs != nil && obs.UnitActive, "running_as": runningAs,
		"qmp": qmp, "serial_socket": serial, "vnc_socket": vnc, "qga_socket": qga,
		"last_applied": applied,
	}
}

func decodeQemuApplied(raw json.RawMessage) qemuProtoApplied {
	var out qemuProtoApplied
	if len(raw) == 0 || raw[0] == '[' {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func honestAccel(accel string) string {
	return strings.TrimSpace(accel)
}
