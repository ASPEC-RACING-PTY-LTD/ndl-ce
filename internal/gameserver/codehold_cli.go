package gameserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The CodeHold CLI ("ch") is mounted read-only at /usr/local/bin/ch in every
// game server container, so a terminal in the server folder can use it. It
// is the same static binary, with Git built in, that system containers get
// from the CodeHold CLI extra: downloaded from the current codehold.dev
// release, checked against its SHA-256 and kept on the host. When the
// release cannot be reached and nothing is kept, the host's own ch is passed
// through instead. Each user signs in with
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
		if url, sum := r.CodeholdCLIURL(ctx); url != "" && sum != "" {
			if fileSHA256(cached) == sum {
				// Already the current release: keep it another day.
				now := time.Now()
				_ = os.Chtimes(cached, now, now)
				fresh = true
			} else {
				dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				if err := downloadCH(dctx, url, sum, cached); err == nil {
					fresh = true
				}
				cancel()
			}
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

// downloadCH replaces dest only with a Linux executable matching sum.
func downloadCH(ctx context.Context, url, sum, dest string) error {
	tmp := dest + ".new"
	if err := fetchFile(ctx, url, tmp, chMaxBytes); err != nil {
		return err
	}
	if !isELF(tmp) || fileSHA256(tmp) != sum {
		_ = os.Remove(tmp)
		return os.ErrInvalid
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
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
