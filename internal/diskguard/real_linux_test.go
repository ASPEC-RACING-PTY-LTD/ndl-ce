//go:build linux

package diskguard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealFilesystem runs the guard against the machine it is on.
func TestRealFilesystem(t *testing.T) {
	dir := t.TempDir()
	g := New(filepath.Join(dir, "reserve", "ballast"), Watch{Path: dir, Role: "data"}, Watch{Path: "/", Role: "root"})
	g.Policy.ReserveBytes = 1 << 20
	st := g.Refresh()
	if len(st.Filesystems) == 0 || st.Filesystems[0].TotalBytes <= 0 {
		t.Fatalf("%+v", st)
	}
	if st.Level == LevelOK && !st.ReserveHeld && st.ReserveNote == "" {
		t.Fatalf("a healthy disk must hold the reserve or say why not: %+v", st)
	}
	if st.ReserveHeld {
		fi, err := os.Stat(g.ReservePath)
		if err != nil || allocatedBytes(fi) < 1<<20 {
			t.Fatalf("reserve must really allocate space: %v %v", fi, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	u := g.Measure(context.Background(), dir, 5*time.Second)
	if u.FS == nil {
		t.Fatalf("%+v", u)
	}
}

func TestPreallocateReservesRealBlocks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ballast")
	if err := preallocate(p, 8<<20); err != nil {
		if err == errReserveUnsupported {
			t.Skip("filesystem cannot preallocate")
		}
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil || fi.Size() != 8<<20 || allocatedBytes(fi) < 8<<20 {
		t.Fatalf("size=%d allocated=%d err=%v", fi.Size(), allocatedBytes(fi), err)
	}
}
