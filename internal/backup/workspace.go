package backup

import (
	"errors"
	"fmt"
)

// ErrWorkspaceFull is returned when accepting more backup data would exceed the
// configured local repository ceiling or cross the host free-space reserve. The
// engine stops accepting data safely rather than filling the host filesystem;
// it never deletes production data or corrupts existing backups to make room.
var ErrWorkspaceFull = errors.New("backup workspace limit reached")

// checkWorkspace enforces both the repository-size ceiling and the host
// minimum-free-space reserve. Either violation blocks new data.
func (e *Engine) checkWorkspace() error {
	if e.cfg.MaxLocalBytes > 0 {
		size, err := e.repo.SizeOnDisk()
		if err != nil {
			return err
		}
		if size >= e.cfg.MaxLocalBytes {
			return fmt.Errorf("%w: repository %d bytes at or above ceiling %d", ErrWorkspaceFull, size, e.cfg.MaxLocalBytes)
		}
	}
	if e.cfg.MinHostFreeBytes > 0 {
		free, err := hostFreeBytes(e.repo.root)
		if err != nil {
			return err
		}
		if free <= e.cfg.MinHostFreeBytes {
			return fmt.Errorf("%w: host free %d bytes at or below reserve %d", ErrWorkspaceFull, free, e.cfg.MinHostFreeBytes)
		}
	}
	return nil
}
