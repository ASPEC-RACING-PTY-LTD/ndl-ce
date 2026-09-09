package ndnet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type hostBackupManifest struct {
	Files []hostBackupFile `json:"files"`
}

type hostBackupFile struct {
	Rel  string `json:"rel"`
	Dest string `json:"dest"`
}

func (e *Engine) hostBackupDir(snap string) string {
	return filepath.Join(snap, "host")
}

func (e *Engine) snapshotHostManagers(snap string) error {
	dir := e.hostBackupDir(snap)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var man hostBackupManifest
	add := func(rel string) {
		src := e.etcPath(strings.Split(rel, "/")...)
		info, err := os.Lstat(src)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return
		}
		name := strings.ReplaceAll(rel, "/", "_")
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			return
		}
		man.Files = append(man.Files, hostBackupFile{Rel: name, Dest: rel})
	}
	add("network/interfaces")
	if entries, err := os.ReadDir(e.etcPath("network", "interfaces.d")); err == nil {
		for _, ent := range entries {
			if !ent.IsDir() {
				add(filepath.ToSlash(filepath.Join("network", "interfaces.d", ent.Name())))
			}
		}
	}
	add("dhcpcd.conf")
	add("resolv.conf")
	body, err := json.Marshal(man)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), body, 0600)
}

func (e *Engine) restoreHostManagers(snap string) error {
	dir := e.hostBackupDir(snap)
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil
	}
	var man hostBackupManifest
	if json.Unmarshal(b, &man) != nil {
		return nil
	}
	for _, file := range man.Files {
		src := filepath.Join(dir, file.Rel)
		raw, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dest := e.etcPath(strings.Split(file.Dest, "/")...)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			continue
		}
		_ = os.WriteFile(dest, raw, 0644)
	}
	return nil
}

func (e *Engine) migrateUplinkManagers(ctx context.Context, uplink string, conflicts []HostManagerConflict) ([]string, error) {
	var done []string
	for _, item := range conflicts {
		switch item.Action {
		case "disable_stanza":
			if err := e.migrateIfupdownFile(item.Path, uplink); err != nil {
				return done, err
			}
			done = append(done, item.Manager+":"+item.Path)
		case "release_lease":
			if err := e.migrateDhcpcd(ctx, uplink); err != nil {
				return done, err
			}
			done = append(done, "dhcpcd")
		case "unmanage":
			if err := e.migrateNetworkManager(ctx, uplink); err != nil {
				return done, err
			}
			done = append(done, "NetworkManager")
		}
	}
	if err := e.preserveResolvNameservers(); err != nil {
		return done, err
	}
	return done, nil
}

func (e *Engine) migrateIfupdownFile(rel, uplink string) error {
	path := e.etcPath(strings.Split(filepath.ToSlash(rel), "/")...)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next, changed := disableIfupdownIface(string(b), uplink)
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(next), 0644)
}

func (e *Engine) migrateDhcpcd(ctx context.Context, uplink string) error {
	path := e.etcPath("dhcpcd.conf")
	if b, err := os.ReadFile(path); err == nil {
		next := dhcpcdDenyIface(string(b), uplink)
		if next != string(b) {
			if err := os.WriteFile(path, []byte(next), 0644); err != nil {
				return err
			}
		}
	}
	_ = e.run(ctx, "/sbin/dhcpcd", "-k", uplink)
	_ = e.run(ctx, "/usr/sbin/dhcpcd", "-k", uplink)
	return nil
}

func dhcpcdDenyIface(content, iface string) string {
	if dhcpcdDenies(content, iface) {
		return content
	}
	line := "denyinterfaces " + iface + "\n"
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line
}

func (e *Engine) migrateNetworkManager(ctx context.Context, uplink string) error {
	dir := e.etcPath("NetworkManager", "conf.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	name := nmUnmanagedName(uplink)
	body := "[keyfile]\nunmanaged-devices=interface-name:" + uplink + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		return err
	}
	_ = e.run(ctx, "/usr/bin/nmcli", "general", "reload")
	return nil
}

func (e *Engine) preserveResolvNameservers() error {
	path := e.etcPath("resolv.conf")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if resolvHasNameserver(string(b)) {
		return nil
	}
	snap := filepath.Join(e.stateDir(), "resolv.backup")
	prev, err := os.ReadFile(snap)
	if err != nil || !resolvHasNameserver(string(prev)) {
		return nil
	}
	return os.WriteFile(path, prev, 0644)
}

func (e *Engine) backupResolvIfPresent() {
	path := e.etcPath("resolv.conf")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil || !resolvHasNameserver(string(b)) {
		return
	}
	_ = os.MkdirAll(e.stateDir(), 0700)
	_ = os.WriteFile(filepath.Join(e.stateDir(), "resolv.backup"), b, 0644)
}

func resolvHasNameserver(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "nameserver" {
			return true
		}
	}
	return false
}
