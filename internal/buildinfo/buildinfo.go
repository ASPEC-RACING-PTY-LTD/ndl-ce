// Package buildinfo identifies the running build. Packages stamp Version;
// live updates also stamp Build with the update's ID, so the updater can
// tell that the restarted process is the one it installed.
package buildinfo

// Set with -ldflags "-X github.com/no-dal/ndl-ce/internal/buildinfo.Version=… -X …Build=…".
var (
	Version = "dev"
	Build   = ""
)

// ID is the live update ID when there is one, else the version.
func ID() string {
	if Build != "" {
		return Build
	}
	return Version
}
