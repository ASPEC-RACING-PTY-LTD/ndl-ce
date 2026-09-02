package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/oci"
	"github.com/no-dal/ndl-ce/internal/peercred"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

const version = "0.1.0"

// Handler is the typed agent service.
type Handler struct {
	Ident         identity.Files
	AllowedUID    uint32
	Lookup        func() (hostos.Platform, error)
	Peer          func(ctx context.Context) (peercred.Creds, error)
	Collect       func() inventory.Inventory
	Metrics       *metrics.Store
	Storage       *storage.Directory
	Uploads       *storage.Uploads
	Nets          *ndnet.Engine
	Workloads     *lxc.Engine
	QEMU          *qemu.Engine
	OCI           *oci.Engine
	ZFS           *storage.ZFSEngine
	LVM           *storage.LVMEngine
	Datastore     *storage.DatastoreEngine
	Distributed   *storage.DistributedEngine
	Journal       *journald.Engine
	SkipHostCmds  bool
	GuestSocketFn func(id string) string
	QGASocketFn   func(id string) string

	mu         sync.Mutex
	last       inventory.Inventory
	uploadOnce sync.Once
}

var _ agentv1connect.AgentServiceHandler = (*Handler)(nil)

// Hello reports version and host platform.
func (h *Handler) Hello(ctx context.Context, _ *connect.Request[agentv1.HelloRequest]) (*connect.Response[agentv1.HelloResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.platform()
	if err != nil && !errors.As(err, &hostos.Error{}) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.HelloResponse{
		AgentVersion: version,
		HostPlatform: protoPlatform(p),
	}), nil
}

