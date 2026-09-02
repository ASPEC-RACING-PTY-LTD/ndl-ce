package migration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type PVEClient struct {
	Base     string
	Token    string
	Client   *http.Client
	Insecure bool
}

func (c *PVEClient) parseBase() (*url.URL, error) {
	u, err := ParseHTTPEndpoint(c.Base)
	if err != nil {
		if c.Base == "" {
			return nil, fmt.Errorf("proxmox endpoint is required")
		}
		return nil, fmt.Errorf("proxmox endpoint is invalid")
	}
	if u.Scheme != "https" && !c.Insecure {
		return nil, fmt.Errorf("proxmox requires https unless insecure is explicitly set")
	}
	return u, nil
}

func (c *PVEClient) get(path string, dest any) error {
	if _, err := c.parseBase(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "PVEAPIToken="+c.Token)
	}
	res, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("proxmox source is unavailable")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("proxmox permission failure")
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("malformed source response")
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("malformed source response")
	}
	return nil
}

type pveList struct {
	Data []map[string]any `json:"data"`
}

func (c *PVEClient) DiscoverRemote() (Discovery, error) {
	d := Discovery{Adapter: AdapterProxmox, Endpoint: c.Base}
	var nodes pveList
	if err := c.get("/api2/json/nodes", &nodes); err != nil {
		return Discovery{}, err
	}
	for _, n := range nodes.Data {
		node, _ := n["node"].(string)
		if node == "" {
			continue
		}
		d.Storages = append(d.Storages, NamedRef{ID: node, Name: node, Kind: "node"})
		var st pveList
		_ = c.get("/api2/json/nodes/"+url.PathEscape(node)+"/storage", &st)
		types := pveStorageTypes(st.Data)
		for _, s := range st.Data {
			id, _ := s["storage"].(string)
			if id == "" {
				continue
			}
			d.Storages = append(d.Storages, NamedRef{ID: id, Name: id, Kind: fmt.Sprint(s["type"])})
		}
		backups := collectPVEBackups(c, node, st.Data)
		var qemu pveList
		if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/qemu", &qemu); err != nil {
			return Discovery{}, err
		}
		for _, vm := range qemu.Data {
			w := pveGuest(node, "vm", "Virtual Machine", vm)
			EnrichPVEWorkload(c, &w, types, backups)
			d.Workloads = append(d.Workloads, w)
		}
		var lxc pveList
		if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/lxc", &lxc); err != nil {
			return Discovery{}, err
		}
		for _, ct := range lxc.Data {
			w := pveGuest(node, KindContainer, "System Container", ct)
			EnrichPVEWorkload(c, &w, types, backups)
			d.Workloads = append(d.Workloads, w)
		}
		var nets pveList
		if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/network", &nets); err != nil {
			continue
		}
		for _, net := range nets.Data {
			iface, _ := net["iface"].(string)
			if iface == "" {
				continue
			}
			d.Networks = append(d.Networks, NamedRef{ID: iface, Name: iface, Kind: fmt.Sprint(net["type"])})
		}
	}
	return d, nil
}

func pveGuest(node, kind, label string, row map[string]any) DiscoveredWorkload {
	id := fmt.Sprint(intFrom(row["vmid"]))
	name, _ := row["name"].(string)
	if name == "" {
		name, _ = row["hostname"].(string)
	}
	status, _ := row["status"].(string)
	w := DiscoveredWorkload{
		SourceID: node + "/" + id, Name: name, Kind: kind, TypeLabel: label, Node: node,
		Running: status == "running", CPUs: int(intFrom(row["cpus"])),
		MemoryBytes: int64(intFrom(row["maxmem"])), DiskBytes: int64(intFrom(row["maxdisk"])),
		EstimatedBytes: int64(intFrom(row["maxdisk"])),
	}
	if w.Kind == "vm" {
		w.Kind = KindVM
	}
	return w
}

func intFrom(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int:
		return int64(t)
	case int64:
		return t
	case json.Number:
		n, _ := t.Int64()
		return n
	default:
		return 0
	}
}

func PVEManifest(kind, node, vmid string, cfg map[string]any) Manifest {
	return PVEManifestStorage(kind, node, vmid, cfg, nil)
}

