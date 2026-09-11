package qemu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backuppack"
	"github.com/no-dal/ndl-ce/internal/ctbackup"
	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	BackupCopy        = "copy"
	BackupReplace     = "replace"
	BackupDelete      = "delete"
	BackupMkdir       = "mkdir"
	BackupStat        = "stat"
	BackupArchive     = "archive"
	BackupExtractRoot = "extract-root"
	BackupWrite       = "write"
	BackupSyncTree    = "sync-tree"
	BackupPack        = "pack"
	BackupUnpack      = "unpack"
	BackupStatFS      = "statfs"
	BackupRmTree      = "rm-tree"
)

// ArchiveAction encodes an optional cgroup freeze unit as archive:<unit>.
func ArchiveAction(unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return BackupArchive
	}
	return BackupArchive + ":" + unit
}

// SyncTreeAction encodes an optional cgroup freeze unit as sync-tree:<unit>.
func SyncTreeAction(unit string) string {
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return BackupSyncTree
	}
	return BackupSyncTree + ":" + unit
}

// CopyOffline materializes a standalone qcow2 backup artifact, or mutates a
// typed backup locator. qemu-img convert is used so overlay backing files are
// flattened into the artifact. Live disks are refused.
func (e *Engine) CopyOffline(ctx context.Context, action, src, dest string) (storage.CopyResult, error) {
	act := strings.TrimSpace(action)
	if act == "" {
		act = BackupCopy
	}
	switch act {
	case BackupMkdir:
		if err := storage.AllowedArtifactPath(dest); err != nil {
			return storage.CopyResult{}, err
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup mkdir was not run")
		}
		if err := os.MkdirAll(dest, 0o750); err != nil {
			return storage.CopyResult{}, fmt.Errorf("backup mkdir: %w", err)
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	case BackupWrite:
		if err := storage.AllowedArtifactPath(src); err != nil {
			return storage.CopyResult{}, err
		}
		if err := storage.AllowedArtifactPath(dest); err != nil {
			return storage.CopyResult{}, err
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup write was not run")
		}
		return writeArtifactFile(src, dest)
	case BackupStatFS:
		if dest == "" || strings.Contains(dest, "..") {
			return storage.CopyResult{}, storage.ErrForbiddenPath
		}
		if err := storage.AllowedArtifactPath(dest); err != nil {
			if !strings.HasPrefix(dest, ctbackup.StagingRoot) {
				return storage.CopyResult{}, err
			}
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup statfs was not run")
		}
		n, err := ctbackup.AvailableBytes(dest)
		if err != nil {
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, Size: n, Format: "statfs"}, nil
	case BackupRmTree:
		if err := storage.AllowedArtifactPath(dest); err != nil {
			return storage.CopyResult{}, err
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup rmtree was not run")
		}
		if err := os.RemoveAll(dest); err != nil {
			return storage.CopyResult{}, fmt.Errorf("backup rmtree: %w", err)
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	case BackupPack:
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; pack was not run")
		}
		return packLocal(ctx, src, dest)
	case BackupUnpack:
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; unpack was not run")
		}
		return unpackLocal(ctx, src, dest)
	case BackupStat:
		if dest == "" || strings.Contains(dest, "..") {
			return storage.CopyResult{}, storage.ErrForbiddenPath
		}
		if strings.HasPrefix(dest, "/") && !strings.HasPrefix(dest, "//") {
			if err := storage.AllowedArtifactPath(dest); err != nil {
				return storage.CopyResult{}, err
			}
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup stat was not run")
		}
		info, err := os.Stat(dest)
		if err != nil || !info.IsDir() {
			return storage.CopyResult{Dest: dest, Size: 0, Format: "directory"}, nil
		}
		return storage.CopyResult{Dest: dest, Size: 1, Format: "directory"}, nil
	case BackupDelete:
		if err := e.AssertDiskOffline(ctx, dest); err != nil {
			return storage.CopyResult{}, err
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup delete was not run")
		}
		if err := storage.RemoveFile(dest); err != nil {
			return storage.CopyResult{}, fmt.Errorf("backup delete: %w", err)
		}
		return storage.CopyResult{Dest: dest, Format: "qcow2"}, nil
	case BackupCopy, BackupReplace:
		if err := e.AssertDiskOffline(ctx, src); err != nil {
			return storage.CopyResult{}, err
		}
		if err := e.AssertDiskOffline(ctx, dest); err != nil {
			return storage.CopyResult{}, err
		}
		if act == BackupCopy {
			if err := storage.AllowedArtifactPath(dest); err != nil {
				return storage.CopyResult{}, err
			}
			if strings.ContainsAny(dest, ",=") {
				return storage.CopyResult{}, fmt.Errorf("backup dest contains a banned character")
			}
		} else if err := ValidateDiskPath(dest); err != nil {
			return storage.CopyResult{}, err
		}
		srcFmt := "qcow2"
		if strings.HasPrefix(src, "/dev/") {
			if err := storage.ValidateLVMDevice(src); err != nil {
				return storage.CopyResult{}, err
			}
			srcFmt = "raw"
		}
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; backup copy was not run")
		}
		if act == BackupReplace {
			_ = os.Remove(dest)
		}
		var convErr error
		if act == BackupCopy {
			convErr = e.convertToBackupArtifact(ctx, src, dest, srcFmt)
		} else {
			convErr = e.ConvertOffline(ctx, ConvertRequest{
				SourcePath: src, DestPath: dest, SourceFormat: srcFmt, DestFormat: "qcow2",
			})
		}
		if convErr != nil {
			return storage.CopyResult{}, fmt.Errorf("backup convert: %w", convErr)
		}
		sum, size, err := checksumFile(dest)
		if err != nil {
			_ = os.Remove(dest)
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, SHA256: sum, Size: size, Format: "qcow2"}, nil
	}
	if unit, err := ctbackup.ParseFreezeUnit(act); err == nil && (act == BackupArchive || strings.HasPrefix(act, BackupArchive+":")) {
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; container archive was not run")
		}
		d := storage.Directory{Run: storage.LiveRun}
		if err := d.EnsureDirectoryRootMounted(ctx, src); err != nil {
			return storage.CopyResult{}, err
		}
		sidecar := ctbackup.MetaSidecar(dest)
		meta, _ := os.ReadFile(sidecar)
		defer func() { _ = os.Remove(sidecar) }()
		return ctbackup.Archive(ctx, src, dest, unit, meta)
	}
	if unit, err := ctbackup.ParseSyncTree(act); err == nil && (act == BackupSyncTree || strings.HasPrefix(act, BackupSyncTree+":")) {
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; container sync-tree was not run")
		}
		d := storage.Directory{Run: storage.LiveRun}
		if err := d.EnsureDirectoryRootMounted(ctx, src); err != nil {
			return storage.CopyResult{}, err
		}
		if err := ctbackup.CopyTree(ctx, src, dest, unit, unit != ""); err != nil {
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	}
	if act == BackupExtractRoot {
		if e.SkipHostCmds {
			return storage.CopyResult{}, fmt.Errorf("host commands skipped; container extract was not run")
		}
		if err := ctbackup.Extract(ctx, src, dest); err != nil {
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, Format: "directory"}, nil
	}
	return storage.CopyResult{}, fmt.Errorf("unsupported backup action")
}

