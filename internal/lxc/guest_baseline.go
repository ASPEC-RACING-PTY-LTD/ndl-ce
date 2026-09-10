package lxc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	guestNDLDir            = "etc/ndl"
	guestBaselineRel       = "etc/ndl/guest-baseline"
	guestFirstBootstrapRel = "etc/ndl/needs-first-bootstrap"
	guestDNSFallbackRel    = "etc/systemd/resolved.conf.d/ndl-dns-fallback.conf"
	guestBaselineSchema    = "1"
)

// helperScriptDebianPackages is the literal Debian helper-script base set
// (sudo, curl, mc, gnupg2, jq). No-DAL does not pretend this list includes
// git or Docker.
var helperScriptDebianPackages = []string{"sudo", "curl", "mc", "gnupg2", "jq"}

// ndlCompatDebianPackages are extra packages No-DAL installs because our
// application workloads and later Docker/compose installs need them. Distinct
// from helperScriptDebianPackages.
var ndlCompatDebianPackages = []string{"ca-certificates", "wget", "iproute2"}

func debianBasePackages() []string {
	out := make([]string, 0, len(helperScriptDebianPackages)+len(ndlCompatDebianPackages))
	out = append(out, helperScriptDebianPackages...)
	out = append(out, ndlCompatDebianPackages...)
	return out
}

func provisionGuest(rootfs string, spec Spec) error {
	hostname := hostnameOf(spec.Name, spec.WorkloadID)
	if err := ensureGuestNetwork(rootfs, spec.IP); err != nil {
		return err
	}
	if err := ensureGuestIdentity(rootfs, hostname); err != nil {
		return err
	}
	if err := ensureGuestLocale(rootfs); err != nil {
		return err
	}
	if err := ensureGuestBaseline(rootfs, spec); err != nil {
		return err
	}
	return ensureGuestNano(rootfs)
}

func ensureGuestBaseline(rootfs string, spec Spec) error {
	if strings.TrimSpace(rootfs) == "" {
		return nil
	}
	if err := ensureGuestEnvironment(rootfs); err != nil {
		return err
	}
	if err := ensureGuestTimezone(rootfs); err != nil {
		return err
	}
	if err := ensureGuestSystemdUnits(rootfs); err != nil {
		return err
	}
	if err := ensureGuestConsole(rootfs); err != nil {
		return err
	}
	if err := ensureGuestMotd(rootfs, hostnameOf(spec.Name, spec.WorkloadID)); err != nil {
		return err
	}
	if err := ensureGuestSSHConfig(rootfs, spec.SSHRoot); err != nil {
		return err
	}
	if err := ensureGuestPythonCompat(rootfs, spec.PythonSystemPIP); err != nil {
		return err
	}
	return sanitizeRootfs(rootfs, spec)
}

func maybeMarkFirstBootstrap(rootfs string) error {
	if _, err := os.Lstat(filepath.Join(rootfs, guestBaselineRel)); err == nil {
		return nil
	}
	if _, err := os.Lstat(filepath.Join(rootfs, guestFirstBootstrapRel)); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(rootfs, guestNDLDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(rootfs, guestFirstBootstrapRel), []byte("1\n"), 0o644)
}

func ensureGuestLocale(rootfs string) error {
	body := []byte("LANG=C.UTF-8\nLC_ALL=C.UTF-8\nLANGUAGE=C.UTF-8\n")
	if err := os.MkdirAll(filepath.Join(rootfs, "etc", "default"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "default", "locale"), body, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(rootfs, "etc", "locale.conf"), body, 0o644)
}

