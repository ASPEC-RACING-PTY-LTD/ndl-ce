package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/appdb"
	"github.com/no-dal/ndl-ce/internal/edge"
	"github.com/no-dal/ndl-ce/internal/features"
	"github.com/no-dal/ndl-ce/internal/shield"
)

// fakeNode is a node's agent: edge forwarding keeps the last applied spec
// and reports a public key per tunnel; DDoS protection is a fakeShield.
type fakeNode struct {
	*fakeShield
	name  string
	mu    sync.Mutex
	spec  *edge.Spec
	calls []string
	err   error
}

func (f *fakeNode) key(tunnel, role string) string {
	sum := sha256.Sum256([]byte(f.name + tunnel + role))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (f *fakeNode) Edge(_ context.Context, action string, spec *edge.Spec) (edge.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action)
	if f.err != nil {
		return edge.Status{}, f.err
	}
	switch action {
	case "apply":
		norm, err := edge.Normalize(*spec)
		if err != nil {
			return edge.Status{}, err
		}
		f.spec = &norm
	case "disable":
		f.spec = nil
	}
	st := edge.Status{Tunnels: map[string]edge.TunnelStatus{}, Forwards: map[string]edge.Counters{}}
	if f.spec != nil {
		st.Active, st.SpecHash = true, f.spec.Hash()
		for _, t := range f.spec.Tunnels {
			ts := edge.TunnelStatus{Interface: edge.IfName(t.ID), Role: t.Role, PublicKey: f.key(t.ID, t.Role), Up: true}
			if t.PeerKey != "" {
				ts.Handshake = time.Now().UTC()
			}
			st.Tunnels[t.ID] = ts
			for _, fw := range t.Forwards {
				st.Forwards[t.ID+"/"+fw.ID] = edge.Counters{Packets: 10, Bytes: 1000}
			}
		}
	}
	return st, nil
}

func (f *fakeNode) edgeSpec() *edge.Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spec
}

