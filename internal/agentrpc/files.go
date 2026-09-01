package agentrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/iojail"
	"github.com/no-dal/ndl-ce/internal/lxc"
	"github.com/no-dal/ndl-ce/internal/qemu"
)

const filesChunk = 8 << 20

type fileEntry struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	ModTime string `json:"mtime"`
	Path    string `json:"path"`
}

func (h *Handler) resolveJail(targetKind, targetID, requested string) (string, error) {
	kind := strings.TrimSpace(targetKind)
	switch kind {
	case "", iojail.TargetHost, "node":
		if requested == "" || requested == "/" {
			return "/", nil
		}
		return filepath.Clean(requested), nil
	case iojail.TargetCT, "workload":
		if h.Workloads != nil && strings.TrimSpace(targetID) != "" {
			applied, err := h.Workloads.LastApplied(targetID)
			if err == nil && applied.Spec.RootfsPath != "" {
				return filepath.Clean(applied.Spec.RootfsPath), nil
			}
		}
		if strings.TrimSpace(requested) == "" {
			return "", fmt.Errorf("jail_root is required for a system container")
		}
		return filepath.Clean(requested), nil
	case "vm-guest":
		id := strings.TrimSpace(targetID)
		if err := qemu.ValidateWorkloadID(id); err != nil {
			return "", err
		}
		return guestJailRoot, nil
	case "vm":
		id := strings.TrimSpace(targetID)
		if err := qemu.ValidateWorkloadID(id); err != nil {
			return "", err
		}
		p := filepath.Clean(strings.TrimSpace(requested))
		if requested == "" || p == "/" || p == "guest" || p == guestJailRoot {
			return guestJailRoot, nil
		}
		prefix := filepath.Join("/var/lib/ndl/runtime/qemu", id) + string(filepath.Separator)
		if !strings.HasPrefix(p, prefix) || strings.Contains(p, "..") {
			return "", fmt.Errorf("console socket is invalid")
		}
		switch filepath.Base(p) {
		case "serial.sock", "vnc.sock":
			return p, nil
		default:
			return "", fmt.Errorf("console socket is invalid")
		}
	default:
		return "", fmt.Errorf("unsupported target kind %q", kind)
	}
}

func runFilesOp(root, action, rel, dest string, mode uint32) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "list":
		return filesList(root, rel)
	case "stat":
		return filesStat(root, rel)
	case "mkdir":
		if mode == 0 {
			mode = 0o755
		}
		if err := filesMkdir(root, rel, fs.FileMode(mode)); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": rel})
	case "delete":
		if err := filesDelete(root, rel); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": rel})
	case "rename":
		if err := filesRename(root, rel, dest); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": dest})
	case "copy":
		if err := filesCopy(root, rel, dest); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"ok": true, "path": dest})
	default:
		return nil, fmt.Errorf("unknown files action %q", action)
	}
}

func filesList(root, rel string) ([]byte, error) {
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory")
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	entries := make([]fileEntry, 0, len(names))
	for _, name := range names {
		child := path.Join(rel, name)
		if rel == "" || rel == "." || rel == "/" {
			child = name
		}
		ent, err := statEntry(root, child, name)
		if err != nil {
			continue
		}
		entries = append(entries, ent)
	}
	return json.Marshal(map[string]any{"path": rel, "entries": entries})
}

func filesStat(root, rel string) ([]byte, error) {
	ent, err := statEntry(root, rel, path.Base(rel))
	if err != nil {
		return nil, err
	}
	return json.Marshal(ent)
}

func statEntry(root, rel, name string) (fileEntry, error) {
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return fileEntry{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fileEntry{}, err
	}
	kind := "file"
	if info.IsDir() {
		kind = "dir"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	}
	return fileEntry{
		Name:    name,
		Type:    kind,
		Size:    info.Size(),
		Mode:    uint32(info.Mode().Perm()),
		ModTime: info.ModTime().UTC().Format(time.RFC3339),
		Path:    rel,
	}, nil
}

func filesMkdir(root, rel string, mode fs.FileMode) error {
	return iojail.MkdirBeneath(root, rel, mode)
}

func filesDelete(root, rel string) error {
	return iojail.RemoveBeneath(root, rel)
}

func filesRename(root, src, dest string) error {
	return iojail.RenameBeneath(root, src, dest)
}

func filesCopy(root, src, dest string) error {
	in, _, err := iojail.OpenBeneath(root, src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(root, src, dest)
	}
	part := dest + ".part"
	out, _, err := iojail.OpenBeneath(root, part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = filesDelete(root, part)
		return copyErr
	}
	if closeErr != nil {
		_ = filesDelete(root, part)
		return closeErr
	}
	return filesRename(root, part, dest)
}

func copyDir(root, src, dest string) error {
	if err := filesMkdir(root, dest, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	f, _, err := iojail.OpenBeneath(root, src, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := filesCopy(root, path.Join(src, name), path.Join(dest, name)); err != nil {
			return err
		}
	}
	return nil
}

func writePartThenRename(root, rel string, mode uint32, r io.Reader, maxBytes int64, expectedSHA string) ([]byte, error) {
	if mode == 0 {
		mode = 0o644
	}
	part := rel + ".part"
	out, _, err := iojail.OpenBeneath(root, part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(mode))
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	var written int64
	buf := make([]byte, filesChunk)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			if n > filesChunk {
				_ = out.Close()
				_ = filesDelete(root, part)
				return nil, fmt.Errorf("chunk exceeds 8 MiB")
			}
			if maxBytes > 0 && written+int64(n) > maxBytes {
				_ = out.Close()
				_ = filesDelete(root, part)
				return nil, fmt.Errorf("upload exceeds max_bytes")
			}
			if _, err := out.Write(buf[:n]); err != nil {
				_ = out.Close()
				_ = filesDelete(root, part)
				return nil, err
			}
			_, _ = h.Write(buf[:n])
			written += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = out.Close()
			_ = filesDelete(root, part)
			return nil, readErr
		}
	}
	if err := out.Close(); err != nil {
		_ = filesDelete(root, part)
		return nil, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if expectedSHA != "" && !strings.EqualFold(expectedSHA, sum) {
		_ = filesDelete(root, part)
		return nil, fmt.Errorf("sha256 mismatch")
	}
	if err := filesRename(root, part, rel); err != nil {
		_ = filesDelete(root, part)
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": rel, "size": written, "sha256": sum})
}

func readFileSHA(root, rel string, emit func(chunk []byte, sha string) error) error {
	f, _, err := iojail.OpenBeneath(root, rel, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	h := sha256.New()
	buf := make([]byte, filesChunk)
	var pending []byte
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
			if pending != nil {
				if err := emit(pending, ""); err != nil {
					return err
				}
			}
			pending = append([]byte(nil), buf[:n]...)
		}
		if readErr == io.EOF {
			sum := hex.EncodeToString(h.Sum(nil))
			if err := emit(pending, sum); err != nil {
				return err
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func lxcRuntimePath(e *lxc.Engine) string {
	if e != nil {
		return e.RuntimeLXC()
	}
	return "/var/lib/ndl/runtime/lxc"
}
