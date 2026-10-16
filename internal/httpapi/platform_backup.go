package httpapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/auth"
	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/platformbackup"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/rbac"
	"github.com/no-dal/ndl-ce/migrations"
)

// PlatformHooks replace parts of the platform backup in tests.
type PlatformHooks struct {
	// Dump replaces pg_dump of the control database.
	Dump func(ctx context.Context) ([]byte, error)
	// ConfigDir replaces /etc/ndl.
	ConfigDir string
	// Store replaces the bucket of a target.
	Store func(tgt appdb.BackupTarget) platformbackup.Store
	// KDF replaces the Argon2id cost.
	KDF *platformbackup.KDF
	// Sync runs Owner sign-in work inline instead of in the background.
	Sync bool
	// NoTick stops scheduled platform backups (tests).
	NoTick bool
}

type platformRuntime struct {
	mu   sync.Mutex
	busy atomic.Bool
}

const (
	// platformSnapshotEvery is how often the platform is backed up.
	platformSnapshotEvery = 24 * time.Hour
	// platformRetryAfter spaces attempts after a failure.
	platformRetryAfter = time.Hour
	// platformDeepEvery is how often restore points are read back deeply.
	platformDeepEvery = 7 * 24 * time.Hour
	// platformStaleAfter marks a workload whose newest remote restore point
	// is older than this as stale.
	platformStaleAfter = 48 * time.Hour
	// platformDeepSample is how many packs of each restore point the deep
	// check looks for.
	platformDeepSample = 8
	// maxPlatformConfigFile bounds one /etc/ndl file in the snapshot.
	maxPlatformConfigFile = 1 << 20
)

type platformState struct {
	Disabled bool   `json:"disabled,omitempty"`
	TargetID string `json:"target_id,omitempty"`
	// Wraps are the snapshot key wrapped for each Owner, by user id.
	Wraps     map[string]platformbackup.Wrap `json:"wraps,omitempty"`
	Envelope  platformEnvelopeState          `json:"envelope"`
	Snapshot  platformSnapshotState          `json:"snapshot"`
	Health    platformHealth                 `json:"health"`
	RotatedAt *time.Time                     `json:"rotated_at,omitempty"`
}

