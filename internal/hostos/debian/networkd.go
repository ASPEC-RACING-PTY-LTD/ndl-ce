package debian

import (
	"path/filepath"

	"github.com/no-dal/ndl-ce/internal/ndnet"
)

// NetworkDir is the Debian 13 systemd-networkd drop-in directory.
const NetworkDir = "/etc/systemd/network"

// PersistKind is the host-platform persistence name. It is not identity.
const PersistKind = ndnet.PersistNetworkd

// NetworkdFiles returns Debian networkd artifacts for a shared plan.
// Paths are locators. The network UUID remains the desired identity.
func NetworkdFiles(plan ndnet.Plan) []ndnet.File {
	out := make([]ndnet.File, 0, len(plan.Files))
	for _, file := range plan.Files {
		out = append(out, ndnet.File{
			RelPath: filepath.Base(file.RelPath),
			Body:    file.Body,
		})
	}
	return out
}

// Owned reports whether a filename is a No-dal-owned networkd drop-in.
func Owned(name string) bool {
	base := filepath.Base(name)
	return len(base) >= 7 && (base[:7] == "50-ndl-" || base[:7] == "50-NDL-")
}
