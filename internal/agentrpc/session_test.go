package agentrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/internal/identity"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestOpenSessionAccepted(t *testing.T) {
	h := &Handler{Ident: identity.Files{Dir: t.TempDir()}, SkipHostCmds: true}
	res, err := h.OpenSession(context.Background(), connect.NewRequest(&agentv1.OpenSessionRequest{
		NodeId: "n1", ClusterId: "c1", ListenAddr: "10.64.8.2:9444",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Msg.GetAccepted() || res.Msg.GetSessionId() == "" {
		t.Fatalf("%+v", res.Msg)
	}
}

func TestApplyDesiredSessionDialsControl(t *testing.T) {
	var got map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cluster/sessions" {
			http.NotFound(w, r)
			return
		}
		got = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer ts.Close()

	root := t.TempDir()
	eng := &ndnet.Engine{
		Root: root, NetworkDir: filepath.Join(root, "etc/systemd/network"),
		StateDir: filepath.Join(root, "var/lib/ndl/net"), SecretDir: filepath.Join(root, "secrets"),
		SkipHostCmds: true,
	}
	h := &Handler{Ident: identity.Files{Dir: root}, Nets: eng, SkipHostCmds: true}
	id := uuid.NewString()
	_, pub, err := ndnet.GenerateWGKey()
	if err != nil {
		t.Fatal(err)
	}
	desired := WGDesired{
		PeerID: id, ListenPort: 51820, AddressCIDR: "10.64.8.2/24",
		ControlURL: ts.URL, PairingToken: "tok",
		Peers: []ndnet.WGPeerSpec{{PublicKey: pub, AllowedIPs: "10.64.8.1/32", PersistentKeepalive: 25}},
	}
	raw, _ := json.Marshal(desired)
	if err := os.MkdirAll(filepath.Dir(DesiredWGPath(root)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DesiredWGPath(root), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyDesiredSession(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got["peer_id"] != id || got["pairing_token"] != "tok" {
		t.Fatalf("%v", got)
	}
	if got["handshake_unix"] != float64(0) {
		t.Fatalf("SkipHostCmds must report handshake 0: %v", got)
	}
	if err := h.ApplyDesiredSession(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if got["pairing_token"] != nil && got["pairing_token"] != "" {
		t.Fatalf("pairing token must be stripped after first success: %v", got)
	}
}