// Observe scrapes typed host inventory. It does not execute a host command.
func (h *Handler) Observe(ctx context.Context, req *connect.Request[agentv1.ObserveRequest]) (*connect.Response[agentv1.ObserveResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	inv := h.refresh()
	return connect.NewResponse(&agentv1.ObserveResponse{
		ObservedAt:    inv.ObservedAt.UTC().Format(timeRFC3339),
		SchemaVersion: inv.SchemaVersion,
		InventoryJson: mustJSON(inv),
		StorageJson:   h.observeStorage(decodeHints(req.Msg.GetStoragePools())),
		NetworkJson:   h.observeNetworks(decodeNetworkHints(req.Msg.GetNetworks())),
		WorkloadJson:  h.observeWorkloads(decodeWorkloadHints(req.Msg.GetWorkloads())),
	}), nil
}

const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

func (h *Handler) refresh() inventory.Inventory {
	var inv inventory.Inventory
	if h.Collect != nil {
		inv = h.Collect()
	} else {
		inv = inventory.Collect(inventory.Options{})
	}
	h.mu.Lock()
	h.last = inv
	h.mu.Unlock()
	return inv
}

func (h *Handler) cachedOrRefresh() inventory.Inventory {
	h.mu.Lock()
	last := h.last
	h.mu.Unlock()
	if last.SchemaVersion == "" {
		return h.refresh()
	}
	return last
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// Execute handles typed methods only.
func (h *Handler) Execute(ctx context.Context, req *connect.Request[agentv1.ExecuteRequest]) (*connect.Response[agentv1.ExecuteResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	switch {
	case req.Msg.GetPing() != nil:
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "pong"}), nil
	case req.Msg.GetCreateDirectoryPool() != nil:
		m := req.Msg.GetCreateDirectoryPool()
		res, err := h.driver().CreatePool(ctx, storage.CreatePoolRequest{
			PoolID: m.GetPoolId(), Name: m.GetName(), RootPath: m.GetRootPath(), Create: m.GetCreate(),
		}, m.GetExistingRoots())
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "created", ResultJson: mustJSON(res)}), nil
	case req.Msg.GetCreateDirectoryVolume() != nil:
		m := req.Msg.GetCreateDirectoryVolume()
		hint := storage.PoolHint{PoolID: m.GetPoolId(), BackendType: storage.BackendDirectory, RootPath: m.GetRootPath()}
		if len(m.GetBackingJson()) > 0 {
			_ = json.Unmarshal(m.GetBackingJson(), &hint.Backing)
		}
		res, err := h.driver().CreateVolume(ctx, storage.CreateVolumeRequest{
			VolumeID: m.GetVolumeId(), PoolID: m.GetPoolId(), RootPath: m.GetRootPath(),
			Class: m.GetClass(), Size: m.GetSizeBytes(), Format: m.GetFormat(),
		}, hint)
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return connect.NewResponse(&agentv1.ExecuteResponse{Ok: true, Message: "created", ResultJson: mustJSON(res)}), nil
	case req.Msg.GetNetDryRun() != nil:
		return h.execNetDryRun(ctx, req.Msg.GetNetDryRun())
	case req.Msg.GetNetApply() != nil:
		return h.execNetApply(ctx, req.Msg.GetNetApply())
	case req.Msg.GetCtCreate() != nil:
		return h.execCTCreate(ctx, req.Msg.GetCtCreate())
	case req.Msg.GetCtLifecycle() != nil:
		return h.execCTLifecycle(ctx, req.Msg.GetCtLifecycle())
	case req.Msg.GetQemuProtoStart() != nil:
		return h.execQemuProtoStart(ctx, req.Msg.GetQemuProtoStart())
	case req.Msg.GetQemuProtoStop() != nil:
		return h.execQemuProtoStop(ctx, req.Msg.GetQemuProtoStop())
	case req.Msg.GetQemuProtoStatus() != nil:
		return h.execQemuProtoStatus(ctx, req.Msg.GetQemuProtoStatus())
	case req.Msg.GetVmPrepare() != nil:
		return h.execVMPrepare(ctx, req.Msg.GetVmPrepare())
	case req.Msg.GetVmLifecycle() != nil:
		return h.execVMLifecycle(ctx, req.Msg.GetVmLifecycle())
	case req.Msg.GetVmQueryPci() != nil:
		return h.execVMQueryPCI(ctx, req.Msg.GetVmQueryPci())
	case req.Msg.GetVmSnapshot() != nil:
		return h.execVMSnapshot(ctx, req.Msg.GetVmSnapshot())
	case req.Msg.GetBackupCopy() != nil:
		return h.execBackupCopy(ctx, req.Msg.GetBackupCopy())
	case req.Msg.GetHostUpdate() != nil:
		return h.execHostUpdate(ctx, req.Msg.GetHostUpdate())
	case req.Msg.GetGpuAssign() != nil:
		return h.execGPUAssign(ctx, req.Msg.GetGpuAssign())
	case req.Msg.GetZfsPool() != nil:
		return h.execZFSPool(ctx, req.Msg.GetZfsPool())
	case req.Msg.GetVmHotplug() != nil:
		return h.execVMHotplug(ctx, req.Msg.GetVmHotplug())
	case req.Msg.GetVmGuest() != nil:
		return h.execVMGuest(ctx, req.Msg.GetVmGuest())
	case req.Msg.GetOciRuntime() != nil:
		return h.execOCIRuntime(ctx, req.Msg.GetOciRuntime())
	case req.Msg.GetBackupObject() != nil:
		return h.execBackupObject(ctx, req.Msg.GetBackupObject())
	case req.Msg.GetBackupVerify() != nil:
		return h.execBackupVerify(ctx, req.Msg.GetBackupVerify())
	case req.Msg.GetBackupExtract() != nil:
		return h.execBackupExtract(ctx, req.Msg.GetBackupExtract())
	case req.Msg.GetLvmPool() != nil:
		return h.execLVMPool(ctx, req.Msg.GetLvmPool())
	case req.Msg.GetDatastore() != nil:
		return h.execDatastore(ctx, req.Msg.GetDatastore())
	case req.Msg.GetDistributed() != nil:
		return h.execDistributed(ctx, req.Msg.GetDistributed())
	case req.Msg.GetNetAdvanced() != nil:
		return h.execNetAdvanced(ctx, req.Msg.GetNetAdvanced())
	case req.Msg.GetWireguard() != nil:
		return h.execWireGuard(ctx, req.Msg.GetWireguard())
	case req.Msg.GetComputeMigrate() != nil:
		return h.execComputeMigrate(ctx, req.Msg.GetComputeMigrate())
	case req.Msg.GetDiskConvert() != nil:
		return h.execDiskConvert(ctx, req.Msg.GetDiskConvert())
	case req.Msg.GetArchiveExtract() != nil:
		return h.execArchiveExtract(ctx, req.Msg.GetArchiveExtract())
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown execute method"))
	}
}

