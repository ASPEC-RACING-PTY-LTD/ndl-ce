package debian

import (
	"testing"

	"github.com/google/uuid"
	"github.com/no-dal/ndl-ce/internal/ndnet"
)

func TestNetworkdFilesKeepUUIDOutOfIdentity(t *testing.T) {
	id := uuid.NewString()
	plan, err := ndnet.BuildPlan(ndnet.Spec{NetworkID: id, Name: "iso", Kind: ndnet.KindIsolated}, ndnet.HostView{})
	if err != nil {
		t.Fatal(err)
	}
	files := NetworkdFiles(plan)
	if len(files) == 0 {
		t.Fatal("no files")
	}
	for _, f := range files {
		if !Owned(f.RelPath) {
			t.Fatalf("unowned %s", f.RelPath)
		}
		if f.RelPath == id {
			t.Fatal("raw UUID used as filename identity")
		}
	}
	if PersistKind != ndnet.PersistNetworkd {
		t.Fatal(PersistKind)
	}
}
