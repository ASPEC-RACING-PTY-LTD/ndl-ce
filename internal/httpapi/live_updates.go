package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/buildinfo"
	"github.com/no-dal/ndl-ce/internal/liveupdate"
	"github.com/no-dal/ndl-ce/internal/store"
)

// LiveUpdatePaths locate the live update files; zero values use defaults.
type LiveUpdatePaths struct {
	Root    string
	KeysDir string
	Enabled string
}

func (p LiveUpdatePaths) root() string {
	if p.Root != "" {
		return p.Root
	}
	return liveupdate.DefaultRoot
}

func (p LiveUpdatePaths) keysDir() string {
	if p.KeysDir != "" {
		return p.KeysDir
	}
	return liveupdate.DefaultKeysDir
}

func (p LiveUpdatePaths) enabled() bool {
	marker := p.Enabled
	if marker == "" {
		marker = liveupdate.DefaultEnabledMarker
	}
	_, err := os.Stat(marker)
	return err == nil
}

// changesInFlight counts requests that change something, so a live update
// waits for them instead of restarting a process under them.
var changesInFlight atomic.Int64

func countChanges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !strings.HasPrefix(r.URL.Path, "/api/v1/live-updates") && !strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
				changesInFlight.Add(1)
				defer changesInFlight.Add(-1)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// liveBusy lists what a live update must not interrupt.
func (s *Server) liveBusy(ctx context.Context, clusterID string) []string {
	var busy []string
	if n := changesInFlight.Load(); n > 0 {
		busy = append(busy, fmt.Sprintf("%d change(s) in progress", n))
	}
	if runs, err := s.Store.ListBackupRuns(ctx, clusterID); err == nil {
		n := 0
		for _, r := range runs {
			if r.Status == appdb.BackupRunning {
				n++
			}
		}
		if n > 0 {
			busy = append(busy, fmt.Sprintf("%d backup run(s)", n))
		}
	} else {
		busy = append(busy, "backup runs could not be read")
	}
	if s.Backup != nil {
		settings, _ := s.Store.GetBackupWorkspaceSettings(ctx, clusterID)
		if settings == nil {
			settings = appdb.DefaultBackupWorkspaceSettings(clusterID)
		}
		if st, err := s.v2Status(ctx, clusterID, *settings); err == nil && (st.Workspace.CaptureBusy || st.Workspace.CaptureActive > 0) {
			busy = append(busy, "a backup is copying data")
		}
	}
	if n := s.deploymentsBusy(ctx, clusterID); n > 0 {
		busy = append(busy, fmt.Sprintf("%d project deployment(s)", n))
	}
	if mv := s.repoMove.status(s.now()); mv != nil && mv["running"] == true {
		busy = append(busy, "the backup repository is moving")
	}
	return busy
}

func (s *Server) appliedMigrations(ctx context.Context) []string {
	pg, ok := s.Store.(*appdb.Postgres)
	if !ok || pg.DB == nil {
		return nil
	}
	got, err := store.SQLDB{DB: pg.DB}.QueryApplied(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(got))
	for name := range got {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// orEmpty keeps a list a JSON array, never null.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func packageVersion() string {
	out, err := exec.Command("dpkg-query", "-W", "-f=${Version}", "ndl-control").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getLiveUpdates reports live update state. Owners see it in the UI; the
// updater (root on the control socket) reads busy and migrations from it.
func (s *Server) getLiveUpdates(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	paths := s.LiveUpdates
	st, err := liveupdate.ReadState(paths.root())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "live update state is unreadable: "+err.Error())
		return
	}
	queued := []string{}
	if entries, err := os.ReadDir(filepath.Join(paths.root(), "inbox")); err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) == liveupdate.BundleExt {
				queued = append(queued, strings.TrimSuffix(e.Name(), liveupdate.BundleExt))
			}
		}
	}
	keys, _ := liveupdate.LoadKeys(paths.keysDir())
	keyIDs := make([]string, 0, len(keys))
	for id := range keys {
		keyIDs = append(keyIDs, id)
	}
	sort.Strings(keyIDs)
	history := st.History
	if history == nil {
		history = []liveupdate.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":            paths.enabled(),
		"current":            st.Current,
		"pending":            st.Pending,
		"waiting":            st.Waiting,
		"queued":             queued,
		"history":            history,
		"trusted_keys":       keyIDs,
		"busy":               orEmpty(s.liveBusy(r.Context(), p.User.ClusterID)),
		"applied_migrations": orEmpty(s.appliedMigrations(r.Context())),
		"package_version":    packageVersion(),
		"version":            buildinfo.Version,
		"build":              buildinfo.ID(),
	})
}

// postLiveUpdateBundle queues a signed bundle for the updater. The bundle is
// verified here so a bad upload is refused at once; the updater, which runs
// as root, verifies it again before applying it.
func (s *Server) postLiveUpdateBundle(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	paths := s.LiveUpdates
	inbox := filepath.Join(paths.root(), "inbox")
	if _, err := os.Stat(inbox); err != nil {
		writeErr(w, http.StatusConflict, "live updates are not set up on this host; run ndl-live enable as root")
		return
	}
	keys, err := liveupdate.LoadKeys(paths.keysDir())
	if err != nil || len(keys) == 0 {
		writeErr(w, http.StatusConflict, "this host trusts no update signing key; add one with ndl-live trust")
		return
	}
	tmp, err := os.CreateTemp(inbox, ".upload-*")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	m, err := liveupdate.Inspect(io.TeeReader(http.MaxBytesReader(w, r.Body, 600<<20), tmp), keys)
	_ = tmp.Close()
	if err != nil {
		s.audit(r, p.User.ClusterID, p.User.ID, "live_update.upload", "refused", err.Error())
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Rename(tmp.Name(), filepath.Join(inbox, m.ID+liveupdate.BundleExt)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "live_update.upload", "ok", m.ID+" "+m.Version)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": m.ID, "version": m.Version, "components": m.Components(), "enabled": paths.enabled()})
}

// postLiveUpdateRollback asks the updater to undo the newest live update.
func (s *Server) postLiveUpdateRollback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	inbox := filepath.Join(s.LiveUpdates.root(), "inbox")
	req := filepath.Join(inbox, liveupdate.RollbackRequest)
	if err := os.WriteFile(req, []byte(time.Now().UTC().Format(time.RFC3339)+" "+p.User.Username+"\n"), 0o640); err != nil {
		writeErr(w, http.StatusConflict, "live updates are not set up on this host: "+err.Error())
		return
	}
	s.audit(r, p.User.ClusterID, p.User.ID, "live_update.rollback", "ok", "")
	writeJSON(w, http.StatusAccepted, map[string]any{"requested": true})
}
