package storecatalog

import (
	"testing"

	"github.com/no-dal/ndl-ce/internal/storetrust"
)

func TestOfficialSampleParses(t *testing.T) {
	files, err := Official()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Manifest.Name != "sample-web" || files[0].Manifest.Class != "official" {
		t.Fatalf("%+v", files)
	}
}

func TestOfficialPinVerifiesBundledSample(t *testing.T) {
	pin := OfficialPublicKey()
	if pin == "" {
		t.Fatal("official.pub must be embedded")
	}
	pub, err := storetrust.ParsePublic(pin)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Official()
	if err != nil || len(files) != 1 {
		t.Fatalf("official: %v %+v", err, files)
	}
	if files[0].Signature == "" {
		t.Fatal("bundled Official sample must ship a publisher signature")
	}
	if err := storetrust.Verify(pub, []byte(files[0].YAML), files[0].Signature); err != nil {
		t.Fatalf("pinned Official signature: %v", err)
	}
}