func TestEdgeForwardsGameServerThroughVPS(t *testing.T) {
	ctx := context.Background()
	s, mem, token := testServer(t)
	cluster, _ := mem.GetCluster(ctx)
	home, vps := uuid.NewString(), uuid.NewString()
	_ = mem.UpsertNode(ctx, appdb.Node{ID: home, ClusterID: cluster.ID, Name: "local", Role: "control"})
	_ = mem.UpsertNode(ctx, appdb.Node{ID: vps, ClusterID: cluster.ID, Name: "r-server71377-llv", Role: "worker"})
	if err := mem.CreateRemoteNode(ctx, appdb.RemoteNode{ID: vps, ClusterID: cluster.ID, Name: "r-server71377-llv", ListenAddr: "45.38.149.183:9444"}); err != nil {
		t.Fatal(err)
	}
	homeShield := &fakeShield{now: time.Now()}
	homeAgent := &fakeNode{fakeShield: homeShield, name: "home"}
	vpsAgent := &fakeNode{fakeShield: &fakeShield{now: time.Now()}, name: "vps"}
	s.Shield = homeShield
	s.NodeAgents = map[string]NodeAgent{home: homeAgent, vps: vpsAgent}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	cookie := claimAdmin(t, ts, token)

	_ = mem.UpsertFeature(ctx, appdb.Feature{ClusterID: cluster.ID, ID: features.IDGameServers, Enabled: true, UpdatedAt: time.Now()})
	ports, _ := json.Marshal([]map[string]any{
		{"name": "game", "container_port": 27015, "host_port": 27015, "protocol": "udp", "primary": true},
		{"name": "rcon", "container_port": 27015, "host_port": 27016, "protocol": "tcp"},
	})
	gsID := uuid.NewString()
	if err := mem.CreateGameServer(ctx, appdb.GameServer{ID: gsID, ClusterID: cluster.ID, NodeID: home, Name: "JvS", Family: "source", PortsJSON: ports}); err != nil {
		t.Fatal(err)
	}

	// Publishing on the edge's own node is refused, a reserved port too.
	code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/edge/forwards", `{"kind":"host","edge_node_id":"`+vps+`","origin_node_id":"`+home+`","ports":[{"protocol":"tcp","port":22}]}`)
	if code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "SSH") {
		t.Fatalf("ssh forward = %d %v", code, out)
	}
	code, out = jsonCall(t, ts, cookie, "POST", "/api/v1/edge/forwards", `{"kind":"gameserver","ref_id":"`+gsID+`","edge_node_id":"`+vps+`"}`)
	if code != http.StatusOK {
		t.Fatalf("create %d %v", code, out)
	}
	s.tickEdge(ctx)

	es, os := vpsAgent.edgeSpec(), homeAgent.edgeSpec()
	if es == nil || os == nil || len(es.Tunnels) != 1 || len(os.Tunnels) != 1 {
		t.Fatalf("specs: edge %+v origin %+v", es, os)
	}
	et, ot := es.Tunnels[0], os.Tunnels[0]
	// Only the game port goes through: the Source RCON port stays home.
	if et.Role != edge.RoleEdge || et.ListenPort != 51871 || et.Address != "10.66.0.5/30" || et.Peer != "10.66.0.6" ||
		len(et.Forwards) != 1 || et.Forwards[0].Protocol != "udp" || et.Forwards[0].Port != 27015 {
		t.Fatalf("edge tunnel = %+v", et)
	}
	if ot.Role != edge.RoleOrigin || ot.Endpoint != "45.38.149.183:51871" || ot.Address != "10.66.0.6/30" ||
		strings.Join(ot.Egress, ",") != "ndl-gs-"+gsID || len(ot.Forwards) != 1 || ot.Forwards[0].Target != "" {
		t.Fatalf("origin tunnel = %+v", ot)
	}
	// Each side has the other's public key after one tick.
	if et.PeerKey != homeAgent.key(et.ID, edge.RoleOrigin) || ot.PeerKey != vpsAgent.key(ot.ID, edge.RoleEdge) {
		t.Fatalf("keys: edge has %q, origin has %q", et.PeerKey, ot.PeerKey)
	}
	// A second tick changes nothing.
	applies := strings.Count(strings.Join(vpsAgent.calls, ","), "apply")
	s.tickEdge(ctx)
	if n := strings.Count(strings.Join(vpsAgent.calls, ","), "apply"); n != applies {
		t.Fatalf("re-applied an unchanged spec: %v", vpsAgent.calls)
	}

	code, out = jsonCall(t, ts, cookie, "GET", "/api/v1/edge", "")
	if code != http.StatusOK {
		t.Fatalf("get %d %v", code, out)
	}
	fw := out["forwards"].([]any)[0].(map[string]any)
	tn := out["tunnels"].([]any)[0].(map[string]any)
	if fw["public_addr"] != "45.38.149.183" || fw["active"] != true || fw["ref_name"] != "JvS" || fw["packets"].(float64) != 10 || tn["connected"] != true {
		t.Fatalf("view: forward %v tunnel %v", fw, tn)
	}

	// DDoS protection follows the game server to the edge, on its public
	// port, without trusting the VPS's neighbours.
	if code, out := jsonCall(t, ts, cookie, "POST", "/api/v1/features/ddos/enable", `{}`); code != http.StatusOK {
		t.Fatalf("enable ddos %d %v", code, out)
	}
	s.tickDDoS(ctx)
	r := vpsAgent.rule(ddosAutoPrefix + gsID)
	if r == nil || !r.Host || r.Protocol != shield.ProtoUDP || strings.Join(r.Ports, ",") != "27015" || vpsAgent.fakeShield.spec.TrustLocal {
		t.Fatalf("edge ddos rule = %+v", r)
	}
	if code, out = jsonCall(t, ts, cookie, "GET", "/api/v1/ddos", ""); code != http.StatusOK {
		t.Fatalf("ddos view %d %v", code, out)
	}
	edges := out["edges"].([]any)
	if len(edges) != 1 || edges[0].(map[string]any)["name"] != "r-server71377-llv" || edges[0].(map[string]any)["active"] != true {
		t.Fatalf("ddos edges = %v", edges)
	}
	if code, _ := jsonCall(t, ts, cookie, "POST", "/api/v1/ddos/bans", `{"addr":"203.0.113.9","minutes":10}`); code != http.StatusOK {
		t.Fatalf("ban %d", code)
	}
	if !strings.Contains(strings.Join(vpsAgent.fakeShield.calls, ","), "ban 203.0.113.9") {
		t.Fatalf("ban did not reach the edge: %v", vpsAgent.fakeShield.calls)
	}

	// An agent too old for edge forwarding says so.
	vpsAgent.err = errors.New("unknown execute method")
	s.applyEdgeNow(ctx)
	_, out = jsonCall(t, ts, cookie, "GET", "/api/v1/edge", "")
	for _, n := range out["nodes"].([]any) {
		n := n.(map[string]any)
		if n["id"] == vps && n["error"] != edgeOldAgent {
			t.Fatalf("old agent error = %v", n["error"])
		}
	}
	vpsAgent.err = nil

	// Removing the forward takes the tunnel down on both nodes and the
	// edge's DDoS rules with it.
	id := fw["id"].(string)
	if code, out := jsonCall(t, ts, cookie, "DELETE", "/api/v1/edge/forwards/"+id, ""); code != http.StatusOK {
		t.Fatalf("delete %d %v", code, out)
	}
	s.tickEdge(ctx)
	s.tickDDoS(ctx)
	if vpsAgent.edgeSpec() != nil || homeAgent.edgeSpec() != nil {
		t.Fatalf("tunnels left: %+v %+v", vpsAgent.edgeSpec(), homeAgent.edgeSpec())
	}
	if vpsAgent.fakeShield.spec != nil {
		t.Fatalf("edge ddos rules left: %+v", vpsAgent.fakeShield.spec)
	}
	cfg, _ := s.loadEdge(ctx, cluster.ID)
	if len(cfg.Tunnels) != 0 {
		t.Fatalf("tunnel kept: %+v", cfg.Tunnels)
	}
}
