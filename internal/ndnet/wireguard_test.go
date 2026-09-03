package ndnet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestWGNameLength(t *testing.T) {
	name, err := WGName(uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if len(name) > 15 || !strings.HasPrefix(name, "ndlw") {
		t.Fatalf("iface %s", name)
	}
}

func TestWGKeysRoundTrip(t *testing.T) {
	priv, pub, err := GenerateWGKey()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidWGPublicKey(pub) {
		t.Fatal(pub)
	}
	derived, err := PublicFromPrivate(priv)
	if err != nil || derived != pub {
		t.Fatalf("derived=%s pub=%s err=%v", derived, pub, err)
	}
}

func TestWGApplySkipHostCmdsUnavailable(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	_, peerPub, err := GenerateWGKey()
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: id, ListenPort: 51820, AddressCIDR: "10.64.8.1/24",
		Peers: []WGPeerSpec{{PublicKey: peerPub, AllowedIPs: "10.64.8.2/32", Endpoint: "203.0.113.8:51820", PersistentKeepalive: 25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnavailable || res.LastHandshakeUnix != 0 || !strings.Contains(res.Reason, "handshake") {
		t.Fatalf("%+v", res)
	}
	if res.PublicKey == "" || res.Locator == "" {
		t.Fatal("public key and locator required")
	}
	body := ""
	for _, f := range res.Files {
		body += f.Body
		if strings.Contains(f.Body, "PrivateKey=") && !strings.Contains(f.Body, "PrivateKeyFile=") {
			t.Fatal("private key in netdev")
		}
	}
	if !strings.Contains(body, "Kind=wireguard") || !strings.Contains(body, "PrivateKeyFile=") || !strings.Contains(body, "PublicKey="+peerPub) {
		t.Fatal(body)
	}
	raw, err := os.ReadFile(res.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(res.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("perm %v", st.Mode())
	}
	if strings.Contains(body, strings.TrimSpace(string(raw))) {
		t.Fatal("local private key leaked into netdev")
	}
}

func TestWGApplyRefusesInvalidPortAndAddress(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	_, peerPub, err := GenerateWGKey()
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: id, ListenPort: 70000, AddressCIDR: "10.64.8.1/24",
		Peers: []WGPeerSpec{{PublicKey: peerPub, AllowedIPs: "10.64.8.2/32"}},
	})
	if err == nil || !strings.Contains(err.Error(), "wireguard listen port is invalid") {
		t.Fatalf("port: %v", err)
	}
	_, err = e.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: id, ListenPort: 51820, AddressCIDR: "not-a-cidr",
		Peers: []WGPeerSpec{{PublicKey: peerPub, AllowedIPs: "10.64.8.2/32"}},
	})
	if err == nil || !strings.Contains(err.Error(), "wireguard address must be CIDR") {
		t.Fatalf("addr: %v", err)
	}
}

