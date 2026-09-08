package migration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFileBackupStoragePrefersDownloadable(t *testing.T) {
	t.Parallel()
	id, reason := FileBackupStorage([]map[string]any{
		{"storage": "local-lvm", "type": "lvmthin", "content": "rootdir,images"},
		{"storage": "local", "type": "dir", "content": "iso,backup,vztmpl"},
	})
	if id != "local" || reason != "" {
		t.Fatalf("got %s %q", id, reason)
	}
}

func TestFileBackupStorageBlocksPBS(t *testing.T) {
	t.Parallel()
	id, reason := FileBackupStorage([]map[string]any{
		{"storage": "pbs-main", "type": "pbs", "content": "backup"},
	})
	if id != "" || !strings.Contains(reason, "Proxmox Backup Server") {
		t.Fatalf("got %s %q", id, reason)
	}
}

func TestLXCRootfsBlockReasonNamesStorage(t *testing.T) {
	t.Parallel()
	root := &Artifact{Path: "local-lvm:subvol-104-disk-0", Format: "dir"}
	msg := LXCRootfsBlockReason(root, map[string]string{"local-lvm": "lvmthin"}, "", "This Proxmox node has no directory, NFS, or CIFS storage that can hold a downloadable vzdump.", false)
	if !strings.Contains(msg, "local-lvm") || !strings.Contains(msg, "lvmthin") || !strings.Contains(msg, "directory, NFS, or CIFS") {
		t.Fatalf("reason %q", msg)
	}
}

func TestEnsureLXCTarCreatesThenReuses(t *testing.T) {
	t.Parallel()
	var backups []map[string]any
	created := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/storage/local/content", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": backups})
	})
	mux.HandleFunc("/api2/json/nodes/pve/vzdump", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "vmid=104") || !strings.Contains(string(body), "storage=local") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		created++
		backups = append(backups, map[string]any{
			"vmid": 104.0, "volid": "local:backup/vzdump-lxc-104-2026_09_08-16_00_00.tar.zst",
			"format": "tar.zst", "ctime": 100.0, "notes": TempDumpNote,
		})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": "UPID:pve:0001:0001:0001:vzdump:104:root@pam:"})
	})
	mux.HandleFunc("/api2/json/nodes/pve/tasks/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"status": "stopped", "exitstatus": "OK"}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "user@pam!tok=secret", Insecure: true, Client: ts.Client(), PollEvery: time.Millisecond}
	vol, ours, err := c.EnsureLXCTar(context.Background(), "pve", "104", "local")
	if err != nil || !ours || !strings.Contains(vol, "vzdump-lxc-104") {
		t.Fatalf("create %v %v %s", err, ours, vol)
	}
	vol2, ours2, err := c.EnsureLXCTar(context.Background(), "pve", "104", "local")
	if err != nil || !ours2 || vol2 != vol {
		t.Fatalf("reuse ours %v %v %s", err, ours2, vol2)
	}
	if created != 1 {
		t.Fatalf("created %d", created)
	}
}

func TestEnsureLXCTarLeavesOperatorBackup(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/storage/local/content", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{
			"vmid": 104.0, "volid": "local:backup/vzdump-lxc-104-2026_01_01-00_00_00.tar.zst",
			"format": "tar.zst", "ctime": 50.0, "notes": "weekly",
		}}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/vzdump", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("must not create a vzdump when an operator tar exists")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "user@pam!tok=secret", Insecure: true, Client: ts.Client(), PollEvery: time.Millisecond}
	vol, ours, err := c.EnsureLXCTar(context.Background(), "pve", "104", "local")
	if err != nil || ours || !strings.Contains(vol, "vzdump-lxc-104") {
		t.Fatalf("operator tar %v %v %s", err, ours, vol)
	}
}

func TestEnrichLXCUsesTempVZdumpOrBlocks(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/lxc/104/config", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"hostname": "SoundDock", "cores": 4.0, "memory": 8192.0,
			"rootfs": "local-zfs:subvol-104-disk-0,size=120G",
			"net0":   "name=eth0,bridge=vmbr0",
		}})
	})
	mux.HandleFunc("/api2/json/nodes/pve/lxc/104/snapshot", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := &PVEClient{Base: ts.URL, Token: "t", Insecure: true, Client: ts.Client()}
	w := DiscoveredWorkload{SourceID: "pve/104", Name: "SoundDock", Kind: KindContainer, Running: true}
	EnrichPVEWorkload(c, &w, []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "content": "rootdir,images"},
		{"storage": "local", "type": "dir", "content": "backup,iso"},
	}, nil)
	if !w.TempBackup || w.BackupStorage != "local" {
		t.Fatalf("temp %+v", w)
	}
	hasBackup := false
	for _, cap := range w.Caps {
		if cap == ModeBackup {
			hasBackup = true
		}
		if cap == ModeDisk || cap == ModeOffline {
			t.Fatalf("block storage LXC must not offer %s", cap)
		}
	}
	if !hasBackup {
		t.Fatal("expected backup capability")
	}
	blocked := DiscoveredWorkload{SourceID: "pve/104", Name: "SoundDock", Kind: KindContainer, Running: true}
	EnrichPVEWorkload(c, &blocked, []map[string]any{
		{"storage": "local-zfs", "type": "zfspool", "content": "rootdir,images"},
	}, nil)
	if blocked.TempBackup || blocked.BlockReason == "" || !strings.Contains(blocked.BlockReason, "zfspool") {
		t.Fatalf("blocked %+v", blocked)
	}
	mode, f := SuggestModeForStrategy(blocked, StrategyConsistent)
	if mode != "" || f == nil || f.Level != CompatBlocked {
		t.Fatalf("suggest %s %+v", mode, f)
	}
	plan, err := BuildPlan("j1", AdapterProxmox, []DiscoveredWorkload{w}, []string{w.SourceID}, nil, map[string]Manifest{
		w.SourceID: {Kind: KindContainer, Container: &ContainerSection{Rootfs: &Artifact{Path: "local-zfs:subvol-104-disk-0", Format: "dir"}}},
	}, Mapping{}, nil, nil, false, nil)
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Mode != ModeBackup || !plan.Items[0].TempBackup || plan.Items[0].Compatibility == CompatBlocked {
		t.Fatalf("temp plan %v %+v", err, plan)
	}
}
