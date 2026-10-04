package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func (e *Engine) writeConfig(spec Spec) error {
	if err := os.MkdirAll(filepath.Dir(e.configPath(spec.WorkloadID)), 0o750); err != nil {
		return err
	}
	return os.WriteFile(e.configPath(spec.WorkloadID), []byte(hostLXCIncludes(spec)+RenderConfig(spec)+hostLXCOverrides(spec)), 0o640)
}

func hostLXCIncludes(spec Spec) string {
	var b strings.Builder
	if _, err := os.Stat("/usr/share/lxc/config/common.conf"); err == nil {
		b.WriteString("lxc.include = /usr/share/lxc/config/common.conf\n")
	}
	if !spec.Privileged {
		if _, err := os.Stat("/usr/share/lxc/config/userns.conf"); err == nil {
			b.WriteString("lxc.include = /usr/share/lxc/config/userns.conf\n")
		}
	}
	return b.String()
}

func hostLXCOverrides(spec Spec) string {
	// The apparmor kernel module can be present while the LSM is not usable
	// (no securityfs). A generated profile would then prevent lxc-start.
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		return "lxc.apparmor.profile = unconfined\n"
	}
	if !SpecWantsNesting(spec) {
		return "lxc.apparmor.profile = " + ApparmorGeneratedProfile + "\n" +
			"lxc.apparmor.raw = deny mount -> /proc/,\n" +
			"lxc.apparmor.raw = deny mount -> /sys/,\n"
	}
	return nestingLXCConfig()
}

func nestingLXCConfig() string {
	// Coherent nested-engine feature set for an unprivileged system container.
	// Generated AppArmor plus allow_nesting is LXC's nested-container policy
	// (overlay, bind, rbind, cgroup, fuse mounts). seccomp.allow_nesting lets
	// Docker/runc load a nested filter. userns.conf (included for unprivileged
	// guests) keeps keyctl by leaving cap.drop/keep empty. The start-host hook
	// applies current LXC generated-profile nesting semantics on hosts whose
	// liblxc still emits the pre-6.0.6 /proc and /sys write denials. Do not
	// unconfine, and do not add a No-DAL blanket mount rule.
	return "lxc.apparmor.profile = " + ApparmorGeneratedProfile + "\n" +
		"lxc.apparmor.allow_nesting = 1\n" +
		"lxc.seccomp.allow_nesting = 1\n" +
		"lxc.mount.entry = /dev/fuse dev/fuse none bind,optional,create=file 0 0\n" +
		"lxc.hook.version = 1\n" +
		"lxc.hook.start-host = " + BinNestingApparmor + "\n"
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
	if spec.Privileged {
		fmt.Fprintf(&b, "lxc.mount.auto = proc:mixed sys:mixed cgroup:mixed\n")
	} else {
		fmt.Fprintf(&b, "lxc.mount.auto = proc:mixed sys:rw cgroup:mixed\n")
	}
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
		b.WriteString(renderNetIP(spec.IP))
	}
	if spec.TUN {
		b.WriteString("lxc.mount.entry = /dev/net/tun dev/net/tun none bind,optional,create=file 0 0\n")
	}
	b.WriteString(renderMounts(spec))
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
	var gpuRules []string
	for _, dev := range gpuDeviceNodes(spec) {
		rel := strings.TrimPrefix(dev, "/")
		fmt.Fprintf(&b, "lxc.mount.entry = %s %s none bind,optional,create=file\n", dev, rel)
		if rule := cgroupDeviceRule(dev); rule != "" {
			gpuRules = append(gpuRules, rule)
		}
	}
	b.WriteString(renderDevicePolicy(spec, gpuRules))
	return b.String()
}

