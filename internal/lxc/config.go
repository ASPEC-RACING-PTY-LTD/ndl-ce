package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

func (e *Engine) writeConfig(spec Spec) error {
	if err := os.MkdirAll(filepath.Dir(e.configPath(spec.WorkloadID)), 0o750); err != nil {
		return err
	}
	return os.WriteFile(e.configPath(spec.WorkloadID), []byte(hostLXCIncludes()+RenderConfig(spec)+hostLXCOverrides()), 0o640)
}

func hostLXCIncludes() string {
	if _, err := os.Stat("/usr/share/lxc/config/common.conf"); err == nil {
		return "lxc.include = /usr/share/lxc/config/common.conf\n"
	}
	return ""
}

func hostLXCOverrides() string {
	// The apparmor kernel module can be present while the LSM is not usable
	// (no securityfs). A generated profile would then prevent lxc-start.
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		return "lxc.apparmor.profile = unconfined\n"
	}
	return ""
}

// RenderConfig writes an LXC 5.x config. Privileged containers omit idmap.
func RenderConfig(spec Spec) string {
	name := hostnameOf(spec.Name, spec.WorkloadID)
	cpus := spec.CPUs
	if cpus < 1 {
		cpus = DefaultCPUs
	}
	mem := spec.MemoryBytes
	if mem < 1 {
		mem = DefaultMemoryBytes
	}
	var b strings.Builder
	fmt.Fprintf(&b, "lxc.uts.name = %s\n", name)
	fmt.Fprintf(&b, "lxc.arch = x86_64\n")
	fmt.Fprintf(&b, "lxc.rootfs.path = dir:%s\n", spec.RootfsPath)
	fmt.Fprintf(&b, "lxc.tty.max = 1\n")
	fmt.Fprintf(&b, "lxc.pty.max = 1024\n")
	fmt.Fprintf(&b, "lxc.mount.auto = proc:mixed sys:mixed cgroup:mixed\n")
	fmt.Fprintf(&b, "lxc.cgroup2.memory.max = %d\n", mem)
	fmt.Fprintf(&b, "lxc.cgroup2.cpu.max = %d 100000\n", cpus*100000)
	if spec.BridgeName != "" {
		fmt.Fprintf(&b, "lxc.net.0.type = veth\n")
		fmt.Fprintf(&b, "lxc.net.0.link = %s\n", spec.BridgeName)
		fmt.Fprintf(&b, "lxc.net.0.flags = up\n")
		fmt.Fprintf(&b, "lxc.net.0.name = eth0\n")
		if spec.MAC != "" {
			fmt.Fprintf(&b, "lxc.net.0.hwaddr = %s\n", spec.MAC)
		}
	}
	if !spec.Privileged {
		uid := spec.UIDMap
		if uid == "" {
			uid = DefaultUIDMap
		}
		gid := spec.GIDMap
		if gid == "" {
			gid = DefaultGIDMap
		}
		fmt.Fprintf(&b, "lxc.idmap = %s\n", uid)
		fmt.Fprintf(&b, "lxc.idmap = %s\n", gid)
	}
	seenAllow := map[string]bool{}
	for _, dev := range spec.GPUDevices {
		dev = strings.TrimSpace(dev)
		if dev == "" || strings.Contains(dev, "..") || !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		rel := strings.TrimPrefix(dev, "/")
		fmt.Fprintf(&b, "lxc.mount.entry = %s %s none bind,optional,create=file\n", dev, rel)
		if line := cgroupAllowLine(dev); line != "" && !seenAllow[line] {
			seenAllow[line] = true
			b.WriteString(line)
		}
	}
	return b.String()
}

func cgroupAllowLine(dev string) string {
	if line := cgroupAllowFromStat(dev); line != "" {
		return line
	}
	return cgroupAllowFromName(dev)
}

func cgroupAllowFromStat(dev string) string {
	st, err := os.Lstat(dev)
	if err != nil {
		return ""
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return ""
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	rdev := uint64(sys.Rdev)
	maj := unixMajor(rdev)
	min := unixMinor(rdev)
	if maj == 0 && min == 0 {
		return ""
	}
	return fmt.Sprintf("lxc.cgroup2.devices.allow = c %d:%d rwm\n", maj, min)
}

func cgroupAllowFromName(dev string) string {
	base := filepath.Base(dev)
	switch {
	case strings.HasPrefix(base, "renderD"):
		n := strings.TrimPrefix(base, "renderD")
		if digitsOnly(n) {
			return "lxc.cgroup2.devices.allow = c 226:" + n + " rwm\n"
		}
	case strings.HasPrefix(base, "card"):
		n := strings.TrimPrefix(base, "card")
		if digitsOnly(n) {
			return "lxc.cgroup2.devices.allow = c 226:" + n + " rwm\n"
		}
	case base == "nvidiactl":
		return "lxc.cgroup2.devices.allow = c 195:255 rwm\n"
	case strings.HasPrefix(base, "nvidia"):
		n := strings.TrimPrefix(base, "nvidia")
		if digitsOnly(n) {
			return "lxc.cgroup2.devices.allow = c 195:" + n + " rwm\n"
		}
	}
	return ""
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func unixMajor(rdev uint64) uint32 {
	return uint32((rdev >> 8) & 0xfff)
}

func unixMinor(rdev uint64) uint32 {
	return uint32((rdev & 0xff) | ((rdev >> 12) & 0xfff00))
}

func hostnameOf(name, id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
		} else if r == '_' || r == '.' || r == ' ' {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = strings.ReplaceAll(id, "-", "")
		if len(out) > 12 {
			out = out[:12]
		}
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}
