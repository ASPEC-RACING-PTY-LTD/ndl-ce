package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/agentrpc"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/migrate"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"github.com/no-dal/ndl-ce/internal/rbac"
)

// destAgentClient returns a TCP southbound client for a worker dest node.
// It never falls back to the control unix agent.
func (s *Server) destAgentClient(ctx context.Context, dest *appdb.Node) (agentrpc.Client, bool) {
	if dest == nil {
		return agentrpc.Client{}, false
	}
	if s.destEligibleLocal(ctx, dest) {
		return agentrpc.Client{}, false
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
	last := int64(0)
	if remote.LastSeenAt != nil && !remote.LastSeenAt.IsZero() {
		last = remote.LastSeenAt.Unix()
	}
	now := s.now().Unix()
	if !ndnet.RemoteReady(remote.LastHandshakeUnix, last, now) && remote.Status != ndnet.NodeReady {
		return agentrpc.Client{}, false
	}
	return agentrpc.Client{TCPAddr: listen}, true
}

func (s *Server) destWorkloads(ctx context.Context, dest *appdb.Node) (WorkloadRPC, bool) {
	if dest == nil {
		return nil, false
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
