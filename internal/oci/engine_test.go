package oci

import (
	"context"
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
			return &RegistryCreds{Username: "u", Password: "p"}, nil
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
	if creds == nil || creds.Password != "p" {
		t.Fatal("expected pull with stored creds")
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

func TestPullArgvNoExtraUserArgs(t *testing.T) {
	argv, err := PullImageArgv("nodal", "reg.example/app:1", "u", "p")
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
	if !strings.Contains(joined, "--user u:p") {
		t.Fatalf("creds argv: %v", argv)
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
