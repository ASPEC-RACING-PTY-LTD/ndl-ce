package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrDiskResizing is returned when a container root image is being resized.
var ErrDiskResizing = errors.New("the container disk is being resized; try again when the resize finishes")

func imageLockPath(img string) string { return img + ".resize-lock" }

// lockImage takes the resize lock of a container root image. It is a flock,
// so it holds across processes and is released if the holder dies.
func lockImage(img string) (func(), error) {
	f, err := os.OpenFile(imageLockPath(img), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock container disk: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrDiskResizing
		}
		return nil, fmt.Errorf("lock container disk: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// imageLocked reports whether a resize holds the image.
func imageLocked(img string) bool {
	f, err := os.Open(imageLockPath(img))
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
