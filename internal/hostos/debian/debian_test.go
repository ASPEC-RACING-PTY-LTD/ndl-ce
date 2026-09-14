package debian

import "testing"

func TestDebian13Constants(t *testing.T) {
	if !Is("debian", "13") {
		t.Fatal("expected debian 13")
	}
	if Is("ubuntu", "24.04") {
		t.Fatal("ubuntu is not debian 13")
	}
	if PackageTool != "apt" || NetworkPersist != "systemd-networkd" {
		t.Fatal("debian adapter constants")
	}
	caps := Capabilities()
	if len(caps) != 2 || caps[0] != PackageTool || caps[1] != NetworkPersist {
		t.Fatalf("capabilities=%v", caps)
	}
}