func ensureGuestEnvironment(rootfs string) error {
	path := filepath.Join(rootfs, "etc", "environment")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(b)
	for _, line := range []string{"LANG=C.UTF-8", "LC_ALL=C.UTF-8", "LANGUAGE=C.UTF-8"} {
		key := strings.SplitN(line, "=", 2)[0] + "="
		if strings.Contains(text, key) {
			continue
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += line + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func hostTimezone() string {
	b, err := os.ReadFile("/etc/timezone")
	if err == nil {
		tz := strings.TrimSpace(string(b))
		if tz != "" && !strings.Contains(tz, "..") && !strings.HasPrefix(tz, "/") {
			return tz
		}
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			tz := strings.TrimPrefix(target[i:], "zoneinfo/")
			if tz != "" && !strings.Contains(tz, "..") {
				return tz
			}
		}
	}
	return "Etc/UTC"
}

func ensureGuestTimezone(rootfs string) error {
	tz := hostTimezone()
	etc := filepath.Join(rootfs, "etc")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(etc, "timezone"), []byte(tz+"\n"), 0o644); err != nil {
		return err
	}
	zone := filepath.Join(rootfs, "usr", "share", "zoneinfo", filepath.FromSlash(tz))
	localtime := filepath.Join(etc, "localtime")
	_ = os.Remove(localtime)
	if _, err := os.Stat(zone); err == nil {
		rel, err := filepath.Rel(etc, zone)
		if err == nil {
			return os.Symlink(rel, localtime)
		}
	}
	if host, err := os.ReadFile("/etc/localtime"); err == nil && len(host) > 0 {
		return os.WriteFile(localtime, host, 0o644)
	}
	return os.Symlink("/usr/share/zoneinfo/"+tz, localtime)
}

func maskSystemdUnit(rootfs, name string) error {
	path := filepath.Join(rootfs, "etc", "systemd", "system", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Symlink("/dev/null", path)
}

func ensureGuestSystemdUnits(rootfs string) error {
	if err := maskSystemdUnit(rootfs, "systemd-networkd-wait-online.service"); err != nil {
		return err
	}
	if err := maskSystemdUnit(rootfs, "systemd-homed.service"); err != nil {
		return err
	}
	return maskSystemdUnit(rootfs, "systemd-homed-firstboot.service")
}

func writeGettyDropin(rootfs, unit, exec string) error {
	dir := filepath.Join(rootfs, "etc", "systemd", "system", unit+".d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "[Service]\n" +
		"ExecStart=\n" +
		"ExecStart=" + exec + "\n" +
		"Environment=TERM=linux\n"
	return os.WriteFile(filepath.Join(dir, "ndl-autologin.conf"), []byte(body), 0o644)
}

func ensureGuestConsole(rootfs string) error {
	agetty := "-/sbin/agetty --autologin root --noclear --keep-baud pts/%I 115200,38400,9600 linux"
	if err := writeGettyDropin(rootfs, "container-getty@.service", agetty); err != nil {
		return err
	}
	console := "-/sbin/agetty --autologin root --noclear --keep-baud console 115200,38400,9600 linux"
	if err := writeGettyDropin(rootfs, "console-getty.service", console); err != nil {
		return err
	}
	dir := filepath.Join(rootfs, "etc", "profile.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	term := `# No-DAL terminal defaults.
# LXC console uses TERM=linux. SSH sessions use xterm-256color.
if [ -n "${SSH_CONNECTION:-}" ] || [ -n "${SSH_TTY:-}" ]; then
  case "${TERM:-}" in
    linux|dumb|"") export TERM=xterm-256color ;;
  esac
else
  export TERM="${TERM:-linux}"
fi
if [ -n "${BASH_VERSION:-}" ]; then
  bind 'set enable-bracketed-paste off' 2>/dev/null || true
fi
`
	return os.WriteFile(filepath.Join(dir, "00-ndl-term.sh"), []byte(term), 0o644)
}

func ensureGuestMotd(rootfs, hostname string) error {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		hostname = "ndl"
	}
	motd := "No-DAL system container: " + hostname + "\n"
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "motd"), []byte(motd), 0o644); err != nil {
		return err
	}
	dir := filepath.Join(rootfs, "etc", "profile.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := `#!/bin/sh
# No-DAL container details. Does not replace application MOTD customizations.
if [ -z "${NDL_DETAILS_SHOWN:-}" ]; then
  NDL_DETAILS_SHOWN=1
  export NDL_DETAILS_SHOWN
  echo "OS: $(. /etc/os-release 2>/dev/null; echo "${PRETTY_NAME:-Debian}")"
  echo "Hostname: $(hostname 2>/dev/null || cat /etc/hostname 2>/dev/null)"
  ip -4 -o addr show dev eth0 2>/dev/null | awk '{print "IPv4: " $4}' | sed 's#/.*##'
fi
`
	if err := os.WriteFile(filepath.Join(dir, "00-ndl-details.sh"), []byte(script), 0o644); err != nil {
		return err
	}
	update := filepath.Join(rootfs, "etc", "update-motd.d")
	ents, err := os.ReadDir(update)
	if err != nil {
		return nil
	}
	for _, ent := range ents {
		p := filepath.Join(update, ent.Name())
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			continue
		}
		_ = os.Chmod(p, st.Mode()&^0o111)
	}
	return nil
}

func ensureGuestSSHConfig(rootfs string, enabled bool) error {
	if !enabled {
		return nil
	}
	dir := filepath.Join(rootfs, "etc", "ssh", "sshd_config.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ndl-root.conf"), []byte("PermitRootLogin yes\n"), 0o644)
}

func ensureGuestPythonCompat(rootfs string, enable bool) error {
	if !enable {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(rootfs, "usr", "lib", "python3.*", "EXTERNALLY-MANAGED"))
	if err != nil {
		return err
	}
	for _, p := range matches {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("python compat marker: %w", err)
		}
	}
	return nil
}

func writeGuestDNSFallback(rootfs string) error {
	dir := filepath.Join(rootfs, "etc", "systemd", "resolved.conf.d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := "[Resolve]\nFallbackDNS=8.8.8.8 1.1.1.1\n"
	return os.WriteFile(filepath.Join(rootfs, guestDNSFallbackRel), []byte(body), 0o644)
}

func guestBaselineChownRels() []string {
	return []string{
		"etc/environment",
		"etc/timezone",
		"etc/localtime",
		"etc/motd",
		"etc/profile.d",
		"etc/profile.d/00-ndl-term.sh",
		"etc/profile.d/00-ndl-details.sh",
		"etc/systemd/system/systemd-networkd-wait-online.service",
		"etc/systemd/system/systemd-homed.service",
		"etc/systemd/system/systemd-homed-firstboot.service",
		"etc/systemd/system/container-getty@.service.d",
		"etc/systemd/system/container-getty@.service.d/ndl-autologin.conf",
		"etc/systemd/system/console-getty.service.d",
		"etc/systemd/system/console-getty.service.d/ndl-autologin.conf",
		"etc/ssh/sshd_config.d",
		"etc/ssh/sshd_config.d/ndl-root.conf",
		guestNDLDir,
		guestBaselineRel,
		guestFirstBootstrapRel,
		guestDNSFallbackRel,
		"etc/systemd/resolved.conf.d",
	}
}
