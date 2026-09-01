package lxc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	return &Engine{
		DataDir:      dir,
		SkipHostCmds: true,
		FakeUnpack:   true,
		HTTP:         fakeImageHTTP(t, "alpine/3.21/amd64/default"),
	}
}

func fakeImageHTTP(t *testing.T, pin string) HTTPDoer {
	t.Helper()
	payload := []byte("ndl-fake-rootfs")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])
	idx, _ := json.Marshal(streamsFile{Products: map[string]streamsProduct{
		ProductKey(pin): {Versions: map[string]streamsVersion{
			"20260101_00:00": {Items: map[string]streamsItem{
				"root.tar.xz": {Ftype: "root.tar.xz", SHA256: sha, Path: "images/" + pin + "/rootfs.tar.xz", Size: int64(len(payload))},
			}},
		}},
	}})
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := idx
		if strings.Contains(req.URL.Path, "rootfs.tar.xz") {
			body = payload
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestEnsureTraverseAddsExecute(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ensureTraverse(nested); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 != 0o111 {
		t.Fatalf("want execute bits, got %o", st.Mode())
	}
}

func TestDearmorEmbeddedKey(t *testing.T) {
	ring, err := dearmorPublicKey(lxcImageKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(ring) < 64 {
		t.Fatalf("keyring too short: %d", len(ring))
	}
}

func TestHostMapStart(t *testing.T) {
	if hostMapStart(DefaultUIDMap) != 100000 {
		t.Fatal(hostMapStart(DefaultUIDMap))
	}
	if hostMapStart("u 0 200000 65536") != 200000 {
		t.Fatal(hostMapStart("u 0 200000 65536"))
	}
}

func TestEnsureGuestDHCP(t *testing.T) {
	root := t.TempDir()
	if err := ensureGuestDHCP(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "etc", "network", "interfaces"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "iface eth0 inet dhcp") {
		t.Fatal(string(b))
	}
	if err := ensureGuestDHCP(root); err != nil {
		t.Fatal(err)
	}
}

func TestPinReject(t *testing.T) {
	if err := ValidatePin("alpine/edge/amd64/default"); err == nil {
		t.Fatal("edge pin must be rejected")
	}
	if err := ValidatePin("alpine/3.21/amd64/default"); err != nil {
		t.Fatal(err)
	}
}

func TestMACStableAndLocallyAdministered(t *testing.T) {
	id := "086be497-e232-4d69-8bb3-0423c31ba734"
	a := MACFromUUID(id)
	b := MACFromUUID(id)
	if a != b {
		t.Fatalf("mac not stable %s %s", a, b)
	}
	if a == MACFromUUID(uuid.NewString()) {
		t.Fatal("different uuid must not share mac")
	}
	first, _ := hex.DecodeString(strings.ReplaceAll(a, ":", "")[:2])
	if first[0]&0x01 != 0 {
		t.Fatal("mac must be unicast")
	}
	if first[0]&0x02 == 0 {
		t.Fatal("mac must be locally administered")
	}
}

func TestUnprivilegedConfigHasIDMap(t *testing.T) {
	cfg := RenderConfig(Spec{
		WorkloadID: uuid.NewString(), Name: "ct", RootfsPath: "/vol/root",
		CPUs: 1, MemoryBytes: DefaultMemoryBytes, BridgeName: "ndldeadbeef",
		MAC: "02:00:00:00:00:01", Privileged: false,
	})
	if !strings.Contains(cfg, "lxc.idmap = u 0 100000 65536") {
		t.Fatal(cfg)
	}
	if !strings.Contains(cfg, "lxc.idmap = g 0 100000 65536") {
		t.Fatal(cfg)
	}
	if !strings.Contains(cfg, "lxc.cgroup2.memory.max") || !strings.Contains(cfg, "lxc.cgroup2.cpu.max") {
		t.Fatal(cfg)
	}
}

func TestPrivilegedConfigOmitsIDMap(t *testing.T) {
	cfg := RenderConfig(Spec{
		WorkloadID: uuid.NewString(), Name: "ct", RootfsPath: "/vol/root",
		Privileged: true, BridgeName: "ndldeadbeef",
	})
	if strings.Contains(cfg, "lxc.idmap") {
		t.Fatal(cfg)
	}
}

func TestDryCreateWritesLastApplied(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	vol := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", id)
	res, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "alpine", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: vol, RootfsPath: root, BridgeName: "ndldeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.VolumeID != vol {
		t.Fatalf("volume %s", res.VolumeID)
	}
	if _, err := os.Stat(e.lastAppliedPath(id)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, RootfsMarker)); err != nil {
		t.Fatal("fake unpack must write marker")
	}
	if !res.ImageVerified {
		t.Fatal("image must be sha256 verified")
	}
}

func TestObserveMissingIsUnavailable(t *testing.T) {
	e := testEngine(t)
	obs, err := e.Observe(context.Background(), []Hint{{WorkloadID: uuid.NewString(), Kind: KindSystemContainer}})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Workloads) != 1 || obs.Workloads[0].Status != StatusUnavailable {
		t.Fatalf("%+v", obs)
	}
	if obs.Workloads[0].Reason == "" {
		t.Fatal("unavailable must include a reason")
	}
}

func TestCloneNewUUID(t *testing.T) {
	e := testEngine(t)
	src := uuid.NewString()
	vol := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", src)
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: src, Name: "src", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: vol, RootfsPath: root,
	}); err != nil {
		t.Fatal(err)
	}
	cloneID := uuid.NewString()
	cloneVol := uuid.NewString()
	cloneRoot := filepath.Join(e.DataDir, "rootfs", cloneID)
	res, err := e.Clone(context.Background(), LifecycleRequest{
		WorkloadID: src, Action: "clone", CloneID: cloneID,
		CloneVolumeID: cloneVol, CloneRootfsPath: cloneRoot, CloneName: "dst",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.WorkloadID != cloneID || res.WorkloadID == src {
		t.Fatalf("clone id %s", res.WorkloadID)
	}
	if res.VolumeID != cloneVol {
		t.Fatal(res.VolumeID)
	}
	if res.MAC == MACFromUUID(src) {
		t.Fatal("clone mac must follow the new uuid")
	}
}

func TestCreateIdempotentSameWorkloadID(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	vol1 := uuid.NewString()
	vol2 := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", id)
	first, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: vol1, RootfsPath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "ct", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: vol2, RootfsPath: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.VolumeID != vol1 || second.VolumeID != vol1 {
		t.Fatalf("second create allocated another volume: %s %s", first.VolumeID, second.VolumeID)
	}
	applied, err := e.readApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Spec.VolumeID != vol1 {
		t.Fatal(applied.Spec.VolumeID)
	}
}
