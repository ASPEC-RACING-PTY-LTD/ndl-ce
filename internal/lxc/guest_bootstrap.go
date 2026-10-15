package lxc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

// GuestCommand runs one fixed command inside a running system container.
// Callers pass a compile-time binary and arguments, never a shell string.
func (e *Engine) GuestCommand(ctx context.Context, id string, cmd ...string) ([]byte, error) {
	return e.attach(ctx, id, cmd...)
}

func (e *Engine) attach(ctx context.Context, id string, cmd ...string) ([]byte, error) {
	args := []string{
		"-P", e.lxcPath(), "-n", id,
		"--clear-env",
		"-v", "DEBIAN_FRONTEND=noninteractive",
		"-v", "LC_ALL=C.UTF-8",
		"-v", "LANG=C.UTF-8",
		"-v", "PATH=/usr/sbin:/usr/bin:/sbin:/bin",
		"--",
	}
	args = append(args, cmd...)
	return e.run(ctx, BinLXCAttach, args...)
}

// Reachability probes inside a guest are bounded. With no route out of the
// guest a resolver lookup can hang far longer than its own timeout, and an
// unbounded probe stalls whoever called it (agent startup included).
const (
	guestDNSProbeTimeout = 3 * time.Second
	guestNetProbeTimeout = 8 * time.Second
	guestUnitTimeout     = 15 * time.Second
)

// probe runs one in-guest check under its own deadline.
func (e *Engine) probe(ctx context.Context, d time.Duration, id string, cmd ...string) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	_, err := e.attach(ctx, id, cmd...)
	return err
}

func (e *Engine) waitNeedsIPv4(id string) bool {
	applied, err := e.readApplied(id)
	if err != nil {
		return true
	}
	return applied.Spec.IP.IPv4Mode != IPModeDisabled
}

func (e *Engine) bootstrapGuest(ctx context.Context, id string, reconcileOnly bool) error {
	if e.SkipHostCmds || e.FakeUnpack {
		return nil
	}
	applied, err := e.readApplied(id)
	if err != nil {
		return nil
	}
	spec, err := normalizeSpec(applied.Spec)
	if err != nil {
		return err
	}
	rootfs := spec.RootfsPath
	if strings.TrimSpace(rootfs) == "" {
		return nil
	}
	first := false
	if _, err := os.Lstat(filepath.Join(rootfs, guestFirstBootstrapRel)); err == nil {
		first = true
	}
	if reconcileOnly {
		first = false
	}
	if guestextras.Family(spec.ImagePin) == "alpine" {
		return e.bootstrapAlpineGuest(ctx, id, rootfs, spec, first, reconcileOnly)
	}
	err = e.ensureGuestDNS(ctx, id, rootfs, spec)
	e.noteGuestNet(id, err)
	if err != nil {
		if first && !reconcileOnly {
			return err
		}
	}
	if !reconcileOnly {
		if err := e.ensureGuestPackages(ctx, id, rootfs, spec, first); err != nil {
			if first {
				return err
			}
		}
	}
	if err := ensureGuestConsole(rootfs); err != nil {
		if first && !reconcileOnly {
			return err
		}
	} else {
		_ = chownMappedGuest(rootfs, spec, "etc/profile.d/00-ndl-term.sh", "etc/ndl/inputrc")
	}
	return nil
}

func (e *Engine) bootstrapAlpineGuest(ctx context.Context, id, rootfs string, spec Spec, first, reconcileOnly bool) error {
	err := e.ensureAlpineGuestDNS(ctx, id, rootfs, spec)
	e.noteGuestNet(id, err)
	if err != nil {
		if first && !reconcileOnly {
			return err
		}
	}
	if err := ensureGuestConsole(rootfs); err != nil {
		if first && !reconcileOnly {
			return err
		}
	} else {
		_ = chownMappedGuest(rootfs, spec, "etc/profile.d/00-ndl-term.sh", "etc/ndl/inputrc")
	}
	return e.writeBaselineMarker(rootfs, spec, first)
}

func (e *Engine) ensureAlpineGuestDNS(ctx context.Context, id, rootfs string, spec Spec) error {
	if e.guestAlpineReachable(ctx, id) {
		return nil
	}
	if err := writeAlpineResolvFallback(rootfs, spec); err != nil {
		return fmt.Errorf("dns fallback: %w", err)
	}
	if e.guestAlpineReachable(ctx, id) {
		return nil
	}
	return fmt.Errorf("guest DNS and repository reachability failed")
}

func (e *Engine) guestAlpineReachable(ctx context.Context, id string) bool {
	for _, argv := range alpineReachabilityArgv() {
		if ctx.Err() != nil {
			return false
		}
		if e.probe(ctx, guestNetProbeTimeout, id, argv...) == nil {
			return true
		}
	}
	return false
}

