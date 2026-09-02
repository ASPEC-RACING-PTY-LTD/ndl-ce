package features

import "testing"

func TestCatalogDefaultSmall(t *testing.T) {
	var optional int
	for _, m := range Catalog() {
		if m.Core {
			if m.Package != "" {
				t.Fatalf("core %s must not name a feature package", m.ID)
			}
			continue
		}
		optional++
		if m.Package == "" {
			t.Fatalf("%s missing package", m.ID)
		}
		if m.StartsRuntime {
			t.Fatalf("%s must not start a runtime from Phase 35", m.ID)
		}
	}
	if optional != 5 {
		t.Fatalf("optional=%d", optional)
	}
	k8s, ok := Lookup(IDK8s)
	if !ok || !k8s.RequiresK8sAck || k8s.StartsRuntime {
		t.Fatalf("%+v", k8s)
	}
	alias, ok := Lookup("kubernetes")
	if !ok || alias.ID != IDK8s {
		t.Fatalf("kubernetes alias %+v", alias)
	}
	if !TinyNode(0) || !TinyNode(TinyK8sMemoryBytes) || TinyNode(TinyK8sMemoryBytes+1) {
		t.Fatal("tiny node floor")
	}
}
