package migration

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const pveMaxDownload = 512 << 30

var fileStorageTypes = map[string]struct{}{
	"dir": {}, "nfs": {}, "cifs": {}, "btrfs": {}, "cephfs": {},
}

var blockStorageTypes = map[string]struct{}{
	"lvm": {}, "lvmthin": {}, "zfspool": {}, "rbd": {}, "iscsi": {}, "iscsidirect": {},
}

func StorageTypeDownloadable(t string) bool {
	_, ok := fileStorageTypes[strings.ToLower(strings.TrimSpace(t))]
	return ok
}

func VolumeLooksLikeFile(volid, format string) bool {
	low := strings.ToLower(volid + " " + format)
	for _, ext := range []string{".qcow2", ".qcow", ".raw", ".vmdk", ".img", ".tar", ".tgz", ".gz", ".zst"} {
		if strings.Contains(low, ext) {
			return true
		}
	}
	switch strings.ToLower(format) {
	case "qcow2", "raw", "vmdk", "vpc", "vhdx", "tar", "tgz":
		return true
	}
	return false
}

func PVEVolumeID(spec string) string {
	base := spec
	if i := strings.Index(spec, ","); i >= 0 {
		base = spec[:i]
	}
	return strings.TrimSpace(base)
}

func PVEVolumeFormat(spec, fallback string) string {
	for _, p := range strings.Split(spec, ",") {
		if strings.HasPrefix(p, "format=") {
			return NormalizeFormat(strings.TrimPrefix(p, "format="), "")
		}
	}
	id := PVEVolumeID(spec)
	if ext := filepath.Ext(id); ext != "" {
		return NormalizeFormat("", id)
	}
	if fallback != "" {
		return fallback
	}
	return "qcow2"
}

func (c *PVEClient) applyConn(src SourceConn) {
	if src.Endpoint != "" {
		c.Base = src.Endpoint
	}
	if src.Token != "" {
		c.Token = src.Token
	}
	c.Insecure = src.Insecure
}

func (c *PVEClient) ID() string { return AdapterProxmox }

func (c *PVEClient) Info() AdapterInfo {
	a, _ := AdapterByID(AdapterProxmox)
	return a
}

func (c *PVEClient) Discover(_ SourceConn) (Discovery, error) {
	return c.DiscoverRemote()
}

func (c *PVEClient) Manifest(src SourceConn, sourceID string) (Manifest, error) {
	c.applyConn(src)
	node, vmid, ok := strings.Cut(sourceID, "/")
	if !ok {
		return Manifest{}, fmt.Errorf("proxmox source id must be node/vmid")
	}
	kind := KindVM
	if cfg, err := c.GuestConfig(node, KindContainer, vmid); err == nil && cfg != nil {
		if _, has := cfg["rootfs"]; has {
			kind = KindContainer
		}
	}
	return c.ManifestFor(node, kind, vmid)
}

func (c *PVEClient) Capabilities(src SourceConn, sourceID string) (Caps, error) {
	c.applyConn(src)
	m, err := c.Manifest(src, sourceID)
	if err != nil {
		return Caps{}, err
	}
	running := m.Source.Running
	downloadable := false
	backupFmt := ""
	if m.VM != nil {
		for _, d := range m.VM.Disks {
			if d.Downloadable {
				downloadable = true
			}
		}
	}
	if m.Container != nil && m.Container.Rootfs != nil {
		backupFmt = m.Container.Rootfs.Format
		if m.Container.Rootfs.Path != "" && VolumeLooksLikeFile(m.Container.Rootfs.Path, backupFmt) {
			downloadable = true
		}
	}
	return PVECaps(running, m.Kind, backupFmt, downloadable), nil
}

func (c *PVEClient) OpenArtifact(src SourceConn, sourceID, artifact string) (Readable, error) {
	c.applyConn(src)
	node, _, _ := strings.Cut(sourceID, "/")
	volid := artifact
	storage := ""
	if i := strings.Index(volid, ":"); i >= 0 {
		storage = volid[:i]
	}
	if storage == "" || node == "" {
		return nil, fmt.Errorf("proxmox volume id is invalid")
	}
	tmp, err := os.CreateTemp("", "ndl-pve-*.bin")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	_ = tmp.Close()
	if err := c.DownloadVolume(node, storage, volid, name); err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	f, err := os.Open(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, err
	}
	st, _ := f.Stat()
	return &tempReadable{File: f, size: st.Size(), path: name}, nil
}

