package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const journalSample = `2026-10-16T10:00:01+1100 host cloudflared[812]: 2026-10-15T23:00:01Z INF Registered tunnel connection connIndex=0
2026-10-16T10:01:02+1100 host cloudflared[812]: 2026-10-15T23:01:02Z ERR error="Unable to reach the origin service. The service may be down or it may not be responding to traffic from cloudflared: dial tcp 127.0.0.1:8080: connect: connection refused" cfRay=8c1 event=1 ingressRule=0 originService=http://localhost:8080
2026-10-16T10:01:09+1100 host cloudflared[812]: 2026-10-15T23:01:09Z ERR  error="Unable to reach the origin service. The service may be down or it may not be responding to traffic from cloudflared: dial tcp 127.0.0.1:8080: connect: connection refused" connIndex=1 dest=https://shop.example.com/cart event=0 ip=198.41.200.13 type=http
2026-10-16T10:02:00+1100 host cloudflared[812]: 2026-10-15T23:02:00Z ERR error="dial tcp 172.18.0.4:3000: i/o timeout" originService=http://172.18.0.4:3000
`

func TestParseTunnelErrorsGroupsByOrigin(t *testing.T) {
	errs := ParseTunnelErrors(journalSample)
	if len(errs) < 2 {
		t.Fatalf("errors %+v", errs)
	}
	var local *TunnelError
	for i := range errs {
		if strings.Contains(errs[i].Origin, "8080") {
			local = &errs[i]
		}
	}
	if local == nil || local.Count < 1 {
		t.Fatalf("no 8080 origin: %+v", errs)
	}
	found := false
	for _, e := range errs {
		if e.Hostname == "shop.example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hostname from dest= not read: %+v", errs)
	}
}

func TestLinkTunnelsFindsTheContainerBehindAnOrigin(t *testing.T) {
	inv := Inventory{
		Machines: []Machine{{ID: "ct-1", Name: "apps", IPv4: "10.0.0.5"}},
		Containers: []Container{
			{Ref: "ct-1/a", MachineID: "ct-1", EngineID: "a", Name: "shop-web-1", State: "exited", Health: HealthDegraded,
				Ports: []Port{{PrivatePort: 80, PublicPort: 8080}}},
			{Ref: "ct-1/b", MachineID: "ct-1", EngineID: "b", Name: "api", State: "running", Health: HealthHealthy, IPs: []string{"172.18.0.4"}},
			{Ref: "host/c", MachineID: "host", EngineID: "c", Name: "other", State: "running", Ports: []Port{{PrivatePort: 80, PublicPort: 8080}}},
		},
	}
	tunnels := []Tunnel{{
		MachineID: "ct-1", Unit: "cloudflared.service", Active: true,
		Ingress: []IngressRule{{Hostname: "shop.example.com", Service: "http://localhost:8080"}, {Service: "http_status:404"}},
		Errors:  ParseTunnelErrors(journalSample),
	}}
	tunnels[0].ErrorCount = 3
	got := LinkTunnels(tunnels, inv)[0]
	if got.Health != HealthDegraded || !strings.Contains(got.Reason, "shop-web-1") {
		t.Fatalf("health %s %q", got.Health, got.Reason)
	}
	names := map[string]TunnelLink{}
	for _, l := range got.Links {
		names[l.Name] = l
	}
	if _, ok := names["other"]; ok {
		t.Fatal("a container on another machine must not be linked")
	}
	if l, ok := names["shop-web-1"]; !ok || len(l.Hostnames) == 0 || l.Hostnames[0] != "shop.example.com" {
		t.Fatalf("localhost:8080 must link to shop-web-1 with its hostname: %+v", got.Links)
	}
	if _, ok := names["api"]; !ok {
		t.Fatalf("a container IP origin must link: %+v", got.Links)
	}
	down := LinkTunnels([]Tunnel{{MachineID: "ct-1", Unit: "cloudflared.service", State: "failed", SubState: "failed"}}, inv)[0]
	if down.Health != HealthCritical {
		t.Fatalf("a stopped tunnel is critical: %+v", down)
	}
}

func TestTunnelsReadUnitsJournalAndConfigWithoutTheToken(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/cloudflared"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "etc/cloudflared/config.yml"), []byte("tunnel: abc\ningress:\n  - hostname: shop.example.com\n    service: http://localhost:8080\n  - service: http_status:404\n"), 0o644)
	var ran []string
	e := &Engine{
		LXCInfo: func(context.Context, string) (int, string, error) { return 0, "", nil },
		HostRun: func(_ context.Context, name string, args ...string) ([]byte, error) {
			ran = append(ran, name+" "+strings.Join(args, " "))
			switch {
			case strings.Contains(strings.Join(args, " "), "list-units"):
				return []byte("cloudflared.service loaded active running cloudflared\nnot-it.service loaded active running x\n"), nil
			case strings.Contains(strings.Join(args, " "), "ExecStart"):
				return []byte("{ path=/usr/bin/cloudflared ; argv[]=/usr/bin/cloudflared --no-autoupdate tunnel run --token eySECRET ; }"), nil
			case name == binJournalctl:
				return []byte(journalSample), nil
			}
			return nil, nil
		},
	}
	// The host root is "/" in production; point the test at the temp dir.
	tunnels := e.machineTunnels(context.Background(), MachineHint{ID: HostMachineID, Name: "Host"})
	if len(tunnels) != 1 || tunnels[0].Unit != "cloudflared.service" || !tunnels[0].Active || tunnels[0].ErrorCount == 0 {
		t.Fatalf("tunnels %+v", tunnels)
	}
	if !tunnels[0].RemoteManaged {
		t.Fatal("a --token tunnel is remotely managed")
	}
	for _, f := range []string{tunnels[0].Reason, tunnels[0].ConfigPath} {
		if strings.Contains(f, "eySECRET") {
			t.Fatal("the tunnel token must never be reported")
		}
	}
	if got := ParseIngress([]byte("ingress:\n  - hostname: a.example\n    service: http://localhost:1\n")); len(got) != 1 || got[0].Hostname != "a.example" {
		t.Fatalf("ingress %+v", got)
	}
	_ = root
}

func TestRecoveryOrderRestartsDependenciesFirst(t *testing.T) {
	db := Container{Name: "shop-db-1", Service: "db"}
	cache := Container{Name: "shop-cache-1", Service: "cache", Labels: map[string]string{LabelDependsOn: "db:service_healthy:false"}}
	web := Container{Name: "shop-web-1", Service: "web", Labels: map[string]string{LabelDependsOn: "db:service_healthy:false,cache:service_started:true"}}
	other := Container{Name: "shop-cron-1", Service: "cron"}
	layers := RecoveryOrder([]Container{db, cache, web, other}, []Container{web})
	if len(layers) != 3 || layers[0][0].Service != "db" || layers[1][0].Service != "cache" || layers[2][0].Service != "web" {
		t.Fatalf("layers %+v", layers)
	}
	for _, l := range layers {
		for _, c := range l {
			if c.Service == "cron" {
				t.Fatal("unrelated services must not restart")
			}
		}
	}
	loop := RecoveryOrder([]Container{
		{Name: "a", Service: "a", Labels: map[string]string{LabelDependsOn: "b"}},
		{Name: "b", Service: "b", Labels: map[string]string{LabelDependsOn: "a"}},
	}, []Container{{Name: "a", Service: "a", Labels: map[string]string{LabelDependsOn: "b"}}})
	total := 0
	for _, l := range loop {
		total += len(l)
	}
	if total != 2 {
		t.Fatalf("a dependency cycle must still restart both once: %+v", loop)
	}
}
