package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type proxyHost struct {
	cmds     []string
	validate error
	active   string
}

func (h *proxyHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	h.cmds = append(h.cmds, cmd)
	switch {
	case strings.Contains(cmd, " validate ") || strings.HasSuffix(cmd, "-t -q"):
		if h.validate != nil {
			return []byte("Error: bad config"), h.validate
		}
		return []byte("Valid configuration"), nil
	case strings.Contains(cmd, "ActiveState"):
		return []byte(h.active + "\n"), nil
	case strings.HasSuffix(cmd, " version"):
		return []byte("v2.6.2 h1:abc\n"), nil
	}
	return nil, nil
}

func proxyEngine(t *testing.T, files map[string]string) (*Engine, *proxyHost, string) {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, files)
	h := &proxyHost{active: "active"}
	return &Engine{HostRoot: root, HostRun: h.run}, h, root
}

func TestNginxSitesAndIncludes(t *testing.T) {
	e, _, _ := proxyEngine(t, map[string]string{
		"/usr/sbin/nginx": "",
		"/etc/nginx/nginx.conf": `user www-data;
events { worker_connections 768; }
http {
	include /etc/nginx/mime.types;
	include /etc/nginx/conf.d/*.conf;
	include /etc/nginx/sites-enabled/*;
}`,
		"/etc/nginx/sites-enabled/app": `server {
	listen 80;
	server_name app.example.com www.app.example.com; # comment
	location /api { proxy_pass http://10.0.3.9:9000; }
	location / { proxy_pass http://10.0.3.9:8080; }
}
server { listen 443 ssl; server_name secure.example.com; ssl_certificate /x.pem; location / { proxy_pass https://up:443; } }`,
	})
	res, err := e.Proxy(context.Background(), ProxyRequest{Action: ProxyScan, Kind: ProxyNginx})
	if err != nil || len(res.Instances) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	in := res.Instances[0]
	if !in.Included || !in.Active || len(in.Sites) != 2 {
		t.Fatalf("instance %+v", in)
	}
	if s := in.Sites[0]; s.Upstream != "http://10.0.3.9:8080" || len(s.Domains) != 2 || s.TLS != ProxyTLSHTTP || s.Managed {
		t.Fatalf("site %+v", s)
	}
	if s := in.Sites[1]; s.TLS != ProxyTLSCert || s.Upstream != "https://up:443" {
		t.Fatalf("tls site %+v", s)
	}
}

