package agentrpc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/inventory"
	"github.com/no-dal/ndl-ce/internal/journald"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/metrics"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/storage"
	"github.com/no-dal/ndl-ce/internal/transport"
	"golang.org/x/net/http2"
	"io"
	"time"
)

// Client is the control-plane southbound client.
type Client struct {
	Socket string
}

// Hello is the idle Observe-first ping.
func (c Client) Hello(ctx context.Context) error {
	_, err := c.rpc().Hello(ctx, connect.NewRequest(&agentv1.HelloRequest{}))
	return err
}

// Enroll performs local enroll.
func (c Client) Enroll(ctx context.Context, clusterID string) (string, json.RawMessage, error) {
	cli := c.rpc()
	res, err := cli.Enroll(ctx, connect.NewRequest(&agentv1.EnrollRequest{ClusterId: clusterID}))
	if err != nil {
		return "", nil, err
	}
	return res.Msg.GetNodeId(), HostPlatformJSON(res.Msg.GetHostPlatform()), nil
}

// Observe asks the agent for a fresh inventory snapshot.
func (c Client) Observe(ctx context.Context) (inventory.Inventory, error) {
	res, err := c.rpc().Observe(ctx, connect.NewRequest(&agentv1.ObserveRequest{}))
	if err != nil {
		return inventory.Inventory{}, err
	}
	return decodeInventory(res.Msg.GetInventoryJson())
}

// GetInventory returns the agent's cached observation.
func (c Client) GetInventory(ctx context.Context) (inventory.Inventory, error) {
	res, err := c.rpc().GetInventory(ctx, connect.NewRequest(&agentv1.GetInventoryRequest{}))
	if err != nil {
		return inventory.Inventory{}, err
	}
	return decodeInventory(res.Msg.GetInventoryJson())
}

// GetMetrics reads agent-side SQLite samples over RPC.
func (c Client) GetMetrics(ctx context.Context, from, to time.Time) (metrics.QueryResult, error) {
	res, err := c.rpc().GetMetrics(ctx, connect.NewRequest(&agentv1.GetMetricsRequest{
		From: from.UTC().Format(time.RFC3339),
		To:   to.UTC().Format(time.RFC3339),
	}))
	if err != nil {
		return metrics.QueryResult{Status: metrics.StatusUnavailable}, err
	}
	var out metrics.QueryResult
	if len(res.Msg.GetSeriesJson()) == 0 {
		return metrics.QueryResult{Status: metrics.Status(res.Msg.GetStatus())}, nil
	}
	if err := json.Unmarshal(res.Msg.GetSeriesJson(), &out); err != nil {
		return metrics.QueryResult{Status: metrics.StatusUnavailable}, err
	}
	return out, nil
}

// GetLogs reads typed journalctl output over RPC.
func (c Client) GetLogs(ctx context.Context, unit string, lines int, since time.Time) (journald.Result, error) {
	req := &agentv1.GetLogsRequest{Unit: unit, Lines: int32(lines)}
	if !since.IsZero() {
		req.Since = since.UTC().Format(time.RFC3339)
	}
	res, err := c.rpc().GetLogs(ctx, connect.NewRequest(req))
	if err != nil {
		return journald.Result{Status: journald.StatusUnavailable, Lines: []string{}}, err
	}
	linesOut := res.Msg.GetLines()
	if linesOut == nil {
		linesOut = []string{}
	}
	return journald.Result{
		Status:  res.Msg.GetStatus(),
		Unit:    res.Msg.GetUnit(),
		Lines:   linesOut,
		Message: res.Msg.GetMessage(),
	}, nil
}

func encodeHints(hints []storage.PoolHint) []*agentv1.StoragePoolHint {
	out := make([]*agentv1.StoragePoolHint, 0, len(hints))
	for _, h := range hints {
		backing, _ := json.Marshal(h.Backing)
		out = append(out, &agentv1.StoragePoolHint{
			PoolId: h.PoolID, BackendType: h.BackendType, RootPath: h.RootPath, BackingJson: backing,
		})
	}
	return out
}

func decodeStorage(raw []byte) (storage.Observation, error) {
	var obs storage.Observation
	if len(raw) == 0 {
		return obs, nil
	}
	if err := json.Unmarshal(raw, &obs); err != nil {
		return obs, err
	}
	return obs, nil
}

// ObserveStorage asks the agent to scrape inventory and known pools.
func (c Client) ObserveStorage(ctx context.Context, hints []storage.PoolHint) (inventory.Inventory, storage.Observation, error) {
	res, err := c.rpc().Observe(ctx, connect.NewRequest(&agentv1.ObserveRequest{StoragePools: encodeHints(hints)}))
	if err != nil {
		return inventory.Inventory{}, storage.Observation{}, err
	}
	inv, err := decodeInventory(res.Msg.GetInventoryJson())
	if err != nil {
		return inventory.Inventory{}, storage.Observation{}, err
	}
	obs, err := decodeStorage(res.Msg.GetStorageJson())
	return inv, obs, err
}

