package migration

import "fmt"

func ApplyMapping(m Manifest, bulk Mapping, override *Mapping) Manifest {
	mapn := bulk
	if override != nil {
		if override.Storage != nil {
			if mapn.Storage == nil {
				mapn.Storage = map[string]string{}
			}
			for k, v := range override.Storage {
				mapn.Storage[k] = v
			}
		}
		if override.Network != nil {
			if mapn.Network == nil {
				mapn.Network = map[string]string{}
			}
			for k, v := range override.Network {
				mapn.Network[k] = v
			}
		}
		if override.VLAN != nil {
			if mapn.VLAN == nil {
				mapn.VLAN = map[string]string{}
			}
			for k, v := range override.VLAN {
				mapn.VLAN[k] = v
			}
		}
	}
	if m.VM != nil {
		for i := range m.VM.Disks {
			if dest, ok := mapn.Storage[m.VM.Disks[i].Storage]; ok {
				m.VM.Disks[i].Storage = dest
			}
		}
		for i := range m.VM.NICs {
			key := m.VM.NICs[i].Bridge
			if m.VM.NICs[i].VLAN > 0 {
				tagged := fmt.Sprintf("%s/%d", m.VM.NICs[i].Bridge, m.VM.NICs[i].VLAN)
				if dest, ok := mapn.Network[tagged]; ok {
					m.VM.NICs[i].Network = dest
					continue
				}
			}
			if dest, ok := mapn.Network[key]; ok {
				m.VM.NICs[i].Network = dest
			}
		}
	}
	if m.Container != nil {
		for i := range m.Container.NICs {
			if dest, ok := mapn.Network[m.Container.NICs[i].Bridge]; ok {
				m.Container.NICs[i].Network = dest
			}
		}
	}
	return m
}

func MissingMappings(m Manifest, mapping Mapping) []Finding {
	var out []Finding
	checkNet := func(bridge string, vlan int) {
		if bridge == "" {
			return
		}
		if vlan > 0 {
			if mapping.Network[fmt.Sprintf("%s/%d", bridge, vlan)] != "" || mapping.Network[bridge] != "" {
				return
			}
			out = append(out, Finding{Level: CompatRequiresMapping, Code: "network", Message: "Source network " + fmt.Sprintf("%s VLAN %d", bridge, vlan) + " has no destination mapping."})
			return
		}
		if mapping.Network[bridge] == "" {
			out = append(out, Finding{Level: CompatRequiresMapping, Code: "network", Message: "Source network " + bridge + " has no destination mapping."})
		}
	}
	if m.VM != nil {
		for _, d := range m.VM.Disks {
			if d.Storage != "" && mapping.Storage[d.Storage] == "" {
				out = append(out, Finding{Level: CompatRequiresMapping, Code: "storage", Message: "Source storage " + d.Storage + " has no destination mapping."})
			}
		}
		for _, n := range m.VM.NICs {
			checkNet(n.Bridge, n.VLAN)
		}
	}
	if m.Container != nil {
		for _, n := range m.Container.NICs {
			checkNet(n.Bridge, n.VLAN)
		}
	}
	return out
}
