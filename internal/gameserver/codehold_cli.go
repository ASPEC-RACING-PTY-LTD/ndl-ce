package gameserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"time"
)

// The CodeHold CLI ("ch") is mounted read-only at /usr/local/bin/ch in every
// game server container, so a terminal in the server folder can use it. It
// is the same static binary system containers get from the CodeHold CLI
// extra, downloaded from the configured CodeHold server and kept on the host.
// When no CodeHold address is set, or the download fails with nothing kept,
// the host's own ch is passed through instead. Each user signs in with
// "ch auth login"; HOME is the server folder, so that survives restarts.

const (
	chContainerPath = "/usr/local/bin/ch"
	chHostPath      = "/usr/local/bin/ch"
	chRefresh       = 24 * time.Hour
	chMaxBytes      = 256 << 20
)

// codeholdCLI is the host path of the ch binary to mount, or "" for none.
func (r *Runtime) codeholdCLI(ctx context.Context) string {
	r.chMu.Lock()
	defer r.chMu.Unlock()
	cached := filepath.Join(r.Root, "tools", "ch")
	info, err := os.Stat(cached)
	fresh := err == nil && time.Since(info.ModTime()) < chRefresh
	if !fresh && r.CodeholdCLIURL != nil {
		if url := r.CodeholdCLIURL(ctx); url != "" {
			dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := downloadCH(dctx, url, cached); err == nil {
				fresh = true
			}
			cancel()
		}
	}
	if fresh || isELF(cached) {
		return cached
	}
	host := firstNonEmpty(r.HostCH, chHostPath)
	if isELF(host) {
		return host
	}
	return ""
}

// downloadCH replaces dest only with a complete Linux executable.
func downloadCH(ctx context.Context, url, dest string) error {
	tmp := dest + ".new"
	if err := fetchFile(ctx, url, tmp, chMaxBytes); err != nil {
		return err
	}
	if !isELF(tmp) {
		_ = os.Remove(tmp)
		return os.ErrInvalid
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.Read(head); err != nil {
		return false
	}
	return bytes.Equal(head, []byte("\x7fELF"))
}
