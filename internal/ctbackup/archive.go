package ctbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	BinTar     = "/usr/bin/tar"
	MetaName   = "ndl-backup-meta.json"
	FormatZstd = "tar.zst"
	FormatGzip = "tar.gz"
	FormatTar  = "tar"
)

var tarExcludes = []string{
	"./proc", "./sys", "./dev", "./run",
	"./ndl-backup-meta.json", MetaName,
}

// MetaSidecar is the sibling metadata path written next to an archive dest.
func MetaSidecar(dest string) string {
	for _, ext := range []string{".tar.zst", ".tar.gz", ".tgz", ".tar"} {
		if strings.HasSuffix(dest, ext) {
			return strings.TrimSuffix(dest, ext) + ".ndl-meta.json"
		}
	}
	return dest + ".ndl-meta.json"
}

func isCTArchiveFormat(format string) bool {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case FormatZstd, FormatGzip, FormatTar, "tgz":
		return true
	}
	return false
}

// IsArchiveFormat reports whether a catalogued artifact is a container rootfs archive.
func IsArchiveFormat(format string) bool {
	return isCTArchiveFormat(format) || strings.TrimSpace(strings.ToLower(format)) == "ndlb"
}

const StagingRoot = "/var/lib/ndl/backup-staging"

// Archive writes a GNU tar of srcRootfs to dest. The guest is never frozen,
// paused, or stopped. guestUnit, when set, runs optional in-guest hooks.
// Metadata is stored as an archive member and is never written into the live rootfs.
func Archive(ctx context.Context, srcRootfs, dest, guestUnit string, meta []byte) (storage.CopyResult, error) {
	if err := validateRootfs(srcRootfs); err != nil {
		return storage.CopyResult{}, err
	}
	if err := storage.AllowedArtifactPath(dest); err != nil {
		return storage.CopyResult{}, err
	}
	info, err := os.Stat(srcRootfs)
	if err != nil {
		return storage.CopyResult{}, fmt.Errorf("container rootfs: %w", err)
	}
	if !info.IsDir() {
		return storage.CopyResult{}, fmt.Errorf("container rootfs must be a directory")
	}
	if err := runGuestHook(ctx, guestUnit, srcRootfs, GuestHookPre); err != nil {
		_ = runGuestHook(ctx, guestUnit, srcRootfs, GuestHookPost)
		return storage.CopyResult{}, err
	}
	defer func() { _ = runGuestHook(ctx, guestUnit, srcRootfs, GuestHookPost) }()
	syncRootfs(srcRootfs)

	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return storage.CopyResult{}, err
	}

	format := FormatGzip
	destOut := dest
	if strings.HasSuffix(dest, ".tar.zst") {
		destOut = strings.TrimSuffix(dest, ".tar.zst") + ".tar.gz"
	}
	args := []string{
		"--format=pax", "--acls", "--xattrs", "--xattrs-include=*",
		"--numeric-owner", "--sparse", "--one-file-system", "-z", "-cf", destOut,
	}

	if len(bytes.TrimSpace(meta)) > 0 {
		dir, err := os.MkdirTemp(filepath.Dir(destOut), "ndl-ct-meta-")
		if err != nil {
			return storage.CopyResult{}, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		if err := os.WriteFile(filepath.Join(dir, MetaName), meta, 0o600); err != nil {
			return storage.CopyResult{}, err
		}
		args = append(args, "-C", dir, MetaName)
	}
	args = append(args, "-C", srcRootfs)
	for _, ex := range tarExcludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, ".")

	if err := runTar(ctx, args...); err != nil {
		_ = os.Remove(destOut)
		return storage.CopyResult{}, err
	}
	sum, size, err := checksumFile(destOut)
	if err != nil {
		_ = os.Remove(destOut)
		return storage.CopyResult{}, err
	}
	return storage.CopyResult{Dest: destOut, SHA256: sum, Size: size, Format: format}, nil
}

