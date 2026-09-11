package ctbackup

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/storage"
)

const BinRsync = "/usr/bin/rsync"

var copyTreeCmd = copyTreeDefault

// StreamTar writes an uncompressed PAX archive of srcRootfs to a pipe.
// Compression happens per-chunk after this stream so GNU tar never launches
// zstd/gzip as a child of the agent.
func StreamTar(ctx context.Context, srcRootfs string) (io.ReadCloser, error) {
	if err := validateRootfs(srcRootfs); err != nil {
		return nil, err
	}
	info, err := os.Stat(srcRootfs)
	if err != nil {
		return nil, fmt.Errorf("container rootfs: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("container rootfs must be a directory")
	}
	if _, err := os.Stat(BinTar); err != nil {
		return nil, fmt.Errorf("tar is not installed: %w", err)
	}
	pr, pw := io.Pipe()
	args := tarPackArgs(srcRootfs)
	cmd := exec.CommandContext(ctx, BinTar, args...)
	cmd.Stdout = pw
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return nil, fmt.Errorf("tar: %w", err)
	}
	go func() {
		err := cmd.Wait()
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				_ = pw.CloseWithError(fmt.Errorf("tar: %w", err))
			} else {
				_ = pw.CloseWithError(fmt.Errorf("tar: %w: %s", err, msg))
			}
			return
		}
		_ = pw.Close()
	}()
	return pr, nil
}

func tarPackArgs(srcRootfs string) []string {
	args := []string{
		"--format=pax", "--acls", "--xattrs", "--xattrs-include=*",
		"--numeric-owner", "--sparse", "--one-file-system",
		"-cf", "-",
	}
	args = append(args, "-C", srcRootfs)
	for _, ex := range tarExcludes {
		args = append(args, "--exclude="+ex)
	}
	args = append(args, ".")
	return args
}

// CopyTree copies srcRootfs to dest while optionally freezing a container
// cgroup. The freeze is released before this function returns so compression
// and upload never run against a frozen production workload.
func CopyTree(ctx context.Context, srcRootfs, dest, freezeUnit string, requireFreeze bool) error {
	if err := validateRootfs(srcRootfs); err != nil {
		return err
	}
	if err := storageAllowedDest(dest); err != nil {
		return err
	}
	info, err := os.Stat(srcRootfs)
	if err != nil {
		return fmt.Errorf("container rootfs: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("container rootfs must be a directory")
	}
	unfreeze, err := freezeUnitIfPresent(freezeUnit, requireFreeze)
	if err != nil {
		return err
	}
	defer unfreeze()
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	return copyTreeCmd(ctx, srcRootfs, dest)
}

func copyTreeDefault(ctx context.Context, src, dest string) error {
	if _, err := os.Stat(BinRsync); err == nil {
		return runRsync(ctx, src, dest)
	}
	return copyTreeTarPipe(ctx, src, dest)
}

func runRsync(ctx context.Context, src, dest string) error {
	args := []string{
		"-aHAX", "-x", "--numeric-ids", "--sparse", "--delete",
		"--exclude=/proc", "--exclude=/sys", "--exclude=/dev", "--exclude=/run",
		strings.TrimRight(src, "/") + "/",
		strings.TrimRight(dest, "/") + "/",
	}
	cmd := exec.CommandContext(ctx, BinRsync, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("rsync: %w", err)
		}
		return fmt.Errorf("rsync: %w: %s", err, msg)
	}
	return nil
}

func copyTreeTarPipe(ctx context.Context, src, dest string) error {
	if _, err := os.Stat(BinTar); err != nil {
		return fmt.Errorf("tar is not installed: %w", err)
	}
	create := exec.CommandContext(ctx, BinTar, tarPackArgs(src)...)
	extract := exec.CommandContext(ctx, BinTar, "--acls", "--xattrs", "--xattrs-include=*", "--numeric-owner", "--sparse", "-xf", "-", "-C", dest)
	var errBuf strings.Builder
	pipe, err := create.StdoutPipe()
	if err != nil {
		return err
	}
	extract.Stdin = pipe
	create.Stderr = &errBuf
	extract.Stderr = &errBuf
	if err := extract.Start(); err != nil {
		return err
	}
	if err := create.Start(); err != nil {
		_ = extract.Wait()
		return fmt.Errorf("tar: %w", err)
	}
	cErr := create.Wait()
	eErr := extract.Wait()
	if cErr != nil || eErr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			if cErr != nil {
				return fmt.Errorf("tar: %w", cErr)
			}
			return fmt.Errorf("tar: %w", eErr)
		}
		return fmt.Errorf("tar: %s", msg)
	}
	return nil
}

func storageAllowedDest(dest string) error {
	if dest == "" || !filepath.IsAbs(dest) || filepath.Clean(dest) != dest || strings.Contains(dest, "..") {
		return fmt.Errorf("staging path is not a clean absolute path")
	}
	return storage.AllowedArtifactPath(dest)
}
