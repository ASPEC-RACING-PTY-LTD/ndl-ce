package vmspec

import (
	"fmt"
	"strings"
)

// RenderUserData builds cloud-config. Password is included only in the seed file.
func RenderUserData(n NoCloud) (string, error) {
	if strings.TrimSpace(n.UserData) != "" {
		if strings.ContainsAny(n.UserData, "\x00") {
			return "", fmt.Errorf("user-data contains a banned character")
		}
		return n.UserData, nil
	}
	user := strings.TrimSpace(n.Username)
	if user == "" {
		user = "debian"
	}
	if !safeCloudToken(user) {
		return "", fmt.Errorf("username is not valid")
	}
	host := strings.TrimSpace(n.Hostname)
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if host != "" {
		if !safeCloudToken(host) {
			return "", fmt.Errorf("hostname is not valid")
		}
		fmt.Fprintf(&b, "hostname: %s\n", host)
		b.WriteString("manage_etc_hosts: true\n")
	}
	fmt.Fprintf(&b, "users:\n  - name: %s\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    groups: sudo\n    shell: /bin/bash\n    lock_passwd: %t\n", user, n.Password == "")
	if len(n.SSHAuthorizedKeys) > 0 {
		b.WriteString("    ssh_authorized_keys:\n")
		for _, key := range n.SSHAuthorizedKeys {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if strings.ContainsAny(key, "\n\r\x00") {
				return "", fmt.Errorf("ssh key contains a banned character")
			}
			fmt.Fprintf(&b, "      - %s\n", key)
		}
	}
	if n.Password != "" {
		if strings.ContainsAny(n.Password, "\n\r\x00") {
			return "", fmt.Errorf("password contains a banned character")
		}
		fmt.Fprintf(&b, "chpasswd:\n  expire: false\n  list: |\n    %s:%s\n", user, n.Password)
		b.WriteString("ssh_pwauth: true\n")
	}
	return b.String(), nil
}

func RenderMetaData(n NoCloud, instanceID string) string {
	host := strings.TrimSpace(n.Hostname)
	if host == "" {
		host = "nodal"
	}
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", instanceID, host)
}

func safeCloudToken(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			continue
		}
		if r == '-' && i > 0 {
			continue
		}
		return false
	}
	return true
}

// ClassifyEdit reports how each changed field applies.
func ClassifyEdit(prev, next Spec) []ApplyClass {
	prev = Normalize(prev)
	next = Normalize(next)
	var out []ApplyClass
	add := func(field, apply, reason string, changed bool) {
		if changed {
			out = append(out, ApplyClass{Field: field, Apply: apply, Reason: reason})
		}
	}
	add("autostart", ApplyLive, "systemd enablement can change while the VM runs", prev.Autostart != next.Autostart)
	add("cpus", ApplyRestart, "CPU hotplug is Phase 18", prev.CPUs != next.CPUs)
	add("memory_bytes", ApplyRestart, "memory balloon resize is not a live product action in Phase 8", prev.MemoryBytes != next.MemoryBytes)
	add("machine", ApplyUnsupported, "machine ABI is frozen after create", prev.Machine != next.Machine)
	add("firmware", ApplyStop, "firmware changes require a stopped VM", prev.Firmware != next.Firmware)
	add("iso_library_id", ApplyStop, "installation media changes require a stopped VM", prev.ISOLibraryID != next.ISOLibraryID)
	add("nocloud", ApplyRestart, "NoCloud seed is applied at boot", nocloudChanged(prev.NoCloud, next.NoCloud))
	add("disks", ApplyStop, "disk topology changes require a stopped VM", disksChanged(prev.Disks, next.Disks))
	add("nics", ApplyStop, "NIC topology changes require a stopped VM", nicsChanged(prev.NICs, next.NICs))
	add("name", ApplyRestart, "display name is desired state; guest hostname follows NoCloud", prev.Name != next.Name)
	return out
}

func RequiresStop(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyStop || c.Apply == ApplyUnsupported {
			return true
		}
	}
	return false
}

func RequiresRestart(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyRestart {
			return true
		}
	}
	return false
}

func HasUnsupported(classes []ApplyClass) bool {
	for _, c := range classes {
		if c.Apply == ApplyUnsupported {
			return true
		}
	}
	return false
}

func nocloudChanged(a, b NoCloud) bool {
	if a.Enable != b.Enable || a.Hostname != b.Hostname || a.Username != b.Username || a.UserData != b.UserData || a.NetworkConfig != b.NetworkConfig {
		return true
	}
	if a.Password != "" || b.Password != "" {
		return true
	}
	if len(a.SSHAuthorizedKeys) != len(b.SSHAuthorizedKeys) {
		return true
	}
	for i := range a.SSHAuthorizedKeys {
		if a.SSHAuthorizedKeys[i] != b.SSHAuthorizedKeys[i] {
			return true
		}
	}
	return false
}

func disksChanged(a, b []Disk) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i].VolumeID != b[i].VolumeID || a[i].Role != b[i].Role || a[i].ReadOnly != b[i].ReadOnly {
			return true
		}
	}
	return false
}

func nicsChanged(a, b []NIC) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i].NetworkID != b[i].NetworkID || a[i].ID != b[i].ID {
			return true
		}
	}
	return false
}
