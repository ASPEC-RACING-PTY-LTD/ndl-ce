package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// destAgentOverride stands in for a Ready dest-listen worker in tests.
type destAgentOverride struct {
	listen    string
	workloads WorkloadRPC
	backup    BackupRPC
	pulled    []migrate.VolumeCopy
}

// destAgentClient returns a TCP southbound client for a worker dest node.
// It never falls back to the control unix agent.
func (s *Server) destAgentClient(ctx context.Context, dest *appdb.Node) (agentrpc.Client, bool) {
	if dest == nil {
		return agentrpc.Client{}, false
	}
	if s.destEligibleLocal(ctx, dest) {
		return agentrpc.Client{}, false
	}
	if s.destOverride != nil {
		listen := strings.TrimSpace(s.destOverride.listen)
		if listen == "" {
			listen = "10.64.8.2:9444"
		}
		return agentrpc.Client{TCPAddr: listen}, true
	}
	remote, err := s.Store.GetRemoteNode(ctx, dest.ClusterID, dest.ID)
	if err != nil || remote == nil {
		remotes, lerr := s.Store.ListRemoteNodes(ctx, dest.ClusterID)
		if lerr != nil {
			return agentrpc.Client{}, false
		}
		for i := range remotes {
			r := remotes[i]
			if r.ID == dest.ID || (dest.Name != "" && r.Name == dest.Name) {
				remote = &r
				break
			}
		}
	}
	if remote == nil {
		return agentrpc.Client{}, false
	}
	listen := strings.TrimSpace(remote.ListenAddr)
	if listen == "" || ndnet.ValidListenAddr(listen) != nil {
		return agentrpc.Client{}, false
	}
	// dest-listen advertised TCP is dest-agent connectivity for a joined
	// worker. WireGuard handshake freshness is pre-join overlay status; it
	// must not hide a registered LAN dest agent.
	return agentrpc.Client{TCPAddr: listen}, true
}

func destGuestIP() lxc.IPConfig {
	// Dest isolated-nat DHCP is node-local and may not lease before start
	// returns. Dest-listen migrate/restore wait on init pid, not guest IPv4.
	return lxc.IPConfig{IPv4Mode: lxc.IPModeDisabled, IPv6Mode: lxc.IPModeDisabled}
}

func (s *Server) ownerNodeID(wl appdb.Workload) string {
	return firstNonEmpty(wl.NodeID, wl.OwnerNodeID, wl.DesiredNodeID)
}

// workloadsOn returns the agent that owns a node's guests. A worker never
// falls back to the control unix agent.
func (s *Server) workloadsOn(ctx context.Context, clusterID, nodeID string) (WorkloadRPC, bool) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" || s.applyLocal(ctx, clusterID, nodeID) {
		if s.Workloads == nil {
			return nil, false
		}
		return s.Workloads, true
	}
	if s.Store == nil {
		return nil, false
	}
	node, err := s.Store.GetNodeByID(ctx, clusterID, nodeID)
	if err != nil || node == nil {
		return nil, false
	}
	return s.destWorkloads(ctx, node)
}

func (s *Server) ensureDestIsolatedNet(ctx context.Context, dest *appdb.Node, netw *appdb.Network) (string, error) {
	if dest == nil || netw == nil {
		return "", nil
	}
	if !ndnet.Isolated(netw.Kind) {
		return netw.BridgeName, nil
	}
	if s.destOverride != nil && !s.destEligibleLocal(ctx, dest) {
		return netw.BridgeName, nil
	}
	spec := ndnet.Spec{
		NetworkID: netw.ID, Name: netw.Name, Kind: netw.Kind,
		IPv4CIDR: firstNonEmpty(netw.IPv4CIDR, ndnet.DefaultIPv4),
		DHCP:     true,
		DNS:      true,
	}
	var (
		res ndnet.ApplyResult
		err error
	)
	if s.applyLocal(ctx, dest.ClusterID, dest.ID) {
		if s.Network == nil {
			return netw.BridgeName, nil
		}
		res, err = s.Network.ApplyNetwork(ctx, spec)
	} else {
		c, ok := s.destAgentClient(ctx, dest)
		if !ok {
			return "", fmt.Errorf("dest network agent is not connected")
		}
		res, err = c.ApplyNetwork(ctx, spec)
	}
	if err != nil {
		return "", err
	}
	if name := strings.TrimSpace(res.BridgeName); name != "" {
		return name, nil
	}
	return netw.BridgeName, nil
}