type platformEnvelopeState struct {
	Key         string     `json:"key,omitempty"`
	KeyID       string     `json:"key_id,omitempty"`
	Owners      int        `json:"owners,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Error       string     `json:"error,omitempty"`
	// Dirty is set when the envelope must be published again.
	Dirty bool `json:"dirty,omitempty"`
}

type platformSnapshotState struct {
	Last          *platformbackup.SnapshotRef  `json:"last,omitempty"`
	Published     []platformbackup.SnapshotRef `json:"published,omitempty"`
	LastAttemptAt *time.Time                   `json:"last_attempt_at,omitempty"`
	Error         string                       `json:"error,omitempty"`
}

type platformHealth struct {
	CheckedAt   *time.Time         `json:"checked_at,omitempty"`
	DeepAt      *time.Time         `json:"deep_at,omitempty"`
	Total       int                `json:"total"`
	Recoverable int                `json:"recoverable"`
	Stale       int                `json:"stale"`
	Missing     int                `json:"missing"`
	Items       []workloadRecovery `json:"items,omitempty"`
	Error       string             `json:"error,omitempty"`
}

// Workload recovery status.
const (
	recoverRecoverable = "recoverable"
	recoverStale       = "stale"
	recoverMissing     = "missing"
)

type workloadRecovery struct {
	WorkloadID string     `json:"workload_id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	BackupID   string     `json:"backup_id,omitempty"`
	Namespace  string     `json:"namespace,omitempty"`
	TargetID   string     `json:"target_id,omitempty"`
	LatestAt   *time.Time `json:"latest_at,omitempty"`
	Deep       bool       `json:"deep,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type platformKeys struct {
	Current string `json:"current"`
}

func (s *Server) loadPlatformState(ctx context.Context, clusterID string) platformState {
	var st platformState
	if raw, err := s.Store.GetPlatformBackupState(ctx, clusterID); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	if st.Wraps == nil {
		st.Wraps = map[string]platformbackup.Wrap{}
	}
	return st
}

func (s *Server) savePlatformState(ctx context.Context, clusterID string, st platformState) {
	raw, _ := json.Marshal(st)
	_ = s.Store.PutPlatformBackupState(context.WithoutCancel(ctx), clusterID, string(raw))
}

// platformKey returns the current snapshot key, creating it when asked.
func (s *Server) platformKey(ctx context.Context, clusterID string, create bool) ([]byte, error) {
	var keys platformKeys
	if raw, err := s.Store.GetPlatformBackupKeys(ctx, clusterID); err != nil {
		return nil, err
	} else if raw != "" {
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			return nil, err
		}
	}
	if keys.Current != "" {
		return hex.DecodeString(keys.Current)
	}
	if !create {
		return nil, nil
	}
	return s.newPlatformKey(ctx, clusterID)
}

func (s *Server) newPlatformKey(ctx context.Context, clusterID string) ([]byte, error) {
	key, err := platformbackup.NewKey()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(platformKeys{Current: hex.EncodeToString(key)})
	if err := s.Store.PutPlatformBackupKeys(ctx, clusterID, string(raw)); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *Server) platformKDF() platformbackup.KDF {
	if s.Platform.KDF != nil {
		return *s.Platform.KDF
	}
	return platformbackup.DefaultKDF
}

// platformTarget is the object storage target the platform is backed up
// to: the chosen one, else the first object storage target.
func (s *Server) platformTarget(ctx context.Context, clusterID string, st platformState) *appdb.BackupTarget {
	targets, _ := s.Store.ListBackupTargets(ctx, clusterID)
	sort.SliceStable(targets, func(i, j int) bool { return targets[i].CreatedAt.Before(targets[j].CreatedAt) })
	for i := range targets {
		if st.TargetID != "" && targets[i].ID == st.TargetID && isObjectBackupKind(targets[i].Kind) {
			return &targets[i]
		}
	}
	if st.TargetID != "" {
		return nil
	}
	for i := range targets {
		if isObjectBackupKind(targets[i].Kind) {
			return &targets[i]
		}
	}
	return nil
}

// agentPlatformStore reaches the bucket through the agent. Bodies pass
// through a private temporary directory.
type agentPlatformStore struct {
	s    *Server
	spec backuphost.TargetSpec
	dir  string
	n    atomic.Int64
}

func (s *Server) platformStore(ctx context.Context, tgt appdb.BackupTarget) (platformbackup.Store, func(), error) {
	if s.Platform.Store != nil {
		return s.Platform.Store(tgt), func() {}, nil
	}
	if s.Backup == nil {
		return nil, nil, errUnavailable("backup agent is unavailable")
	}
	dir, err := os.MkdirTemp("", "ndl-platform-")
	if err != nil {
		return nil, nil, err
	}
	st := &agentPlatformStore{s: s, spec: s.v2TargetSpec(ctx, tgt), dir: dir}
	return st, func() { _ = os.RemoveAll(dir) }, nil
}

func (a *agentPlatformStore) call(ctx context.Context, req backuphost.Request) (backuphost.Result, error) {
	req.Action = backuphost.ActionPlatformObject
	req.Target = a.spec
	raw, _ := json.Marshal(req)
	res, err := a.s.Backup.CopyBackup(ctx, qemu.BackupV2PlatformObject, "", string(raw))
	if err != nil {
		return backuphost.Result{}, err
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	return out, nil
}

func (a *agentPlatformStore) file() string {
	return filepath.Join(a.dir, strconv.FormatInt(a.n.Add(1), 10))
}

func (a *agentPlatformStore) Put(ctx context.Context, key string, body []byte) error {
	path := a.file()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(path) }()
	_, err := a.call(ctx, backuphost.Request{ObjectOp: "put", ObjectKey: key, Path: path})
	return err
}

func (a *agentPlatformStore) Get(ctx context.Context, key string) ([]byte, error) {
	path := a.file()
	defer func() { _ = os.Remove(path) }()
	out, err := a.call(ctx, backuphost.Request{ObjectOp: "get", ObjectKey: key, Path: path})
	if err != nil {
		return nil, err
	}
	if out.NotFound {
		return nil, platformbackup.ErrNotFound
	}
	return os.ReadFile(path)
}

func (a *agentPlatformStore) Delete(ctx context.Context, key string) error {
	_, err := a.call(ctx, backuphost.Request{ObjectOp: "delete", ObjectKey: key})
	return err
}

func (a *agentPlatformStore) List(ctx context.Context, prefix string) ([]platformbackup.Object, error) {
	out, err := a.call(ctx, backuphost.Request{ObjectOp: "list", ObjectKey: prefix})
	if err != nil {
		return nil, err
	}
	objs := make([]platformbackup.Object, 0, len(out.Objects))
	for _, o := range out.Objects {
		objs = append(objs, platformbackup.Object{Key: o.Key, Size: o.Size})
	}
	return objs, nil
}

// platformOwners are the enabled people with the Owner (admin) role.
func (s *Server) platformOwners(ctx context.Context, clusterID string) map[string]appdb.User {
	users, _ := s.Store.ListUsers(ctx, clusterID)
	out := map[string]appdb.User{}
	for _, u := range users {
		if u.Kind == appdb.UserKindService || userDisabled(u) {
			continue
		}
		roles, _ := s.Store.UserRoles(ctx, u.ID)
		for _, r := range roles {
			if r == rbac.Admin {
				out[u.ID] = u
			}
		}
	}
	return out
}

// platformOwnerSignedIn wraps the snapshot key for an Owner whose password
// was just checked (sign-in, setup, password change) and publishes the
// envelope when it changed. Others are ignored. The password is used only
// here and is not kept.
func (s *Server) platformOwnerSignedIn(clusterID string, user appdb.User, password string) {
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, ok := s.platformOwners(ctx, clusterID)[user.ID]; !ok {
			return
		}
		s.platform.mu.Lock()
		defer s.platform.mu.Unlock()
		st := s.loadPlatformState(ctx, clusterID)
		if st.Disabled {
			return
		}
		key, err := s.platformKey(ctx, clusterID, true)
		if err != nil {
			return
		}
		id := platformbackup.KeyID(key)
		w, ok := st.Wraps[user.ID]
		current := ok && w.KeyID == id && w.Owner == platformbackup.OwnerID(user.Username)
		if current {
			if _, err := platformbackup.UnwrapKey(w, password); err != nil {
				current = false
			}
		}
		// A wrap of the same key that no longer opens means the password
		// changed: the old password must not stay in the fallback envelope.
		replaced := ok && w.KeyID == id && !current
		if !current {
			nw, err := platformbackup.WrapKey(key, user.Username, password, s.platformKDF(), s.now())
			if err != nil {
				return
			}
			st.Wraps[user.ID] = nw
			st.Envelope.Dirty = true
		}
		if st.Envelope.Dirty || st.Envelope.KeyID != id || st.Envelope.Key == "" {
			creds := &platformOwnerCreds{username: user.Username, password: password}
			s.publishPlatformEnvelope(ctx, clusterID, &st, key, creds)
			if replaced && !st.Envelope.Dirty {
				// Both kept envelopes are now verified and hold only the
				// new password.
				s.publishPlatformEnvelope(ctx, clusterID, &st, key, creds)
			}
		}
		s.savePlatformState(ctx, clusterID, st)
	}
	if s.Platform.Sync {
		run()
		return
	}
	go run()
}

type platformOwnerCreds struct{ username, password string }

// publishPlatformEnvelope publishes the wraps of the current Owners. It must
// be called with s.platform.mu held. With creds, the published envelope is
// also unlocked with that Owner's password before it is switched to.
func (s *Server) publishPlatformEnvelope(ctx context.Context, clusterID string, st *platformState, key []byte, creds *platformOwnerCreds) {
	owners := s.platformOwners(ctx, clusterID)
	id := platformbackup.KeyID(key)
	var wraps []platformbackup.Wrap
	for uid, w := range st.Wraps {
		u, ok := owners[uid]
		if !ok || w.Owner != platformbackup.OwnerID(u.Username) {
			// No longer an Owner, or renamed: the wrap goes.
			delete(st.Wraps, uid)
			continue
		}
		if w.KeyID == id {
			wraps = append(wraps, w)
		}
	}
	st.Envelope.Dirty = true
	tgt := s.platformTarget(ctx, clusterID, *st)
	if tgt == nil {
		st.Envelope.Error = "no object storage backup target is set up"
		return
	}
	store, done, err := s.platformStore(ctx, *tgt)
	if err != nil {
		st.Envelope.Error = err.Error()
		return
	}
	defer done()
	env := platformbackup.NewEnvelope(key, wraps, s.now())
	ptr, err := platformbackup.PublishEnvelope(ctx, store, env, func(got platformbackup.Envelope) error {
		if got.KeyID != id || got.KeyCheck != platformbackup.KeyCheck(key) || len(got.Wraps) != len(env.Wraps) {
			return errors.New("the envelope read back does not match this key")
		}
		if creds != nil {
			k, err := got.Unlock(creds.username, creds.password)
			if err != nil || hex.EncodeToString(k) != hex.EncodeToString(key) {
				return errors.New("the envelope read back does not unlock with the Owner's password")
			}
		}
		return nil
	}, s.now())
	if err != nil {
		st.Envelope.Error = remoteErrText(err)
		s.platformAlert(ctx, clusterID, "platform.backup.envelope_failed", "The platform backup key envelope could not be published: "+st.Envelope.Error)
		return
	}
	now := s.now()
	st.Envelope = platformEnvelopeState{Key: ptr.Envelope, KeyID: id, Owners: len(wraps), PublishedAt: &now}
	st.Snapshot.Published = ptr.Snapshots
}

// schemaLevel is the newest migration this build applies.
func schemaLevel() int {
	level := 0
	entries, _ := fs.ReadDir(migrations.FS, ".")
	for _, e := range entries {
		name := e.Name()
		i := strings.IndexFunc(name, func(r rune) bool { return r < '0' || r > '9' })
		if i <= 0 {
			continue
		}
		if n, err := strconv.Atoi(name[:i]); err == nil && n > level {
			level = n
		}
	}
	return level
}

func (s *Server) platformDump(ctx context.Context) ([]byte, error) {
	if s.Platform.Dump != nil {
		return s.Platform.Dump(ctx)
	}
	if strings.TrimSpace(s.DSN) == "" {
		return nil, errors.New("the control database address is unknown")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-privileges", "--no-password", "--dbname="+s.DSN)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return nil, fmt.Errorf("pg_dump: %v %s", err, msg)
	}
	return out, nil
}

func (s *Server) platformConfigFiles() map[string][]byte {
	dir := s.Platform.ConfigDir
	if dir == "" {
		dir = "/etc/ndl"
	}
	out := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxPlatformConfigFile {
			continue
		}
		if body, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			out[platformbackup.ConfigDirName+e.Name()] = body
		}
	}
	return out
}

func (s *Server) platformRepoKey(ctx context.Context) ([]byte, error) {
	if s.Backup == nil {
		return nil, errUnavailable("backup agent is unavailable")
	}
	raw, _ := json.Marshal(backuphost.Request{Action: backuphost.ActionKey})
	res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2Key, "", string(raw))
	if err != nil {
		return nil, err
	}
	var out backuphost.Result
	_ = json.Unmarshal([]byte(res.Extra), &out)
	if out.Key == "" {
		return nil, errors.New("the backup repository key could not be read")
	}
	return []byte(out.Key), nil
}

// platformManifest describes the cluster for the snapshot.
func (s *Server) platformManifest(ctx context.Context, clusterID string) platformbackup.Manifest {
	host, _ := os.Hostname()
	m := platformbackup.Manifest{Version: 1, SchemaLevel: schemaLevel(), CreatedAt: s.now().UTC(), ClusterID: clusterID, Hostname: host}
	covered := s.coveredWorkloads(ctx, clusterID)
	runs, _ := s.Store.ListBackupRuns(ctx, clusterID)
	runTarget := map[string]string{}
	for _, r := range runs {
		runTarget[r.ID] = r.TargetID
	}
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	points := map[string][]platformbackup.PointRef{}
	for _, a := range arts {
		if a.Format != backup.Format || a.BackupID == "" {
			continue
		}
		points[a.WorkloadID] = append(points[a.WorkloadID], platformbackup.PointRef{
			ArtifactID: a.ID, BackupID: a.BackupID, Namespace: a.Namespace, TargetID: runTarget[a.RunID],
			RemoteState: a.RemoteState, CaptureMode: a.CaptureMode, CreatedAt: a.CreatedAt,
		})
	}
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	for _, wl := range wls {
		ps := points[wl.ID]
		sort.Slice(ps, func(i, j int) bool { return ps[i].CreatedAt.After(ps[j].CreatedAt) })
		m.Workloads = append(m.Workloads, platformbackup.WorkloadRef{ID: wl.ID, Name: wl.Name, Kind: wl.Kind, Protected: covered[wl.ID], Points: ps})
	}
	targets, _ := s.Store.ListBackupTargets(ctx, clusterID)
	for _, t := range targets {
		m.Targets = append(m.Targets, platformbackup.TargetRef{ID: t.ID, Name: t.Name, Kind: t.Kind, Endpoint: t.Endpoint, Bucket: t.Bucket, Prefix: t.Prefix})
	}
	pools, _ := s.Store.ListStoragePools(ctx, clusterID)
	for _, p := range pools {
		m.Pools = append(m.Pools, platformbackup.PoolRef{ID: p.ID, Name: p.Name, Backend: p.BackendType, Path: p.RootPath})
	}
	nets, _ := s.Store.ListNetworks(ctx, clusterID)
	for _, n := range nets {
		m.Networks = append(m.Networks, platformbackup.NetworkRef{ID: n.ID, Name: n.Name, Bridge: n.BridgeName, CIDR: n.IPv4CIDR})
	}
	nodes, _ := s.Store.ListClusterNodes(ctx, clusterID)
	for _, n := range nodes {
		m.Nodes = append(m.Nodes, platformbackup.NodeRef{ID: n.ID, Name: n.Name})
	}
	return m
}

// coveredWorkloads are the workloads a backup policy is meant to protect.
func (s *Server) coveredWorkloads(ctx context.Context, clusterID string) map[string]bool {
	out := map[string]bool{}
	pols, _ := s.Store.ListBackupPolicies(ctx, clusterID)
	for _, pol := range pols {
		ids, err := s.policyWorkloadIDs(ctx, clusterID, pol)
		if err != nil {
			continue
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out
}

// runPlatformSnapshot backs the platform up now.
func (s *Server) runPlatformSnapshot(ctx context.Context, clusterID string) error {
	s.platform.mu.Lock()
	defer s.platform.mu.Unlock()
	st := s.loadPlatformState(ctx, clusterID)
	now := s.now()
	st.Snapshot.LastAttemptAt = &now
	err := s.platformSnapshotLocked(ctx, clusterID, &st)
	prevErr := st.Snapshot.Error
	if err != nil {
		st.Snapshot.Error = remoteErrText(err)
		if st.Snapshot.Error != prevErr {
			s.platformAlert(ctx, clusterID, "platform.backup.failed", "The NDL-CE platform backup failed: "+st.Snapshot.Error)
		}
	} else {
		st.Snapshot.Error = ""
		deep := st.Health.DeepAt == nil || now.Sub(*st.Health.DeepAt) >= platformDeepEvery
		s.updatePlatformHealth(ctx, clusterID, &st, deep)
	}
	s.savePlatformState(ctx, clusterID, st)
	return err
}

func (s *Server) platformSnapshotLocked(ctx context.Context, clusterID string, st *platformState) error {
	if st.Disabled {
		return errors.New("the platform backup is turned off")
	}
	key, err := s.platformKey(ctx, clusterID, false)
	if err != nil {
		return err
	}
	if key == nil {
		return errors.New("an Owner must sign in once so the platform backup can be unlocked with their password")
	}
	tgt := s.platformTarget(ctx, clusterID, *st)
	if tgt == nil {
		return errors.New("no object storage backup target is set up")
	}
	store, done, err := s.platformStore(ctx, *tgt)
	if err != nil {
		return err
	}
	defer done()
	if st.Envelope.Dirty || st.Envelope.KeyID != platformbackup.KeyID(key) {
		s.publishPlatformEnvelope(ctx, clusterID, st, key, nil)
		if st.Envelope.Dirty {
			return fmt.Errorf("the key envelope is not published: %s", st.Envelope.Error)
		}
	}
	dump, err := s.platformDump(ctx)
	if err != nil {
		return err
	}
	repoKey, err := s.platformRepoKey(ctx)
	if err != nil {
		return fmt.Errorf("backup repository key: %w", err)
	}
	files := s.platformConfigFiles()
	files[platformbackup.DatabaseFile] = dump
	files[platformbackup.RepoKeyFile] = repoKey
	man := s.platformManifest(ctx, clusterID)
	archive, err := platformbackup.BuildArchive(man, files)
	if err != nil {
		return err
	}
	ptr, err := platformbackup.PublishSnapshot(ctx, store, key, archive, platformbackup.SnapshotRef{
		SchemaLevel: man.SchemaLevel, Workloads: len(man.Workloads),
	}, platformbackup.DefaultRetention, s.now())
	if err != nil {
		return err
	}
	st.Snapshot.Published = ptr.Snapshots
	st.Snapshot.Last = ptr.Latest(platformbackup.KeyID(key))
	return nil
}

// updatePlatformHealth checks that every protected workload's newest remote
// restore point is in its target. The quick check looks for the manifest
// and location map; the deep check downloads and authenticates them and
// looks for a sample of their packs.
func (s *Server) updatePlatformHealth(ctx context.Context, clusterID string, st *platformState, deep bool) {
	prev := st.Health
	h := s.platformHealthCheck(ctx, clusterID, deep)
	if !deep {
		h.DeepAt = prev.DeepAt
	}
	st.Health = h
	if h.Error == "" && (h.Recoverable < prev.Recoverable || h.Missing > prev.Missing || h.Stale > prev.Stale) && h.Recoverable < h.Total {
		s.platformAlert(ctx, clusterID, "platform.recovery.degraded",
			fmt.Sprintf("%d of %d workloads are recoverable from offsite backups (%d stale, %d missing).", h.Recoverable, h.Total, h.Stale, h.Missing))
	}
}

func (s *Server) platformHealthCheck(ctx context.Context, clusterID string, deep bool) platformHealth {
	now := s.now()
	h := platformHealth{CheckedAt: &now}
	if deep {
		h.DeepAt = &now
	}
	covered := s.coveredWorkloads(ctx, clusterID)
	wls, _ := s.Store.ListWorkloads(ctx, clusterID)
	targets, _ := s.Store.ListBackupTargets(ctx, clusterID)
	tgtByID := map[string]appdb.BackupTarget{}
	for _, t := range targets {
		tgtByID[t.ID] = t
	}
	runs, _ := s.Store.ListBackupRuns(ctx, clusterID)
	runTarget := map[string]string{}
	for _, r := range runs {
		runTarget[r.ID] = r.TargetID
	}
	arts, _ := s.Store.ListBackupArtifacts(ctx, clusterID)
	byWorkload := map[string][]appdb.BackupArtifact{}
	for _, a := range arts {
		t, ok := tgtByID[runTarget[a.RunID]]
		if a.Format != backup.Format || a.BackupID == "" || a.RemoteState != string(backup.RemoteProtected) || !ok || !isObjectBackupKind(t.Kind) {
			continue
		}
		byWorkload[a.WorkloadID] = append(byWorkload[a.WorkloadID], a)
	}
	// The newest three remote points of each workload are checked, grouped
	// by target; the newest one present decides.
	newest := map[string][]appdb.BackupArtifact{}
	byTarget := map[string][]appdb.BackupArtifact{}
	items := map[string]*workloadRecovery{}
	for _, wl := range wls {
		if !covered[wl.ID] {
			continue
		}
		h.Total++
		items[wl.ID] = &workloadRecovery{WorkloadID: wl.ID, Name: wl.Name, Status: recoverMissing, Error: "no offsite restore point"}
		as := byWorkload[wl.ID]
		sort.Slice(as, func(i, j int) bool { return as[i].CreatedAt.After(as[j].CreatedAt) })
		if len(as) > 3 {
			as = as[:3]
		}
		newest[wl.ID] = as
		for _, a := range as {
			byTarget[runTarget[a.RunID]] = append(byTarget[runTarget[a.RunID]], a)
		}
	}
	found := map[string]backup.RemoteCheck{}
	for tid, as := range byTarget {
		if s.Backup == nil {
			h.Error = "backup agent is unavailable"
			break
		}
		req := backuphost.Request{Action: backuphost.ActionRemoteCheck, Target: s.v2TargetSpec(ctx, tgtByID[tid]), Deep: deep, Sample: platformDeepSample}
		for _, a := range as {
			req.Checks = append(req.Checks, backuphost.PointID{Namespace: a.Namespace, BackupID: a.BackupID})
		}
		raw, _ := json.Marshal(req)
		res, err := s.Backup.CopyBackup(ctx, qemu.BackupV2RemoteCheck, "", string(raw))
		if err != nil {
			h.Error = remoteErrText(err)
			continue
		}
		var out backuphost.Result
		_ = json.Unmarshal([]byte(res.Extra), &out)
		for _, c := range out.RemoteChecks {
			found[c.Namespace+"/"+c.BackupID] = c
		}
	}
	for id, as := range newest {
		it := items[id]
		for _, a := range as {
			res, ok := found[a.Namespace+"/"+a.BackupID]
			if !ok {
				continue
			}
			if !res.Present || deep && !res.Deep {
				if it.BackupID == "" {
					it.BackupID, it.Namespace, it.TargetID, it.Error = a.BackupID, a.Namespace, runTarget[a.RunID], res.Error
				}
				continue
			}
			at := a.CreatedAt
			*it = workloadRecovery{
				WorkloadID: it.WorkloadID, Name: it.Name, Status: recoverRecoverable,
				BackupID: a.BackupID, Namespace: a.Namespace, TargetID: runTarget[a.RunID],
				LatestAt: &at, Deep: res.Deep,
			}
			if now.Sub(at) > platformStaleAfter {
				it.Status = recoverStale
			}
			break
		}
	}
	for _, it := range items {
		switch it.Status {
		case recoverRecoverable:
			h.Recoverable++
		case recoverStale:
			h.Stale++
		default:
			h.Missing++
		}
		h.Items = append(h.Items, *it)
	}
	sort.Slice(h.Items, func(i, j int) bool { return h.Items[i].Name < h.Items[j].Name })
	return h
}

func (s *Server) platformAlert(ctx context.Context, clusterID, kind, message string) {
	payload, _ := json.Marshal(map[string]any{"kind": kind, "message": message})
	s.emitUpdatePayload(ctx, clusterID, kind, payload)
	s.notify(ctx, clusterID, payload)
}

// TickPlatformBackup publishes a pending envelope, backs the platform up
// once a day and reads restore points back deeply once a week.
func (s *Server) TickPlatformBackup(ctx context.Context) {
	if s.Platform.NoTick || !s.platform.busy.CompareAndSwap(false, true) {
		return
	}
	defer s.platform.busy.Store(false)
	cluster, err := s.Store.GetCluster(ctx)
	if err != nil || cluster == nil || cluster.SetupCompletedAt == nil {
		return
	}
	st := s.loadPlatformState(ctx, cluster.ID)
	if st.Disabled || s.platformTarget(ctx, cluster.ID, st) == nil {
		return
	}
	now := s.now()
	if st.Snapshot.LastAttemptAt != nil && now.Sub(*st.Snapshot.LastAttemptAt) < platformRetryAfter {
		return
	}
	if st.Snapshot.Last == nil || now.Sub(st.Snapshot.Last.CreatedAt) >= platformSnapshotEvery || st.Envelope.Dirty {
		_ = s.runPlatformSnapshot(ctx, cluster.ID)
		return
	}
	if st.Health.DeepAt == nil || now.Sub(*st.Health.DeepAt) >= platformDeepEvery {
		s.platform.mu.Lock()
		st = s.loadPlatformState(ctx, cluster.ID)
		s.updatePlatformHealth(ctx, cluster.ID, &st, true)
		s.savePlatformState(ctx, cluster.ID, st)
		s.platform.mu.Unlock()
	}
}

func (s *Server) platformJSON(ctx context.Context, clusterID string, st platformState) map[string]any {
	owners := s.platformOwners(ctx, clusterID)
	key, _ := s.platformKey(ctx, clusterID, false)
	id := ""
	if key != nil {
		id = platformbackup.KeyID(key)
	}
	ready := []string{}
	waiting := []string{}
	for uid, u := range owners {
		if w, ok := st.Wraps[uid]; ok && w.KeyID == id && id != "" && st.Envelope.KeyID == id {
			ready = append(ready, u.Username)
		} else {
			waiting = append(waiting, u.Username)
		}
	}
	sort.Strings(ready)
	sort.Strings(waiting)
	tgt := s.platformTarget(ctx, clusterID, st)
	out := map[string]any{
		"enabled": !st.Disabled, "target_id": st.TargetID,
		"owners_ready": ready, "owners_waiting": waiting,
		"envelope": st.Envelope, "snapshot": st.Snapshot, "health": st.Health,
		"rotated_at": st.RotatedAt, "schema_level": schemaLevel(),
	}
	if tgt != nil {
		out["target"] = map[string]any{"id": tgt.ID, "name": tgt.Name, "kind": tgt.Kind, "bucket": tgt.Bucket}
	}
	return out
}

func (s *Server) getPlatformBackup(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupRead)
	if err != nil {
		return
	}
	st := s.loadPlatformState(r.Context(), p.User.ClusterID)
	writeJSON(w, http.StatusOK, s.platformJSON(r.Context(), p.User.ClusterID, st))
}

func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request) (*principal, bool) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return nil, false
	}
	if !hasRole(p, rbac.Admin) {
		writeErr(w, http.StatusForbidden, "only an Owner can manage the platform backup")
		return nil, false
	}
	return p, true
}

func (s *Server) patchPlatformBackup(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled  *bool   `json:"enabled"`
		TargetID *string `json:"target_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx := r.Context()
	s.platform.mu.Lock()
	st := s.loadPlatformState(ctx, p.User.ClusterID)
	if req.Enabled != nil {
		st.Disabled = !*req.Enabled
	}
	if req.TargetID != nil {
		id := strings.TrimSpace(*req.TargetID)
		if id != "" {
			t, err := s.Store.GetBackupTarget(ctx, p.User.ClusterID, id)
			if err != nil || t == nil || !isObjectBackupKind(t.Kind) {
				s.platform.mu.Unlock()
				writeErr(w, http.StatusBadRequest, "the platform backup needs an object storage target")
				return
			}
		}
		if id != st.TargetID {
			st.TargetID = id
			// The new bucket has no envelope yet.
			st.Envelope.Dirty = true
		}
	}
	s.savePlatformState(ctx, p.User.ClusterID, st)
	s.platform.mu.Unlock()
	s.audit(r, p.User.ClusterID, p.User.ID, "platform.backup.update", "ok", "")
	writeJSON(w, http.StatusOK, s.platformJSON(ctx, p.User.ClusterID, st))
}

