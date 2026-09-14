package qemu

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestChownRuntimeOwnsCIDATAFiles(t *testing.T) {
	if _, err := user.Lookup(QEMUUser); err != nil {
		t.Skip("ndl-qemu user missing")
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root to chown")
	}
	e := &Engine{DataDir: t.TempDir()}
	id := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if err := e.ensureDirs(id); err != nil {
		t.Fatal(err)
	}
	cidata := filepath.Join(e.runtimeDir(id), "cidata.fat")
	seed := filepath.Join(e.runtimeDir(id), "cidata-user-data")
	if err := os.WriteFile(cidata, []byte("fat"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte("#cloud-config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.chownRuntime(id); err != nil {
		t.Fatal(err)
	}
	u, err := user.Lookup(QEMUUser)
	if err != nil {
		t.Fatal(err)
	}
	wantUID, err := strconv.Atoi(u.Uid)
	if err != nil {
		t.Fatal(err)
	}
	wantGID, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.runtimeDir(id), cidata, seed} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			t.Fatalf("stat %s: missing Stat_t", p)
		}
		if int(sys.Uid) != wantUID || int(sys.Gid) != wantGID {
			t.Fatalf("%s uid/gid=%d/%d want %d/%d", p, sys.Uid, sys.Gid, wantUID, wantGID)
		}
	}
}
