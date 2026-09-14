package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	TempDumpNote   = "ndl-temp-migration"
	defaultPVEPoll = 2 * time.Second
	pveTaskWaitMax = 6 * time.Hour
)

// FileBackupStorage returns a directory, NFS, CIFS, or similar store that can
// hold a vzdump archive. PBS is refused because those archives cannot be read
// as a host file or streamed over the content API.
func FileBackupStorage(storages []map[string]any) (id string, reason string) {
	var pbs []string
	var other []string
	for _, s := range storages {
		name, _ := s["storage"].(string)
		if name == "" {
			continue
		}
		kind := strings.ToLower(fmt.Sprint(s["type"]))
		if kind == "pbs" {
			pbs = append(pbs, name)
			continue
		}
		if !StorageTypeDownloadable(kind) {
			other = append(other, name+" ("+kind+")")
			continue
		}
		if hasContentField(s) && !storageHasBackup(s) {
			continue
		}
		return name, ""
	}
	if len(pbs) > 0 {
		return "", "Backup storage " + strings.Join(pbs, ", ") + " uses Proxmox Backup Server and cannot be downloaded over the content API. Add a directory, NFS, or CIFS storage with backup content."
	}
	if len(other) > 0 {
		return "", "This Proxmox node has no directory, NFS, or CIFS storage that can hold a downloadable vzdump. Existing storage: " + strings.Join(other, ", ") + "."
	}
	return "", "This Proxmox node has no directory, NFS, or CIFS storage that can hold a downloadable vzdump."
}

func hasContentField(row map[string]any) bool {
	_, ok := row["content"]
	return ok
}

func storageHasBackup(row map[string]any) bool {
	switch t := row["content"].(type) {
	case string:
		for _, p := range strings.Split(t, ",") {
			if strings.TrimSpace(p) == "backup" {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if fmt.Sprint(x) == "backup" {
				return true
			}
		}
	}
	return false
}

func LXCRootfsDownloadable(root *Artifact, storageTypes map[string]string) bool {
	if root == nil || root.Path == "" {
		return false
	}
	st, _ := pveVolume(root.Path)
	kind := ""
	if storageTypes != nil {
		kind = storageTypes[st]
	}
	if kind != "" && !StorageTypeDownloadable(kind) {
		return false
	}
	if root.Format == "dir" || root.Format == "" {
		return StorageTypeDownloadable(kind) && VolumeLooksLikeFile(root.Path, root.Format)
	}
	return StorageTypeDownloadable(kind) && VolumeLooksLikeFile(root.Path, root.Format)
}

func LXCRootfsBlockReason(root *Artifact, storageTypes map[string]string, backupStore, backupReason string, hasTar bool) string {
	if hasTar {
		return ""
	}
	if LXCRootfsDownloadable(root, storageTypes) {
		return ""
	}
	st := "rootfs"
	if root != nil {
		if name, _ := pveVolume(root.Path); name != "" {
			st = name
		}
	}
	kind := ""
	if storageTypes != nil {
		kind = storageTypes[st]
	}
	if kind == "" {
		kind = "unknown"
	}
	if st == "" {
		st = "rootfs"
	}
	base := "LXC rootfs on " + st + " (" + kind + ") is not HTTP-downloadable"
	if backupStore != "" {
		return ""
	}
	if backupReason != "" {
		return base + ". " + backupReason
	}
	return base + ". Add a directory, NFS, or CIFS storage with backup content so No-dal can create a temporary vzdump."
}

func newestLXCTar(rows []map[string]any, vmid string) string {
	volid, _ := pickLXCTar(rows, vmid)
	return volid
}

func pickLXCTar(rows []map[string]any, vmid string) (volid string, ours bool) {
	var best string
	var bestC int64
	var bestOurs bool
	for _, b := range rows {
		if fmt.Sprint(intFrom(b["vmid"])) != vmid {
			continue
		}
		id, _ := b["volid"].(string)
		if id == "" || IsVMA(id) {
			continue
		}
		fmtName := backupFormatOf(id)
		if fmtName == "" || fmtName == "vma" {
			continue
		}
		if !VolumeLooksLikeFile(id, fmt.Sprint(b["format"])) {
			continue
		}
		ct := intFrom(b["ctime"])
		if ct >= bestC {
			bestC = ct
			best = id
			bestOurs = notesMarkTempDump(b)
		}
	}
	return best, bestOurs
}

func notesMarkTempDump(row map[string]any) bool {
	notes := strings.ToLower(fmt.Sprint(row["notes"]))
	return strings.Contains(notes, strings.ToLower(TempDumpNote))
}

func (c *PVEClient) pollEvery() time.Duration {
	if c.PollEvery > 0 {
		return c.PollEvery
	}
	return defaultPVEPoll
}

func (c *PVEClient) postForm(path string, fields url.Values) ([]byte, int, error) {
	if _, err := c.parseBase(); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.Base, "/")+path, strings.NewReader(fields.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.Token != "" {
		req.Header.Set("Authorization", "PVEAPIToken="+c.Token)
	}
	res, err := c.http().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("proxmox source is unavailable")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	return body, res.StatusCode, nil
}

func (c *PVEClient) doDelete(path string) error {
	if _, err := c.parseBase(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodDelete, strings.TrimRight(c.Base, "/")+path, nil)
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
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("proxmox permission failure")
	}
	if res.StatusCode >= 400 && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("proxmox delete failed (%d)", res.StatusCode)
	}
	return nil
}

