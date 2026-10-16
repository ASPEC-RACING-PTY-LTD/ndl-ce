package docker

import (
	"context"
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
