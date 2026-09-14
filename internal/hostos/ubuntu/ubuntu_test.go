package ubuntu

import "testing"

func TestRefuseDebianNetworkd(t *testing.T) {
	if err := RefuseDebianNetworkd("debian", "/etc/systemd/network"); err == nil {
		t.Fatal("expected refuse")
	}
	if err := RefuseDebianNetworkd("ubuntu", "/etc/systemd/network"); err == nil {
		t.Fatal("must not dual-write networkd")
	}
}

func TestGapsAreHonest(t *testing.T) {
	if len(QualificationGaps) == 0 {
		t.Fatal("gaps")
	}
	if Is("debian") || !Is("ubuntu") {
		t.Fatal("id")
	}
}
