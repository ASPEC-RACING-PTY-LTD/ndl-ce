package lxc

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

// GuestSetup installs optional extras inside an already created container.
// Failures are returned as SetupWarnings. The workload is never deleted here.
func (e *Engine) GuestSetup(ctx context.Context, req LifecycleRequest) (Result, error) {
	out := Result{WorkloadID: req.WorkloadID, Status: StatusRunning}
	extras := guestextras.Resolve(req.Extras)
	if len(extras) == 0 {
		return out, nil
	}
	if e.SkipHostCmds || e.FakeUnpack {
		return out, nil
	}
	applied, err := e.readApplied(req.WorkloadID)
	if err != nil {
		return out, fmt.Errorf("guest setup could not read applied spec")
	}
	pin := applied.Spec.ImagePin
	family := guestextras.Family(pin)
	var warnings []SetupWarning
	for _, id := range extras {
		av := guestextras.AvailabilityFor(pin, id)
		if !av.Available {
			warnings = append(warnings, SetupWarning{Extra: id, Message: av.Reason})
			continue
		}
		if err := e.installGuestExtra(ctx, req.WorkloadID, family, id, av.Packages); err != nil {
			warnings = append(warnings, SetupWarning{Extra: id, Message: sanitizeGuestSetupErr(err)})
		}
	}
	out.SetupWarnings = warnings
	return out, nil
}

func (e *Engine) installGuestExtra(ctx context.Context, id, family, extra string, pkgs []string) error {
	if extra == guestextras.Compose {
		return e.installGuestCompose(ctx, id, family, pkgs)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages for %s", extra)
	}
	switch family {
	case "debian":
		if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
				return fmt.Errorf("package index update failed")
			}
		}
		args := append([]string{"install", "-y"}, pkgs...)
		if err := e.aptNoninteractive(ctx, id, args...); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptNoninteractive(ctx, id, args...); err != nil {
				return fmt.Errorf("could not install %s", extra)
			}
		}
		if extra == guestextras.Docker {
			if err := e.startGuestDocker(ctx, id, family); err != nil {
				return err
			}
		}
		return nil
	case "alpine":
		args := append([]string{"/sbin/apk", "add", "--no-cache"}, pkgs...)
		if _, err := e.attach(ctx, id, args...); err != nil {
			args[0] = "/usr/bin/apk"
			if _, err := e.attach(ctx, id, args...); err != nil {
				return fmt.Errorf("could not install %s", extra)
			}
		}
		if extra == guestextras.Docker {
			if err := e.startGuestDocker(ctx, id, family); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported guest family %s", family)
	}
}

func (e *Engine) guestComposePluginOK(ctx context.Context, id string) bool {
	if _, err := e.attach(ctx, id, "/usr/bin/docker", "compose", "version"); err == nil {
		return true
	}
	_, err := e.attach(ctx, id, "/bin/docker", "compose", "version")
	return err == nil
}

func (e *Engine) aptPackageAvailable(ctx context.Context, id, pkg string) bool {
	out, err := e.attach(ctx, id, "/usr/bin/apt-cache", "show", pkg)
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "Package: "+pkg)
}

func firstAvailableComposePackage(pkgs []string, available func(string) bool) string {
	for _, pkg := range pkgs {
		if available(pkg) {
			return pkg
		}
	}
	return ""
}

func (e *Engine) installGuestCompose(ctx context.Context, id, family string, pkgs []string) error {
	if e.guestComposePluginOK(ctx, id) {
		return nil
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("Docker Compose plugin installation failed: no packages for compose")
	}
	switch family {
	case "debian":
		if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptNoninteractive(ctx, id, "update"); err != nil {
				return fmt.Errorf("Docker Compose plugin installation failed: package index update failed")
			}
		}
		pkg := firstAvailableComposePackage(pkgs, func(name string) bool {
			return e.aptPackageAvailable(ctx, id, name)
		})
		if pkg == "" {
			return fmt.Errorf("Docker Compose plugin installation failed: no Compose v2 package in guest repositories (tried %s)", strings.Join(pkgs, ", "))
		}
		args := []string{"install", "-y", pkg}
		if err := e.aptNoninteractive(ctx, id, args...); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptNoninteractive(ctx, id, args...); err != nil {
				return fmt.Errorf("Docker Compose plugin installation failed: could not install %s", pkg)
			}
		}
	case "alpine":
		args := append([]string{"/sbin/apk", "add", "--no-cache"}, pkgs...)
		if _, err := e.attach(ctx, id, args...); err != nil {
			args[0] = "/usr/bin/apk"
			if _, err := e.attach(ctx, id, args...); err != nil {
				return fmt.Errorf("Docker Compose plugin installation failed: could not install %s", strings.Join(pkgs, ", "))
			}
		}
	default:
		return fmt.Errorf("Docker Compose plugin installation failed: unsupported guest family %s", family)
	}
	if !e.guestComposePluginOK(ctx, id) {
		return fmt.Errorf("Docker Compose plugin installation failed: package is present but `docker compose version` did not succeed")
	}
	return nil
}

func dockerStartArgv(family string) [][]string {
	switch family {
	case "debian":
		return [][]string{
			{"/bin/systemctl", "enable", "--now", "docker"},
			{"/bin/systemctl", "start", "docker"},
			{"/usr/sbin/service", "docker", "start"},
		}
	case "alpine":
		return [][]string{
			{"/sbin/rc-update", "add", "docker", "default"},
			{"/sbin/rc-service", "docker", "start"},
			{"/usr/sbin/rc-service", "docker", "start"},
		}
	default:
		return nil
	}
}

func (e *Engine) startGuestDocker(ctx context.Context, id, family string) error {
	tries, delay := 15, 2*time.Second
	if e.Run != nil {
		tries, delay = 3, time.Millisecond
	}
	for i := 0; i < tries; i++ {
		for _, argv := range dockerStartArgv(family) {
			_, _ = e.attach(ctx, id, argv...)
		}
		if _, err := e.attach(ctx, id, "/usr/bin/docker", "info"); err == nil {
			return nil
		}
		if _, err := e.attach(ctx, id, "/bin/docker", "info"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("docker is installed but the engine is not running")
}

func sanitizeGuestSetupErr(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.ReplaceAll(msg, "\n", " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}
