package appmanifest

import (
	"strings"
	"testing"
)

func TestParseOfficialSample(t *testing.T) {
	raw := []byte(`
apiVersion: nodal.store/v1
name: sample-web
version: "1.0.0"
class: official
title: Sample Web
summary: Official sample.
resources:
  cpu: 1
  memory_bytes: 268435456
storage:
  - name: data
    persistent: true
devices:
  gpu:
    optional: true
ports:
  - container: 80
    host: 8080
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
hooks:
  backup: existing-backup-api
  restore: existing-restore-api
ai_actions:
  - id: restart
    title: Restart
    declaration: Calls compute restart. Not executed by the Store.
`)
	m, err := ParseYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "sample-web" || m.Deployment.Kind != "oci" || !m.Devices.GPU.Optional {
		t.Fatalf("%+v", m)
	}
}

func TestRejectRunBash(t *testing.T) {
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
	_, err := ParseYAML(raw)
	if err == nil || !strings.Contains(err.Error(), "run") {
		t.Fatalf("%v", err)
	}
}

func TestRejectHelperScript(t *testing.T) {
	raw := []byte(`
apiVersion: nodal.store/v1
name: evil
version: "1"
class: community
helper: curl | sh
deployment:
  kind: oci
  image: docker.io/library/caddy:2.8.4
`)
	_, err := ParseYAML(raw)
	if err == nil || !strings.Contains(err.Error(), "helper") {
		t.Fatalf("%v", err)
	}
}