func PVEManifestStorage(kind, node, vmid string, cfg map[string]any, storageTypes map[string]string) Manifest {
	m := Manifest{
		SchemaVersion: ManifestSchema,
		Identity:      Identity{Name: fmt.Sprint(cfg["name"]), SourceID: node + "/" + vmid},
		Source:        SourceMeta{Adapter: AdapterProxmox, Node: node, Type: kind},
	}
	if name, ok := cfg["hostname"].(string); ok && m.Identity.Name == "" {
		m.Identity.Name = name
	}
	if onboot, ok := cfg["onboot"]; ok && fmt.Sprint(onboot) == "1" {
		m.Startup = &Startup{Autostart: true}
	}
	if kind == KindContainer || kind == "lxc" {
		m.Kind = KindContainer
		priv := fmt.Sprint(cfg["unprivileged"]) == "0"
		m.Container = &ContainerSection{
			CPUs: int(intFrom(cfg["cores"])), MemoryBytes: intFrom(cfg["memory"]) * 1024 * 1024,
			Privileged: priv, Hostname: fmt.Sprint(cfg["hostname"]),
		}
		if !priv {
			m.Container.UIDMap = "u 0 100000 65536"
			m.Container.GIDMap = "g 0 100000 65536"
		}
		if m.Container.CPUs == 0 {
			m.Container.CPUs = 1
		}
		if root, ok := cfg["rootfs"].(string); ok {
			storage, size := pveVolume(root)
			volid := PVEVolumeID(root)
			st := ""
			if storageTypes != nil {
				st = storageTypes[storage]
			}
			format := "dir"
			if VolumeLooksLikeFile(volid, "") {
				format = PVEVolumeFormat(root, "tar")
			}
			m.Container.Rootfs = &Artifact{Path: volid, Format: format, Size: size}
			if !StorageTypeDownloadable(st) || format == "dir" {
				m.Warnings = append(m.Warnings, Finding{Level: CompatWarning, Code: "ct-rootfs", Message: "LXC rootfs is not a downloadable file. Import an existing vzdump tar backup, or provide a root filesystem archive."})
			}
		}
		for k, v := range cfg {
			if strings.HasPrefix(k, "mp") {
				m.Container.Mounts = append(m.Container.Mounts, Mount{Source: fmt.Sprint(v)})
			}
			if strings.HasPrefix(k, "net") {
				m.Container.NICs = append(m.Container.NICs, pveNet(fmt.Sprint(v)))
			}
		}
		m.Warnings = append(m.Warnings, SnapshotsNotMigrated())
		return m
	}
	m.Kind = KindVM
	m.VM = &VMSection{
		CPUs: int(intFrom(cfg["cores"])), MemoryBytes: intFrom(cfg["memory"]) * 1024 * 1024,
		Firmware: "bios", CPUType: fmt.Sprint(cfg["cpu"]),
	}
	if m.VM.CPUs == 0 {
		m.VM.CPUs = int(intFrom(cfg["sockets"]))
	}
	if strings.EqualFold(fmt.Sprint(cfg["bios"]), "ovmf") {
		m.VM.Firmware = "uefi"
	}
	if tags, ok := cfg["tags"].(string); ok {
		m.Tags = strings.Split(tags, ";")
	}
	if desc, ok := cfg["description"].(string); ok {
		m.Description = desc
	}
	if boot, ok := cfg["boot"].(string); ok {
		m.VM.BootOrder = parsePVEBoot(boot)
	}
	if _, ok := cfg["tpmstate0"]; ok {
		m.VM.TPM = &TPMMeta{Present: true, Notes: "TPM state is not transferred."}
	}
	di := 0
	for k, v := range cfg {
		sv := fmt.Sprint(v)
		if strings.HasPrefix(k, "scsi") || strings.HasPrefix(k, "virtio") || strings.HasPrefix(k, "sata") || strings.HasPrefix(k, "ide") || strings.HasPrefix(k, "efidisk") {
			if k == "scsihw" {
				continue
			}
			if strings.Contains(sv, "cloudinit") {
				m.VM.CloudInit = &CloudInit{MetaData: "cloud-init disk referenced on the source. Guest seed is not embedded in this manifest."}
				m.Warnings = append(m.Warnings, Finding{Level: CompatWarning, Code: "cloud-init", Message: "Cloud-init disk is referenced. Transfer the seed separately if required."})
				continue
			}
			if strings.Contains(sv, "media=cdrom") {
				continue
			}
			storage, size := pveVolume(sv)
			bus := "virtio"
			if strings.HasPrefix(k, "scsi") {
				bus = "scsi"
			}
			if strings.HasPrefix(k, "sata") {
				bus = "sata"
			}
			if strings.HasPrefix(k, "ide") {
				bus = "ide"
			}
			if strings.HasPrefix(k, "efidisk") {
				bus = "scsi"
			}
			volid := PVEVolumeID(sv)
			format := PVEVolumeFormat(sv, "qcow2")
			st := ""
			if storageTypes != nil {
				st = storageTypes[storage]
			}
			dl := StorageTypeDownloadable(st) && VolumeLooksLikeFile(volid, format)
			m.VM.Disks = append(m.VM.Disks, Disk{ID: k, Role: roleFor(di), Source: sv, Format: format, Bus: bus, SizeBytes: size, Storage: storage, VolID: volid, Downloadable: dl})
			if !dl {
				m.Warnings = append(m.Warnings, Finding{Level: CompatWarning, Code: "disk-download", Message: "Disk " + k + " on " + storage + " is not HTTP-downloadable. Use Disk import of a copied image, or directory-backed storage."})
			}
			di++
		}
		if strings.HasPrefix(k, "net") {
			m.VM.NICs = append(m.VM.NICs, pveNet(sv))
		}
	}
	m.Warnings = append(m.Warnings, SnapshotsNotMigrated())
	return m
}

