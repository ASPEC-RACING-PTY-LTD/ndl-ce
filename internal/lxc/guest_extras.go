package lxc

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/no-dal/ndl-ce/internal/guestextras"
)

type extraMode int

const (
	extraInstall extraMode = iota
	extraUpdate
	extraReinstall
)

// GuestSetup installs optional extras inside an already created container.
// Failures are returned as SetupWarnings. The workload is never deleted here.
func (e *Engine) GuestSetup(ctx context.Context, req LifecycleRequest) (Result, error) {
	return e.guestExtras(ctx, req, extraInstall)
}

// guestExtras installs, updates or reinstalls extras and reports what is
// installed afterwards. One extra failing does not stop the others.
func (e *Engine) guestExtras(ctx context.Context, req LifecycleRequest, mode extraMode) (Result, error) {
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
	indexed := false
	for _, id := range extras {
		av := guestextras.AvailabilityFor(pin, id)
		if !av.Available {
			warnings = append(warnings, SetupWarning{Extra: id, Message: av.Reason})
			continue
		}
		if av.Extra.Download {
			change, err := e.installDownloadExtra(ctx, req, pin, family, av, mode)
			if err != nil {
				warnings = append(warnings, SetupWarning{Extra: id, Message: sanitizeGuestSetupErr(err)})
			} else {
				out.Changes = append(out.Changes, change)
			}
			continue
		}
		if family == "debian" && !indexed {
			if err := e.aptRetry(ctx, req.WorkloadID, "update"); err != nil {
				_ = e.repairDPKG(ctx, req.WorkloadID)
				if err := e.aptRetry(ctx, req.WorkloadID, "update"); err != nil {
					warnings = append(warnings, SetupWarning{Extra: id, Message: "package index update failed: " + sanitizeGuestSetupErr(err)})
					continue
				}
			}
			indexed = true
		}
		if err := e.installGuestExtra(ctx, req.WorkloadID, family, id, av.Packages, mode); err != nil {
			warnings = append(warnings, SetupWarning{Extra: id, Message: sanitizeGuestSetupErr(err)})
		}
	}
	out.SetupWarnings = warnings
	if st, err := e.GuestExtrasStatus(ctx, req.WorkloadID); err == nil {
		out.Extras = st.Extras
	}
	return out, nil
}

// installDownloadExtra installs an extra that is not a distribution package.
// A later opt-in extra (such as a CodeHold Actions runner) adds its case here.
func (e *Engine) installDownloadExtra(ctx context.Context, req LifecycleRequest, pin, family string, av guestextras.Availability, mode extraMode) (ExtraChange, error) {
	switch av.Extra.ID {
	case guestextras.CodeholdCLI:
		return e.installGuestCodeholdCLI(ctx, req.WorkloadID, pin, family, av.Packages, mode, req.Codehold)
	case guestextras.Cloudflared:
		return e.installGuestCloudflared(ctx, req.WorkloadID, family, av.Packages, mode)
	}
	return ExtraChange{Extra: av.Extra.ID}, fmt.Errorf("no installer for %s", av.Extra.ID)
}

func aptModeArgs(mode extraMode, pkgs []string) []string {
	args := []string{"install", "-y"}
	switch mode {
	case extraUpdate:
		args = append(args, "--only-upgrade")
	case extraReinstall:
		args = append(args, "--reinstall")
	}
	return append(args, pkgs...)
}

func apkModeArgs(bin string, mode extraMode, pkgs []string) []string {
	switch mode {
	case extraUpdate:
		return append([]string{bin, "add", "--no-cache", "--upgrade"}, pkgs...)
	case extraReinstall:
		return append([]string{bin, "fix", "--reinstall"}, pkgs...)
	}
	return append([]string{bin, "add", "--no-cache"}, pkgs...)
}

func (e *Engine) installGuestExtra(ctx context.Context, id, family, extra string, pkgs []string, mode extraMode) error {
	if extra == guestextras.Compose {
		return e.installGuestCompose(ctx, id, family, pkgs, mode)
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("no packages for %s", extra)
	}
	switch family {
	case "debian":
		if mode != extraInstall {
			pkgs = e.installedDebPackages(ctx, id, pkgs)
			if len(pkgs) == 0 {
				return fmt.Errorf("%s is not installed; install it first", extra)
			}
		}
		args := aptModeArgs(mode, pkgs)
		if err := e.aptRetry(ctx, id, args...); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptRetry(ctx, id, args...); err != nil {
				return fmt.Errorf("could not install %s: %s", extra, sanitizeGuestSetupErr(err))
			}
		}
	case "alpine":
		if err := e.apk(ctx, id, mode, pkgs); err != nil {
			return fmt.Errorf("could not install %s: %s", extra, sanitizeGuestSetupErr(err))
		}
	default:
		return fmt.Errorf("unsupported guest family %s", family)
	}
	if extra == guestextras.Docker {
		if err := e.startGuestDocker(ctx, id, family); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) apk(ctx context.Context, id string, mode extraMode, pkgs []string) error {
	_, err := e.attach(ctx, id, apkModeArgs("/sbin/apk", mode, pkgs)...)
	if err != nil {
		_, err = e.attach(ctx, id, apkModeArgs("/usr/bin/apk", mode, pkgs)...)
	}
	return err
}

