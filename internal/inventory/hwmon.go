package inventory

import (
	"sort"
	"strings"
)

func collectTemps(opt Options) []Sensor {
	fs := opt.fs()
	chips := fs.list("sys/class/hwmon")
	sort.Strings(chips)

	var out []Sensor
	for _, chip := range chips {
		if !strings.HasPrefix(chip, "hwmon") {
			continue
		}
		base := "sys/class/hwmon/" + chip
		name := fs.readOK(base + "/name")
		ents := fs.list(base)
		sort.Strings(ents)
		for _, ent := range ents {
			if !strings.HasPrefix(ent, "temp") || !strings.HasSuffix(ent, "_input") {
				continue
			}
			milli, ok := fs.readInt(base + "/" + ent)
			if !ok {
				continue
			}
			stem := strings.TrimSuffix(ent, "_input")
			v := int64(milli)
			out = append(out, Sensor{
				ID:     chip + "/" + stem,
				Name:   name,
				Label:  fs.readOK(base + "/" + stem + "_label"),
				MilliC: &v,
				Status: StatusAvailable,
			})
		}
	}
	return out
}