func (s *Server) runPlatformBackupAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	err := s.runPlatformSnapshot(ctx, p.User.ClusterID)
	s.audit(r, p.User.ClusterID, p.User.ID, "platform.backup.run", map[bool]string{true: "ok", false: "failed"}[err == nil], "")
	st := s.loadPlatformState(ctx, p.User.ClusterID)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": remoteErrText(err), "platform": s.platformJSON(ctx, p.User.ClusterID, st)})
		return
	}
	writeJSON(w, http.StatusOK, s.platformJSON(ctx, p.User.ClusterID, st))
}

func (s *Server) verifyPlatformBackupAPI(w http.ResponseWriter, r *http.Request) {
	p, err := s.require(w, r, rbac.BackupCreate)
	if err != nil {
		return
	}
	var req struct {
		Deep bool `json:"deep"`
	}
	_ = readJSON(r, &req)
	ctx := context.WithoutCancel(r.Context())
	s.platform.mu.Lock()
	st := s.loadPlatformState(ctx, p.User.ClusterID)
	s.updatePlatformHealth(ctx, p.User.ClusterID, &st, req.Deep)
	s.savePlatformState(ctx, p.User.ClusterID, st)
	s.platform.mu.Unlock()
	writeJSON(w, http.StatusOK, s.platformJSON(ctx, p.User.ClusterID, st))
}

