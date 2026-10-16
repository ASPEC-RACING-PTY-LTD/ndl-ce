package backuphost

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// platformPrefix confines ActionPlatformObject to the platform backup.
const platformPrefix = "ndl-platform/"

// maxPlatformObject bounds one platform object read into memory.
const maxPlatformObject = 2 << 30

// platformObject puts, gets, deletes or lists one platform backup object.
// Bodies travel through files: put reads req.Path; get writes req.Path,
// owned like its directory, so the unprivileged control plane can read it.
func (h *Host) platformObject(ctx context.Context, req Request) (Result, error) {
	if !hasTarget(req.Target) || !isObjectKind(req.Target.Kind) {
		return Result{}, fmt.Errorf("the platform backup needs an object storage target")
	}
	key := strings.TrimSpace(req.ObjectKey)
	if !strings.HasPrefix(key, platformPrefix) || strings.Contains(key, "..") {
		return Result{}, fmt.Errorf("platform objects live under %s", platformPrefix)
	}
	tgt, err := h.remoteTarget(req.Target)
	if err != nil {
		return Result{}, err
	}
	out := Result{}
	switch req.ObjectOp {
	case "put":
		body, err := os.ReadFile(req.Path)
		if err != nil {
			return Result{}, err
		}
		return out, tgt.Put(ctx, key, body)
	case "get":
		ok, size, err := tgt.Head(ctx, key)
		if err != nil {
			return Result{}, err
		}
		if !ok {
			out.NotFound = true
			return out, nil
		}
		if size > maxPlatformObject {
			return Result{}, fmt.Errorf("%s is too large", key)
		}
		body, err := tgt.Get(ctx, key)
		if err != nil {
			return Result{}, err
		}
		return out, writeOwnedLikeDir(req.Path, body)
	case "delete":
		rc, ok := tgt.(backup.RemoteCleaner)
		if !ok {
			return Result{}, fmt.Errorf("this target cannot delete objects")
		}
		return out, rc.Delete(ctx, key)
	case "list":
		rc, ok := tgt.(backup.RemoteCleaner)
		if !ok {
			return Result{}, fmt.Errorf("this target cannot list objects")
		}
		objs, err := rc.List(ctx, key)
		if err != nil {
			return Result{}, err
		}
		out.Objects = objs
		return out, nil
	}
	return Result{}, fmt.Errorf("unknown object operation %q", req.ObjectOp)
}

func writeOwnedLikeDir(path string, body []byte) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("an absolute path is required")
	}
	dir := filepath.Dir(path)
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	tmp := path + ".part"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(tmp, int(sys.Uid), int(sys.Gid))
	}
	return os.Rename(tmp, path)
}

// remoteCheck checks that restore points can be fetched from a target.
func (h *Host) remoteCheck(ctx context.Context, req Request) (Result, error) {
	if !hasTarget(req.Target) {
		return Result{}, fmt.Errorf("a target is required")
	}
	tgt, err := h.remoteTarget(req.Target)
	if err != nil {
		return Result{}, err
	}
	out := Result{}
	for _, p := range req.Checks {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		out.RemoteChecks = append(out.RemoteChecks, backup.CheckRemote(ctx, tgt, h.repo.Keys(), p.Namespace, p.BackupID, req.Deep, req.Sample))
	}
	return out, nil
}

// importKey installs a recovered repository key, so restore points made by
// the lost host can be read. It refuses when this repository already holds
// restore points sealed with another key.
func (h *Host) importKey(req Request) (Result, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(req.ImportKey))
	if err != nil || len(raw) < 32 {
		return Result{}, fmt.Errorf("the recovered key is not valid")
	}
	keys, err := backup.NewKeys(raw)
	if err != nil {
		return Result{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cur, _ := os.ReadFile(filepath.Join(h.root, "master.key"))
	if bytes.Equal(cur, raw) {
		return Result{}, nil
	}
	if len(h.busy) > 0 {
		return Result{}, fmt.Errorf("a backup is running")
	}
	if states, _ := h.repo.ListStates(); len(states) > 0 {
		return Result{}, fmt.Errorf("this host already has %d restore points sealed with its own key; the recovered key was not installed", len(states))
	}
	if err := writeKey(filepath.Join(h.root, "master.key"), raw); err != nil {
		return Result{}, err
	}
	if h.keyPath != "" {
		if err := writeKey(h.keyPath, raw); err != nil {
			return Result{}, err
		}
	}
	repo, err := backup.OpenRepository(h.root, keys)
	if err != nil {
		return Result{}, err
	}
	for id, q := range h.queues {
		if q != nil {
			q.Stop()
		}
		delete(h.queues, id)
	}
	h.repo = repo
	h.eng = backup.NewEngine(repo, h.engineConfig(h.settings))
	h.keyNote = ""
	return Result{}, nil
}