type tempReadable struct {
	*os.File
	size int64
	path string
}

func (t *tempReadable) Size() int64 { return t.size }

func (t *tempReadable) Close() error {
	err := t.File.Close()
	_ = os.Remove(t.path)
	return err
}

func (c *PVEClient) http() *http.Client {
	base := c.Client
	if base == nil {
		base = &http.Client{Timeout: 45 * time.Second}
	}
	cp := *base
	if c.Insecure {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		if base.Transport != nil {
			if bt, ok := base.Transport.(*http.Transport); ok {
				tr = bt.Clone()
			}
		}
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		cp.Transport = tr
	}
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return fmt.Errorf("refusing redirect to an unexpected scheme")
		}
		if len(via) > 5 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	}
	return &cp
}

func (c *PVEClient) downloadHTTP() *http.Client {
	h := c.http()
	cp := *h
	cp.Timeout = 0
	return &cp
}

func (c *PVEClient) DownloadVolume(node, storage, volid, dest string) error {
	if c.Base == "" {
		return fmt.Errorf("proxmox endpoint is required")
	}
	u, err := url.Parse(c.Base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("proxmox endpoint is invalid")
	}
	if u.Scheme != "https" && !c.Insecure {
		return fmt.Errorf("proxmox requires https unless insecure is explicitly set")
	}
	if storage == "" || volid == "" || node == "" {
		return fmt.Errorf("proxmox volume is invalid")
	}
	path := "/api2/json/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + url.PathEscape(volid)
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.Base, "/")+path, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "PVEAPIToken="+c.Token)
	}
	res, err := c.downloadHTTP().Do(req)
	if err != nil {
		return fmt.Errorf("proxmox source is unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("proxmox permission failure")
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("proxmox volume download failed (%d)", res.StatusCode)
	}
	ct := strings.ToLower(res.Header.Get("Content-Type"))
	limited := io.LimitReader(res.Body, pveMaxDownload+1)
	peek := make([]byte, 256)
	n, _ := io.ReadFull(limited, peek)
	head := peek[:n]
	if strings.Contains(ct, "json") || looksJSONObject(head) {
		return fmt.Errorf("source storage does not expose a downloadable disk file for %s. Copy the image on the source host and use Disk import, or import an LXC tar backup", volid)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(head); err != nil {
		_ = f.Close()
		_ = os.Remove(dest)
		return err
	}
	written, err := io.Copy(f, limited)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return closeErr
	}
	if int64(n)+written > pveMaxDownload {
		_ = os.Remove(dest)
		return fmt.Errorf("proxmox volume exceeds download size limit")
	}
	return nil
}

func looksJSONObject(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return strings.HasPrefix(s, "{") && strings.Contains(s, `"data"`)
}

func (c *PVEClient) ListContent(node, storage, content string) ([]map[string]any, error) {
	path := "/api2/json/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content"
	if content != "" {
		path += "?content=" + url.QueryEscape(content)
	}
	var wrap pveList
	if err := c.get(path, &wrap); err != nil {
		return nil, err
	}
	return wrap.Data, nil
}

func (c *PVEClient) ListSnapshots(node, kind, vmid string) int {
	pathKind := "qemu"
	if kind == KindContainer || kind == "lxc" {
		pathKind = "lxc"
	}
	var wrap pveList
	if err := c.get("/api2/json/nodes/"+url.PathEscape(node)+"/"+pathKind+"/"+url.PathEscape(vmid)+"/snapshot", &wrap); err != nil {
		return 0
	}
	n := 0
	for _, row := range wrap.Data {
		name, _ := row["name"].(string)
		if name != "" && name != "current" {
			n++
		}
	}
	return n
}