// convertToBackupArtifact writes a flattened qcow2 under an allowed backup
// locator. Dest is an artifact path, not a VM disk under the storage root.
func (e *Engine) convertToBackupArtifact(ctx context.Context, src, dest, srcFmt string) error {
	if err := storage.AllowedArtifactPath(dest); err != nil {
		return err
	}
	if err := ValidateDiskPath(src); err != nil && !strings.HasPrefix(src, "/var/lib/ndl/storage/") && !strings.HasPrefix(src, "/dev/") {
		return fmt.Errorf("source image locator is invalid")
	}
	if strings.Contains(src, "..") || strings.ContainsAny(src, ",=\n") {
		return fmt.Errorf("source image locator is invalid")
	}
	if srcFmt == "" {
		srcFmt = "qcow2"
	}
	if srcFmt != "qcow2" && srcFmt != "raw" {
		return fmt.Errorf("source format must be qcow2 or raw")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	argv := []string{BinQEMUImg, "convert", "-f", srcFmt, "-O", "qcow2", src, dest}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("qemu-img convert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func writeArtifactFile(src, dest string) (storage.CopyResult, error) {
	in, err := os.Open(src)
	if err != nil {
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", err)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", err)
	}
	if !st.Mode().IsRegular() {
		return storage.CopyResult{}, fmt.Errorf("backup write source must be a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", err)
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", err)
	}
	n, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return storage.CopyResult{}, fmt.Errorf("backup write: %w", closeErr)
	}
	return storage.CopyResult{Dest: dest, Size: n, Format: "json"}, nil
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

func packLocal(ctx context.Context, src, dest string) (storage.CopyResult, error) {
	if err := storage.AllowedArtifactPath(dest); err != nil {
		return storage.CopyResult{}, err
	}
	payload, err := openLocalPayload(ctx, src)
	if err != nil {
		return storage.CopyResult{}, err
	}
	defer payload.Close()
	config, _ := os.ReadFile(filepath.Join(src, "config.json"))
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return storage.CopyResult{}, err
	}
	meta, err := backuppack.Write(ctx, backuppack.DirRepo{Root: dest}, "", payload, config, backuppack.GzipWrap, backuppack.Manifest{
		PayloadKind: backuppack.PayloadTar,
	})
	if err != nil {
		return storage.CopyResult{}, err
	}
	return storage.CopyResult{Dest: dest, SHA256: meta.PayloadSHA256, Size: meta.PayloadSize, Format: backuppack.FormatNDLB}, nil
}

func unpackLocal(ctx context.Context, src, dest string) (storage.CopyResult, error) {
	if err := storage.AllowedArtifactPath(src); err != nil {
		return storage.CopyResult{}, err
	}
	if err := storage.AllowedArtifactPath(dest); err != nil {
		return storage.CopyResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return storage.CopyResult{}, err
	}
	f, err := os.Create(dest)
	if err != nil {
		return storage.CopyResult{}, err
	}
	meta, err := backuppack.Reconstruct(ctx, backuppack.DirRepo{Root: src}, "", f, backuppack.GzipUnwrap)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(dest)
		return storage.CopyResult{}, err
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return storage.CopyResult{}, closeErr
	}
	if cfg, err := backuppack.ReadConfig(ctx, backuppack.DirRepo{Root: src}, "", backuppack.GzipUnwrap); err == nil && len(cfg) > 0 {
		_ = os.WriteFile(dest+".ndl-meta.json", cfg, 0o600)
	}
	return storage.CopyResult{Dest: dest, SHA256: meta.PayloadSHA256, Size: meta.PayloadSize, Format: backuppack.PayloadTar}, nil
}

func openLocalPayload(ctx context.Context, src string) (io.ReadCloser, error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return os.Open(src)
	}
	tree := src
	if st, err := os.Stat(filepath.Join(src, "tree")); err == nil && st.IsDir() {
		tree = filepath.Join(src, "tree")
	}
	return ctbackup.StreamTar(ctx, tree)
}