// Enroll writes durable node identity. Unsupported hosts fail closed.
func (h *Handler) Enroll(ctx context.Context, req *connect.Request[agentv1.EnrollRequest]) (*connect.Response[agentv1.EnrollResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	p, err := h.platform()
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	clusterID := req.Msg.GetClusterId()
	if clusterID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("cluster_id is required"))
	}
	if strings.TrimSpace(req.Msg.GetJoinToken()) != "" {
		existing, existingCluster, err := h.Ident.LoadNode()
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("cluster join is HTTP POST /api/v1/cluster/join; unix Enroll does not consume join tokens"))
		}
		if existingCluster != "" && existingCluster != clusterID {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("node already enrolled in another cluster"))
		}
		cid := existingCluster
		if cid == "" {
			cid = clusterID
		}
		return connect.NewResponse(&agentv1.EnrollResponse{
			ClusterId:    cid,
			NodeId:       existing,
			HostPlatform: protoPlatform(p),
		}), nil
	}
	nodeID := uuid.NewString()
	if existing, existingCluster, err := h.Ident.LoadNode(); err == nil {
		nodeID = existing
		if existingCluster != "" && existingCluster != clusterID {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("node already enrolled in another cluster"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := h.Ident.SaveCluster(clusterID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := h.Ident.SaveNode(nodeID, clusterID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentv1.EnrollResponse{
		ClusterId:    clusterID,
		NodeId:       nodeID,
		HostPlatform: protoPlatform(p),
	}), nil
}

// OpenSession returns the local bind for a remote-worker session.
// Agents also dial the control plane HTTP path. Cluster join remains Phase 30.
func (h *Handler) OpenSession(ctx context.Context, req *connect.Request[agentv1.OpenSessionRequest]) (*connect.Response[agentv1.OpenSessionResponse], error) {
	if err := h.authorize(ctx); err != nil {
		return nil, err
	}
	nodeID, clusterID, _ := h.Ident.LoadNode()
	if req.Msg.GetNodeId() != "" {
		nodeID = req.Msg.GetNodeId()
	}
	if req.Msg.GetClusterId() != "" {
		clusterID = req.Msg.GetClusterId()
	}
	listen := strings.TrimSpace(req.Msg.GetListenAddr())
	if listen == "" {
		listen = strings.TrimSpace(os.Getenv("NODAL_AGENT_TCP_LISTEN"))
	}
	hs := req.Msg.GetHandshakeUnix()
	pub := req.Msg.GetWgPublicKey()
	if req.Msg.GetPeerId() != "" {
		st, err := h.nets().ApplyWireGuard(ctx, ndnet.WGOp{Action: ndnet.ActionWGStatus, PeerID: req.Msg.GetPeerId()})
		if err == nil {
			if hs == 0 {
				hs = st.LastHandshakeUnix
			}
			if pub == "" {
				pub = st.PublicKey
			}
			if listen == "" && st.AddressCIDR != "" {
				listen = strings.Split(st.AddressCIDR, "/")[0] + ":9444"
			}
		}
	}
	return connect.NewResponse(&agentv1.OpenSessionResponse{
		SessionId: uuid.NewString(), Accepted: true, ControlListenAddr: listen,
		Reason: fmt.Sprintf("node=%s cluster=%s pub=%s handshake=%d", nodeID, clusterID, pub, hs),
	}), nil
}

func (h *Handler) authorize(ctx context.Context) error {
	if h.Peer == nil {
		if h.AllowedUID == 0 {
			return nil
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New("peer credentials required"))
	}
	c, err := h.Peer(ctx)
	if err != nil {
		return connect.NewError(connect.CodePermissionDenied, err)
	}
	if c.UID != h.AllowedUID {
		return connect.NewError(connect.CodePermissionDenied, errors.New("unauthorized peer"))
	}
	return nil
}

func (h *Handler) platform() (hostos.Platform, error) {
	if h.Lookup != nil {
		return h.Lookup()
	}
	return hostos.Detect()
}

func protoPlatform(p hostos.Platform) *agentv1.HostPlatform {
	return &agentv1.HostPlatform{
		Id:           p.ID,
		VersionId:    p.VersionID,
		Family:       p.Family,
		Architecture: p.Architecture,
		SupportTier:  p.SupportTier,
		Capabilities: p.Capabilities,
	}
}

// HostPlatformJSON encodes the proto platform for Postgres.
func HostPlatformJSON(p *agentv1.HostPlatform) json.RawMessage {
	b, _ := json.Marshal(p)
	return b
}
