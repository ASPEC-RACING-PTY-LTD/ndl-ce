package storage

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestImageLockBlocksRemount(t *testing.T) {
	img := filepath.Join(t.TempDir(), "root.img")
	if imageLocked(img) {
		t.Fatal("unlocked image reported locked")
	}
	unlock, err := lockImage(img)
	if err != nil {
		t.Fatal(err)
	}
	if !imageLocked(img) {
		t.Fatal("locked image not reported locked")
	}
	if _, err := lockImage(img); !errors.Is(err, ErrDiskResizing) {
		t.Fatalf("second lock err=%v", err)
	}
	unlock()
	if imageLocked(img) {
		t.Fatal("image still locked after unlock")
	}
}
