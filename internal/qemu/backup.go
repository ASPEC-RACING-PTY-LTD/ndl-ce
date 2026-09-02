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
	default:
		return storage.CopyResult{}, fmt.Errorf("unsupported backup action")
	}
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