// GetStorage observes known Directory pools.
func (c Client) GetStorage(ctx context.Context, hints []storage.PoolHint) (storage.Observation, error) {
	res, err := c.rpc().GetStorage(ctx, connect.NewRequest(&agentv1.GetStorageRequest{StoragePools: encodeHints(hints)}))
	if err != nil {
		return storage.Observation{}, err
	}
	return decodeStorage(res.Msg.GetStorageJson())
}

// CreateDirectoryPool is a typed Execute method.
func (c Client) CreateDirectoryPool(ctx context.Context, req storage.CreatePoolRequest, existing []string) (storage.CreatePoolResult, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_CreateDirectoryPool{CreateDirectoryPool: &agentv1.CreateDirectoryPool{
			PoolId: req.PoolID, Name: req.Name, RootPath: req.RootPath, Create: req.Create, ExistingRoots: existing,
		}},
	}))
	if err != nil {
		return storage.CreatePoolResult{}, err
	}
	var out storage.CreatePoolResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.CreatePoolResult{}, err
	}
	return out, nil
}

// CreateDirectoryVolume is a typed Execute method.
func (c Client) CreateDirectoryVolume(ctx context.Context, req storage.CreateVolumeRequest, hint storage.PoolHint) (storage.CreateVolumeResult, error) {
	backing, _ := json.Marshal(hint.Backing)
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_CreateDirectoryVolume{CreateDirectoryVolume: &agentv1.CreateDirectoryVolume{
			VolumeId: req.VolumeID, PoolId: req.PoolID, RootPath: req.RootPath, Class: req.Class,
			SizeBytes: req.Size, Format: req.Format, BackingJson: backing,
		}},
	}))
	if err != nil {
		return storage.CreateVolumeResult{}, err
	}
	var out storage.CreateVolumeResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.CreateVolumeResult{}, err
	}
	return out, nil
}

func encodeNetworkHints(hints []ndnet.Hint) []*agentv1.NetworkHint {
	out := make([]*agentv1.NetworkHint, 0, len(hints))
	for _, h := range hints {
		out = append(out, &agentv1.NetworkHint{
			NetworkId: h.NetworkID, Kind: h.Kind, BridgeName: h.BridgeName, UplinkIfname: h.UplinkIfName,
		})
	}
	return out
}

func decodeNetworks(raw []byte) (ndnet.Observation, error) {
	var obs ndnet.Observation
	if len(raw) == 0 {
		return obs, nil
	}
	if err := json.Unmarshal(raw, &obs); err != nil {
		return obs, err
	}
	return obs, nil
}

// GetNetworks observes known network objects.
func (c Client) GetNetworks(ctx context.Context, hints []ndnet.Hint) (ndnet.Observation, error) {
	res, err := c.rpc().GetNetworks(ctx, connect.NewRequest(&agentv1.GetNetworksRequest{Networks: encodeNetworkHints(hints)}))
	if err != nil {
		return ndnet.Observation{}, err
	}
	return decodeNetworks(res.Msg.GetNetworkJson())
}

// DryRunNetwork is a typed Execute method.
func (c Client) DryRunNetwork(ctx context.Context, spec ndnet.Spec) (ndnet.Preview, error) {
	reservations, _ := json.Marshal(spec.Reservations)
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_NetDryRun{NetDryRun: &agentv1.NetDryRun{
			NetworkId: spec.NetworkID, Name: spec.Name, Kind: spec.Kind, Ipv4Cidr: spec.IPv4CIDR,
			Dhcp: spec.DHCP, Dns: spec.DNS, UplinkIfname: spec.UplinkIfName,
			ConfirmIfname: spec.ConfirmIfName, ReservationsJson: reservations,
		}},
	}))
	if err != nil {
		return ndnet.Preview{}, err
	}
	var out ndnet.Preview
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return ndnet.Preview{}, err
	}
	return out, nil
}

// ApplyNetwork is a typed Execute method.
func (c Client) ApplyNetwork(ctx context.Context, spec ndnet.Spec) (ndnet.ApplyResult, error) {
	reservations, _ := json.Marshal(spec.Reservations)
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_NetApply{NetApply: &agentv1.NetApply{
			NetworkId: spec.NetworkID, Name: spec.Name, Kind: spec.Kind, Ipv4Cidr: spec.IPv4CIDR,
			Dhcp: spec.DHCP, Dns: spec.DNS, UplinkIfname: spec.UplinkIfName,
			ConfirmIfname: spec.ConfirmIfName, ReservationsJson: reservations, ArmRollback: spec.ArmRollback,
		}},
	}))
	if err != nil {
		return ndnet.ApplyResult{}, err
	}
	var out ndnet.ApplyResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return ndnet.ApplyResult{}, err
	}
	return out, nil
}

