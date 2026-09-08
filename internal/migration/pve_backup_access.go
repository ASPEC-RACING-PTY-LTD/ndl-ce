package migration

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func looksArchiveBytes(b []byte) bool {
	if len(b) >= 4 && b[0] == 0x28 && b[1] == 0xb5 && b[2] == 0x2f && b[3] == 0xfd {
		return true
	}
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		return true
	}
	if len(b) >= 6 && b[0] == 0xfd && b[1] == 0x37 && b[2] == 0x7a && b[3] == 0x58 && b[4] == 0x5a && b[5] == 0x00 {
		return true
	}
	if len(b) >= 262 && string(b[257:262]) == "ustar" {
		return true
	}
	return false
}

func vzdumpBasename(volid string) string {
	name := strings.TrimSpace(volid)
	if i := strings.Index(name, ":"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func IsVzdumpArchive(volid string) bool {
	base := strings.ToLower(vzdumpBasename(volid))
	return strings.HasPrefix(base, "vzdump-lxc-") || strings.HasPrefix(base, "vzdump-qemu-")
}

func archiveExt(name string) string {
	low := strings.ToLower(name)
	for _, ext := range []string{".tar.zst", ".tar.gz", ".tar.xz", ".tgz", ".tar"} {
		if strings.HasSuffix(low, ext) {
			return ext
		}
	}
	return ""
}

func ArchiveStagingName(volid string) string {
	base := vzdumpBasename(volid)
	if base == "" {
		return "rootfs-archive"
	}
	safe := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, base)
	if strings.HasPrefix(strings.ToLower(safe), "vzdump-") || archiveExt(safe) != "" {
		return safe
	}
	if ext := archiveExt(volid); ext != "" {
		return "rootfs-archive" + ext
	}
	return "rootfs-archive"
}

func (c *PVEClient) localBackupFile(node, volid, storage string, metaJSON []byte) (string, bool) {
	var candidates []string
	candidates = append(candidates, pathsFromVolumeMeta(metaJSON)...)
	if rows, err := c.ListNodeStorage(node); err == nil {
		candidates = append(candidates, pathsFromStorages(rows, storage, volid)...)
	}
	candidates = append(candidates, defaultVzdumpPaths(volid)...)
	seen := map[string]struct{}{}
	for _, p := range candidates {
		p = filepath.Clean(strings.TrimSpace(p))
		if p == "" || p == "." {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		if !allowedBackupSource(p) {
			continue
		}
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		return p, true
	}
	return "", false
}

func pathsFromVolumeMeta(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var wrap struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrap) != nil || len(wrap.Data) == 0 {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(wrap.Data, &obj) != nil {
		return nil
	}
	var out []string
	for _, key := range []string{"path", "filepath", "file", "fullpath"} {
		s, _ := obj[key].(string)
		s = strings.TrimSpace(s)
		if s == "" || !strings.HasPrefix(s, "/") {
			continue
		}
		out = append(out, filepath.Clean(s))
	}
	return out
}

func pathsFromStorages(rows []map[string]any, storage, volid string) []string {
	base := vzdumpBasename(volid)
	if base == "" {
		return nil
	}
	var out []string
	for _, row := range rows {
		name, _ := row["storage"].(string)
		if storage != "" && name != storage {
			continue
		}
		p, _ := row["path"].(string)
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = filepath.Clean(p)
		out = append(out, filepath.Join(p, "dump", base), filepath.Join(p, "backup", base), filepath.Join(p, base))
	}
	return out
}

func defaultVzdumpPaths(volid string) []string {
	base := vzdumpBasename(volid)
	if !strings.HasPrefix(strings.ToLower(base), "vzdump-") {
		return nil
	}
	return []string{
		filepath.Join("/var/lib/vz", "dump", base),
		filepath.Join("/var/lib/vz", "backup", base),
	}
}

func allowedBackupSource(p string) bool {
	slash := filepath.ToSlash(filepath.Clean(p))
	base := filepath.Base(slash)
	if ValidateLocalMigrationPath(p) == nil || ValidateHostPath(p) == nil {
		return true
	}
	if !strings.HasPrefix(strings.ToLower(base), "vzdump-") {
		return false
	}
	return strings.Contains(slash, "/dump/") || strings.Contains(slash, "/backup/")
}

func copyAllowedBackupFile(src, dest string) error {
	if !allowedBackupSource(src) {
		return fmt.Errorf("backup archive path is not allowed")
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("cannot read Proxmox vzdump archive %s", src)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	written, err := io.Copy(out, io.LimitReader(in, pveMaxDownload+1))
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	if written > pveMaxDownload {
		_ = os.Remove(dest)
		return fmt.Errorf("proxmox volume exceeds download size limit")
	}
	return nil
}

func backupAccessError(volid string, meta []byte) string {
	base := vzdumpBasename(volid)
	if IsVzdumpArchive(volid) || strings.Contains(strings.ToLower(string(meta)), "vzdump-lxc-") {
		if base == "" {
			base = volid
		}
		return volid + " is a Proxmox LXC vzdump archive, not a disk image. The storage content API returned volume metadata only and does not stream the .tar.zst. No-dal could not read the archive on this host (typical path is the directory storage dump folder, for example /var/lib/vz/dump/" + base + "). Run No-dal on the Proxmox host so that path is readable, or copy the archive here and import it."
	}
	return "source storage does not expose a downloadable disk file for " + volid + ". Copy the image on the source host and use Disk import, or import an LXC tar backup"
}