func parsePVEBoot(boot string) []string {
	boot = strings.TrimSpace(boot)
	if strings.HasPrefix(boot, "order=") {
		boot = strings.TrimPrefix(boot, "order=")
	}
	var out []string
	for _, p := range strings.Split(boot, ";") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func pveVolume(spec string) (storage string, size int64) {
	base := spec
	if i := strings.Index(spec, ","); i >= 0 {
		base = spec[:i]
		rest := spec[i+1:]
		for _, p := range strings.Split(rest, ",") {
			if strings.HasPrefix(p, "size=") {
				size = parseSize(strings.TrimPrefix(p, "size="))
			}
		}
	}
	if i := strings.Index(base, ":"); i >= 0 {
		storage = base[:i]
	}
	return storage, size
}

func pveNet(spec string) NIC {
	n := NIC{Model: "virtio"}
	for i, p := range strings.Split(spec, ",") {
		if i == 0 && strings.Contains(p, "=") {
			kv := strings.SplitN(p, "=", 2)
			n.Model = kv[0]
			n.MAC = kv[1]
		}
		if strings.HasPrefix(p, "bridge=") {
			n.Bridge = strings.TrimPrefix(p, "bridge=")
		}
		if strings.HasPrefix(p, "tag=") {
			fmt.Sscanf(strings.TrimPrefix(p, "tag="), "%d", &n.VLAN)
		}
	}
	return n
}

func parseSize(s string) int64 {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "G"):
		mult = 1 << 30
		s = strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "M"):
		mult = 1 << 20
		s = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "K"):
		mult = 1 << 10
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "T"):
		mult = 1 << 40
		s = strings.TrimSuffix(s, "T")
	}
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n * mult
}

func PVECapabilities(running bool, kind, backupFmt string) Caps {
	c := Caps{Offline: true, Disk: true, Backup: backupFmt != "vma" && backupFmt != ""}
	if kind == KindVM && backupFmt == "vma" {
		c.Backup = false
		c.BackupNote = "Proxmox VM vzdump vma archives cannot be read without a vma extractor. Import a QEMU disk or an LXC tar backup instead."
	}
	if kind == KindContainer && (backupFmt == "tar" || backupFmt == "tgz" || backupFmt == "zst" || backupFmt == "tar.zst") {
		c.Backup = true
	}
	c.Snapshot = false
	c.SnapshotNote = "Snapshot-assisted copy from Proxmox storage is not implemented. V1 will not create source snapshots."
	c.Live = false
	c.LiveNote = "Live transfer from Proxmox VE is unavailable in V1. Use Offline on a downloadable stopped disk, or an existing LXC tar backup."
	return c
}

func (c *PVEClient) GuestConfig(node, kind, vmid string) (map[string]any, error) {
	pathKind := "qemu"
	if kind == KindContainer || kind == "lxc" {
		pathKind = "lxc"
	}
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/"+pathKind+"/"+url.PathEscape(vmid)+"/config", &wrap); err != nil {
		return nil, err
	}
	if wrap.Data == nil {
		return nil, fmt.Errorf("malformed source response")
	}
	return wrap.Data, nil
}

func (c *PVEClient) ManifestFor(node, kind, vmid string) (Manifest, error) {
	cfg, err := c.GuestConfig(node, kind, vmid)
	if err != nil {
		return Manifest{}, err
	}
	types := map[string]string{}
	var st pveList
	if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/storage", &st); err == nil {
		types = pveStorageTypes(st.Data)
	}
	return PVEManifestStorage(kind, node, vmid, cfg, types), nil
}

func IsVMA(name string) bool {
	low := strings.ToLower(name)
	return strings.Contains(low, ".vma")
}
