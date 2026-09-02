package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// WriteOVFPackage writes an OVF compatible export package. It does not create a remote VM.
func WriteOVFPackage(dir string, m Manifest, diskFiles map[string]string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	name := m.Identity.Name
	if name == "" {
		name = "exported"
	}
	var files []ovfFile
	var disks []ovfDisk
	i := 0
	copied := map[string]string{}
	for id, src := range diskFiles {
		href := filepath.Base(src)
		if href == "" || href == "." {
			href = "disk" + strconv.Itoa(i) + ".vmdk"
		}
		dst := filepath.Join(dir, href)
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o640); err != nil {
			return err
		}
		fid := "file" + strconv.Itoa(i)
		files = append(files, ovfFile{ID: fid, Href: href, Size: int64(len(body))})
		cap := "1"
		if m.VM != nil && i < len(m.VM.Disks) && m.VM.Disks[i].SizeBytes > 0 {
			cap = strconv.FormatInt(m.VM.Disks[i].SizeBytes, 10)
		}
		disks = append(disks, ovfDisk{FileRef: fid, Capacity: cap, Format: "http://www.vmware.com/interfaces/specifications/vmdk.html#streamOptimized"})
		copied[id] = href
		i++
	}
	cpus, mem := 1, int64(512)
	if m.VM != nil {
		if m.VM.CPUs > 0 {
			cpus = m.VM.CPUs
		}
		if m.VM.MemoryBytes > 0 {
			mem = m.VM.MemoryBytes / (1024 * 1024)
		}
	}
	ovf := `<?xml version="1.0" encoding="UTF-8"?>
<Envelope xmlns="http://schemas.dmtf.org/ovf/envelope/1" xmlns:ovf="http://schemas.dmtf.org/ovf/envelope/1" xmlns:rasd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_ResourceAllocationSettingData" xmlns:vssd="http://schemas.dmtf.org/wbem/wscim/1/cim-schema/2/CIM_VirtualSystemSettingData">
  <References>
`
	for _, f := range files {
		ovf += fmt.Sprintf("    <File ovf:id=%q ovf:href=%q ovf:size=%q/>\n", f.ID, f.Href, strconv.FormatInt(f.Size, 10))
	}
	ovf += `  </References>
  <DiskSection>
    <Info>Virtual disk information</Info>
`
	for i, d := range disks {
		ovf += fmt.Sprintf("    <Disk ovf:diskId=%q ovf:fileRef=%q ovf:capacity=%q ovf:format=%q/>\n", "disk"+strconv.Itoa(i), d.FileRef, d.Capacity, d.Format)
	}
	ovf += fmt.Sprintf(`  </DiskSection>
  <VirtualSystem ovf:id=%q>
    <Name>%s</Name>
    <VirtualHardwareSection>
      <Info>Virtual hardware</Info>
      <Item>
        <rasd:Description>Number of Virtual CPUs</rasd:Description>
        <rasd:ElementName>%d virtual CPU(s)</rasd:ElementName>
        <rasd:ResourceType>3</rasd:ResourceType>
        <rasd:VirtualQuantity>%d</rasd:VirtualQuantity>
      </Item>
      <Item>
        <rasd:AllocationUnits>byte * 2^20</rasd:AllocationUnits>
        <rasd:Description>Memory Size</rasd:Description>
        <rasd:ElementName>%d MB of memory</rasd:ElementName>
        <rasd:ResourceType>4</rasd:ResourceType>
        <rasd:VirtualQuantity>%d</rasd:VirtualQuantity>
      </Item>
    </VirtualHardwareSection>
  </VirtualSystem>
</Envelope>
`, xmlEscape(name), xmlEscape(name), cpus, cpus, mem, mem)
	if err := os.WriteFile(filepath.Join(dir, name+".ovf"), []byte(ovf), 0o640); err != nil {
		return err
	}
	notes := "COMPATIBLE EXPORT PACKAGE\n\nThis directory is an OVF package. Import it with a hypervisor that accepts OVF.\nNo-dal did not create a remote virtual machine.\nSource remains unchanged.\nExported at " + time.Now().UTC().Format(time.RFC3339) + "\n"
	return os.WriteFile(filepath.Join(dir, "IMPORT.txt"), []byte(notes), 0o640)
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

// WritePVEPackage writes disks plus translated notes for Proxmox qm/pct import.
func WritePVEPackage(dir string, m Manifest, diskFiles map[string]string) error {
	if err := os.MkdirAll(filepath.Join(dir, "disks"), 0o750); err != nil {
		return err
	}
	files := map[string]string{}
	for id, src := range diskFiles {
		base := filepath.Base(src)
		dst := filepath.Join("disks", base)
		files[dst] = src
		_ = id
	}
	if err := WriteBundle(dir, m, files); err != nil {
		return err
	}
	notes := "COMPATIBLE EXPORT PACKAGE\n\nProxmox VE V1 export does not create a remote guest over the API.\nImport the converted disks with qm importdisk / pct restore as documented by Proxmox.\nDo not delete the original No-dal workload unless you choose to.\n"
	if m.Kind == KindVM && m.VM != nil {
		notes += fmt.Sprintf("\nSuggested: qm create <vmid> --name %s --memory %d --cores %d --bios %s\n", m.Identity.Name, m.VM.MemoryBytes/(1024*1024), m.VM.CPUs, m.VM.Firmware)
	}
	if m.Kind == KindContainer && m.Container != nil {
		notes += fmt.Sprintf("\nSuggested: pct restore after converting the rootfs archive. Privileged=%v\n", m.Container.Privileged)
	}
	return os.WriteFile(filepath.Join(dir, "IMPORT.txt"), []byte(notes), 0o640)
}

func DefaultQEMUFormats() map[string]bool {
	return map[string]bool{"qcow2": true, "raw": true, "vmdk": true, "vpc": true, "vhdx": true}
}

func RejectDestructiveRequest(raw []byte) error {
	low := strings.ToLower(string(raw))
	for _, key := range []string{
		`"delete_source"`, `"cleanup_source"`, `"destroy_source"`, `"decommission_source"`,
		`"delete_source_after"`, `"remove_source"`,
	} {
		if strings.Contains(low, key) {
			return fmt.Errorf("source destruction is not a migration operation")
		}
	}
	return nil
}
