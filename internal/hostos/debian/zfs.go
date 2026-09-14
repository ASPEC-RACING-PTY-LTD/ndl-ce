package debian

const ZFSUnsupportedHost = "ZFS runtime install uses the Debian 13 adapter. This host is not Debian 13 amd64."

// ZFSRuntimePackages are optional. They are not Depends of ndl-agent.
var ZFSRuntimePackages = []string{"zfsutils-linux"}

// ZFSRuntimeInstallArgv is typed apt-get. Dry-run is the Cloud-safe default.
func ZFSRuntimeInstallArgv(dryRun bool) []string {
	argv := []string{"/usr/bin/apt-get", "-o", "APT::Get::AllowUnauthenticated=false", "-y", "--no-install-recommends"}
	if dryRun {
		argv = append(argv, "--dry-run")
	}
	argv = append(argv, "install")
	argv = append(argv, ZFSRuntimePackages...)
	return argv
}
