package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExtractVzdumpOrRootfs unpacks a plain rootfs tar or a Proxmox LXC vzdump
// wrapper (outer tar plus inner backup/rootfs) into dest.
func ExtractVzdumpOrRootfs(src, dest string) error {
	if err := os.MkdirAll(dest, 0o750); err != nil {
		return err
	}
	if err := ExtractArchiveFile(src, dest); err != nil {
		return err
	}
	if VerifyCopiedRootfs(dest) == nil {
		return nil
	}
	inner, err := findNestedVzdumpRootfs(dest)
	if err != nil {
		return fmt.Errorf("proxmox vzdump archive did not contain a usable rootfs: %w", err)
	}
	if inner == dest {
		return VerifyCopiedRootfs(dest)
	}
	tmp := dest + ".ndl-inner"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return err
	}
	if err := ExtractArchiveFile(inner, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := VerifyCopiedRootfs(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("inner vzdump backup is empty or incomplete: %w", err)
	}
	if err := replaceDir(dest, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

func findNestedVzdumpRootfs(root string) (string, error) {
	var inner string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		name := strings.ToLower(d.Name())
		if name == "backup" || strings.HasPrefix(name, "rootfs") || archiveExt(name) != "" {
			if inner == "" || name == "backup" {
				inner = path
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if inner == "" {
		return "", fmt.Errorf("no inner backup tar was found")
	}
	return inner, nil
}

func replaceDir(dest, src string) error {
	bak := dest + ".ndl-old"
	_ = os.RemoveAll(bak)
	if err := os.Rename(dest, bak); err != nil {
		return err
	}
	if err := os.Rename(src, dest); err != nil {
		_ = os.Rename(bak, dest)
		return err
	}
	_ = os.RemoveAll(bak)
	return nil
}

func looksArchiveName(path string) bool {
	return archiveExt(path) != "" || strings.HasPrefix(strings.ToLower(filepath.Base(path)), "vzdump-")
}

func LooksLikeArchiveDest(path string) bool {
	return looksArchiveName(path)
}

func AllowedBackupSource(path string) bool {
	return allowedBackupSource(path)
}

func CopyHostArchive(src, dest string) error {
	return copyAllowedBackupFile(src, dest)
}