func TestCaddyApplyAddsImportValidatesAndRollsBack(t *testing.T) {
	e, h, root := proxyEngine(t, map[string]string{
		"/usr/bin/caddy": "",
		"/etc/caddy/Caddyfile": `{
	email ops@example.com
}

(common) {
	encode gzip
}

old.example.com, www.old.example.com {
	import common
	reverse_proxy /api/* 10.0.3.5:9000
	reverse_proxy 10.0.3.5:8080
}
`,
	})
	ctx := context.Background()
	site := &ProxySite{Domains: []string{"New.Example.com"}, Upstream: "10.0.3.7:3000", TLS: ProxyTLSInternal}
	res, err := e.Proxy(ctx, ProxyRequest{Action: ProxyApply, Kind: ProxyCaddy, Site: site})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Site == nil || res.Site.Name != "new.example.com" {
		t.Fatalf("%+v", res)
	}
	body, _ := os.ReadFile(filepath.Join(root, "/etc/caddy/ndl.d/new.example.com.caddy"))
	if !strings.HasPrefix(string(body), proxyMarker) || !strings.Contains(string(body), "new.example.com {\n\ttls internal\n\treverse_proxy http://10.0.3.7:3000\n}") {
		t.Fatalf("site file:\n%s", body)
	}
	cf, _ := os.ReadFile(filepath.Join(root, "/etc/caddy/Caddyfile"))
	if !strings.HasSuffix(string(cf), "import /etc/caddy/ndl.d/*.caddy\n") || !strings.HasPrefix(string(cf), "{\n\temail") {
		t.Fatalf("Caddyfile:\n%s", cf)
	}
	if !strings.Contains(strings.Join(h.cmds, "\n"), "systemctl reload caddy") {
		t.Fatalf("no reload: %v", h.cmds)
	}
	// The scan shows the file site and the managed site.
	scan, _ := e.Proxy(ctx, ProxyRequest{Action: ProxyScan, Kind: ProxyCaddy})
	sites := scan.Instances[0].Sites
	if len(sites) != 2 || sites[0].Upstream != "10.0.3.5:8080" || len(sites[0].Domains) != 2 || !sites[1].Managed || sites[1].TLS != ProxyTLSInternal {
		t.Fatalf("scan %+v", sites)
	}
	// A domain another site serves is refused.
	if _, err := e.Proxy(ctx, ProxyRequest{Action: ProxyApply, Kind: ProxyCaddy, Site: &ProxySite{Domains: []string{"old.example.com"}, Upstream: "10.0.0.1:80"}}); err == nil {
		t.Fatal("duplicate domain accepted")
	}
	// A change the validator rejects is put back.
	h.validate = errors.New("exit 1")
	site.Upstream = "10.0.3.8:3000"
	if _, err := e.Proxy(ctx, ProxyRequest{Action: ProxyApply, Kind: ProxyCaddy, Site: site}); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("expected rejection: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "/etc/caddy/ndl.d/new.example.com.caddy"))
	if string(after) != string(body) {
		t.Fatalf("rollback failed:\n%s", after)
	}
	// Remove only touches managed files.
	h.validate = nil
	if _, err := e.Proxy(ctx, ProxyRequest{Action: ProxyRemove, Kind: ProxyCaddy, Name: "new.example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "/etc/caddy/ndl.d/new.example.com.caddy")); !os.IsNotExist(err) {
		t.Fatal("managed file not removed")
	}
}

func TestNginxApplyRendersAndNeedsInclude(t *testing.T) {
	e, _, root := proxyEngine(t, map[string]string{
		"/usr/sbin/nginx":       "",
		"/etc/nginx/nginx.conf": "events {}\nhttp {\n\tinclude conf.d/*.conf;\n}\n",
		"/etc/ssl/a.pem":        "x",
		"/etc/ssl/a.key":        "x",
	})
	ctx := context.Background()
	_, err := e.Proxy(ctx, ProxyRequest{Action: ProxyApply, Kind: ProxyNginx, Site: &ProxySite{
		Domains: []string{"shop.example.com"}, Upstream: "http://10.0.3.4:8080", TLS: ProxyTLSCert, CertPath: "/etc/ssl/a.pem", KeyPath: "/etc/ssl/a.key", Websocket: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(root, "/etc/nginx/conf.d/ndl-shop.example.com.conf"))
	for _, want := range []string{"return 301 https://$host$request_uri;", "listen 443 ssl;", "ssl_certificate /etc/ssl/a.pem;", "proxy_pass http://10.0.3.4:8080;", "proxy_set_header Upgrade $http_upgrade;"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %q in\n%s", want, body)
		}
	}
	scan, _ := e.Proxy(ctx, ProxyRequest{Action: ProxyScan, Kind: ProxyNginx})
	var managed []ProxySite
	for _, s := range scan.Instances[0].Sites {
		if s.Managed {
			managed = append(managed, s)
		}
	}
	if len(managed) != 2 || managed[1].CertPath != "/etc/ssl/a.pem" || !managed[1].Websocket {
		t.Fatalf("managed %+v", managed)
	}
	// Without the include nothing is written.
	writeTree(t, root, map[string]string{"/etc/nginx/nginx.conf": "events {}\nhttp {}\n"})
	if _, err := e.Proxy(ctx, ProxyRequest{Action: ProxyApply, Kind: ProxyNginx, Site: &ProxySite{Domains: []string{"x.example.com"}, Upstream: "10.0.0.2:80"}}); err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("expected include error: %v", err)
	}
}

func TestCleanProxySiteRefusesInjection(t *testing.T) {
	bad := []ProxySite{
		{Domains: []string{"a.com { }"}, Upstream: "10.0.0.1:80"},
		{Domains: []string{"a.com;"}, Upstream: "10.0.0.1:80"},
		{Domains: []string{"a.com"}, Upstream: "10.0.0.1:80; evil"},
		{Domains: []string{"a.com"}, Upstream: "http://10.0.0.1:80/x\n}"},
		{Domains: []string{"a.com"}, Upstream: "169.254.169.254:80"},
		{Domains: []string{"a.com"}, Upstream: "10.0.0.1"},
		{Domains: []string{"a.com"}, Upstream: "unix:/run/x.sock"},
	}
	for _, s := range bad {
		if _, err := CleanProxySite(ProxyCaddy, s); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	if _, err := CleanProxySite(ProxyNginx, ProxySite{Domains: []string{"a.com"}, Upstream: "10.0.0.1:80", TLS: ProxyTLSCert, CertPath: "/etc/x;", KeyPath: "/k"}); err == nil {
		t.Fatal("bad cert path accepted")
	}
}

func TestRootConfinedReadsIgnoreHostSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc/nginx"), 0o755); err != nil {
		t.Fatal(err)
	}
	// An absolute link inside the container must resolve inside it.
	if err := os.Symlink(outside, filepath.Join(root, "etc/nginx/nginx.conf")); err != nil {
		t.Fatal(err)
	}
	if b, err := readInRoot(root, "/etc/nginx/nginx.conf"); err == nil {
		t.Fatalf("read through a host symlink: %q", b)
	}
	if err := os.Symlink("/", filepath.Join(root, "etc/caddy")); err != nil {
		t.Fatal(err)
	}
	if err := writeInRoot(root, "/etc/caddy/ndl.d/x.caddy", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ndl.d/x.caddy")); err != nil {
		t.Fatalf("write must stay inside root: %v", err)
	}
}
