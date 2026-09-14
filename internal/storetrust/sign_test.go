package storetrust

import (
	"strings"
	"testing"
)

func TestSignVerifyAndTamperFailsClosed(t *testing.T) {
	kp, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte("apiVersion: nodal.store/v1\nname: sample-web\n")
	sig, err := Sign(kp.Private, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(kp.Public, manifest, sig); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte{}, manifest...)
	tampered[len(tampered)-2] = 'X'
	if err := Verify(kp.Public, tampered, sig); err == nil || !strings.Contains(err.Error(), "tamper") {
		t.Fatalf("tamper must fail closed: %v", err)
	}
	if PayloadSHA256(manifest) == PayloadSHA256(tampered) {
		t.Fatal("digest must change")
	}
}