// Extract unpacks a container archive into destRootfs as host root. Numeric
// owners, ACLs, xattrs, capabilities, and sparse files are preserved. The
// metadata member is not extracted into the guest tree.
func Extract(ctx context.Context, srcArchive, destRootfs string) error {
	if err := validateArchive(srcArchive); err != nil {
		return err
	}
	if err := validateRootfs(destRootfs); err != nil {
		return err
	}
	if err := os.MkdirAll(destRootfs, 0o755); err != nil {
		return err
	}
	if err := emptySameFS(destRootfs); err != nil {
		return err
	}
	args := append(extractFlags(srcArchive), "-x", "-C", destRootfs, "-f", srcArchive)
	args = append(args, "--exclude="+MetaName, "--exclude=./"+MetaName)
	return runTar(ctx, args...)
}

// ReadMeta returns the backup metadata member, or nil when it is absent.
func ReadMeta(ctx context.Context, srcArchive string) ([]byte, error) {
	if err := validateArchive(srcArchive); err != nil {
		return nil, err
	}
	for _, name := range []string{MetaName, "./" + MetaName} {
		cmd := exec.CommandContext(ctx, BinTar, append(extractFlags(srcArchive), "-xO", "-f", srcArchive, name)...)
		out, err := cmd.Output()
		if err == nil && len(bytes.TrimSpace(out)) > 0 {
			return out, nil
		}
	}
	return nil, nil
}

// ExtractFile copies one archived path to dest. Numeric owners are not
// required for this host-side file restore.
func ExtractFile(ctx context.Context, srcArchive, guestPath, dest string) error {
	if err := validateArchive(srcArchive); err != nil {
		return err
	}
	guestPath = strings.TrimSpace(guestPath)
	if guestPath == "" || strings.Contains(guestPath, "..") || strings.ContainsAny(guestPath, "\n\x00") {
		return fmt.Errorf("guest path is invalid")
	}
	guestPath = strings.TrimPrefix(guestPath, "/")
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	for _, name := range []string{guestPath, "./" + guestPath} {
		cmd := exec.CommandContext(ctx, BinTar, append(extractFlags(srcArchive), "-xO", "-f", srcArchive, name)...)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if int64(len(out)) > 1<<20 {
			return fmt.Errorf("extracted file exceeds 1MiB restore-file cap")
		}
		return os.WriteFile(dest, out, 0o640)
	}
	return fmt.Errorf("guest path is not in the archive")
}

func extractFlags(archive string) []string {
	switch {
	case strings.HasSuffix(archive, ".tar.zst"):
		return []string{"--zstd", "--acls", "--xattrs", "--xattrs-include=*", "--numeric-owner", "--sparse"}
	case strings.HasSuffix(archive, ".tar.gz"), strings.HasSuffix(archive, ".tgz"):
		return []string{"-z", "--acls", "--xattrs", "--xattrs-include=*", "--numeric-owner", "--sparse"}
	default:
		return []string{"--acls", "--xattrs", "--xattrs-include=*", "--numeric-owner", "--sparse"}
	}
}

func zstdAvailable() bool {
	_, err := exec.LookPath("zstd")
	return err == nil
}

var runTarCmd = runTarDefault

func runTar(ctx context.Context, args ...string) error {
	return runTarCmd(ctx, args...)
}

func runTarDefault(ctx context.Context, args ...string) error {
	if _, err := os.Stat(BinTar); err != nil {
		return fmt.Errorf("tar is not installed: %w", err)
	}
	cmd := exec.CommandContext(ctx, BinTar, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("tar: %w", err)
		}
		return fmt.Errorf("tar: %w: %s", err, msg)
	}
	return nil
}

func validateRootfs(p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "..") {
		return fmt.Errorf("container rootfs path is not a clean absolute path")
	}
	for _, prefix := range []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/root", "/var/lib/postgresql"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return storage.ErrForbiddenPath
		}
	}
	if p == "/" || p == "/var" || p == "/var/lib" {
		return storage.ErrForbiddenPath
	}
	return nil
}

func validateArchive(p string) error {
	if err := storage.AllowedArtifactPath(p); err != nil {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Contains(p, "..") {
			return fmt.Errorf("archive path is not a clean absolute path")
		}
		for _, prefix := range []string{"/etc", "/usr", "/boot", "/proc", "/sys", "/dev", "/root"} {
			if p == prefix || strings.HasPrefix(p, prefix+"/") {
				return storage.ErrForbiddenPath
			}
		}
	}
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("archive must be a file")
	}
	return nil
}

func checksumFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	sum := sha256.New()
	n, err := io.Copy(sum, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(sum.Sum(nil)), n, nil
}
