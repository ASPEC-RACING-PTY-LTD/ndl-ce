package gpu

import (
	"context"
	"strings"

	"github.com/no-dal/ndl-ce/internal/hostos"
	"github.com/no-dal/ndl-ce/internal/hostos/debian"
)

const (
	RuntimeNotInstalled = "not_installed"
	RuntimeUnsupported  = "unsupported"
	RuntimeReady        = "ready"
	RuntimePending      = "pending"
)

// RuntimeStatus is host-platform GPU userland, not a fake CUDA capability.
type RuntimeStatus struct {
	HostSupported bool              `json:"host_supported"`
	Status        string            `json:"status"`
	Reason        string            `json:"reason,omitempty"`
	CUDA          string            `json:"cuda"`
	ROCm          string            `json:"rocm"`
	Packages      []string          `json:"packages,omitempty"`
	Argv          []string          `json:"argv,omitempty"`
	Flags         map[string]string `json:"flags,omitempty"`
}

// EvaluateRuntime reports Debian 13 runtime install capability. Other hosts stay unsupported.
func EvaluateRuntime(p hostos.Platform, lookPath func(string) (string, error)) RuntimeStatus {
	out := RuntimeStatus{
		CUDA:  inventoryNotReported("cuda"),
		ROCm:  inventoryNotReported("rocm"),
		Flags: map[string]string{"nvidia_visible_devices": "never_all"},
	}
	if lookPath != nil {
		if _, err := lookPath("nvidia-smi"); err == nil {
			out.CUDA = "available"
		}
		if _, err := lookPath("rocminfo"); err == nil {
			out.ROCm = "available"
		} else if _, err := lookPath("rocm-smi"); err == nil {
			out.ROCm = "available"
		}
	}
	if p.ID != "debian" || p.VersionID != "13" || p.Architecture != "amd64" {
		out.HostSupported = false
		out.Status = RuntimeUnsupported
		out.Reason = debian.GPUUnsupportedHost
		return out
	}
	out.HostSupported = true
	out.Status = RuntimeNotInstalled
	out.Packages = debian.GPURuntimePackages
	out.Argv = debian.GPURuntimeInstallArgv(true)
	if out.CUDA == "available" || out.ROCm == "available" {
		out.Status = RuntimeReady
	}
	return out
}

func inventoryNotReported(name string) string {
	_ = name
	return "not_reported"
}

// RuntimeInstallArgv is typed Debian argv. It is never NVIDIA_VISIBLE_DEVICES=all.
func RuntimeInstallArgv(p hostos.Platform, dryRun bool) (RuntimeStatus, error) {
	st := EvaluateRuntime(p, nil)
	if !st.HostSupported {
		return st, nil
	}
	st.Argv = debian.GPURuntimeInstallArgv(dryRun)
	if dryRun {
		st.Status = RuntimePending
	}
	return st, nil
}

// RunRuntimeInstall executes typed argv when exec is non-nil.
func RunRuntimeInstall(ctx context.Context, p hostos.Platform, dryRun bool, exec hostos.ExecFunc) (RuntimeStatus, error) {
	st, err := RuntimeInstallArgv(p, dryRun)
	if err != nil || !st.HostSupported {
		return st, err
	}
	if exec == nil {
		return st, nil
	}
	out, err := exec(ctx, st.Argv)
	if err != nil {
		st.Status = StatusFailed
		st.Reason = strings.TrimSpace(out + " " + err.Error())
		return st, nil
	}
	if !dryRun {
		st.Status = RuntimePending
		st.Reason = "packages requested; reboot and DKMS build are host-platform follow-up"
	}
	return st, nil
}
