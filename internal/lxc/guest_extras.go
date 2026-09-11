package lxc

import (
	"context"
	"fmt"
	"strings"

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
			_, _ = e.attach(ctx, id, "/bin/systemctl", "enable", "--now", "docker")
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
			_, _ = e.attach(ctx, id, "/sbin/rc-update", "add", "docker", "default")
			_, _ = e.attach(ctx, id, "/sbin/rc-service", "docker", "start")
		}
		return nil
	default:
		return fmt.Errorf("unsupported guest family %s", family)
	}
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
