package backuphost

import (
	"context"
	"strings"

	"github.com/no-dal/ndl-ce/internal/backup"
	"github.com/no-dal/ndl-ce/internal/backupscope"
	"github.com/no-dal/ndl-ce/internal/docker"
	"github.com/no-dal/ndl-ce/internal/lxc"
)

func (h *Host) dockerInventory(ctx context.Context, workloadID, name string) (*backup.DockerInfo, *backupscope.DockerHint) {
	if h == nil || strings.TrimSpace(workloadID) == "" {
		return nil, nil
	}
	eng := h.docker
	if eng == nil {
		eng = &docker.Engine{}
	}
	inv, err := eng.Snapshot(ctx, []docker.MachineHint{{
		ID: workloadID, Name: name, Kind: lxc.KindSystemContainer,
	}})
	if err != nil {
		return nil, nil
	}
	info := &backup.DockerInfo{}
	hint := &backupscope.DockerHint{}
	seenVol := map[string]struct{}{}
	seenBind := map[string]struct{}{}
	seenImg := map[string]struct{}{}
	for _, m := range inv.Machines {
		if m.ID != workloadID {
			continue
		}
		info.EngineVersion = m.DockerVersion
		hint.EngineVersion = m.DockerVersion
		for _, p := range m.Projects {
			if p.Name != "" && p.Name != docker.StandaloneProject {
				info.ComposeProject = appendUnique(info.ComposeProject, p.Name)
				hint.Projects = appendUnique(hint.Projects, p.Name)
			}
			if p.ConfigFiles != "" {
				info.ComposeFiles = appendUnique(info.ComposeFiles, p.ConfigFiles)
			}
		}
	}
	for _, c := range inv.Containers {
		if c.MachineID != workloadID {
			continue
		}
		if c.Image != "" {
			if _, ok := seenImg[c.Image]; !ok {
				seenImg[c.Image] = struct{}{}
				info.Images = append(info.Images, c.Image)
				hint.Images = append(hint.Images, c.Image)
				if isLocalDockerImage(c.Image, c.ImageID) {
					info.LocalImages = appendUnique(info.LocalImages, c.Image)
				} else {
					info.RegistryImages = appendUnique(info.RegistryImages, c.Image)
				}
			}
		}
		for _, m := range c.Mounts {
			switch strings.ToLower(m.Type) {
			case "volume":
				name := firstNonEmpty(m.Name, m.Source)
				if name == "" {
					continue
				}
				if _, ok := seenVol[name]; ok {
					continue
				}
				seenVol[name] = struct{}{}
				info.NamedVolumes = append(info.NamedVolumes, name)
				hint.NamedVolumes = append(hint.NamedVolumes, name)
				// Keep an explicit host path only when it already points into
				// the Docker volumes tree; otherwise resolve the named volume to
				// its real host location so the backup captures the data.
				hostPath := strings.TrimSpace(m.Source)
				if !strings.HasPrefix(hostPath, "/var/lib/docker/volumes/") {
					hostPath = backupscope.ResolveDockerVolumePath("", name)
				}
				hint.VolumePaths = append(hint.VolumePaths, hostPath)
			case "bind":
				src := firstNonEmpty(m.Source, m.Destination)
				if src == "" {
					continue
				}
				if _, ok := seenBind[src]; ok {
					continue
				}
				seenBind[src] = struct{}{}
				info.BindMounts = append(info.BindMounts, src)
				hint.BindMounts = append(hint.BindMounts, src)
			}
		}
	}
	if info.EngineVersion == "" && len(info.NamedVolumes)+len(info.BindMounts)+len(info.Images) == 0 {
		return nil, nil
	}
	info.Reconstructable = len(info.NamedVolumes)+len(info.BindMounts)+len(info.Images) > 0
	if len(info.Images) > 0 {
		info.Uncertainty = append(info.Uncertainty, "container process state and anonymous layers are not rebuilt")
	}
	if len(info.LocalImages) > 0 {
		info.Uncertainty = append(info.Uncertainty, "local-only images cannot be pulled from a registry during restore")
	}
	if len(info.ComposeProject) > 0 || len(info.ComposeFiles) > 0 {
		info.Uncertainty = append(info.Uncertainty, "compose projects are inventoried; restore does not recreate the Docker engine")
	}
	return info, hint
}

func isLocalDockerImage(ref, imageID string) bool {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "sha256:") || strings.HasPrefix(imageID, "sha256:") && !strings.Contains(ref, "/") {
		return true
	}
	host, _, ok := strings.Cut(ref, "/")
	if !ok {
		return true
	}
	return !strings.Contains(host, ".") && host != "localhost" && !strings.Contains(host, ":")
}

func appendUnique(in []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return in
	}
	for _, e := range in {
		if e == v {
			return in
		}
	}
	return append(in, v)
}