func (c *PVEClient) CreateVZdump(node, vmid, storage, mode string) (string, error) {
	if node == "" || vmid == "" || storage == "" {
		return "", fmt.Errorf("proxmox vzdump requires node, vmid, and backup storage")
	}
	if mode == "" {
		mode = "snapshot"
	}
	fields := url.Values{}
	fields.Set("vmid", vmid)
	fields.Set("storage", storage)
	fields.Set("mode", mode)
	fields.Set("compress", "zstd")
	fields.Set("remove", "0")
	fields.Set("notes-template", TempDumpNote)
	body, status, err := c.postForm("/api2/json/nodes/"+url.PathEscape(node)+"/vzdump", fields)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "", fmt.Errorf("proxmox permission failure. Grant the API token VM.Backup and Datastore.Allocate on %s", storage)
	}
	if status >= 400 {
		return "", fmt.Errorf("proxmox vzdump create failed (%d). %s", status, pveAPIError(body))
	}
	var wrap struct {
		Data any `json:"data"`
	}
	if err := json.Unmarshal(body, &wrap); err != nil {
		return "", fmt.Errorf("malformed source response")
	}
	upid := fmt.Sprint(wrap.Data)
	if upid == "" || upid == "<nil>" {
		return "", fmt.Errorf("proxmox vzdump did not return a task id")
	}
	return upid, nil
}

func (c *PVEClient) TaskStatus(node, upid string) (status, exit string, err error) {
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	path := "/api2/json/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	if err := c.get(path, &wrap); err != nil {
		return "", "", err
	}
	if wrap.Data == nil {
		return "", "", fmt.Errorf("malformed source response")
	}
	return fmt.Sprint(wrap.Data["status"]), fmt.Sprint(wrap.Data["exitstatus"]), nil
}

func (c *PVEClient) WaitTask(ctx context.Context, node, upid string) error {
	deadline := time.Now().Add(pveTaskWaitMax)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("proxmox vzdump timed out")
		}
		status, exit, err := c.TaskStatus(node, upid)
		if err != nil {
			return err
		}
		if status == "stopped" {
			if exit == "" || strings.EqualFold(exit, "OK") || strings.EqualFold(exit, "ok") {
				return nil
			}
			return fmt.Errorf("proxmox vzdump failed: %s", exit)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.pollEvery()):
		}
	}
}

// OwnedTempBackup is true only when the volume still carries the No-dal
// temporary-migration note. Operator backups are never owned.
func (c *PVEClient) OwnedTempBackup(node, storage, volid string) bool {
	if node == "" || storage == "" || volid == "" || !IsVzdumpArchive(volid) {
		return false
	}
	rows, err := c.ListContent(node, storage, "backup")
	if err != nil {
		return false
	}
	for _, row := range rows {
		id, _ := row["volid"].(string)
		if id != volid {
			continue
		}
		return notesMarkTempDump(row)
	}
	return false
}

func (c *PVEClient) DeleteContent(node, storage, volid string) error {
	if node == "" || storage == "" || volid == "" {
		return fmt.Errorf("proxmox volume delete is invalid")
	}
	path := "/api2/json/nodes/" + url.PathEscape(node) + "/storage/" + url.PathEscape(storage) + "/content/" + url.PathEscape(volid)
	return c.doDelete(path)
}

// EnsureLXCTar returns a downloadable LXC vzdump. It reuses an existing tar
// when present. Otherwise it creates a temporary snapshot-mode vzdump.
// The second result is true when the returned archive is ours to delete
// (created now, or previously created with the ndl-temp-migration note).
func (c *PVEClient) EnsureLXCTar(ctx context.Context, node, vmid, storage string) (volid string, ours bool, err error) {
	existing, err := c.ListContent(node, storage, "backup")
	if err != nil {
		return "", false, fmt.Errorf("cannot list Proxmox backup storage %s. Grant the API token Datastore.Audit or Datastore.Allocate. %w", storage, err)
	}
	if got, owned := pickLXCTar(existing, vmid); got != "" {
		return got, owned, nil
	}
	upid, err := c.CreateVZdump(node, vmid, storage, "snapshot")
	if err != nil {
		return "", false, err
	}
	if err := c.WaitTask(ctx, node, upid); err != nil {
		return "", false, err
	}
	after, err := c.ListContent(node, storage, "backup")
	if err != nil {
		return "", false, err
	}
	got, _ := pickLXCTar(after, vmid)
	if got == "" {
		return "", false, fmt.Errorf("proxmox vzdump finished but no downloadable LXC tar appeared on %s", storage)
	}
	return got, true, nil
}

func pveAPIError(body []byte) string {
	var wrap struct {
		Errors  any    `json:"errors"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &wrap); err == nil {
		if wrap.Message != "" {
			return wrap.Message
		}
		if wrap.Errors != nil {
			return fmt.Sprint(wrap.Errors)
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 180 {
		s = s[:180]
	}
	return s
}
