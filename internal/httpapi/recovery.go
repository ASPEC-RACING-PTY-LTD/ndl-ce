package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/platformbackup"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/secutil"
	"github.com/no-dal/ndl-ce/internal/store"
	"github.com/no-dal/ndl-ce/migrations"
)

// Recovery rebuilds a lost NDL-CE on a fresh, unclaimed host from its
// platform backup: connect the bucket with new credentials, unlock with an
// Owner's password, map pools and networks onto this host, restore. It is
// offered only before setup is claimed, needs the setup token, and once a
// restore starts, setup and other recoveries are locked.

// RecoveryHooks replace parts of recovery in tests.
type RecoveryHooks struct {
	// Restore replaces loading the database dump. It returns the store of
	// the restored database to finish recovery with.
	Restore func(ctx context.Context, dump []byte) (appdb.Store, func(), error)
	// Exit replaces restarting the control plane after recovery.
	Exit func()
}

type recoveryRuntime struct {
	mu       sync.Mutex
	sessions map[string]*recoverySession
	// locked is set once a restore starts; setup and recovery stay closed.
	locked bool
}

type recoverySession struct {
	id      string
	created time.Time
	spec    backuphost.TargetSpec
	ptr     *platformbackup.Pointer
	// Set once unlocked.
	owner    string
	snapshot *platformbackup.SnapshotRef
	manifest *platformbackup.Manifest
	files    map[string][]byte
}

const recoverySessionTTL = time.Hour

var accountIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type recoverConnectRequest struct {
	Token           string `json:"token"`
	Kind            string `json:"kind"`
	AccountID       string `json:"account_id"`
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	APIToken        string `json:"api_token"`
}

func (s *Server) recoveryOpen(ctx context.Context) (bool, error) {
	open, err := s.setupOpen(ctx)
	if err != nil || !open {
		return false, err
	}
	if c, _ := s.Store.GetCluster(ctx); c != nil {
		if n, err := s.Store.CountAdmins(ctx, c.ID); err != nil || n > 0 {
			return false, err
		}
	}
	return true, nil
}

func (s *Server) recoveryLocked() bool {
	s.recovery.mu.Lock()
	defer s.recovery.mu.Unlock()
	return s.recovery.locked
}

func (s *Server) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	open, err := s.recoveryOpen(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": open && !s.recoveryLocked(), "locked": s.recoveryLocked(), "schema_level": schemaLevel(),
	})
}

// recoveryGate checks the setup token and that recovery is still possible.
func (s *Server) recoveryGate(w http.ResponseWriter, r *http.Request, token string) bool {
	key := "recover|" + clientHost(r)
	if err := s.lock().Check(key, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return false
	}
	open, err := s.recoveryOpen(r.Context())
	if err != nil || !open || s.recoveryLocked() {
		writeErr(w, http.StatusConflict, "recovery is only possible on a host that has not been set up")
		return false
	}
	st, err := s.Store.GetSetup(r.Context())
	if err != nil || st == nil || !secutil.EqualHash(st.TokenHash, secutil.HashSHA256(token)) {
		s.lock().Fail(key, s.now())
		writeErr(w, http.StatusUnauthorized, "invalid setup token")
		return false
	}
	return true
}

func clientHost(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	return host
}

