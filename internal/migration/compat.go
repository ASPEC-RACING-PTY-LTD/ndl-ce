package migration

import (
	"fmt"
	"strings"
)

func Analyze(m Manifest, mapping Mapping, destPools, destNets map[string]string, qemuFormats map[string]bool) (string, []Finding) {
	var findings []Finding
	if m.SchemaVersion != "" && m.SchemaVersion != ManifestSchema {
		findings = append(findings, Finding{Level: CompatBlocked, Code: "schema", Message: "Manifest schema is not " + ManifestSchema + "."})
	}
	if m.Kind != KindVM && m.Kind != KindContainer {
		findings = append(findings, Finding{Level: CompatUnsupported, Code: "kind", Message: "Workload kind is not supported by No-dal V1 migration."})
	}
	if m.Kind == KindVM && m.VM == nil {
		findings = append(findings, Finding{Level: CompatBlocked, Code: "vm-section", Message: "VM manifest is missing the vm section."})
	}
	if m.Kind == KindContainer && m.Container == nil {
		findings = append(findings, Finding{Level: CompatBlocked, Code: "ct-section", Message: "Container manifest is missing the container section."})
	}
	if m.Kind == KindVM && m.VM != nil {
		findings = append(findings, analyzeVM(*m.VM, mapping, destPools, destNets, qemuFormats)...)
	}
	if m.Kind == KindContainer && m.Container != nil {
		findings = append(findings, analyzeCT(*m.Container, mapping, destNets)...)
	}
	findings = append(findings, m.Warnings...)
	return rollup(findings), findings
}

func analyzeVM(vm VMSection, mapping Mapping, destPools, destNets map[string]string, qemuFormats map[string]bool) []Finding {
	var out []Finding
	if vm.CPUs <= 0 || vm.MemoryBytes <= 0 {
		out = append(out, Finding{Level: CompatRequiresMapping, Code: "hardware", Message: "CPU and memory must be provided. A disk image does not contain that configuration."})
	}
	if vm.CPUType != "" && !strings.EqualFold(vm.CPUType, "host") && !strings.EqualFold(vm.CPUType, "qemu64") && !strings.EqualFold(vm.CPUType, "kvm64") {
		out = append(out, Finding{Level: CompatWarning, Code: "cpu-type", Message: "Source CPU type does not map exactly. Generic CPU model will be required."})
	}
	if vm.TPM != nil && vm.TPM.Present {
		out = append(out, Finding{Level: CompatWarning, Code: "tpm", Message: "TPM state is not transferred. Destination TPM must be created empty if required."})
	}
	if vm.SecureBoot {
		out = append(out, Finding{Level: CompatWarning, Code: "secure-boot", Message: "Secure boot firmware must exist on the destination host."})
	}
	for _, d := range vm.Disks {
		if d.Storage != "" && mapping.Storage[d.Storage] == "" && destPools[d.Storage] == "" {
			out = append(out, Finding{Level: CompatRequiresMapping, Code: "storage", Message: "Source storage " + d.Storage + " has no destination mapping."})
		}
		format := strings.ToLower(d.Format)
		if format != "" && qemuFormats != nil && !qemuFormats[format] && format != "qcow2" && format != "raw" {
			out = append(out, Finding{Level: CompatBlocked, Code: "disk-format", Message: "Required disk format " + d.Format + " cannot currently be read."})
		}
	}
	if len(vm.Disks) == 0 {
		out = append(out, Finding{Level: CompatBlocked, Code: "disks", Message: "VM has no disks to import."})
	}
	for _, n := range vm.NICs {
		key := n.Bridge
		if n.VLAN > 0 {
			key = fmt.Sprintf("%s/%d", n.Bridge, n.VLAN)
		}
		if key != "" && mapping.Network[key] == "" && mapping.Network[n.Bridge] == "" && destNets[key] == "" {
			out = append(out, Finding{Level: CompatRequiresMapping, Code: "network", Message: "Source network " + key + " has no destination mapping."})
		}
	}
	return out
}

func analyzeCT(ct ContainerSection, mapping Mapping, destNets map[string]string) []Finding {
	var out []Finding
	if ct.Rootfs == nil || ct.Rootfs.Path == "" {
		out = append(out, Finding{Level: CompatBlocked, Code: "rootfs", Message: "Container root filesystem artifact is missing."})
	}
	if ct.Privileged {
		out = append(out, Finding{Level: CompatWarning, Code: "privileged", Message: "Privileged containers require admin to create on No-dal."})
	}
	if ct.UIDMap != "" && !strings.Contains(ct.UIDMap, "100000") {
		out = append(out, Finding{Level: CompatWarning, Code: "idmap", Message: "Source UID/GID mapping is not identical to the No-dal default unprivileged map. Ownership will be translated and may not match bit-for-bit."})
	}
	for _, n := range ct.NICs {
		if n.Bridge != "" && mapping.Network[n.Bridge] == "" && destNets[n.Bridge] == "" {
			out = append(out, Finding{Level: CompatRequiresMapping, Code: "network", Message: "Source network " + n.Bridge + " has no destination mapping."})
		}
	}
	if len(ct.Mounts) > 0 {
		out = append(out, Finding{Level: CompatWarning, Code: "mounts", Message: "Bind mount points are recorded as warnings. No-dal V1 system containers do not recreate arbitrary host bind mounts."})
	}
	return out
}

func rollup(findings []Finding) string {
	rank := map[string]int{
		CompatReady: 0, CompatWarning: 1, CompatRequiresMapping: 2, CompatUnsupported: 3, CompatBlocked: 4,
	}
	best := CompatReady
	for _, f := range findings {
		if rank[f.Level] > rank[best] {
			best = f.Level
		}
	}
	return best
}

func SnapshotsNotMigrated() Finding {
	return Finding{Level: CompatWarning, Code: "snapshots", Message: "Historical snapshots will not be transferred by this migration mode."}
}
