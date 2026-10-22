package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
	"github.com/no-dal/ndl-ce/internal/cluster"
	"github.com/no-dal/ndl-ce/internal/ndnet"
	"golang.org/x/net/http2"
)

type helloAgent struct {
	agentv1connect.UnimplementedAgentServiceHandler
}

func (helloAgent) Hello(context.Context, *connect.Request[agentv1.HelloRequest]) (*connect.Response[agentv1.HelloResponse], error) {
	return connect.NewResponse(&agentv1.HelloResponse{AgentVersion: "1.2.6-test"}), nil
}

func apiJSON(t *testing.T, ts *httptest.Server, cookie, method, path, body string) (int, map[string]any) {
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

// joinWorker creates a join token and joins a worker that advertises addr.
func joinWorker(t *testing.T, ts *httptest.Server, cookie, addr string) map[string]any {
	t.Helper()
	code, tok := apiJSON(t, ts, cookie, "POST", "/api/v1/cluster/join-tokens", `{}`)
	if code != http.StatusCreated {
		t.Fatalf("join token %d %v", code, tok)
	}
	if tok["join_url"] == "" || tok["agent_port"] != "9444" {
		t.Fatalf("join token must carry the join URL and agent port: %v", tok)
	}
	body, _ := json.Marshal(map[string]any{"token": tok["token"], "hostname": "vps-1", "listen_addr": addr})
	code, joined := apiJSON(t, ts, "", "POST", "/api/v1/cluster/join", string(body))
	if code != http.StatusCreated {
		t.Fatalf("join %d %v", code, joined)
	}
	return joined
}

func TestJoinRecordsAdvertisedAgentAddress(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	joined := joinWorker(t, ts, cookie, "203.0.113.9")
	if joined["listen_addr"] != "203.0.113.9:9444" {
		t.Fatalf("a bare host must get the agent port: %v", joined["listen_addr"])
	}
	remote, _ := mem.GetRemoteNode(t.Context(), clusterRow.ID, joined["id"].(string))
	if remote == nil || remote.ListenAddr != "203.0.113.9:9444" || remote.Status != ndnet.NodeNotReady {
		t.Fatalf("join must record the agent address, not yet ready: %+v", remote)
	}

	_, list := apiJSON(t, ts, cookie, "GET", "/api/v1/nodes", "")
	items, _ := list["items"].([]any)
	found := false
	for _, it := range items {
		m := it.(map[string]any)
		if m["id"] != joined["id"] {
			continue
		}
		found = true
		agent, _ := m["agent"].(map[string]any)
		if agent == nil || agent["listen_addr"] != "203.0.113.9:9444" {
			t.Fatalf("worker must list its agent link: %v", m)
		}
	}
	if !found || len(items) != 2 {
		t.Fatalf("worker must be listed once: %v", items)
	}
}

func TestJoinThroughLoopbackLeavesAddressForAdmin(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	// httptest dials from 127.0.0.1, like a join relayed by a local tunnel.
	joined := joinWorker(t, ts, cookie, "")
	if joined["listen_addr"] != "" {
		t.Fatalf("a loopback join source is not the worker's address: %v", joined["listen_addr"])
	}
}

func TestCheckNodeReachesWorkerOverMutualTLS(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	// Reserve a port for the worker agent, then join advertising it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	joined := joinWorker(t, ts, cookie, ln.Addr().String())
	cfg, err := cluster.AgentServerTLS([]byte(joined["ca_cert"].(string)), []byte(joined["node_cert"].(string)), []byte(joined["node_key"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(agentv1connect.NewAgentServiceHandler(helloAgent{}))
	agentSrv := &http.Server{Handler: mux, TLSConfig: cfg}
	if err := http2.ConfigureServer(agentSrv, &http2.Server{}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = agentSrv.ServeTLS(ln, "", "") }()
	defer agentSrv.Close()

	id := joined["id"].(string)
	code, got := apiJSON(t, ts, cookie, "POST", "/api/v1/nodes/"+id+"/check", `{}`)
	if code != http.StatusOK || got["reachable"] != true || got["agent_version"] != "1.2.6-test" {
		t.Fatalf("check must reach the worker agent over mTLS: %d %v", code, got)
	}
	remote, _ := mem.GetRemoteNode(t.Context(), clusterRow.ID, id)
	if remote == nil || remote.Status != ndnet.NodeReady || remote.LastSeenAt == nil {
		t.Fatalf("a reachable worker must be marked ready: %+v", remote)
	}
}

func TestCheckNodeReportsUnreachableWorker(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()

	joined := joinWorker(t, ts, cookie, addr)
	id := joined["id"].(string)
	code, got := apiJSON(t, ts, cookie, "POST", "/api/v1/nodes/"+id+"/check", `{}`)
	if code != http.StatusOK || got["reachable"] != false || !strings.Contains(got["error"].(string), "refused") {
		t.Fatalf("a closed agent port must be reported: %d %v", code, got)
	}
	remote, _ := mem.GetRemoteNode(t.Context(), clusterRow.ID, id)
	if remote == nil || remote.Status != ndnet.NodeNotReady || remote.Reason == "" {
		t.Fatalf("an unreachable worker must say why: %+v", remote)
	}
}

func TestPlainTCPAgentIsRefused(t *testing.T) {
	s, mem, token := testServer(t)
	clusterRow, _ := mem.GetCluster(t.Context())
	seedNode(t, mem, clusterRow.ID, debianInv(), false)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)
	// An agent without TLS (or an impostor) on the advertised port.
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	joined := joinWorker(t, ts, cookie, strings.TrimPrefix(plain.URL, "http://"))
	_, got := apiJSON(t, ts, cookie, "POST", "/api/v1/nodes/"+joined["id"].(string)+"/check", `{}`)
	if got["reachable"] != false {
		t.Fatalf("control must not talk to a worker agent without mutual TLS: %v", got)
	}
}
