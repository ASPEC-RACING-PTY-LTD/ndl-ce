package backuphost

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backup"
)

// KeptImagesDir is where a workload's kept image copies live inside the
// guest, so they are captured with its data and loaded again on restore.
const KeptImagesDir = "/var/lib/ndl-backup/images"

// keptImageName names an image's tar by its id, so an unchanged image is
// not exported again.
func keptImageName(imageID string) string {
	id := strings.TrimPrefix(imageID, "sha256:")
	if len(id) > 32 {
		id = id[:32]
	}
	return id + ".tar"
}

// exportImages keeps a docker-save copy of every image the workload's
// containers run under KeptImagesDir in its root, and removes copies of
// images it no longer runs. The guest's Docker must be running.
func (h *Host) exportImages(ctx context.Context, rootfs string, req Request) (Result, error) {
	if rootfs == "" || !filepath.IsAbs(rootfs) {
		return Result{}, fmt.Errorf("the workload root is required")
	}
	info, _ := h.dockerInventory(ctx, req.WorkloadID, req.WorkloadName)
	if info == nil || len(info.ImagePins) == 0 {
		return Result{}, nil
	}
	dir := filepath.Join(rootfs, strings.TrimPrefix(KeptImagesDir, "/"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, err
	}
	want := map[string]backup.ImagePin{}
	for _, p := range info.ImagePins {
		if p.ImageID != "" {
			want[keptImageName(p.ImageID)] = p
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	out := Result{}
	eng := h.docker
	if eng == nil {
		return Result{}, fmt.Errorf("docker is not available to the agent")
	}
	for _, n := range names {
		path := filepath.Join(dir, n)
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			out.KeptImages = append(out.KeptImages, want[n].Ref)
			continue
		}
		tmp := path + ".part"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return Result{}, err
		}
		err = eng.SaveImage(ctx, req.WorkloadID, want[n].ImageID, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(tmp)
			return Result{}, fmt.Errorf("keep a copy of %s: %w", want[n].Ref, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return Result{}, err
		}
		out.KeptImages = append(out.KeptImages, want[n].Ref)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if _, ok := want[e.Name()]; !ok {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}