// recoverySpec builds the target from the new credentials. A Cloudflare API
// token is turned into R2 S3 credentials: its id is the access key and the
// SHA-256 of its value the secret.
func (s *Server) recoverySpec(ctx context.Context, req recoverConnectRequest) (backuphost.TargetSpec, error) {
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = appdb.BackupR2
	}
	spec := backuphost.TargetSpec{Kind: kind, Bucket: strings.TrimSpace(req.Bucket)}
	if spec.Bucket == "" || strings.ContainsAny(spec.Bucket, "/ ") {
		return spec, errBadRequest("a bucket name is required")
	}
	if kind == appdb.BackupR2 {
		acct := strings.ToLower(strings.TrimSpace(req.AccountID))
		if !accountIDPattern.MatchString(acct) {
			return spec, errBadRequest("the Cloudflare account id is 32 hexadecimal characters")
		}
		spec.Endpoint = "https://" + acct + ".r2.cloudflarestorage.com"
		spec.Region = "auto"
	} else {
		spec.Endpoint = strings.TrimSpace(req.Endpoint)
		if err := validateObjectTarget(kind, spec.Endpoint, spec.Bucket); err != nil {
			return spec, err
		}
	}
	spec.AccessKey = strings.TrimSpace(req.AccessKeyID)
	spec.SecretKey = strings.TrimSpace(req.SecretAccessKey)
	if tok := strings.TrimSpace(req.APIToken); tok != "" && kind == appdb.BackupR2 {
		st, err := s.cfNewClient(tok).VerifyToken(ctx, strings.ToLower(strings.TrimSpace(req.AccountID)))
		if err != nil {
			return spec, errUnprocessable("Cloudflare did not accept the API token: " + err.Error())
		}
		if st.ID == "" {
			return spec, errUnprocessable("Cloudflare did not return the token's id")
		}
		sum := sha256.Sum256([]byte(tok))
		spec.AccessKey, spec.SecretKey = st.ID, hex.EncodeToString(sum[:])
	}
	if spec.AccessKey == "" || spec.SecretKey == "" {
		return spec, errBadRequest("an API token, or an access key id and secret, is required")
	}
	return spec, nil
}

func (s *Server) recoveryStore(ctx context.Context, spec backuphost.TargetSpec) (platformbackup.Store, func(), error) {
	if s.Platform.Store != nil {
		return s.Platform.Store(appdb.BackupTarget{Kind: spec.Kind, Bucket: spec.Bucket, Endpoint: spec.Endpoint}), func() {}, nil
	}
	if s.Backup == nil {
		return nil, nil, errUnavailable("backup agent is unavailable")
	}
	dir, err := os.MkdirTemp("", "ndl-recover-")
	if err != nil {
		return nil, nil, err
	}
	return &agentPlatformStore{s: s, spec: spec, dir: dir}, func() { _ = os.RemoveAll(dir) }, nil
}

func (s *Server) recoverySession(id string) *recoverySession {
	s.recovery.mu.Lock()
	defer s.recovery.mu.Unlock()
	for k, sess := range s.recovery.sessions {
		if s.now().Sub(sess.created) > recoverySessionTTL {
			delete(s.recovery.sessions, k)
		}
	}
	return s.recovery.sessions[id]
}

// recoverConnect finds the platform backup in a bucket.
func (s *Server) recoverConnect(w http.ResponseWriter, r *http.Request) {
	var req recoverConnectRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.recoveryGate(w, r, req.Token) {
		return
	}
	ctx := r.Context()
	spec, err := s.recoverySpec(ctx, req)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	st, done, err := s.recoveryStore(ctx, spec)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	defer done()
	ptr, err := platformbackup.Locate(ctx, st)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "could not read the bucket: "+remoteErrText(err))
		return
	}
	id := randomID()
	s.recovery.mu.Lock()
	if s.recovery.sessions == nil {
		s.recovery.sessions = map[string]*recoverySession{}
	}
	s.recovery.sessions[id] = &recoverySession{id: id, created: s.now(), spec: spec, ptr: ptr}
	s.recovery.mu.Unlock()
	latest := ptr.Latest("")
	out := map[string]any{"session_id": id, "snapshots": len(ptr.Snapshots), "local_schema_level": schemaLevel()}
	if latest != nil {
		out["newest"] = latest.CreatedAt
		out["schema_level"] = latest.SchemaLevel
		out["workloads"] = latest.Workloads
	}
	writeJSON(w, http.StatusOK, out)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// recoverUnlock opens the newest snapshot with an Owner's password.
