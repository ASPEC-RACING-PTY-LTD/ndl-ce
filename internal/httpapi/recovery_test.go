package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/backuphost"
	"github.com/no-dal/ndl-ce/internal/platformbackup"
	"github.com/no-dal/ndl-ce/internal/qemu"
	"github.com/no-dal/ndl-ce/internal/storage"
)

type recoveryAgent struct {
	*platformAgent
	mu      sync.Mutex
	imports []string
}

func (f *recoveryAgent) CopyBackup(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	if action == qemu.BackupV2KeyImport {
		var req backuphost.Request
		_ = json.Unmarshal([]byte(dest), &req)
		f.mu.Lock()
		f.imports = append(f.imports, req.ImportKey)
		f.mu.Unlock()
		return storage.CopyResult{Extra: "{}"}, nil
	}
	return f.platformAgent.CopyBackup(ctx, action, src, dest)
}

func jsonCall(t *testing.T, ts *httptest.Server, cookie, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

const testAccount = "0123456789abcdef0123456789abcdef"

func TestRecoveryRestoresOntoAFreshHostWithTheNewConnection(t *testing.T) {
	ctx := context.Background()
	bucket := platformbackup.NewMemoryStore()

	// The lost host: an Owner, an R2 target with old credentials, a pool, a
	// network and a running workload, backed up once.
	old, oldMem, oldToken := testServer(t)
	oldCluster, _ := oldMem.GetCluster(ctx)
	old.Platform.Store = func(appdb.BackupTarget) platformbackup.Store { return bucket }
	old.Platform.Dump = func(context.Context) ([]byte, error) { return []byte("dump of the lost host"), nil }
	old.Backup = &platformAgent{fakeBackup: &fakeBackup{}, missing: map[string]bool{}}
	targetID := uuid.NewString()
	_ = oldMem.CreateBackupTarget(ctx, appdb.BackupTarget{ID: targetID, ClusterID: oldCluster.ID, Name: "r2", Kind: "r2", Bucket: "ndl", Endpoint: "https://old.r2.cloudflarestorage.com", Username: "old-key", CreatedAt: time.Now()}, "old-secret", "enc-key")
	oldNode := uuid.NewString()
	_ = oldMem.UpsertNode(ctx, appdb.Node{ID: oldNode, ClusterID: oldCluster.ID, Name: "local"})
	poolID, netID := seedCompute(t, oldMem, oldCluster.ID, oldNode)
	wl := appdb.Workload{ID: uuid.NewString(), ClusterID: oldCluster.ID, Name: "app", Kind: "system-container", NodeID: oldNode, DesiredPower: "running"}
	_ = oldMem.CreateWorkload(ctx, wl)
	ots := httptest.NewServer(old.Handler())
	defer ots.Close()
	oldCookie := claimAdmin(t, ots, oldToken)
	if code, st := jsonCall(t, ots, oldCookie, "POST", "/api/v1/platform-backup/run", ""); code != http.StatusOK {
		t.Fatalf("backup %d %v", code, st)
	}

	// The new host, unclaimed.
	s, _, token := testServer(t)
	s.Platform.Store = func(appdb.BackupTarget) platformbackup.Store { return bucket }
	agent := &recoveryAgent{platformAgent: &platformAgent{fakeBackup: &fakeBackup{}, missing: map[string]bool{}}}
	s.Backup = agent
	var dumps []string
	s.Recovery.Restore = func(_ context.Context, dump []byte) (appdb.Store, func(), error) {
		dumps = append(dumps, string(dump))
		return oldMem, func() {}, nil
	}
	exited := make(chan struct{}, 1)
	s.Recovery.Exit = func() { exited <- struct{}{} }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	if _, st := jsonCall(t, ts, "", "GET", "/api/v1/setup/recover", ""); st["available"] != true {
		t.Fatalf("recovery must be offered on an unclaimed host: %v", st)
	}
	conn := `{"token":"%s","kind":"r2","account_id":"` + testAccount + `","bucket":"ndl","access_key_id":"new-key","secret_access_key":"new-secret"}`
	if code, _ := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/connect", strings.Replace(conn, "%s", "wrong-token", 1)); code != http.StatusUnauthorized {
		t.Fatalf("the setup token is required: %d", code)
	}
	code, found := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/connect", strings.Replace(conn, "%s", token, 1))
	if code != http.StatusOK || found["snapshots"] != float64(1) {
		t.Fatalf("connect %d %v", code, found)
	}
	sid := found["session_id"].(string)
	unlock := `{"token":"` + token + `","session_id":"` + sid + `","username":"admin","password":"%s"}`
	if code, _ := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/unlock", strings.Replace(unlock, "%s", "guess", 1)); code != http.StatusUnauthorized {
		t.Fatalf("a wrong password must not unlock: %d", code)
	}
	code, plan := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/unlock", strings.Replace(unlock, "%s", "correct-horse", 1))
	if code != http.StatusOK {
		t.Fatalf("unlock %d %v", code, plan)
	}
	if pools := plan["pools"].([]any); len(pools) != 1 || pools[0].(map[string]any)["id"] != poolID {
		t.Fatalf("plan pools %v", plan["pools"])
	}
	body := `{"token":"` + token + `","session_id":"` + sid + `","pools":{"` + poolID + `":"/srv/ndl/local"},"networks":{"` + netID + `":"ndlnew0"}}`
	code, done := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/restore", body)
	if code != http.StatusOK || done["connection_verified"] != true || done["target_id"] != targetID {
		t.Fatalf("restore %d %v", code, done)
	}
	if len(dumps) != 1 || dumps[0] != "dump of the lost host" {
		t.Fatalf("restored dump %v", dumps)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the control plane must restart after recovery")
	}

	// The new connection replaced the revoked one; the encryption key stayed.
	tgt, _ := oldMem.GetBackupTarget(ctx, oldCluster.ID, targetID)
	pass, enc, _ := oldMem.BackupCredentials(ctx, oldCluster.ID, targetID)
	if tgt.Username != "new-key" || pass != "new-secret" || enc != "enc-key" || tgt.Endpoint != "https://"+testAccount+".r2.cloudflarestorage.com" || tgt.Status != appdb.BackupAvailable {
		t.Fatalf("target %+v pass=%q enc=%q", tgt, pass, enc)
	}
	pools, _ := oldMem.ListStoragePools(ctx, oldCluster.ID)
	nets, _ := oldMem.ListNetworks(ctx, oldCluster.ID)
	if pools[0].RootPath != "/srv/ndl/local" || nets[0].BridgeName != "ndlnew0" {
		t.Fatalf("remap %+v %+v", pools[0], nets[0])
	}
	got, _ := oldMem.GetWorkload(ctx, oldCluster.ID, wl.ID)
	if got.DesiredPower != "stopped" || got.NodeID == oldNode {
		t.Fatalf("workloads are held stopped on the new node: %+v", got)
	}
	nodes, _ := oldMem.ListClusterNodes(ctx, oldCluster.ID)
	for _, n := range nodes {
		if n.ID == oldNode && n.RevokedAt == nil {
			t.Fatal("the lost host's node is revoked")
		}
	}
	st := old.loadPlatformState(ctx, oldCluster.ID)
	if st.Recovery == nil || st.Recovery.Power[wl.ID] != "running" || !st.Recovery.ConnectionVerified || st.TargetID != targetID {
		t.Fatalf("recovery record %+v", st.Recovery)
	}
	if len(agent.imports) != 1 || agent.imports[0] != "00112233" {
		t.Fatalf("the repository key is installed: %v", agent.imports)
	}

	// Locked from here on.
	if _, st := jsonCall(t, ts, "", "GET", "/api/v1/setup/recover", ""); st["available"] != false || st["locked"] != true {
		t.Fatalf("recovery must lock: %v", st)
	}
	res, _ := ts.Client().Post(ts.URL+"/api/v1/setup/claim", "application/json", strings.NewReader(`{"token":"`+token+`","username":"x","password":"correct-horse"}`))
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("setup must be closed during recovery: %d", res.StatusCode)
	}
}

