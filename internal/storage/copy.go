package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyResult is a checksummed backup artifact write.
type CopyResult struct {
	Dest   string
	SHA256 string
	Size   int64
	Format string
}

// CopyFile copies src to dest using typed paths. It does not invoke a shell.
func CopyFile(src, dest string) (CopyResult, error) {
	return checksumCopyFile(src, dest, false)
}

// CopyReplace overwrites dest after a checksummed copy to a sibling temp file.
func CopyReplace(src, dest string) (CopyResult, error) {
	return checksumCopyFile(src, dest, true)
}

// RemoveFile deletes a proven backup artifact path. It does not invoke a shell.
func RemoveFile(p string) error {
	if err := validateCopyPath(p, true); err != nil {
		return err
	}
	return os.Remove(p)
}

func checksumCopyFile(src, dest string, replace bool) (CopyResult, error) {
	if err := validateCopyPath(src, false); err != nil {
		return CopyResult{}, err
	}
	if err := validateCopyPath(dest, true); err != nil {
		return CopyResult{}, err
	}
	if src == dest {
		return CopyResult{}, ErrForbiddenPath
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return CopyResult{}, err
	}
	in, err := os.Open(src)
	if err != nil {
		return CopyResult{}, err
	}
	defer in.Close()
	outPath := dest
	if replace {
		outPath = dest + ".ndl-partial"
		_ = os.Remove(outPath)
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o640)
	if err != nil {
		return CopyResult{}, err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, sum), in)
	if err != nil {
		_ = out.Close()
		_ = os.Remove(outPath)
		return CopyResult{}, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		return CopyResult{}, err
	}
	if replace {
		if err := os.Rename(outPath, dest); err != nil {
			_ = os.Remove(outPath)
			return CopyResult{}, err
		}
	}
	return CopyResult{Dest: dest, SHA256: hex.EncodeToString(sum.Sum(nil)), Size: n, Format: "qcow2"}, nil
}

// AllowedArtifactPath reports whether a backup locator is a typed writable path.
func AllowedArtifactPath(p string) error {
	return validateCopyPath(p, true)
}

func validateCopyPath(p string, dest bool) error {
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "..") || strings.ContainsAny(p, "\x00\n") {
		return ErrForbiddenPath
	}
	cleaned := filepath.Clean(p)
	if cleaned != p {
		return ErrForbiddenPath
	}
	for _, prefix := range []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/root", "/var/lib/postgresql"} {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return ErrForbiddenPath
		}
	}
	if dest && (cleaned == "/" || cleaned == "/var" || cleaned == "/var/lib") {
		return ErrForbiddenPath
	}
	return nil
}
