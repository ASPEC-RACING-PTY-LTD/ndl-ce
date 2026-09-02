package oci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateRejectsHostRootAndAnonymous(t *testing.T) {
	id := uuid.NewString()
	err := ValidateVolumeMount(VolumeMount{HostPath: "/", ContainerPath: "/data"})
	if err == nil || !strings.Contains(err.Error(), "/") {
		t.Fatalf("expected host / reject, got %v", err)
	}
	err = ValidateVolumeMount(VolumeMount{ContainerPath: "/data"})
	if err == nil {
		t.Fatal("anonymous volume must fail")
	}
	err = ValidateSpec(Spec{
		WorkloadID: id, Name: "app", ImagePin: "example.com/app:1",
		Volumes: []VolumeMount{{HostPath: "/", VolumeID: uuid.NewString(), ContainerPath: "/data"}},
	})
	if err == nil {
		t.Fatal("spec with host / must fail")
	}
}

func TestFakePullWithCreds(t *testing.T) {
	rt := &FakeRuntime{}
	digest, err := rt.Pull(context.Background(), PullRequest{
		Image: "registry.example/private/app:1",
		Creds: &RegistryCreds{Username: "user", Password: "s3cret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("digest")
	}
	creds := rt.LastPullCreds("registry.example/private/app:1")
	if creds == nil || creds.Username != "user" || creds.Password != "s3cret" {
		t.Fatalf("creds not recorded: %+v", creds)
	}
}

func TestEngineCreateSkipHostWritesApplied(t *testing.T) {
	root := t.TempDir()
	fake := &FakeRuntime{Now: func() time.Time { return time.Unix(1, 0).UTC() }}
	e := &Engine{
		DataDir: root, Runtime: fake, SkipHostCmds: true,
		Now: func() time.Time { return time.Unix(1, 0).UTC() },
		Creds: func(string) (*RegistryCreds, error) {
			return &RegistryCreds{Username: "u", Password: "s3cret-pull"}, nil
		},
	}
	id := uuid.NewString()
	res, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "web", ImagePin: "reg.example/app:2", RegistryID: uuid.NewString(),
		Health: &Healthcheck{HTTPPath: "/healthz", Port: 8080},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Health.Status != StatusCollecting {
		t.Fatalf("health %s", res.Health.Status)
	}
	applied, err := e.LastApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if applied.SchemaVersion != LastAppliedSchema {
		t.Fatalf("schema %s", applied.SchemaVersion)
	}
	if applied.Spec.ImagePin != "reg.example/app:2" {
		t.Fatal("image")
	}
	creds := fake.LastPullCreds("reg.example/app:2")
	if creds == nil || creds.Password != "s3cret-pull" {
		t.Fatal("expected pull with stored creds")
	}
	raw, err := os.ReadFile(e.lastAppliedPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cret-pull") || strings.Contains(string(raw), "pull_password") {
		t.Fatalf("last-applied leaked pull password: %s", raw)
	}
}

func TestUnitIndependentOfControlPlane(t *testing.T) {
	name := UnitName(uuid.NewString())
	if !strings.HasPrefix(name, "nodal-oci@") || !strings.HasSuffix(name, ".service") {
		t.Fatalf("unit %s", name)
	}
	if strings.Contains(name, "ndl-control") || strings.Contains(name, "ndl-agent") {
		t.Fatal("unit must not bind to control plane")
	}
}

func TestPullArgvDoesNotPutPasswordOnArgv(t *testing.T) {
	argv, err := PullImageArgv("nodal", "reg.example/app:1", "/var/lib/ndl/secrets/oci")
	if err != nil {
		t.Fatal(err)
	}
	if argv[0] != BinCTR {
		t.Fatal(argv[0])
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, ";") || strings.Contains(joined, "|") {
		t.Fatal("shell metachar")
	}
	if strings.Contains(joined, "--user") || strings.Contains(joined, "p") && strings.Contains(joined, "u:p") {
		t.Fatalf("password must not be on pull argv: %v", argv)
	}
	if !strings.Contains(joined, "--hosts-dir /var/lib/ndl/secrets/oci") {
		t.Fatalf("hosts-dir argv: %v", argv)
	}
}

func TestWriteRegistryHostsMode0600(t *testing.T) {
	dir := t.TempDir()
	got, err := writeRegistryHosts(dir, "reg.example/app:1", "u", "s3cret-pull")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("dir %s", got)
	}
	info, err := os.Stat(filepath.Join(dir, "reg.example", "hosts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(filepath.Join(dir, "reg.example", "hosts.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Basic") {
		t.Fatalf("hosts.toml: %s", raw)
	}
}

func TestContainerdPullAndRunSkipHostCmdsError(t *testing.T) {
	c := &Containerd{SkipHostCmds: true}
	digest, err := c.Pull(context.Background(), PullRequest{Image: "reg.example/app:1"})
	if err == nil || digest != "" || digest == "sha256:skip-host" {
		t.Fatalf("Pull must error unavailable, got digest=%q err=%v", digest, err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("Pull: %v", err)
	}
	if err := c.Run(context.Background(), Spec{WorkloadID: uuid.NewString(), Name: "x", ImagePin: "busybox:latest"}); err == nil {
		t.Fatal("Run must error under SkipHostCmds")
	}
}

func TestTaskStartArgvRejectsRootLocator(t *testing.T) {
	id := uuid.NewString()
	vol := uuid.NewString()
	_, err := TaskStartArgv("nodal", Spec{
		WorkloadID: id, Name: "x", ImagePin: "busybox:latest",
		Volumes:     []VolumeMount{{VolumeID: vol, ContainerPath: "/data"}},
		VolumePaths: map[string]string{vol: "/"},
	})
	if err == nil || !strings.Contains(err.Error(), "/") {
		t.Fatalf("expected root bind reject, got %v", err)
	}
}

func TestTaskStartArgvWiresPortsResourcesBridge(t *testing.T) {
	id := uuid.NewString()
	argv, err := TaskStartArgv("nodal", Spec{
		WorkloadID: id, Name: "web", ImagePin: "busybox:latest",
		Ports:      []Port{{ContainerPort: 80, HostPort: 8080, Protocol: "tcp"}},
		Resources:  Resources{CPUs: 2, MemoryBytes: 512 << 20},
		BridgeName: "ndldeadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--cpus 2") {
		t.Fatalf("cpus: %v", argv)
	}
	if !strings.Contains(joined, "--memory-limit 536870912") {
		t.Fatalf("memory: %v", argv)
	}
	if !strings.Contains(joined, "ndl.port=8080:80/tcp") {
		t.Fatalf("port: %v", argv)
	}
	if !strings.Contains(joined, "ndl.bridge=ndldeadbeef") {
		t.Fatalf("bridge: %v", argv)
	}
	if strings.Contains(joined, "NVIDIA_VISIBLE_DEVICES=all") {
		t.Fatal("NVIDIA_VISIBLE_DEVICES=all is refused")
	}
}

func TestTaskStartArgvRefusesNVIDIAAll(t *testing.T) {
	_, err := TaskStartArgv("nodal", Spec{
		WorkloadID: uuid.NewString(), Name: "g", ImagePin: "busybox:latest",
		Env: []EnvVar{{Name: "NVIDIA_VISIBLE_DEVICES", Value: "all"}},
	})
	if err == nil || !strings.Contains(err.Error(), "NVIDIA_VISIBLE_DEVICES") {
		t.Fatalf("expected NVIDIA all refuse, got %v", err)
	}
}

func TestPrivilegedNotDefault(t *testing.T) {
	spec := Spec{WorkloadID: uuid.NewString(), Name: "x", ImagePin: "busybox:latest"}
	if err := ValidateSpec(spec); err != nil {
		t.Fatal(err)
	}
	if spec.Privileged {
		t.Fatal("privileged must default false")
	}
	argv, err := TaskStartArgv("nodal", spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "--privileged" {
			t.Fatal("privileged flag must not appear by default")
		}
	}
}

func TestEngineCreateContainerdSkipHostCmdsErrors(t *testing.T) {
	e := &Engine{DataDir: t.TempDir(), SkipHostCmds: true}
	_, err := e.Create(context.Background(), Spec{WorkloadID: uuid.NewString(), Name: "x", ImagePin: "busybox:latest"})
	if err == nil {
		t.Fatal("Create via Containerd must error under SkipHostCmds")
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("Create: %v", err)
	}
}

func TestContainerdSkipHostNoFakeHealth(t *testing.T) {
	c := &Containerd{SkipHostCmds: true}
	obs, err := c.Observe(context.Background(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if obs.Status != StatusUnavailable {
		t.Fatalf("status %s", obs.Status)
	}
	if obs.Health.Status != StatusUnavailable {
		t.Fatalf("health %s", obs.Health.Status)
	}
}

func TestApplyGPUDevicesWritesLastApplied(t *testing.T) {
	root := t.TempDir()
	e := &Engine{DataDir: root, Runtime: &FakeRuntime{}, SkipHostCmds: true}
	id := uuid.NewString()
	if _, err := e.Create(context.Background(), Spec{WorkloadID: id, Name: "g", ImagePin: "busybox:latest"}); err != nil {
		t.Fatal(err)
	}
	if err := e.ApplyGPUDevices(id, []string{"/dev/dri/renderD128"}); err != nil {
		t.Fatal(err)
	}
	applied, err := e.LastApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Spec.GPUDevices) != 1 || applied.Spec.GPUDevices[0] != "/dev/dri/renderD128" {
		t.Fatalf("%v", applied.Spec.GPUDevices)
	}
}
