package migration

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type libvirtDomain struct {
	XMLName xml.Name `xml:"domain"`
	Name    string   `xml:"name"`
	Memory  struct {
		Unit string `xml:"unit,attr"`
		Val  int64  `xml:",chardata"`
	} `xml:"memory"`
	VCPU int `xml:"vcpu"`
	OS   struct {
		Firmware string `xml:"firmware,attr"`
		Type     struct {
			Machine string `xml:"machine,attr"`
		} `xml:"type"`
	} `xml:"os"`
	Devices struct {
		Disks []libvirtDisk `xml:"disk"`
		Ifs   []libvirtIf   `xml:"interface"`
	} `xml:"devices"`
}

type libvirtDisk struct {
	Type   string `xml:"type,attr"`
	Device string `xml:"device,attr"`
	Driver struct {
		Type string `xml:"type,attr"`
	} `xml:"driver"`
	Source struct {
		File string `xml:"file,attr"`
	} `xml:"source"`
	Target struct {
		Dev string `xml:"dev,attr"`
		Bus string `xml:"bus,attr"`
	} `xml:"target"`
}

type libvirtIf struct {
	Type string `xml:"type,attr"`
	MAC  struct {
		Address string `xml:"address,attr"`
	} `xml:"mac"`
	Source struct {
		Bridge string `xml:"bridge,attr"`
		Net    string `xml:"network,attr"`
	} `xml:"source"`
	Model struct {
		Type string `xml:"type,attr"`
	} `xml:"model"`
}

func ParseLibvirtXML(r io.Reader) (Manifest, error) {
	var d libvirtDomain
	if err := xml.NewDecoder(r).Decode(&d); err != nil {
		return Manifest{}, fmt.Errorf("malformed libvirt domain XML")
	}
	mem := d.Memory.Val
	switch strings.ToLower(d.Memory.Unit) {
	case "kib", "ki", "k":
		mem *= 1024
	case "mib", "mi", "m", "":
		mem *= 1024 * 1024
	case "gib", "gi", "g":
		mem *= 1024 * 1024 * 1024
	}
	fw := "bios"
	if strings.Contains(strings.ToLower(d.OS.Firmware), "efi") {
		fw = "uefi"
	}
	m := Manifest{
		SchemaVersion: ManifestSchema,
		Kind:          KindVM,
		Identity:      Identity{Name: d.Name},
		Source:        SourceMeta{Adapter: AdapterLibvirt, Type: "libvirt-xml"},
		VM: &VMSection{
			CPUs: d.VCPU, MemoryBytes: mem, Firmware: fw, Machine: d.OS.Type.Machine,
		},
	}
	if m.VM.CPUs == 0 {
		m.VM.CPUs = 1
	}
	di := 0
	for _, disk := range d.Devices.Disks {
		if disk.Device == "cdrom" {
			continue
		}
		m.VM.Disks = append(m.VM.Disks, Disk{
			ID: disk.Target.Dev, Role: roleFor(di), Source: disk.Source.File,
			Format: firstNonEmpty(disk.Driver.Type, "qcow2"), Bus: disk.Target.Bus, Artifact: disk.Source.File,
		})
		di++
	}
	for _, iface := range d.Devices.Ifs {
		br := iface.Source.Bridge
		if br == "" {
			br = iface.Source.Net
		}
		m.VM.NICs = append(m.VM.NICs, NIC{Model: firstNonEmpty(iface.Model.Type, "virtio"), MAC: iface.MAC.Address, Bridge: br})
	}
	return m, nil
}

func ParseMemoryKiB(s string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, err
	}
	return n * 1024, nil
}
