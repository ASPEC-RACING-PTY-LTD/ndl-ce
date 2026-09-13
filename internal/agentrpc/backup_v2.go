package agentrpc

import (
	"path/filepath"

	"github.com/no-dal/ndl-ce/internal/backuphost"
)

func (h *Handler) backupHost() (*backuphost.Host, error) {
	h.mu.Lock()
	if h.BackupHost != nil {
		host := h.BackupHost
		h.mu.Unlock()
		return host, nil
	}
	h.mu.Unlock()
	dir := "/var/lib/ndl"
	if h.Ident.Dir != "" {
		dir = h.Ident.Dir
	}
	host, err := backuphost.Open(backuphost.Options{
		Root:   filepath.Join(dir, "backup-repo"),
		Docker: h.docker(),
	})
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	if h.BackupHost == nil {
		h.BackupHost = host
	} else {
		host = h.BackupHost
	}
	h.mu.Unlock()
	return host, nil
}
