package backuphost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/objstore"
)

func (h *Host) targetDir() string {
	return filepath.Join(h.root, "state", "targets")
}

func (h *Host) rememberTarget(spec TargetSpec) error {
	if spec.ID == "" {
		if spec.Kind == "" && spec.Locator == "" && spec.Bucket == "" {
			return nil
		}
		spec.ID = firstNonEmpty(spec.Bucket, spec.Locator, spec.Kind)
	}
	if err := os.MkdirAll(h.targetDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.targetDir(), spec.ID+".json"), raw, 0o600)
}

func (h *Host) resumeTargets(ctx context.Context) error {
	entries, err := os.ReadDir(h.targetDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(h.targetDir(), e.Name()))
		if err != nil {
			continue
		}
		var spec TargetSpec
		if json.Unmarshal(raw, &spec) != nil {
			continue
		}
		if _, err := h.queueFor(spec); err != nil {
			return err
		}
	}
	_ = ctx
	return nil
}

func (h *Host) queueFor(spec TargetSpec) (*backup.UploadQueue, error) {
	id := firstNonEmpty(spec.ID, spec.Bucket, spec.Locator, spec.Kind, "default")
	h.mu.Lock()
	if q := h.queues[id]; q != nil {
		h.mu.Unlock()
		return q, nil
	}
	workers := h.settings.UploadWorkers
	h.mu.Unlock()
	tgt, err := h.remoteTarget(spec)
	if err != nil {
		return nil, err
	}
	q := backup.NewUploadQueue(h.repo, tgt, workers)
	if err := q.Start(context.Background()); err != nil {
		return nil, err
	}
	h.mu.Lock()
	h.queues[id] = q
	h.mu.Unlock()
	return q, nil
}

func (h *Host) remoteTarget(spec TargetSpec) (backup.Target, error) {
	kind := strings.ToLower(strings.TrimSpace(spec.Kind))
	if kind == "" && spec.Locator != "" {
		kind = "local"
	}
	if kind == "local" || kind == "nfs" || kind == "smb" || (kind == "" && spec.Locator != "") {
		root := spec.Locator
		if spec.Prefix != "" {
			root = filepath.Join(root, spec.Prefix)
		}
		return backup.LocalTarget{Root: root}, nil
	}
	if isObjectKind(kind) {
		tr := objstore.NewS3Transport(spec.Endpoint, spec.Region, spec.AccessKey, spec.SecretKey, kind, nil)
		return backup.TransportTarget{T: tr, Bucket: spec.Bucket}, nil
	}
	if spec.Locator != "" {
		return backup.LocalTarget{Root: spec.Locator}, nil
	}
	return backup.LocalTarget{Root: filepath.Join(h.root, "remote-local")}, nil
}

func (h *Host) pendingJobs() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, q := range h.queues {
		if q != nil {
			n += q.PendingJobs()
		}
	}
	return n
}