// unprivilegedDeviceBaseline is what every unprivileged guest needs: the
// liblxc autodev nodes, the console, and devpts. It matches the device list in
// LXC common.conf, without the c/b *:* m wildcards (mknod stays opt-in, and
// the kernel exempts overlayfs whiteouts) and without fuse (nesting adds it).
var unprivilegedDeviceBaseline = []string{
	"c 1:3 rwm",   // /dev/null
	"c 1:5 rwm",   // /dev/zero
	"c 1:7 rwm",   // /dev/full
	"c 1:8 rwm",   // /dev/random
	"c 1:9 rwm",   // /dev/urandom
	"c 5:0 rwm",   // /dev/tty
	"c 5:1 rwm",   // /dev/console
	"c 5:2 rwm",   // /dev/ptmx
	"c 136:* rwm", // /dev/pts/*
}

const (
	deviceRuleFuse = "c 10:229 rwm"
	deviceRuleTUN  = "c 10:200 rwm"
)

// renderDevicePolicy writes the cgroup2 device allowlist.
//
// userns.conf clears every lxc.cgroup2.devices rule inherited from
// common.conf. liblxc then attaches a default-deny eBPF program as soon as a
// single rule is present, so appending only GPU or TUN rules would deny
// /dev/null, /dev/pts, and the rest of the guest's own devices. Unprivileged
// guests therefore get a complete policy that starts from deny-all. Privileged
// guests keep the common.conf allowlist, which userns.conf does not clear.
func renderDevicePolicy(spec Spec, gpuRules []string) string {
	var rules []string
	var b strings.Builder
	if !spec.Privileged {
		b.WriteString("lxc.cgroup2.devices.deny = a\n")
		rules = append(rules, unprivilegedDeviceBaseline...)
		if SpecWantsNesting(spec) {
			rules = append(rules, deviceRuleFuse)
		}
	}
	if spec.TUN {
		rules = append(rules, deviceRuleTUN)
	}
	if spec.AllowMknod {
		rules = append(rules, "c *:* m", "b *:* m")
	}
	rules = append(rules, gpuRules...)
	seen := map[string]bool{}
	for _, rule := range rules {
		if seen[rule] {
			continue
		}
		seen[rule] = true
		fmt.Fprintf(&b, "lxc.cgroup2.devices.allow = %s\n", rule)
	}
	return b.String()
}

// gpuDeviceNodes lists every node the guest receives. A by-path locator is
// followed by the canonical node it names on this host (renderD129, card1),
// because VAAPI, NVENC, and libdrm enumerate /dev/dri/renderD* and card*.
// The config is rendered on every start, so renumbered nodes are picked up.
func gpuDeviceNodes(spec Spec) []string {
	var out []string
	seen := map[string]bool{}
	add := func(dev string) {
		if !seen[dev] {
			seen[dev] = true
			out = append(out, dev)
		}
	}
	for _, dev := range spec.GPUDevices {
		dev = strings.TrimSpace(dev)
		if dev == "" || strings.Contains(dev, "..") || !strings.HasPrefix(dev, "/dev/") {
			continue
		}
		add(dev)
		if canon := resolveDeviceNode(dev); canon != "" {
			add(canon)
		}
	}
	return out
}

// cgroupDeviceRule returns the exact "c MAJOR:MINOR rwm" rule for one host
// node. Unknown nodes get no rule, which the allowlist denies.
func cgroupDeviceRule(dev string) string {
	if rule := cgroupRuleFromStat(dev); rule != "" {
		return rule
	}
	return cgroupRuleFromName(dev)
}

func cgroupRuleFromName(dev string) string {
	base := filepath.Base(dev)
	switch {
	case strings.HasPrefix(base, "renderD"):
		n := strings.TrimPrefix(base, "renderD")
		if digitsOnly(n) {
			return "c 226:" + n + " rwm"
		}
	case strings.HasPrefix(base, "card"):
		n := strings.TrimPrefix(base, "card")
		if digitsOnly(n) {
			return "c 226:" + n + " rwm"
		}
	case base == "nvidiactl":
		return "c 195:255 rwm"
	case strings.HasPrefix(base, "nvidia"):
		n := strings.TrimPrefix(base, "nvidia")
		if digitsOnly(n) {
			return "c 195:" + n + " rwm"
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
