package ndnet

import (
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) etcPath(elem ...string) string {
	parts := append([]string{e.rootOrEmpty(), "etc"}, elem...)
	return filepath.Join(parts...)
}

func (e *Engine) runPath(elem ...string) string {
	parts := append([]string{e.rootOrEmpty(), "run"}, elem...)
	return filepath.Join(parts...)
}

func (e *Engine) rootOrEmpty() string {
	if e.Root == "" || e.Root == "/" {
		return "/"
	}
	return e.Root
}

// InspectHostManagers reports competing configuration for an uplink.
// It does not change host state.
func (e *Engine) InspectHostManagers(uplink string) []HostManagerConflict {
	uplink = strings.TrimSpace(uplink)
	if !ValidIfName(uplink) {
		return nil
	}
	var out []HostManagerConflict
	out = append(out, e.inspectIfupdown(uplink)...)
	out = append(out, e.inspectDhcpcd(uplink)...)
	out = append(out, e.inspectNetworkManager(uplink)...)
	out = append(out, e.adminNetworkdConflicts(uplink)...)
	return out
}

func (e *Engine) inspectIfupdown(uplink string) []HostManagerConflict {
	var out []HostManagerConflict
	main := e.etcPath("network", "interfaces")
	if b, err := os.ReadFile(main); err == nil && ifupdownMentions(string(b), uplink) {
		out = append(out, HostManagerConflict{
			Manager: "ifupdown",
			Path:    "network/interfaces",
			Detail:  "iface stanza still configures this uplink",
			Action:  "disable_stanza",
		})
	}
	dir := e.etcPath("network", "interfaces.d")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		rel := filepath.Join("network", "interfaces.d", ent.Name())
		b, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil || !ifupdownMentions(string(b), uplink) {
			continue
		}
		out = append(out, HostManagerConflict{
			Manager: "ifupdown",
			Path:    rel,
			Detail:  "iface stanza still configures this uplink",
			Action:  "disable_stanza",
		})
	}
	return out
}

func (e *Engine) inspectDhcpcd(uplink string) []HostManagerConflict {
	conf := e.etcPath("dhcpcd.conf")
	b, err := os.ReadFile(conf)
	hasConf := err == nil
	denied := hasConf && dhcpcdDenies(string(b), uplink)
	pid := dhcpcdPIDPresent(e.rootOrEmpty(), uplink)
	if !hasConf && !pid {
		return nil
	}
	if denied && !pid {
		return nil
	}
	detail := "dhcpcd can still manage this uplink"
	if pid {
		detail = "dhcpcd is running against this uplink"
	}
	return []HostManagerConflict{{
		Manager: "dhcpcd",
		Path:    "dhcpcd.conf",
		Detail:  detail,
		Action:  "release_lease",
	}}
}

func (e *Engine) inspectNetworkManager(uplink string) []HostManagerConflict {
	unmanaged := e.etcPath("NetworkManager", "conf.d", nmUnmanagedName(uplink))
	if _, err := os.Stat(unmanaged); err == nil {
		return nil
	}
	if _, err := os.Stat(e.etcPath("NetworkManager")); err != nil {
		return nil
	}
	return []HostManagerConflict{{
		Manager: "NetworkManager",
		Path:    filepath.Join("NetworkManager", "conf.d", nmUnmanagedName(uplink)),
		Detail:  "NetworkManager is present and may still manage this uplink",
		Action:  "unmanage",
	}}
}

func dhcpcdDenies(content, iface string) bool {
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "denyinterfaces") {
			continue
		}
		for _, name := range fields[1:] {
			if sameIface(name, iface) {
				return true
			}
		}
	}
	return false
}

func dhcpcdPIDPresent(root, iface string) bool {
	candidates := []string{
		filepath.Join(root, "run", "dhcpcd", iface+".pid"),
		filepath.Join(root, "run", "dhcpcd-"+iface+".pid"),
		filepath.Join(root, "var", "run", "dhcpcd", iface+".pid"),
		filepath.Join(root, "var", "run", "dhcpcd-"+iface+".pid"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func nmUnmanagedName(iface string) string {
	return "50-ndl-unmanaged-" + strings.ToLower(iface) + ".conf"
}

func hostManagerConflicts(items []HostManagerConflict) []HostManagerConflict {
	var out []HostManagerConflict
	for _, item := range items {
		if item.Action == "conflict" {
			out = append(out, item)
		}
	}
	return out
}

func hostManagerActions(items []HostManagerConflict) []HostManagerConflict {
	var out []HostManagerConflict
	for _, item := range items {
		if item.Action != "" && item.Action != "conflict" {
			out = append(out, item)
		}
	}
	return out
}