func (s *Server) recoverUnlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.recoveryGate(w, r, req.Token) {
		return
	}
	sess := s.recoverySession(req.SessionID)
	if sess == nil {
		writeErr(w, http.StatusNotFound, "connect the bucket again")
		return
	}
	ctx := r.Context()
	st, done, err := s.recoveryStore(ctx, sess.spec)
	if err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	defer done()
	lockKey := "recover-unlock|" + clientHost(r)
	if err := s.lock().Check(lockKey, s.now()); err != nil {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	var (
		key  []byte
		snap *platformbackup.SnapshotRef
		last error = platformbackup.ErrLocked
	)
	// The current envelope first, then the kept fallback; the newest
	// snapshot sealed with the key that opens.
	for _, envKey := range []string{sess.ptr.Envelope, sess.ptr.PreviousEnvelope} {
		if envKey == "" {
			continue
		}
		env, err := platformbackup.LoadEnvelope(ctx, st, envKey)
		if err != nil {
			last = err
			continue
		}
		k, err := env.Unlock(req.Username, req.Password)
		if err != nil {
			continue
		}
		if ref := sess.ptr.Latest(env.KeyID); ref != nil {
			key, snap = k, ref
			break
		}
		// Listed without a pointer: snapshots carry their key id inside.
		for i := range sess.ptr.Snapshots {
			ref := sess.ptr.Snapshots[i]
			if ref.KeyID == "" {
				key, snap = k, &ref
			}
		}
		if snap != nil {
			break
		}
	}
	if key == nil || snap == nil {
		s.lock().Fail(lockKey, s.now())
		if errors.Is(last, platformbackup.ErrLocked) {
			writeErr(w, http.StatusUnauthorized, "that Owner username and password do not unlock this backup")
			return
		}
		writeErr(w, http.StatusUnprocessableEntity, last.Error())
		return
	}
	s.lock().Success(lockKey)
	plain, err := platformbackup.FetchSnapshot(ctx, st, key, *snap)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "the backup could not be read: "+err.Error())
		return
	}
	man, files, err := platformbackup.ReadArchive(plain)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "the backup could not be read: "+err.Error())
		return
	}
	if man.SchemaLevel > schemaLevel() {
		writeErr(w, http.StatusConflict, fmt.Sprintf("this NDL-CE is older than the backup (schema %d, the backup needs %d or newer); install the same or a newer version and try again", schemaLevel(), man.SchemaLevel))
		return
	}
	if _, ok := files[platformbackup.DatabaseFile]; !ok {
		writeErr(w, http.StatusUnprocessableEntity, "the backup has no database")
		return
	}
	s.recovery.mu.Lock()
	sess.owner, sess.snapshot, sess.manifest, sess.files = req.Username, snap, &man, files
	s.recovery.mu.Unlock()
	writeJSON(w, http.StatusOK, recoveryPlanJSON(man, sess.spec.Bucket))
}

func recoveryPlanJSON(m platformbackup.Manifest, bucket string) map[string]any {
	workloads := make([]map[string]any, 0, len(m.Workloads))
	for _, w := range m.Workloads {
		remote := 0
		for _, p := range w.Points {
			if p.RemoteState == "protected" {
				remote++
			}
		}
		workloads = append(workloads, map[string]any{"id": w.ID, "name": w.Name, "kind": w.Kind, "protected": w.Protected, "offsite_points": remote})
	}
	targets := make([]map[string]any, 0, len(m.Targets))
	for _, t := range m.Targets {
		targets = append(targets, map[string]any{"id": t.ID, "name": t.Name, "kind": t.Kind, "bucket": t.Bucket, "this_bucket": t.Bucket == bucket})
	}
	return map[string]any{
		"created_at": m.CreatedAt, "hostname": m.Hostname, "schema_level": m.SchemaLevel, "local_schema_level": schemaLevel(),
		"workloads": workloads, "pools": m.Pools, "networks": m.Networks, "nodes": m.Nodes, "targets": targets,
	}
}

// recoveryState is what recovery leaves for the restore of workloads.
type recoveryState struct {
	At        time.Time `json:"at"`
	Owner     string    `json:"owner"`
	Hostname  string    `json:"hostname"`
	Snapshot  string    `json:"snapshot"`
	CreatedAt time.Time `json:"snapshot_created_at"`
	TargetID  string    `json:"target_id"`
	// Superseded are targets whose old credentials were replaced by the new
	// connection.
	Superseded         []string `json:"superseded,omitempty"`
	ConnectionVerified bool     `json:"connection_verified"`
	ConnectionError    string   `json:"connection_error,omitempty"`
	// Power is each workload's (and "gs:" game server's) desired power
	// before recovery held it stopped.
	Power map[string]string `json:"power,omitempty"`
	// Restored are the workloads whose data was restored.
	Restored map[string]time.Time `json:"restored,omitempty"`
}