// aptRetry runs apt-get, waiting out a package lock held by another apt run
// (common right after a container first boots). The error carries apt's last
// output line.
func (e *Engine) aptRetry(ctx context.Context, id string, aptArgs ...string) error {
	tries, delay := 12, 10*time.Second
	if e.Run != nil {
		tries, delay = 2, time.Millisecond
	}
	cmd := append([]string{"/usr/bin/apt-get", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}, aptArgs...)
	var last error
	for i := 0; i < tries; i++ {
		out, err := e.attach(ctx, id, cmd...)
		if err == nil {
			return nil
		}
		last = aptError(out, err)
		text := string(out)
		if !strings.Contains(text, "Could not get lock") && !strings.Contains(text, "is held by process") {
			return last
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return last
}

func aptError(out []byte, err error) error {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return fmt.Errorf("%s", l)
		}
	}
	return err
}

// installedDebPackages keeps the packages that are installed.
func (e *Engine) installedDebPackages(ctx context.Context, id string, pkgs []string) []string {
	var out []string
	for _, p := range pkgs {
		if ok, _ := e.debInstalled(ctx, id, p); ok {
			out = append(out, p)
		}
	}
	return out
}

func (e *Engine) debInstalled(ctx context.Context, id, pkg string) (bool, string) {
	out, err := e.attach(ctx, id, "/usr/bin/dpkg-query", "-W", "-f=${Status}|${Version}", pkg)
	if err != nil {
		return false, ""
	}
	parts := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(parts) != 2 || !strings.HasSuffix(parts[0], " installed") {
		return false, ""
	}
	return true, parts[1]
}

// GuestExtrasStatus reports which catalog extras are installed in the guest,
// or only the ones named in only.
func (e *Engine) GuestExtrasStatus(ctx context.Context, workloadID string, only ...string) (Result, error) {
	out := Result{WorkloadID: workloadID, Status: StatusRunning}
	if e.SkipHostCmds || e.FakeUnpack {
		return out, nil
	}
	applied, err := e.readApplied(workloadID)
	if err != nil {
		return out, fmt.Errorf("could not read the container's spec")
	}
	pin := applied.Spec.ImagePin
	family := guestextras.Family(pin)
	for _, extra := range guestextras.Catalog() {
		if len(only) > 0 && !slices.Contains(only, extra.ID) {
			continue
		}
		st := ExtraStatus{ID: extra.ID}
		av := guestextras.AvailabilityFor(pin, extra.ID)
		switch {
		case extra.ID == guestextras.CodeholdCLI:
			if v, ok := e.guestCHVersion(ctx, workloadID, guestCHPath); ok {
				st.Installed, st.Version = true, v
			}
		case extra.ID == guestextras.Compose:
			if v, ok := e.composeVersion(ctx, workloadID); ok {
				st.Installed, st.Version = true, v
			}
		case family == "debian":
			for _, p := range av.Packages {
				if ok, v := e.debInstalled(ctx, workloadID, p); ok {
					st.Installed, st.Version, st.Package = true, v, p
					break
				}
			}
		case family == "alpine":
			for _, p := range av.Packages {
				if _, err := e.attach(ctx, workloadID, "/sbin/apk", "info", "-e", p); err == nil {
					st.Installed, st.Package = true, p
					break
				}
			}
		}
		if extra.ID == guestextras.Docker && st.Installed {
			if !e.dockerRunning(ctx, workloadID) {
				st.Detail = "installed, but the Docker engine is not running"
			}
		}
		out.Extras = append(out.Extras, st)
	}
	return out, nil
}

func (e *Engine) dockerRunning(ctx context.Context, id string) bool {
	if _, err := e.attach(ctx, id, "/usr/bin/docker", "info"); err == nil {
		return true
	}
	_, err := e.attach(ctx, id, "/bin/docker", "info")
	return err == nil
}

func (e *Engine) composeVersion(ctx context.Context, id string) (string, bool) {
	for _, bin := range []string{"/usr/bin/docker", "/bin/docker"} {
		out, err := e.attach(ctx, id, bin, "compose", "version", "--short")
		if err == nil {
			return strings.TrimSpace(string(out)), true
		}
	}
	return "", false
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

func (e *Engine) installGuestCompose(ctx context.Context, id, family string, pkgs []string, mode extraMode) error {
	if mode == extraInstall && e.guestComposePluginOK(ctx, id) {
		return nil
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("Docker Compose plugin installation failed: no packages for compose")
	}
	switch family {
	case "debian":
		pkg := ""
		if mode != extraInstall {
			if installed := e.installedDebPackages(ctx, id, pkgs); len(installed) > 0 {
				pkg = installed[0]
			}
		}
		if pkg == "" {
			pkg = firstAvailableComposePackage(pkgs, func(name string) bool {
				return e.aptPackageAvailable(ctx, id, name)
			})
		}
		if pkg == "" {
			return fmt.Errorf("Docker Compose plugin installation failed: no Compose v2 package in guest repositories (tried %s)", strings.Join(pkgs, ", "))
		}
		args := aptModeArgs(mode, []string{pkg})
		if mode == extraUpdate && len(e.installedDebPackages(ctx, id, []string{pkg})) == 0 {
			args = aptModeArgs(extraInstall, []string{pkg})
		}
		if err := e.aptRetry(ctx, id, args...); err != nil {
			_ = e.repairDPKG(ctx, id)
			if err := e.aptRetry(ctx, id, args...); err != nil {
				return fmt.Errorf("Docker Compose plugin installation failed: could not install %s: %s", pkg, sanitizeGuestSetupErr(err))
			}
		}
	case "alpine":
		if err := e.apk(ctx, id, mode, pkgs); err != nil {
			return fmt.Errorf("Docker Compose plugin installation failed: could not install %s: %s", strings.Join(pkgs, ", "), sanitizeGuestSetupErr(err))
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
