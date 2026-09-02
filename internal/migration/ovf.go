package migration

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ovfEnvelope struct {
	XMLName xml.Name  `xml:"Envelope"`
	Files   []ovfFile `xml:"References>File"`
	Disks   []ovfDisk `xml:"DiskSection>Disk"`
	Name    string    `xml:"VirtualSystem>Name"`
	Items   []ovfItem `xml:"VirtualSystem>VirtualHardwareSection>Item"`
}

type ovfFile struct {
	ID   string `xml:"id,attr"`
	Href string `xml:"href,attr"`
	Size int64  `xml:"size,attr"`
}

type ovfDisk struct {
	FileRef       string `xml:"fileRef,attr"`
	Capacity      string `xml:"capacity,attr"`
	CapacityAlloc string `xml:"capacityAllocationUnits,attr"`
	Format        string `xml:"format,attr"`
}

type ovfItem struct {
	Description string `xml:"Description"`
	ElementName string `xml:"ElementName"`
	Resource    string `xml:"ResourceType"`
	VirtualQty  string `xml:"VirtualQuantity"`
	Connection  string `xml:"Connection"`
	Address     string `xml:"Address"`
	Allocation  string `xml:"AllocationUnits"`
}

func ParseOVF(r io.Reader) (Manifest, []string, error) {
	var env ovfEnvelope
	if err := xml.NewDecoder(r).Decode(&env); err != nil {
		return Manifest{}, nil, fmt.Errorf("malformed OVF")
	}
	name := strings.TrimSpace(env.Name)
	if name == "" {
		name = "imported-ovf"
	}
	m := Manifest{
		SchemaVersion: ManifestSchema,
		Kind:          KindVM,
		Identity:      Identity{Name: name},
		Source:        SourceMeta{Adapter: AdapterOVF, Type: "ovf"},
		VM:            &VMSection{Firmware: "bios"},
	}
	files := map[string]ovfFile{}
	for _, f := range env.Files {
		files[f.ID] = f
	}
	var refs []string
	for i, d := range env.Disks {
		f := files[d.FileRef]
		format := "vmdk"
		if strings.Contains(strings.ToLower(d.Format), "qcow") {
			format = "qcow2"
		}
		size, _ := strconv.ParseInt(d.Capacity, 10, 64)
		m.VM.Disks = append(m.VM.Disks, Disk{
			ID: d.FileRef, Role: roleFor(i), Source: f.Href, Format: format, SizeBytes: size, Artifact: f.Href,
		})
		if f.Href != "" {
			refs = append(refs, f.Href)
		}
	}
	for _, it := range env.Items {
		switch strings.TrimSpace(it.Resource) {
		case "3":
			n, _ := strconv.Atoi(it.VirtualQty)
			m.VM.CPUs = n
		case "4":
			n, _ := strconv.ParseInt(it.VirtualQty, 10, 64)
			if strings.Contains(strings.ToLower(it.Allocation), "byte") && !strings.Contains(strings.ToLower(it.Allocation), "kilo") && !strings.Contains(strings.ToLower(it.Allocation), "mega") {
				m.VM.MemoryBytes = n
			} else if strings.Contains(strings.ToLower(it.Allocation), "kilo") {
				m.VM.MemoryBytes = n * 1024
			} else {
				m.VM.MemoryBytes = n * 1024 * 1024
			}
		case "10":
			m.VM.NICs = append(m.VM.NICs, NIC{Model: "virtio", Bridge: it.Connection, MAC: it.Address})
		}
	}
	return m, refs, nil
}

func roleFor(i int) string {
	if i == 0 {
		return "boot"
	}
	return "data"
}

func ExtractOVA(ovaPath, dest string) error {
	f, err := os.Open(ovaPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return ExtractTar(f, dest, 0)
}

func FindOVF(dir string) (string, error) {
	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".ovf") {
			found = path
			return io.EOF
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	if err != nil && err != io.EOF {
		return "", err
	}
	return "", fmt.Errorf("OVA does not contain an OVF descriptor")
}