func (s *Server) destWorkloads(ctx context.Context, dest *appdb.Node) (WorkloadRPC, bool) {
	if dest == nil {
		return nil, false
	}
	if s.destOverride != nil && !s.destEligibleLocal(ctx, dest) && s.destOverride.workloads != nil {
		return s.destOverride.workloads, true
	}
	if s.destEligibleLocal(ctx, dest) {
		if s.Workloads == nil {
			return nil, false
		}
		return s.Workloads, true
	}
	c, ok := s.destAgentClient(ctx, dest)
	if !ok {
		return nil, false
	}
	return c, true
}

func (s *Server) destVM(ctx context.Context, dest *appdb.Node) (VMRPC, bool) {
	if dest == nil {
		return nil, false
	}
	if s.destEligibleLocal(ctx, dest) {
		if s.VM == nil {
			return nil, false
		}
		return s.VM, true
	}
	c, ok := s.destAgentClient(ctx, dest)
	if !ok {
		return nil, false
	}
	return AdaptVM(c), true
}

func (s *Server) destBackup(ctx context.Context, dest *appdb.Node) (BackupRPC, bool) {
	if dest == nil {
		return nil, false
	}
	if s.destOverride != nil && !s.destEligibleLocal(ctx, dest) && s.destOverride.backup != nil {
		return s.destOverride.backup, true
	}
	if s.destEligibleLocal(ctx, dest) {
		if s.Backup == nil {
			return nil, false
		}
		return s.Backup, true
	}
	c, ok := s.destAgentClient(ctx, dest)
	if !ok {
		return nil, false
	}
	return AdaptBackup(c), true
}

func (s *Server) destPullVolume(ctx context.Context, dest *appdb.Node, vol migrate.VolumeCopy) error {
	if s.destOverride != nil && dest != nil && !s.destEligibleLocal(ctx, dest) {
		s.destOverride.pulled = append(s.destOverride.pulled, vol)
		return nil
	}
	c, ok := s.destAgentClient(ctx, dest)
	if !ok {
		return fmt.Errorf("dest agent is not connected")
	}
	return c.PullVolume(ctx, vol)
}

// pullDestRestoredTree archives a control-local restored tree and dest-pulls it.
// Dest must not restore against a control-localhost S3/MinIO target.
func (s *Server) pullDestRestoredTree(ctx context.Context, dest *appdb.Node, destPath, localSrc string) error {
	if dest == nil {
		return fmt.Errorf("dest agent is not connected")
	}
	destBK, ok := s.destBackup(ctx, dest)
	if !ok {
		return fmt.Errorf("dest backup agent is not connected")
	}
	if _, err := destBK.CopyBackup(ctx, qemu.BackupMkdir, "", destPath); err != nil {
		return err
	}
	pack := strings.TrimSpace(localSrc)
	cleanup := func() {}
	info, err := os.Stat(pack)
	if err != nil || info.IsDir() || !looksLikeServedRestorePack(pack) {
		if s.Backup == nil {
			return errUnavailable("backup agent is unavailable")
		}
		// Unique dir under control-owned tmp. A shared restore-packs/ created
		// as root:root 0750 (tests or agent mkdir) is invisible to ndl-control.
		packDir := filepath.Join("/var/lib/ndl/control", "tmp", "rp-"+uuid.NewString())
		if err := os.MkdirAll(packDir, 0o750); err != nil {
			return err
		}
		pack = filepath.Join(packDir, "pack.tar.gz")
		archived, aerr := s.Backup.CopyBackup(ctx, qemu.BackupArchive, localSrc, pack)
		if aerr != nil {
			return aerr
		}
		pack = resolvedMigratePack(pack, archived)
		cleanup = func() {
			_ = os.Remove(pack)
			_ = os.RemoveAll(packDir)
		}
	}
	defer cleanup()
	if _, err := os.Stat(pack); err != nil {
		return fmt.Errorf("restore pack missing after archive: %w", err)
	}
	c, ok := s.destAgentClient(ctx, dest)
	if !ok {
		return fmt.Errorf("dest agent is not connected")
	}
	pullURL, stopServe, err := serveMigratePack(pack, destAdvertiseHint(c))
	if err != nil {
		return err
	}
	defer stopServe()
	return s.destPullVolume(ctx, dest, migrate.VolumeCopy{
		VolumeID: filepath.Base(destPath), SourcePath: pullURL, DestPath: destPath,
	})
}

func looksLikeServedRestorePack(p string) bool {
	p = strings.ToLower(p)
	return strings.Contains(p, ".tar") || strings.HasSuffix(p, ".zst") || strings.HasSuffix(p, ".gz")
}

