package docker

import "testing"

func TestComposeGroupingByProjectAndWorkingDir(t *testing.T) {
	m := attachAndGroup(Machine{ID: "wl-1", Name: "webhost", Kind: KindSystemContainer, IPv4: "10.0.0.8"}, []Container{
		{EngineID: "aaa111aaa111", Name: "web-1", Project: "shop", Service: "web", WorkingDir: "/srv/shop", Image: "nginx", State: "running", HealthStatus: "healthy"},
		{EngineID: "bbb222bbb222", Name: "db-1", Project: "shop", Service: "db", WorkingDir: "/srv/shop", Image: "postgres", State: "running"},
		{EngineID: "ccc333ccc333", Name: "other", Project: "shop", Service: "web", WorkingDir: "/opt/other", Image: "nginx", State: "exited", ExitCode: 1},
		{EngineID: "ddd444ddd444", Name: "orphan", Image: "busybox", State: "exited", ExitCode: 0},
	})
	if len(m.Projects) != 3 {
		t.Fatalf("projects=%d %+v", len(m.Projects), projectNames(m))
	}
	var shop, other, standalone *Project
	for i := range m.Projects {
		p := &m.Projects[i]
		switch {
		case p.Name == "shop" && p.WorkingDir == "/srv/shop":
			shop = p
		case p.Name == "shop" && p.WorkingDir == "/opt/other":
			other = p
		case p.Standalone:
			standalone = p
		}
	}
	if shop == nil || len(shop.Containers) != 2 || shop.Health != HealthHealthy {
		t.Fatalf("shop %+v", shop)
	}
	if other == nil || other.Health != HealthDegraded {
		t.Fatalf("other %+v", other)
	}
	if standalone == nil || standalone.Name != "Standalone" || len(standalone.Containers) != 1 {
		t.Fatalf("standalone %+v", standalone)
	}
	inv := flattenInventory([]Machine{m})
	if len(inv.Containers) != 4 || len(inv.Projects) != 3 {
		t.Fatalf("flat containers=%d projects=%d", len(inv.Containers), len(inv.Projects))
	}
	if inv.Containers[0].MachineIPv4 != "10.0.0.8" {
		t.Fatalf("machine ip missing")
	}
}

func projectNames(m Machine) []string {
	out := make([]string, 0, len(m.Projects))
	for _, p := range m.Projects {
		out = append(out, p.ID+"="+p.Name)
	}
	return out
}

func TestDaemonDownIsCritical(t *testing.T) {
	m := groupMachine(Machine{
		ID: HostMachineID, Name: "Host", Kind: KindHost,
		DaemonOK: false, DaemonError: "connection refused",
	})
	if m.Health != HealthCritical {
		t.Fatalf("health %s", m.Health)
	}
}

func TestProjectIDStable(t *testing.T) {
	a := projectID("host", "shop", "/srv/shop")
	b := projectID("host", "shop", "/srv/shop")
	c := projectID("host", "shop", "/opt/shop")
	if a != b || a == c {
		t.Fatalf("%s %s %s", a, b, c)
	}
	if projectID("host", "", "") != "host/standalone" {
		t.Fatal(projectID("host", "", ""))
	}
}