// rotatePlatformKey replaces the snapshot key. The Owner confirms with their
// password; their wrap of the new key is published at once and other Owners'
// at their next sign-in. A snapshot under the new key follows, and snapshots
// of the old key are deleted only once it is verified. Copies of old
// envelopes or snapshots someone already downloaded are not affected.
func (s *Server) rotatePlatformKey(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "your password is required")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	user, err := s.Store.GetUser(ctx, p.User.ID)
	if err != nil || user == nil || !auth.VerifyPassword(req.Password, user.PasswordHash) {
		s.audit(r, p.User.ClusterID, p.User.ID, "platform.backup.rotate", "denied", "password")
		writeErr(w, http.StatusUnauthorized, "the password is not correct")
		return
	}
	s.platform.mu.Lock()
	st := s.loadPlatformState(ctx, p.User.ClusterID)
	key, err := s.newPlatformKey(ctx, p.User.ClusterID)
	if err != nil {
		s.platform.mu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	wrap, err := platformbackup.WrapKey(key, user.Username, req.Password, s.platformKDF(), s.now())
	if err != nil {
		s.platform.mu.Unlock()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	st.Wraps = map[string]platformbackup.Wrap{user.ID: wrap}
	now := s.now()
	st.RotatedAt = &now
	s.publishPlatformEnvelope(ctx, p.User.ClusterID, &st, key, &platformOwnerCreds{username: user.Username, password: req.Password})
	s.savePlatformState(ctx, p.User.ClusterID, st)
	s.platform.mu.Unlock()
	s.audit(r, p.User.ClusterID, p.User.ID, "platform.backup.rotate", "ok", "")
	if st.Envelope.Dirty {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the new key was made but its envelope could not be published: " + st.Envelope.Error, "platform": s.platformJSON(ctx, p.User.ClusterID, st)})
		return
	}
	if err := s.runPlatformSnapshot(ctx, p.User.ClusterID); err != nil {
		st = s.loadPlatformState(ctx, p.User.ClusterID)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the key was rotated but the first snapshot failed: " + remoteErrText(err), "platform": s.platformJSON(ctx, p.User.ClusterID, st)})
		return
	}
	writeJSON(w, http.StatusOK, s.platformJSON(ctx, p.User.ClusterID, s.loadPlatformState(ctx, p.User.ClusterID)))
}
