package lxc

import (
	"context"
	"fmt"
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

func usernsMapFlag(mapping string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(strings.TrimSpace(mapping)), " "), " ", ":")
}

// usernsExtractArgs runs tar as mapped root so archive UIDs land on the
// persisted host map (0 -> 100000, 100 -> 100100) and chmod is permitted.
func usernsExtractArgs(uidMap, gidMap, archive, rootfs string) []string {
	if uidMap == "" {
		uidMap = DefaultUIDMap
	}
	if gidMap == "" {
		gidMap = DefaultGIDMap
	}
	out := []string{"-m", usernsMapFlag(uidMap), "-m", usernsMapFlag(gidMap), "--", BinTar}
	out = append(out, tarExtractArgs(archive, rootfs)...)
	return out
}

func (e *Engine) unpackTar(ctx context.Context, spec Spec, archive, rootfs string) error {
	if spec.Privileged {
		_, err := e.run(ctx, BinTar, tarExtractArgs(archive, rootfs)...)
		return err
	}
	args := usernsExtractArgs(spec.UIDMap, spec.GIDMap, archive, rootfs)
	_, err := e.run(ctx, BinUsernsExec, args...)
	if err != nil {
		return fmt.Errorf("unprivileged rootfs extract: %w", err)
	}
	return nil
}
