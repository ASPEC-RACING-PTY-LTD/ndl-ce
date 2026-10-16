package backup

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// The uploaded index remembers, per target, which packs are confirmed on the
// remote, so completing a restore point does not HEAD every deduplicated pack
// it shares with earlier backups. Packs are immutable and content-named, so a
// confirmed pack stays valid until this host deletes it: the remote sweep and
// a wipe remove entries, and losing the file only costs extra HEADs.

var uploadedMu sync.Mutex

func (r *Repository) uploadedPath(targetKey string) string {
	return filepath.Join(r.root, "state", "remote-uploaded", sanitize(targetKey)+".txt")
}

// LoadUploaded returns the packs confirmed on the target.
func (r *Repository) LoadUploaded(targetKey string) map[string]struct{} {
	uploadedMu.Lock()
	defer uploadedMu.Unlock()
	return r.loadUploadedLocked(targetKey)
}

func (r *Repository) loadUploadedLocked(targetKey string) map[string]struct{} {
	out := map[string]struct{}{}
	raw, err := os.ReadFile(r.uploadedPath(targetKey))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

// MarkUploaded records packs confirmed on the target.
func (r *Repository) MarkUploaded(targetKey string, ids ...string) error {
	if targetKey == "" || len(ids) == 0 {
		return nil
	}
	uploadedMu.Lock()
	defer uploadedMu.Unlock()
	path := r.uploadedPath(targetKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(ids, "\n") + "\n")
	return err
}

// ForgetUploaded drops packs deleted from the target.
func (r *Repository) ForgetUploaded(targetKey string, ids ...string) error {
	if targetKey == "" || len(ids) == 0 {
		return nil
	}
	uploadedMu.Lock()
	defer uploadedMu.Unlock()
	set := r.loadUploadedLocked(targetKey)
	for _, id := range ids {
		delete(set, id)
	}
	keep := make([]string, 0, len(set))
	for id := range set {
		keep = append(keep, id)
	}
	sort.Strings(keep)
	path := r.uploadedPath(targetKey)
	if len(keep) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(path, []byte(strings.Join(keep, "\n")+"\n"))
}

// ClearUploaded forgets every confirmed pack of a target (after a wipe).
func (r *Repository) ClearUploaded(targetKey string) {
	uploadedMu.Lock()
	defer uploadedMu.Unlock()
	_ = os.Remove(r.uploadedPath(targetKey))
}
