package lxc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// tarExtractArgs is the GNU tar vector. --numeric-owner applies archive
// UIDs/GIDs. Extract unprivileged images through lxc-usernsexec so those
// IDs are the container map (0 -> 100000) and chmod is permitted. Host-root
// extract of the same Debian 13 tree chowns to _apt/systemd then chmod
// returns EPERM without a user namespace.
func tarExtractArgs(archive, rootfs string) []string {
	args := []string{"--numeric-owner", "-x", "-C", rootfs, "-f", archive}
	if strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".xz") {
		args = []string{"--numeric-owner", "-xJ", "-C", rootfs, "-f", archive}
	} else if strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz") {
		args = []string{"--numeric-owner", "-xz", "-C", rootfs, "-f", archive}
	}
	return args
}

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
// stdin by the privileged parent so mapped tar never needs pathname access
// to the root-owned image cache. dest is typically "." after chdir.
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

func (e *Engine) unpackTar(ctx context.Context, spec Spec, archive, rootfs string) error {
	if spec.Privileged {
		_, err := e.run(ctx, BinTar, tarExtractArgs(archive, rootfs)...)
		return err
	}
	args := usernsExtractArgs(spec.UIDMap, spec.GIDMap, archive, ".")
	if e.Run != nil {
		_, err := e.Run(ctx, BinUsernsExec, args...)
		if err != nil {
			return fmt.Errorf("unprivileged rootfs extract: %w", err)
		}
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
	return os.Chmod(rootfs, 0o750)
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
