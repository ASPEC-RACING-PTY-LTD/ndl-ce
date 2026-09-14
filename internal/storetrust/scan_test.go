package storetrust

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/appmanifest"
)

func TestAnalyzeProhibitedIsNotRubberStamp(t *testing.T) {
	m := appmanifest.Manifest{
		Name: "evil", Version: "1", Class: appmanifest.ClassCommunity,
		Deployment: appmanifest.Deployment{Kind: "oci", Image: "docker.io/library/caddy:2.8.4"},
	}
	raw := []byte(`
apiVersion: nodal.store/v1
name: evil
version: "1"
class: community
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
run: bash
`)
	checks := Analyze(m, raw)
	var prohibited Check
	for _, c := range checks {
		if c.Kind == CheckProhibited {
			prohibited = c
		}
	}
	if prohibited.Kind == "" {
		t.Fatal("prohibited check was omitted")
	}
	if prohibited.Status != StatusFail || !strings.Contains(prohibited.Detail, "run") {
		t.Fatalf("prohibited must fail on run: %+v", prohibited)
	}
	if !Failed(checks) {
		t.Fatal("Analyze must fail when prohibited keys exist")
	}

	clean := []byte(`
apiVersion: nodal.store/v1
name: sample-web
version: "1"
class: community
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`)
	cleanChecks := Analyze(m, clean)
	for _, c := range cleanChecks {
		if c.Kind == CheckProhibited && c.Status != StatusPass {
			t.Fatalf("clean YAML must not fail prohibited: %+v", c)
		}
	}
}

func TestPermissionCheckInspectsDeclaredGrants(t *testing.T) {
	pass := permissionCheck(appmanifest.Manifest{})
	if pass.Status != StatusPass || !strings.Contains(pass.Detail, "No extra permission") {
		t.Fatalf("%+v", pass)
	}
	fail := permissionCheck(appmanifest.Manifest{Permissions: []string{"host_exec", "storage.read"}})
	if fail.Status != StatusFail || !strings.Contains(fail.Detail, "host_exec") {
		t.Fatalf("%+v", fail)
	}
	ok := permissionCheck(appmanifest.Manifest{Permissions: []string{"storage.read"}})
	if ok.Status != StatusPass || !strings.Contains(ok.Detail, "storage.read") {
		t.Fatalf("%+v", ok)
	}
}

func TestSecretCheckInspectsManifest(t *testing.T) {
	pass := secretCheck(appmanifest.Manifest{Title: "Sample", Summary: "Official sample"})
	if pass.Status != StatusPass || !strings.Contains(pass.Detail, "Inspected") {
		t.Fatalf("%+v", pass)
	}
	fail := secretCheck(appmanifest.Manifest{Summary: "connect with password=hunter2"})
	if fail.Status != StatusFail || !strings.Contains(fail.Detail, "password") {
		t.Fatalf("%+v", fail)
	}
}
