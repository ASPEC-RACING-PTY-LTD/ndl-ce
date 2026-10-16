package docker

import (
	"context"
	"io"
	"net/url"
)

// PruneResult is what pruning one machine's Docker reclaimed.
type PruneResult struct {
	MachineID      string `json:"machine_id"`
	ImagesDeleted  int    `json:"images_deleted"`
	ImageBytes     int64  `json:"image_bytes"`
	BuildCacheByte int64  `json:"build_cache_bytes"`
}

// PruneUnused removes images no container uses (running or stopped) and the
// build cache. Containers, volumes and networks are never touched.
func (e *Engine) PruneUnused(ctx context.Context, machineID string) (PruneResult, error) {
	out := PruneResult{MachineID: machineID}
	sock, err := e.SocketFor(ctx, machineID)
	if err != nil {
		return out, err
	}
	cli, _ := e.client(sock).(*unixClient)
	if cli == nil {
		cli = sharedUnixClient(sock)
	}
	// dangling=false prunes every unused image, not only untagged ones
	// (docker image prune --all).
	var images struct {
		ImagesDeleted  []map[string]string `json:"ImagesDeleted"`
		SpaceReclaimed int64               `json:"SpaceReclaimed"`
	}
	q := url.Values{"filters": {`{"dangling":["false"]}`}}
	if err := cli.json(ctx, "POST", "/images/prune?"+q.Encode(), nil, &images); err != nil {
		return out, err
	}
	for _, d := range images.ImagesDeleted {
		if d["Deleted"] != "" {
			out.ImagesDeleted++
		}
	}
	out.ImageBytes = images.SpaceReclaimed
	var build struct {
		SpaceReclaimed int64 `json:"SpaceReclaimed"`
	}
	if err := cli.json(ctx, "POST", "/build/prune?all=1", nil, &build); err == nil {
		out.BuildCacheByte = build.SpaceReclaimed
	}
	return out, nil
}

// ImageDigests maps each image ID on a machine to its registry digests
// (repo@sha256:...). A locally built image has none.
func (e *Engine) ImageDigests(ctx context.Context, machineID string) (map[string][]string, error) {
	sock, err := e.SocketFor(ctx, machineID)
	if err != nil {
		return nil, err
	}
	cli, _ := e.client(sock).(*unixClient)
	if cli == nil {
		cli = sharedUnixClient(sock)
	}
	var images []struct {
		ID          string   `json:"Id"`
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := cli.json(ctx, "GET", "/images/json", nil, &images); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(images))
	for _, im := range images {
		out[im.ID] = im.RepoDigests
	}
	return out, nil
}

// SaveImage writes image (a reference or id) to w as a docker-save tar.
func (e *Engine) SaveImage(ctx context.Context, machineID, image string, w io.Writer) error {
	sock, err := e.SocketFor(ctx, machineID)
	if err != nil {
		return err
	}
	cli, _ := e.client(sock).(*unixClient)
	if cli == nil {
		cli = sharedUnixClient(sock)
	}
	res, err := cli.do(ctx, "GET", "/images/"+url.PathEscape(image)+"/get", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, err = io.Copy(w, res.Body)
	return err
}
