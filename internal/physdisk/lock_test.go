package physdisk

import "testing"

func TestClaimReleaseAndSteal(t *testing.T) {
	defer SetRunDirForTest(t.TempDir())()
	id := DeviceID("ata-UNUSED")
	if err := Claim(id, "wl-1", false); err != nil {
		t.Fatal(err)
	}
	if err := Claim(id, "wl-2", false); err == nil {
		t.Fatal("exclusive claim")
	}
	if err := Claim(id, "wl-2", true); err != nil {
		t.Fatal(err)
	}
	if err := Release(id, "wl-2"); err != nil {
		t.Fatal(err)
	}
	if err := Claim(id, "wl-3", false); err != nil {
		t.Fatal(err)
	}
}
