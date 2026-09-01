package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestPhase28WGPeerAndNotReadyHonesty(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{}
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	now := time.Unix(1_700_000_000, 0).UTC()
	s.Now = func() time.Time { return now }

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/wg/peers", strings.NewReader(`{"name":"worker-1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), `"private_key"`) && strings.Contains(string(body), "local") {
		// worker_private_key is shown once; list bodies must not include private_key.
	}
	if !strings.Contains(string(body), "worker_private_key") || !strings.Contains(string(body), "pairing_token") {
		t.Fatalf("once secrets missing %s", body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "PrivateKey=") {
		t.Fatal("private key in JSON")
	}
	peerID := created["id"].(string)
	pairing := created["pairing_token"].(string)

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/cluster/wg", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list %d %s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "worker_private_key") || strings.Contains(string(body), "pairing_token") {
		t.Fatalf("secrets leaked in list %s", body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"status":"NotReady"`) {
		t.Fatalf("expected NotReady before handshake %s", body)
	}

	sessBody := `{"peer_id":"` + peerID + `","pairing_token":"` + pairing + `","listen_addr":"10.64.8.2:9444","handshake_unix":0}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/sessions", strings.NewReader(sessBody))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "NotReady") {
		t.Fatalf("session without handshake must stay NotReady %d %s", res.StatusCode, body)
	}

	sessBody = `{"peer_id":"` + peerID + `","pairing_token":"` + pairing + `","listen_addr":"10.64.8.2:9444","handshake_unix":1700000000}`
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/sessions", strings.NewReader(sessBody))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"Ready"`) {
		t.Fatalf("fresh handshake %d %s", res.StatusCode, body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"status":"Ready"`) {
		t.Fatalf("expected Ready %s", body)
	}

	s.Now = func() time.Time { return now.Add(3 * time.Minute) }
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"status":"NotReady"`) {
		t.Fatalf("stale tunnel must be NotReady %s", body)
	}
}

func TestPhase28GuestsKeepRunningWhenTunnelDown(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{}
	cluster, _ := mem.GetCluster(t.Context())
	local := seedNode(t, mem, cluster.ID, debianInv(), false)
	now := time.Unix(1_700_000_000, 0).UTC()
	s.Now = func() time.Time { return now }
	remoteID := uuid.NewString()
	seen := now
	_ = mem.CreateRemoteNode(t.Context(), appdb.RemoteNode{
		ID: remoteID, ClusterID: cluster.ID, Name: "worker-1", Status: ndnet.NodeReady,
		LastSeenAt: &seen, LastHandshakeUnix: now.Unix(),
	})
	_ = mem.CreateWorkload(t.Context(), appdb.Workload{
		ID: uuid.NewString(), ClusterID: cluster.ID, NodeID: remoteID, Name: "keep-running",
		Kind: "vm", Status: "running", DesiredPower: "running",
	})

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	s.Now = func() time.Time { return now.Add(5 * time.Minute) }
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/nodes", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"status":"NotReady"`) {
		t.Fatalf("tunnel down %s", body)
	}

	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/workloads", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ = ts.Client().Do(req)
	body, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if !strings.Contains(string(body), `"status":"running"`) {
		t.Fatalf("guests must keep running %s", body)
	}
	if !strings.Contains(string(body), local.ID) && !strings.Contains(string(body), "keep-running") {
		t.Fatalf("workload missing %s", body)
	}
}

func TestPhase28OpenSessionRejectsBadToken(t *testing.T) {
	s, mem, token := testServer(t)
	s.Network = fakeNet{}
	cluster, _ := mem.GetCluster(t.Context())
	_ = seedNode(t, mem, cluster.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/cluster/wg/peers", strings.NewReader(`{"name":"w"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	res, _ := ts.Client().Do(req)
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var created map[string]any
	_ = json.Unmarshal(raw, &created)
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/cluster/sessions", strings.NewReader(`{"peer_id":"`+created["id"].(string)+`","pairing_token":"nope"}`))
	req.Header.Set("Content-Type", "application/json")
	res, _ = ts.Client().Do(req)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", res.StatusCode)
	}
	_ = res.Body.Close()
}