func PVECaps(running bool, kind, backupFmt string, downloadable bool) Caps {
	c := PVECapabilities(running, kind, backupFmt)
	if kind == KindVM {
		c.Offline = downloadable
		if !downloadable {
			c.Offline = false
			c.SnapshotNote = firstNonEmpty(c.SnapshotNote, "Snapshot-assisted copy from Proxmox storage is not implemented.")
		}
		if !downloadable {
			c.Disk = true
		}
	}
	if kind == KindContainer {
		tarBackup := backupFmt == "tar" || backupFmt == "tgz" || backupFmt == "zst" || backupFmt == "tar.zst" || backupFmt == "tar.gz"
		c.Backup = tarBackup
		c.Offline = downloadable
		if !c.Backup {
			c.BackupNote = "No completed LXC tar backup is listed on this source. Create a vzdump tar on Proxmox yourself, then import it. No-dal will not create or delete source backups."
		}
	}
	return c
}

func EnrichPVEWorkload(c *PVEClient, w *DiscoveredWorkload, storageTypes map[string]string, backups []map[string]any) {
	node, vmid, _ := strings.Cut(w.SourceID, "/")
	m, err := c.ManifestFor(node, w.Kind, vmid)
	if err != nil {
		w.Caps = []string{"config-unavailable"}
		return
	}
	w.Firmware = ""
	if m.VM != nil {
		w.Firmware = m.VM.Firmware
		for _, d := range m.VM.Disks {
			if d.Storage != "" {
				w.Storage = appendUnique(w.Storage, d.Storage)
			}
		}
		for _, n := range m.VM.NICs {
			if n.Bridge != "" {
				w.Networks = appendUnique(w.Networks, n.Bridge)
			}
		}
	}
	if m.Container != nil {
		for _, n := range m.Container.NICs {
			if n.Bridge != "" {
				w.Networks = appendUnique(w.Networks, n.Bridge)
			}
		}
	}
	w.Snapshots = c.ListSnapshots(node, w.Kind, vmid)
	match := 0
	var tarFmt string
	for _, b := range backups {
		if fmt.Sprint(intFrom(b["vmid"])) != vmid {
			continue
		}
		match++
		volid, _ := b["volid"].(string)
		if IsVMA(volid) {
			continue
		}
		if VolumeLooksLikeFile(volid, fmt.Sprint(b["format"])) {
			tarFmt = backupFormatOf(volid)
		}
	}
	w.Backups = match
	downloadable := false
	if m.VM != nil {
		for _, d := range m.VM.Disks {
			if d.Downloadable || (StorageTypeDownloadable(storageTypes[d.Storage]) && VolumeLooksLikeFile(d.VolID, d.Format)) {
				downloadable = true
			}
		}
	}
	caps := PVECaps(w.Running, w.Kind, tarFmt, downloadable)
	if caps.Offline {
		w.Caps = append(w.Caps, ModeOffline)
	}
	if caps.Backup {
		w.Caps = append(w.Caps, ModeBackup)
	}
	w.Caps = append(w.Caps, ModeDisk)
}

func backupFormatOf(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.Contains(low, ".vma"):
		return "vma"
	case strings.HasSuffix(low, ".tar.zst") || strings.HasSuffix(low, ".zst"):
		return "tar.zst"
	case strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz"):
		return "tgz"
	case strings.HasSuffix(low, ".tar"):
		return "tar"
	default:
		return ""
	}
}

func appendUnique(in []string, v string) []string {
	for _, x := range in {
		if x == v {
			return in
		}
	}
	return append(in, v)
}

func pveStorageTypes(rows []map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range rows {
		id, _ := s["storage"].(string)
		if id == "" {
			continue
		}
		out[id] = fmt.Sprint(s["type"])
	}
	return out
}

func collectPVEBackups(c *PVEClient, node string, storages []map[string]any) []map[string]any {
	var out []map[string]any
	for _, s := range storages {
		id, _ := s["storage"].(string)
		if id == "" {
			continue
		}
		rows, err := c.ListContent(node, id, "backup")
		if err != nil {
			continue
		}
		out = append(out, rows...)
	}
	return out
}

var _ SourceAdapter = (*PVEClient)(nil)
