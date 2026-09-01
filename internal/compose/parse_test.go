package compose_test

import (
	"strings"
	"testing"

	"github.com/no-dal/ndl-ce/internal/compose"
)

func TestParseComposeFixture(t *testing.T) {
	raw := `
services:
  web:
    image: nginx:alpine
    ports:
      - "8080:80"
    environment:
      APP_ENV: prod
    volumes:
      - webdata:/usr/share/nginx/html
  api:
    image: ghcr.io/example/api:1
    environment:
      - LOG_LEVEL=info
    volumes:
      - type: volume
        source: apidata
        target: /var/lib/api
volumes:
  webdata:
  apidata:
`
	got, err := compose.ParseYAML([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("services %d", len(got.Services))
	}
	if got.Services[0].Name != "api" || got.Services[1].Name != "web" {
		t.Fatalf("order %+v", got.Services)
	}
	if len(got.NamedVolumes) != 2 {
		t.Fatalf("named volumes %+v", got.NamedVolumes)
	}
	web := got.Services[1]
	if web.Image != "nginx:alpine" || len(web.Ports) != 1 || web.Ports[0].HostPort != 8080 {
		t.Fatalf("web %+v", web)
	}
	if len(web.Env) != 1 || web.Env[0].Name != "APP_ENV" || web.Env[0].Value != "prod" {
		t.Fatalf("env %+v", web.Env)
	}
	if len(web.Volumes) != 1 || web.Volumes[0].Name != "webdata" {
		t.Fatalf("vols %+v", web.Volumes)
	}
}

func TestRejectPrivilegedAnonymousAndHostRoot(t *testing.T) {
	_, err := compose.ParseYAML([]byte(`
services:
  bad:
    image: busybox:1
    volumes:
      - /data
`))
	if err == nil || !strings.Contains(err.Error(), "anonymous") {
		t.Fatalf("anonymous: %v", err)
	}

	_, err = compose.ParseYAML([]byte(`
services:
  bad:
    image: busybox:1
    volumes:
      - /:/host
`))
	if err == nil || !strings.Contains(err.Error(), "host bind to /") {
		t.Fatalf("host root: %v", err)
	}

	got, err := compose.ParseYAML([]byte(`
services:
  priv:
    image: busybox:1
    privileged: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Services[0].Privileged {
		t.Fatal("privileged flag missing")
	}
}