func TestRecoveryRefusesABackupFromANewerVersion(t *testing.T) {
	ctx := context.Background()
	bucket := platformbackup.NewMemoryStore()
	key, _ := platformbackup.NewKey()
	now := time.Now()
	w, _ := platformbackup.WrapKey(key, "admin", "pw", testPlatformKDF, now)
	if _, err := platformbackup.PublishEnvelope(ctx, bucket, platformbackup.NewEnvelope(key, []platformbackup.Wrap{w}, now), nil, now); err != nil {
		t.Fatal(err)
	}
	archive, _ := platformbackup.BuildArchive(platformbackup.Manifest{Version: 1, SchemaLevel: schemaLevel() + 1}, map[string][]byte{platformbackup.DatabaseFile: []byte("x")})
	if _, err := platformbackup.PublishSnapshot(ctx, bucket, key, archive, platformbackup.SnapshotRef{}, platformbackup.DefaultRetention, now); err != nil {
		t.Fatal(err)
	}
	s, _, token := testServer(t)
	s.Platform.Store = func(appdb.BackupTarget) platformbackup.Store { return bucket }
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	_, found := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/connect", `{"token":"`+token+`","account_id":"`+testAccount+`","bucket":"b","access_key_id":"k","secret_access_key":"s"}`)
	code, out := jsonCall(t, ts, "", "POST", "/api/v1/setup/recover/unlock", `{"token":"`+token+`","session_id":"`+found["session_id"].(string)+`","username":"admin","password":"pw"}`)
	if code != http.StatusConflict || !strings.Contains(out["error"].(string), "older than the backup") {
		t.Fatalf("version check %d %v", code, out)
	}
}
