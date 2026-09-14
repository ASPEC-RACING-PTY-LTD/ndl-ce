package inventory

import "sort"

func collectIOMMU(opt Options) IOMMU {
	fs := opt.fs()
	if !fs.exists("sys/kernel/iommu_groups") {
		return IOMMU{Status: StatusUnavailable}
	}
	groups := listIOMMUGroups(fs)
	if len(groups) == 0 {
		return IOMMU{Status: StatusUnavailable}
	}
	return IOMMU{Status: StatusAvailable, Groups: groups}
}

func listIOMMUGroups(fs FS) []IOMMUGroup {
	var groups []IOMMUGroup
	for _, id := range fs.list("sys/kernel/iommu_groups") {
		if !allDigits(id) {
			continue
		}
		devs := fs.list("sys/kernel/iommu_groups/" + id + "/devices")
		sort.Strings(devs)
		groups = append(groups, IOMMUGroup{ID: id, Devices: devs})
	}
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].ID < groups[j].ID
	})
	return groups
}

func iommuGroupByDevice(groups []IOMMUGroup) map[string]string {
	out := map[string]string{}
	for _, g := range groups {
		for _, d := range g.Devices {
			out[d] = g.ID
		}
	}
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
