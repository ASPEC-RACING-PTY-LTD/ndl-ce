package debian

import "strings"

const GPUUnsupportedHost = "GPU runtime install uses the Debian 13 adapter. This host is not Debian 13 amd64."

// GPURuntimePackages are optional host packages. They are not Depends of ndl-agent.
var GPURuntimePackages = []string{
	"firmware-misc-nonfree",
	"nvidia-driver",
	"nvidia-persistenced",
}

// GPURuntimeInstallArgv is a typed apt-get install. Dry-run is the Cloud-safe default.
func GPURuntimeInstallArgv(dryRun bool) []string {
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y", "--no-install-recommends"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	argv = append(argv, "install")
	argv = append(argv, GPURuntimePackages...)
	return argv
}

// GPUDriverctlOverrideArgv binds one validated BDF to vfio-pci. ADDR must already be a BDF.
func GPUDriverctlOverrideArgv(addr string) []string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	return []string{"/usr/sbin/driverctl", "set-override", addr, "vfio-pci"}
}

// GPUDriverctlRestoreArgv removes a VFIO override so the host driver can bind again.
func GPUDriverctlRestoreArgv(addr string) []string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	return []string{"/usr/sbin/driverctl", "unset-override", addr}
}