func (s *Server) destRuntime(ctx context.Context, dest *appdb.Node) (migrate.Runtime, bool) {
	if dest == nil {
		return nil, false
	}
	if s.destEligibleLocal(ctx, dest) {
		if s.Migrate == nil {
			return nil, false
		}
		if _, ok := s.Migrate.(migrateUnavailable); ok {
			return nil, false
		}
		return s.Migrate, true
	}
	c, ok := s.destAgentClient(ctx, dest)
	if ok {
		dst := AdaptMigrate(c)
		if _, unavail := dst.(migrateUnavailable); !unavail {
			src := s.Migrate
			if src == nil {
				src = migrateUnavailable{}
			}
			return &splitMigrate{src: src, dst: dst}, true
		}
	}
	// Test fixtures (migrate.Fake) are not LocalAgentOnly. Appliance
	// unix agents are. Remote dest must not start incoming on unix.
	if s.Migrate == nil {
		return nil, false
	}
	if _, unavail := s.Migrate.(migrateUnavailable); unavail {
		return nil, false
	}
	if lo, ok := s.Migrate.(interface{ LocalAgentOnly() bool }); ok && lo.LocalAgentOnly() {
		return nil, false
	}
	return s.Migrate, true
}

type splitMigrate struct {
	src migrate.Runtime
	dst migrate.Runtime
}

func (m *splitMigrate) LocalAgentOnly() bool { return false }

func (m *splitMigrate) PrepareDest(ctx context.Context, req migrate.Request) error {
	return m.dst.PrepareDest(ctx, req)
}

func (m *splitMigrate) CopyVolume(ctx context.Context, vol migrate.VolumeCopy) error {
	if c, ok := m.dst.(interface {
		PullVolume(context.Context, migrate.VolumeCopy) error
	}); ok && strings.HasPrefix(vol.SourcePath, "http") {
		return c.PullVolume(ctx, vol)
	}
	return m.dst.CopyVolume(ctx, vol)
}

func (m *splitMigrate) StopSource(ctx context.Context, id string) error {
	return m.src.StopSource(ctx, id)
}

func (m *splitMigrate) StartDest(ctx context.Context, id string) error {
	return m.dst.StartDest(ctx, id)
}

func (m *splitMigrate) LiveMigrate(ctx context.Context, id string) error {
	return m.src.LiveMigrate(ctx, id)
}

func (m *splitMigrate) AbortDest(ctx context.Context, id string) error {
	return m.dst.AbortDest(ctx, id)
}

func (m *splitMigrate) SourceRunning(ctx context.Context, id string) bool {
	return m.src.SourceRunning(ctx, id)
}

func (m *splitMigrate) LiveArgv(ctx context.Context, id string) (source, dest []string) {
	if ar, ok := m.src.(migrate.ABIRuntime); ok {
		source, _ = ar.LiveArgv(ctx, id)
	}
	if ar, ok := m.dst.(migrate.ABIRuntime); ok {
		_, dest = ar.LiveArgv(ctx, id)
	}
	return source, dest
}

// setDestListen records a worker southbound listen address so dest-agent
// migrate/restore can dial TCP. It never binds dest incoming on the control unix agent.
func (s *Server) setDestListen(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.ClusterJoin)
	if err != nil {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	node, err := s.Store.GetNodeByID(r.Context(), p.User.ClusterID, id)
	if err != nil || node == nil || node.RevokedAt != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	if s.destEligibleLocal(r.Context(), node) {
		writeErr(w, http.StatusUnprocessableEntity, "dest listen is for a worker node, not the local control agent")
		return
	}
	var req struct {
		ListenAddr string `json:"listen_addr"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	listen := strings.TrimSpace(req.ListenAddr)
	if err := ndnet.ValidListenAddr(listen); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	now := s.now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	remote, err := s.Store.GetRemoteNode(r.Context(), p.User.ClusterID, node.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if remote == nil {
		remote = &appdb.RemoteNode{
			ID: node.ID, ClusterID: p.User.ClusterID, Name: firstNonEmpty(node.Name, node.Hostname),
		}
		if err := s.Store.CreateRemoteNode(r.Context(), *remote); err != nil {
			writeErr(w, http.StatusConflict, "could not record dest listen")
			return
		}
	}
	remote.ListenAddr = listen
	remote.Status = ndnet.NodeReady
	remote.Reason = ""
	remote.LastSeenAt = &now
	remote.LastHandshakeUnix = now.Unix()
	if err := s.Store.UpdateRemoteNodeSession(r.Context(), *remote); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record dest listen")
		return
	}
	got, gerr := s.Store.GetRemoteNode(r.Context(), p.User.ClusterID, node.ID)
	if gerr != nil || got == nil || got.ListenAddr != listen || got.Status != ndnet.NodeReady {
		writeErr(w, http.StatusInternalServerError, "could not record dest listen")
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "cluster.dest.listen", "ok", node.ID)
	writeJSON(w, http.StatusOK, remoteNodeJSON(*got, now))
}