// recoverRestore restores the database and configuration, then moves the
// cluster onto this host.
func (s *Server) recoverRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token     string            `json:"token"`
		SessionID string            `json:"session_id"`
		Pools     map[string]string `json:"pools"`
		Networks  map[string]string `json:"networks"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.recoveryGate(w, r, req.Token) {
		return
	}
	sess := s.recoverySession(req.SessionID)
	if sess == nil || sess.manifest == nil {
		writeErr(w, http.StatusNotFound, "unlock the backup first")
		return
	}
	for _, p := range req.Pools {
		if p != "" && !filepath.IsAbs(p) {
			writeErr(w, http.StatusBadRequest, "pool paths must be absolute")
			return
		}
	}
	s.recovery.mu.Lock()
	if s.recovery.locked {
		s.recovery.mu.Unlock()
		writeErr(w, http.StatusConflict, "a recovery is already running")
		return
	}
	s.recovery.locked = true
	s.recovery.mu.Unlock()
	ctx := context.WithoutCancel(r.Context())
	out, err := s.runRecovery(ctx, sess, req.Pools, req.Networks)
	if err != nil {
		// Nothing was committed: the database load is one transaction.
		if !out.committed {
			s.recovery.mu.Lock()
			s.recovery.locked = false
			s.recovery.mu.Unlock()
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "restored": out.committed})
		return
	}
	writeJSON(w, http.StatusOK, out.json)
	// A fresh process picks up the restored database.
	go func() {
		time.Sleep(time.Second)
		if s.Recovery.Exit != nil {
			s.Recovery.Exit()
			return
		}
		os.Exit(75)
	}()
}

type recoveryOutcome struct {
	committed bool
	json      map[string]any
}

func (s *Server) runRecovery(ctx context.Context, sess *recoverySession, pools, networks map[string]string) (recoveryOutcome, error) {
	var out recoveryOutcome
	restore := s.Recovery.Restore
	if restore == nil {
		restore = s.restoreDatabase
	}
	st, done, err := restore(ctx, sess.files[platformbackup.DatabaseFile])
	if err != nil {
		return out, fmt.Errorf("the database could not be restored (nothing was changed): %w", err)
	}
	defer done()
	out.committed = true
	cluster, err := st.GetCluster(ctx)
	if err != nil || cluster == nil {
		return out, fmt.Errorf("the restored database has no cluster")
	}
	// This host joins the recovered cluster with a fresh identity.
	host, _ := os.Hostname()
	node := appdb.Node{ID: randomUUID(), ClusterID: cluster.ID, Name: "local", Hostname: host, Role: "control"}
	if s.Agent != nil {
		id, plat, err := s.Agent.Enroll(ctx, cluster.ID)
		if err != nil {
			return out, fmt.Errorf("this host could not join the recovered cluster: %w", err)
		}
		node.ID, node.HostPlatform = id, plat
	}
	poolPaths := map[string]string{}
	for _, p := range sess.manifest.Pools {
		if v := strings.TrimSpace(pools[p.ID]); v != "" {
			poolPaths[p.ID] = v
		} else if p.Path != "" {
			poolPaths[p.ID] = p.Path
		}
	}
	bridges := map[string]string{}
	for _, n := range sess.manifest.Networks {
		if v := strings.TrimSpace(networks[n.ID]); v != "" {
			bridges[n.ID] = v
		} else if n.Bridge != "" {
			bridges[n.ID] = n.Bridge
		}
	}
	power, err := st.RemapRecoveredHost(ctx, cluster.ID, appdb.HostRemap{Node: node, Pools: poolPaths, Networks: bridges})
	if err != nil {
		return out, fmt.Errorf("the restored cluster could not be moved onto this host: %w", err)
	}
	rec := recoveryState{
		At: s.now().UTC(), Owner: sess.owner, Hostname: sess.manifest.Hostname,
		Snapshot: sess.snapshot.Key, CreatedAt: sess.manifest.CreatedAt, Power: power,
	}
	// The new connection wins over the restored, revoked credentials.
	targetID, superseded, err := s.adoptRecoveryTarget(ctx, st, cluster.ID, sess.spec)
	if err != nil {
		return out, fmt.Errorf("the new bucket connection could not be saved: %w", err)
	}
	rec.TargetID, rec.Superseded = targetID, superseded
	// Test it, as saved, before any workload is restored.
	if err := s.testRecoveryTarget(ctx, st, cluster.ID, targetID, sess.spec); err != nil {
		rec.ConnectionError = remoteErrText(err)
	} else {
		rec.ConnectionVerified = true
	}
	if key, ok := sess.files[platformbackup.RepoKeyFile]; ok && s.Backup != nil {
		raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionKeyImport, ImportKey: string(key)})
		if _, err := s.Backup.CopyBackup(ctx, qemu.BackupV2KeyImport, "", string(raw)); err != nil {
			return out, fmt.Errorf("the backup repository key could not be installed: %w", err)
		}
	}
	pst := platformState{Wraps: map[string]platformbackup.Wrap{}}
	if raw, err := st.GetPlatformBackupState(ctx, cluster.ID); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &pst)
	}
	pst.TargetID = targetID
	pst.Envelope.Dirty = true
	pst.Snapshot.LastAttemptAt = nil
	pst.Recovery = &rec
	raw, _ := json.Marshal(pst)
	if err := st.PutPlatformBackupState(ctx, cluster.ID, string(raw)); err != nil {
		return out, err
	}
	payload, _ := json.Marshal(map[string]any{"kind": "platform.recovered", "from": rec.Hostname, "snapshot_created_at": rec.CreatedAt, "connection_verified": rec.ConnectionVerified})
	_ = st.InsertEvent(ctx, appdb.Event{ID: randomUUID(), ClusterID: cluster.ID, Type: "platform.recovered", Payload: payload, CreatedAt: s.now()})
	out.json = map[string]any{
		"restored": true, "cluster_id": cluster.ID, "target_id": targetID, "superseded": superseded,
		"connection_verified": rec.ConnectionVerified, "connection_error": rec.ConnectionError,
		"workloads": len(sess.manifest.Workloads), "restart": true,
	}
	return out, nil
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// adoptRecoveryTarget points the restored target of the recovered bucket at
// the new credentials, or adds one. Restored targets on the same bucket are
// marked superseded: their credentials were revoked with the old host.
func (s *Server) adoptRecoveryTarget(ctx context.Context, st appdb.Store, clusterID string, spec backuphost.TargetSpec) (string, []string, error) {
	targets, err := st.ListBackupTargets(ctx, clusterID)
	if err != nil {
		return "", nil, err
	}
	var pst platformState
	if raw, err := st.GetPlatformBackupState(ctx, clusterID); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &pst)
	}
	chosen := ""
	var same []appdb.BackupTarget
	for _, t := range targets {
		if isObjectBackupKind(t.Kind) && t.Bucket == spec.Bucket {
			same = append(same, t)
		}
	}
	sort.SliceStable(same, func(i, j int) bool {
		// The platform target first, then one on the same endpoint.
		pi, pj := same[i].ID == pst.TargetID, same[j].ID == pst.TargetID
		if pi != pj {
			return pi
		}
		return same[i].Endpoint == spec.Endpoint && same[j].Endpoint != spec.Endpoint
	})
	var superseded []string
	for i, t := range same {
		if i == 0 {
			chosen = t.ID
			if err := st.ReplaceBackupTargetConnection(ctx, clusterID, t.ID, spec.Endpoint, spec.Region, spec.AccessKey, spec.SecretKey); err != nil {
				return "", nil, err
			}
		}
		superseded = append(superseded, t.ID)
	}
	if chosen == "" {
		chosen = randomUUID()
		t := appdb.BackupTarget{
			ID: chosen, ClusterID: clusterID, Name: "Recovered " + spec.Bucket, Kind: spec.Kind,
			Status: appdb.BackupUntested, Username: spec.AccessKey, Endpoint: spec.Endpoint, Region: spec.Region,
			Bucket: spec.Bucket, Locator: "s3://" + spec.Bucket, CreatedAt: s.now(),
		}
		if err := st.CreateBackupTarget(ctx, t, spec.SecretKey, ""); err != nil {
			return "", nil, err
		}
	}
	for _, id := range superseded[min(1, len(superseded)):] {
		_ = st.UpdateBackupTargetStatus(ctx, clusterID, id, "superseded")
	}
	return chosen, superseded, nil
}

// testRecoveryTarget reads the credentials back as saved and lists the
// platform backup with them.
func (s *Server) testRecoveryTarget(ctx context.Context, st appdb.Store, clusterID, targetID string, spec backuphost.TargetSpec) error {
	t, err := st.GetBackupTarget(ctx, clusterID, targetID)
	if err != nil || t == nil {
		return fmt.Errorf("the target was not saved")
	}
	pass, _, err := st.BackupCredentials(ctx, clusterID, targetID)
	if err != nil {
		return err
	}
	if t.Username != spec.AccessKey || pass != spec.SecretKey || t.Endpoint != spec.Endpoint {
		return fmt.Errorf("the saved connection differs from the one that was entered")
	}
	saved := spec
	saved.ID = targetID
	saved.Prefix = t.Prefix
	store, done, err := s.recoveryStore(ctx, saved)
	if err != nil {
		return err
	}
	defer done()
	_, err = platformbackup.Locate(ctx, store)
	if err == nil {
		_ = st.UpdateBackupTargetStatus(ctx, clusterID, targetID, appdb.BackupAvailable)
	}
	return err
}

// restoreDatabase replaces the control database with the dump in one
// transaction, then brings it to this version's schema on a fresh
// connection. On failure the database is unchanged.
func (s *Server) restoreDatabase(ctx context.Context, dump []byte) (appdb.Store, func(), error) {
	if strings.TrimSpace(s.DSN) == "" {
		return nil, nil, errors.New("the control database address is unknown")
	}
	dir, err := os.MkdirTemp("", "ndl-recover-db-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dumpPath := filepath.Join(dir, "db.dump")
	if err := os.WriteFile(dumpPath, dump, 0o600); err != nil {
		return nil, nil, err
	}
	script, err := pgRestoreScript(ctx, dumpPath)
	if err != nil {
		return nil, nil, err
	}
	var in bytes.Buffer
	in.WriteString(recoveryDropSQL)
	in.Write(script)
	cmd := exec.CommandContext(ctx, "psql", "--no-psqlrc", "--quiet", "--no-password", "-v", "ON_ERROR_STOP=1", "--single-transaction", "--dbname="+s.DSN)
	cmd.Stdin = &in
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &strings.Builder{}
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("psql: %v %s", err, trimOutput(stderr.String()))
	}
	db, err := sql.Open("pgx", s.DSN)
	if err != nil {
		return nil, nil, err
	}
	files, err := store.List(migrations.FS, ".")
	if err == nil {
		err = store.Apply(ctx, store.SQLDB{DB: db}, files)
	}
	if err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("migrations after restore: %w", err)
	}
	return &appdb.Postgres{DB: db}, func() { _ = db.Close() }, nil
}

// recoveryDropSQL removes every table of the fresh install, inside the same
// transaction as the load.
const recoveryDropSQL = `
DO $$ DECLARE r record; BEGIN
  FOR r IN SELECT schemaname, tablename FROM pg_tables WHERE schemaname IN ('public','secrets') LOOP
    EXECUTE format('DROP TABLE IF EXISTS %I.%I CASCADE', r.schemaname, r.tablename);
  END LOOP;
  FOR r IN SELECT sequence_schema AS s, sequence_name AS n FROM information_schema.sequences WHERE sequence_schema IN ('public','secrets') LOOP
    EXECUTE format('DROP SEQUENCE IF EXISTS %I.%I CASCADE', r.s, r.n);
  END LOOP;
END $$;
CREATE SCHEMA IF NOT EXISTS secrets;
`

// pgRestoreScript turns a custom-format dump into SQL without the schema
// statements the fresh install already has.
func pgRestoreScript(ctx context.Context, dumpPath string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "pg_restore", "--no-owner", "--no-privileges", "--no-comments", "--file=-", dumpPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pg_restore: %v %s", err, trimOutput(stderr.String()))
	}
	var b bytes.Buffer
	for _, line := range bytes.SplitAfter(out, []byte("\n")) {
		t := strings.TrimSpace(string(line))
		if t == "CREATE SCHEMA public;" || t == "CREATE SCHEMA secrets;" || strings.HasPrefix(t, "\\restrict") || strings.HasPrefix(t, "\\unrestrict") {
			continue
		}
		b.Write(line)
	}
	return b.Bytes(), nil
}

func trimOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	return s
}
