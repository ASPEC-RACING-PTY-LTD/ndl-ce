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
				info.ComposeProject = appendUnique(info.ComposeProject, p.ConfigFiles)
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
				hostPath := strings.TrimSpace(m.Source)
				if !strings.HasPrefix(hostPath, "/") {
					hostPath = backupscope.ResolveDockerVolumePath("", name)
				} else if strings.HasPrefix(hostPath, "/var/lib/docker/volumes/") {
					hostPath = hostPath
				} else {
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
	return info, hint
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
