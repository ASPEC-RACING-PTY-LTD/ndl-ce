package qemu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/no-dal/ndl-ce/internal/storage"
)

const (
	BackupCopy    = "copy"
	BackupReplace = "replace"
	BackupDelete  = "delete"
	BackupMkdir   = "mkdir"
	BackupStat    = "stat"
)

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
		if err := e.ConvertOffline(ctx, ConvertRequest{
			SourcePath: src, DestPath: dest, SourceFormat: srcFmt, DestFormat: "qcow2",
		}); err != nil {
			return storage.CopyResult{}, fmt.Errorf("backup convert: %w", err)
		}
		sum, size, err := checksumFile(dest)
		if err != nil {
			_ = os.Remove(dest)
			return storage.CopyResult{}, err
		}
		return storage.CopyResult{Dest: dest, SHA256: sum, Size: size, Format: "qcow2"}, nil
	default:
		return storage.CopyResult{}, fmt.Errorf("unsupported backup action")
	}
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