// GetWorkloads observes known system containers.
func (c Client) GetWorkloads(ctx context.Context, hints []lxc.Hint) (lxc.Observation, error) {
	res, err := c.rpc().GetWorkloads(ctx, connect.NewRequest(&agentv1.GetWorkloadsRequest{Workloads: encodeWorkloadHints(hints)}))
	if err != nil {
		return lxc.Observation{}, err
	}
	return decodeWorkloads(res.Msg.GetWorkloadJson())
}

// CreateCT is a typed Execute method.
func (c Client) CreateCT(ctx context.Context, spec lxc.Spec) (lxc.Result, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_CtCreate{CtCreate: &agentv1.CTCreate{
			WorkloadId: spec.WorkloadID, Name: spec.Name, ImagePin: spec.ImagePin,
			Cpus: int32(spec.CPUs), MemoryBytes: spec.MemoryBytes, VolumeId: spec.VolumeID,
			RootfsPath: spec.RootfsPath, NetworkId: spec.NetworkID, BridgeName: spec.BridgeName,
			Mac: spec.MAC, Privileged: spec.Privileged, UidMap: spec.UIDMap, GidMap: spec.GIDMap,
		}},
	}))
	if err != nil {
		return lxc.Result{}, err
	}
	var out lxc.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return lxc.Result{}, err
	}
	return out, nil
}

// LifecycleCT is a typed Execute method.
func (c Client) LifecycleCT(ctx context.Context, req lxc.LifecycleRequest) (lxc.Result, error) {
	res, err := c.rpc().Execute(ctx, connect.NewRequest(&agentv1.ExecuteRequest{
		Method: &agentv1.ExecuteRequest_CtLifecycle{CtLifecycle: &agentv1.CTLifecycle{
			WorkloadId: req.WorkloadID, Action: req.Action, CloneId: req.CloneID,
			CloneVolumeId: req.CloneVolumeID, CloneRootfsPath: req.CloneRootfsPath,
			CloneMac: req.CloneMAC, CloneName: req.CloneName,
		}},
	}))
	if err != nil {
		return lxc.Result{}, err
	}
	var out lxc.Result
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return lxc.Result{}, err
	}
	return out, nil
}

// UploadLibrary streams media to the agent.
func (c Client) UploadLibrary(ctx context.Context, begin storage.BeginUploadRequest, hint storage.PoolHint, r io.Reader, expectedSHA string) (storage.UploadResult, error) {
	stream := c.rpc().UploadLibrary(ctx)
	backing, _ := json.Marshal(hint.Backing)
	if err := stream.Send(&agentv1.UploadLibraryRequest{Payload: &agentv1.UploadLibraryRequest_Begin{
		Begin: &agentv1.UploadLibraryBegin{
			ItemId: begin.ItemID, PoolId: begin.PoolID, RootPath: hint.RootPath, Kind: begin.Kind,
			DisplayName: begin.DisplayName, MaxBytes: begin.MaxBytes, BackingJson: backing,
			RejectSha256: begin.RejectChecksums,
		},
	}}); err != nil {
		return storage.UploadResult{}, err
	}
	buf := make([]byte, 1<<20)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			if serr := stream.Send(&agentv1.UploadLibraryRequest{Payload: &agentv1.UploadLibraryRequest_Chunk{Chunk: chunk}}); serr != nil {
				return storage.UploadResult{}, serr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return storage.UploadResult{}, err
		}
	}
	if err := stream.Send(&agentv1.UploadLibraryRequest{Payload: &agentv1.UploadLibraryRequest_Finish{
		Finish: &agentv1.UploadLibraryFinish{ExpectedSha256: expectedSHA},
	}}); err != nil {
		return storage.UploadResult{}, err
	}
	res, err := stream.CloseAndReceive()
	if err != nil {
		return storage.UploadResult{}, err
	}
	var out storage.UploadResult
	if err := json.Unmarshal(res.Msg.GetResultJson(), &out); err != nil {
		return storage.UploadResult{}, err
	}
	return out, nil
}

func decodeInventory(raw []byte) (inventory.Inventory, error) {
	var inv inventory.Inventory
	if len(raw) == 0 {
		return inv, nil
	}
	if err := json.Unmarshal(raw, &inv); err != nil {
		return inventory.Inventory{}, err
	}
	return inv, nil
}

func (c Client) rpc() agentv1connect.AgentServiceClient {
	path := c.Socket
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
