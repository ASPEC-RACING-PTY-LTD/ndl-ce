package debian

const LVMUnsupportedHost = "LVM runtime install uses the Debian 13 adapter. This host is not Debian 13 amd64."

// LVMRuntimePackages are optional. They are not Depends of ndl-agent.
var LVMRuntimePackages = []string{"lvm2"}

// LVMRuntimeInstallArgv is typed apt-get. Dry-run is the Cloud-safe default.
func LVMRuntimeInstallArgv(dryRun bool) []string {
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y", "--no-install-recommends"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	argv = append(argv, "install")
	argv = append(argv, LVMRuntimePackages...)
	return argv
}