func alpineReachabilityArgv() [][]string {
	return [][]string{
		{"/bin/busybox", "wget", "-q", "-O", "/dev/null", "-T", "5", "https://dl-cdn.alpinelinux.org/alpine/"},
		{"/usr/bin/wget", "-q", "-O", "/dev/null", "--timeout=5", "https://dl-cdn.alpinelinux.org/alpine/"},
		{"/bin/wget", "-q", "-O", "/dev/null", "-T", "5", "https://dl-cdn.alpinelinux.org/alpine/"},
		{"/bin/busybox", "nslookup", "dl-cdn.alpinelinux.org"},
		{"/usr/bin/nslookup", "dl-cdn.alpinelinux.org"},
		{"/bin/nslookup", "dl-cdn.alpinelinux.org"},
		{"/bin/busybox", "ping", "-c", "1", "-W", "3", "1.1.1.1"},
		{"/bin/ping", "-c", "1", "-W", "3", "1.1.1.1"},
	}
}

func (e *Engine) ensureGuestDNS(ctx context.Context, id, rootfs string, spec Spec) error {
	if e.guestDNSOK(ctx, id) {
		return nil
	}
	if err := writeGuestDNSFallback(rootfs, spec); err != nil {
		return fmt.Errorf("dns fallback: %w", err)
	}
	_ = e.probe(ctx, guestUnitTimeout, id, "/bin/systemctl", "restart", "systemd-resolved")
	if e.guestDNSOK(ctx, id) {
		return nil
	}
	if e.guestTCP443OK(ctx, id) {
		return nil
	}
	return fmt.Errorf("guest DNS and repository reachability failed")
}

func (e *Engine) guestDNSOK(ctx context.Context, id string) bool {
	hosts := []string{"deb.debian.org", "github.com", "raw.githubusercontent.com", "api.github.com"}
	const need = 2
	ok := 0
	for i, host := range hosts {
		if ctx.Err() != nil || ok+len(hosts)-i < need {
			return false
		}
		if e.probe(ctx, guestDNSProbeTimeout, id, "/usr/bin/getent", "hosts", host) == nil {
			ok++
			if ok >= need {
				return true
			}
		}
	}
	return false
}

func (e *Engine) guestTCP443OK(ctx context.Context, id string) bool {
	targets := []string{"deb.debian.org", "github.com", "1.1.1.1"}
	for _, host := range targets {
		if ctx.Err() != nil {
			return false
		}
		err := e.probe(ctx, guestNetProbeTimeout, id, "/usr/bin/python3", "-c",
			"import socket,sys;s=socket.create_connection(('"+host+"',443),5);s.close()")
		if err == nil {
			return true
		}
	}
	return false
}

func (e *Engine) ensureGuestPackages(ctx context.Context, id, rootfs string, spec Spec, first bool) error {
	pkgs := debianBasePackages()
	if spec.SSHRoot {
		pkgs = append(pkgs, "openssh-server")
	}
	if !first {
		if missing := e.missingGuestPackages(ctx, id, pkgs); len(missing) == 0 {
			return e.writeBaselineMarker(rootfs, spec, false)
		} else {
			pkgs = missing
		}
	}
	if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
		_ = e.repairDPKG(ctx, id)
		if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
			return fmt.Errorf("apt update failed: %w", err)
		}
	}
	if first {
		if err := e.aptNoninteractive(ctx, id, "upgrade", "-y"); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptNoninteractive(ctx, id, "upgrade", "-y"); err != nil {
				return fmt.Errorf("apt upgrade failed: %w", err)
			}
		}
	}
	args := append([]string{"install", "-y"}, pkgs...)
	if err := e.aptNoninteractive(ctx, id, args...); err != nil {
		_ = e.repairDPKG(ctx, id)
		if err := e.aptNoninteractive(ctx, id, args...); err != nil {
			return fmt.Errorf("apt install %s failed: %w", strings.Join(pkgs, " "), err)
		}
	}
	return e.writeBaselineMarker(rootfs, spec, first)
}

func (e *Engine) missingGuestPackages(ctx context.Context, id string, pkgs []string) []string {
	var missing []string
	for _, p := range pkgs {
		out, err := e.attach(ctx, id, "/usr/bin/dpkg-query", "-W", "-f=${Status}", p)
		if err != nil || !strings.Contains(string(out), "install ok installed") {
			missing = append(missing, p)
		}
	}
	return missing
}

func (e *Engine) repairDPKG(ctx context.Context, id string) error {
	_, err := e.attach(ctx, id, "/usr/bin/dpkg", "--configure", "-a")
	if err != nil {
		return err
	}
	return e.aptNoninteractive(ctx, id, "-f", "install", "-y")
}

func (e *Engine) aptNoninteractive(ctx context.Context, id string, aptArgs ...string) error {
	cmd := []string{
		"/usr/bin/apt-get",
		"-o", "Dpkg::Options::=--force-confdef",
		"-o", "Dpkg::Options::=--force-confold",
	}
	cmd = append(cmd, aptArgs...)
	_, err := e.attach(ctx, id, cmd...)
	return err
}

func (e *Engine) writeBaselineMarker(rootfs string, spec Spec, upgraded bool) error {
	if err := os.MkdirAll(filepath.Join(rootfs, guestNDLDir), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("schema=%s\nupgraded=%t\n", guestBaselineSchema, upgraded)
	if err := os.WriteFile(filepath.Join(rootfs, guestBaselineRel), []byte(body), 0o644); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(rootfs, guestFirstBootstrapRel))
	return chownMappedGuest(rootfs, spec, guestNDLDir)
}