func TestValidWGEndpointRefusesCredentials(t *testing.T) {
	if err := ValidWGEndpoint("203.0.113.8:51820"); err != nil {
		t.Fatal(err)
	}
	if err := ValidWGEndpoint(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidWGEndpoint("user@203.0.113.8:51820"); err == nil {
		t.Fatal("userinfo")
	}
	if err := ValidWGEndpoint("https://203.0.113.8:51820"); err == nil {
		t.Fatal("url")
	}
	_, peerPub, err := GenerateWGKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWGPeer(WGPeerSpec{PublicKey: peerPub, Endpoint: "user:SECRET@203.0.113.8:51820"}); err == nil {
		t.Fatal("userinfo peer")
	}
}

func TestValidListenAddrRefusesCredentials(t *testing.T) {
	if err := ValidListenAddr("10.64.8.2:9444"); err != nil {
		t.Fatal(err)
	}
	if err := ValidListenAddr(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidListenAddr("user@10.64.8.2:9444"); err == nil {
		t.Fatal("userinfo")
	}
	if err := ValidListenAddr("https://10.64.8.2:9444"); err == nil {
		t.Fatal("url")
	}
}

func TestValidReplicaEndpointRefusesDSN(t *testing.T) {
	if err := ValidReplicaEndpoint("postgres-replica:5432"); err != nil {
		t.Fatal(err)
	}
	if err := ValidReplicaEndpoint("postgresql://postgres-replica/nodal"); err == nil {
		t.Fatal("dsn")
	}
	if err := ValidReplicaEndpoint("postgresql://ndl:secret-pass@postgres-replica/nodal"); err == nil {
		t.Fatal("userinfo dsn")
	}
}

func TestWGLoopbackHandshakeReady(t *testing.T) {
	a := testEngine(t, testHost())
	b := testEngine(t, testHost())
	idA := uuid.NewString()
	idB := uuid.NewString()
	now := time.Unix(1_700_000_000, 0).UTC()
	a.Now = func() time.Time { return now }
	b.Now = func() time.Time { return now }
	a.Handshake = func(string) (int64, error) { return now.Unix() - 10, nil }
	b.Handshake = func(string) (int64, error) { return now.Unix() - 10, nil }

	resA, err := a.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: idA, AddressCIDR: "10.64.8.1/24",
	})
	if err != nil {
		t.Fatal(err)
	}
	resB, err := b.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: idB, AddressCIDR: "10.64.8.2/24",
		Peers: []WGPeerSpec{{PublicKey: resA.PublicKey, AllowedIPs: "10.64.8.1/32", PersistentKeepalive: 25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resA, err = a.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: idA, AddressCIDR: "10.64.8.1/24",
		Peers: []WGPeerSpec{{PublicKey: resB.PublicKey, AllowedIPs: "10.64.8.2/32", PersistentKeepalive: 25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resA.Status != StatusAvailable || resB.Status != StatusAvailable {
		t.Fatalf("a=%s b=%s", resA.Status, resB.Status)
	}
	if !RemoteReady(resA.LastHandshakeUnix, now.Unix()-5, now.Unix()) {
		t.Fatal("loopback session should be Ready")
	}
}

func TestWGPrivateKeyNotInUnitPath(t *testing.T) {
	if err := refuseKeyInUnit("/etc/systemd/network/50-ndl-x-wg.netdev"); err == nil {
		t.Fatal("expected refuse")
	}
}

func TestParseLatestHandshakes(t *testing.T) {
	out := "abcd+/1234567890abcdefghijklmnopqrstuvw=\t1700000000\n"
	if parseLatestHandshakes(out) != 1700000000 {
		t.Fatal(parseLatestHandshakes(out))
	}
	if parseLatestHandshakes("key\t0\n") != 0 {
		t.Fatal("zero handshake")
	}
}

func TestRemoteReadyHonesty(t *testing.T) {
	now := int64(1_700_000_180)
	if RemoteReady(0, now, now) {
		t.Fatal("handshake 0 must not be Ready")
	}
	if RemoteReady(now, 0, now) {
		t.Fatal("missing session must not be Ready")
	}
	if RemoteReady(now-HandshakeFreshSecs-1, now, now) {
		t.Fatal("stale handshake")
	}
	if !RemoteReady(now-10, now-10, now) {
		t.Fatal("fresh pair must be Ready")
	}
}

func TestWGKeyFileNotUnderSystemd(t *testing.T) {
	e := testEngine(t, testHost())
	id := uuid.NewString()
	_, pub, _ := GenerateWGKey()
	res, err := e.ApplyWireGuard(context.Background(), WGOp{
		Action: ActionWGApply, PeerID: id, Peers: []WGPeerSpec{{PublicKey: pub, AllowedIPs: "10.64.8.2/32"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filepath.Dir(res.PrivateKeyPath), "systemd") {
		t.Fatal(res.PrivateKeyPath)
	}
}
