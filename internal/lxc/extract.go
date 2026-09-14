package lxc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tarExtractArgs is the GNU tar vector for a host-readable archive path.
// Privileged containers extract as host root, so the cache pathname is fine.
func tarExtractArgs(archive, rootfs string) []string {
	args := []string{"--numeric-owner", "-x", "-C", rootfs, "-f", archive}
	if strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".xz") {
		args = []string{"--numeric-owner", "-xJ", "-C", rootfs, "-f", archive}
	} else if strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz") {
		args = []string{"--numeric-owner", "-xz", "-C", rootfs, "-f", archive}
	}
	return args
}

// tarStdinArgs reads the archive from stdin. dest is typically "." after the
// privileged parent chdirs into the rootfs so mapped tar never opens the
// root-owned cache path and does not need to traverse it.
func tarStdinArgs(archive, dest string) []string {
	args := tarExtractArgs(archive, dest)
	if len(args) < 2 {
		return args
	}
	args[len(args)-1] = "-"
	return args
}

func usernsMapFlag(mapping string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(strings.TrimSpace(mapping)), " "), " ", ":")
}

// usernsExtractArgs runs tar as mapped root. The archive is supplied on
// stdin by the privileged parent. dest is typically "." after chdir.
func usernsExtractArgs(uidMap, gidMap, archive, dest string) []string {
	if uidMap == "" {
		uidMap = DefaultUIDMap
	}
	if gidMap == "" {
		gidMap = DefaultGIDMap
	}
	out := []string{"-m", usernsMapFlag(uidMap), "-m", usernsMapFlag(gidMap), "--", BinTar}
	out = append(out, tarStdinArgs(archive, dest)...)
	return out
}

func validateArchivePath(p string) error {
	if p == "" || !filepath.IsAbs(p) || p != filepath.Clean(p) || strings.Contains(p, "..") {
		return fmt.Errorf("image archive path is not a clean absolute path")
	}
	return nil
}

func (e *Engine) unpackTar(ctx context.Context, spec Spec, archive, rootfs string) error {
	return withCreateUmask(0o022, func() error {
		return e.unpackTarUnmasked(ctx, spec, archive, rootfs)
	})
}

func (e *Engine) unpackTarUnmasked(ctx context.Context, spec Spec, archive, rootfs string) error {
	if spec.Privileged {
		_, err := e.run(ctx, BinTar, tarExtractArgs(archive, rootfs)...)
		return err
	}
	if err := validateArchivePath(archive); err != nil {
		return err
	}
	if err := validateRootfsPath(rootfs); err != nil {
		return err
	}
	args := usernsExtractArgs(spec.UIDMap, spec.GIDMap, archive, ".")
	for _, a := range args {
		if a == archive {
			return fmt.Errorf("unprivileged extract must not pass the cache path into the user namespace")
		}
	}
	if e.RunStdin != nil {
		src, err := os.Open(archive)
		if err != nil {
			return fmt.Errorf("open cached image: %w", err)
		}
		defer src.Close()
		_, err = e.RunStdin(ctx, BinUsernsExec, src, args...)
		if err != nil {
			return fmt.Errorf("unprivileged rootfs extract: %w", err)
		}
		return nil
	}
	if e.Run != nil {
		_, err := e.Run(ctx, BinUsernsExec, args...)
		if err != nil {
			return fmt.Errorf("unprivileged rootfs extract: %w", err)
		}
		return nil
	}
	if e.SkipHostCmds {
		return nil
	}
	if err := prepareMappedExtractRoot(rootfs, spec.UIDMap, spec.GIDMap); err != nil {
		return fmt.Errorf("unprivileged rootfs extract: %w", err)
	}
	if err := e.unpackTarMapped(ctx, args, archive, rootfs); err != nil {
		return fmt.Errorf("unprivileged rootfs extract: %w", err)
	}
	return nil
}

func prepareMappedExtractRoot(rootfs, uidMap, gidMap string) error {
	if err := os.MkdirAll(rootfs, 0o750); err != nil {
		return err
	}
	uid := hostMapStart(uidMap)
	gid := hostMapStart(gidMap)
	if err := os.Chown(rootfs, uid, gid); err != nil {
		return fmt.Errorf("chown mapped rootfs: %w", err)
	}
	return os.Chmod(rootfs, 0o755)
}

func (e *Engine) unpackTarMapped(ctx context.Context, args []string, archive, rootfs string) error {
	if !allowedBin(BinUsernsExec) {
		return fmt.Errorf("refusing unlisted binary %s", BinUsernsExec)
	}
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	cmd := exec.CommandContext(ctx, BinUsernsExec, args...)
	cmd.Dir = rootfs
	cmd.Stdin = f
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) == 0 {
			return err
		}
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
