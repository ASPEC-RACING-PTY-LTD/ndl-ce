package ndltls

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGenerateLoadFingerprint(t *testing.T) {
	d := Dir{Root: t.TempDir()}
	mat, err := d.Generate("nodal.local", []string{"localhost", "127.0.0.1"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if mat.Fingerprint == "" || mat.CommonName != "nodal.local" {
		t.Fatalf("material %+v", mat)
	}
	st, err := os.Stat(d.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o", st.Mode().Perm())
	}
	loaded, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != mat.Fingerprint {
		t.Fatal("fingerprint drifted")
	}
}

func TestImportMismatchKeepsLastGood(t *testing.T) {
	d := Dir{Root: t.TempDir()}
	first, err := d.Generate("keep.example", nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	other := Dir{Root: t.TempDir()}
	if _, err := other.Generate("other.example", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	otherPEM, err := os.ReadFile(other.certPath())
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := os.ReadFile(d.keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Import(otherPEM, keyPEM); err == nil {
		t.Fatal("mismatched import must fail")
	}
	loaded, err := d.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint != first.Fingerprint {
		t.Fatal("last good must remain")
	}
}

func TestProbeDirectoryRejectsHTTP(t *testing.T) {
	if err := ProbeDirectory(context.Background(), "http://acme.example/directory"); err == nil {
		t.Fatal("http directory")
	}
}
