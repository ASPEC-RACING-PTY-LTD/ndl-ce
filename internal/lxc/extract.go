package lxc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// tarExtractArgs is the GNU tar vector for a host-readable archive path.
// Privileged containers extract as host root, so the cache pathname is fine.
func tarExtractArgs(archive, rootfs string) []string {
	return tarExtractArgsWithFile(archive, rootfs, archive)
}

// tarExtractStdinArgs reads the archive from stdin. Used inside lxc-usernsexec
// so mapped root (host UID 100000) never opens the root-owned cache path.
func tarExtractStdinArgs(archive, rootfs string) []string {
	return tarExtractArgsWithFile(archive, rootfs, "-")
}

func tarExtractArgsWithFile(archive, rootfs, file string) []string {
	args := []string{"--numeric-owner", "-x", "-C", rootfs, "-f", file}
	if strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".xz") {
		args = []string{"--numeric-owner", "-xJ", "-C", rootfs, "-f", file}
	} else if strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz") {
		args = []string{"--numeric-owner", "-xz", "-C", rootfs, "-f", file}
	}
	return args
}

func usernsMapFlag(mapping string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(strings.TrimSpace(mapping)), " "), " ", ":")
}

// usernsExtractArgs runs tar as mapped root so archive UIDs land on the
// persisted host map (0 -> 100000, 100 -> 100100) and chmod is permitted.
// The archive path is used only to choose the decompressor. The bytes come
// from stdin, which the privileged parent already opened.
func usernsExtractArgs(uidMap, gidMap, archive, rootfs string) []string {
	if uidMap == "" {
		uidMap = DefaultUIDMap
	}
	if gidMap == "" {
		gidMap = DefaultGIDMap
	}
	out := []string{"-m", usernsMapFlag(uidMap), "-m", usernsMapFlag(gidMap), "--", BinTar}
	out = append(out, tarExtractStdinArgs(archive, rootfs)...)
	return out
}

func validateArchivePath(p string) error {
	if p == "" || !filepath.IsAbs(p) || p != filepath.Clean(p) || strings.Contains(p, "..") {
		return fmt.Errorf("image archive path is not a clean absolute path")
	}
	return nil
}

func (e *Engine) unpackTar(ctx context.Context, spec Spec, archive, rootfs string) error {
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
	src, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open cached image: %w", err)
	}
	defer src.Close()
	if err := e.prepareMappedExtractRoot(rootfs, spec); err != nil {
		return err
	}
	args := usernsExtractArgs(spec.UIDMap, spec.GIDMap, archive, rootfs)
	for _, a := range args {
		if a == archive {
			return fmt.Errorf("unprivileged extract must not pass the cache path into the user namespace")
		}
	}
	_, err = e.runStdin(ctx, src, BinUsernsExec, args...)
	if err != nil {
		return fmt.Errorf("unprivileged rootfs extract: %w", err)
	}
	return nil
}

func (e *Engine) prepareMappedExtractRoot(rootfs string, spec Spec) error {
	if e.Run != nil || e.RunStdin != nil || e.SkipHostCmds {
		return nil
	}
	if err := ensureTraverse(rootfs); err != nil {
		return err
	}
	return chownMappedRoot(rootfs, hostMapStart(spec.UIDMap), hostMapStart(spec.GIDMap))
}
