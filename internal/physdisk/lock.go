package physdisk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var runDir = "/run/ndl/physdisk"

// SetRunDirForTest redirects runtime claims. Tests must restore the previous dir.
func SetRunDirForTest(dir string) func() {
	prev := runDir
	runDir = dir
	return func() { runDir = prev }
}

var assignMu sync.Mutex

// AssignGate serializes assignment and start-time claims so two requests
// cannot hand the same disk to two running VMs.
func AssignGate() *sync.Mutex { return &assignMu }

// Claim records exclusive runtime ownership for a running VM. Stale files
// from a crashed unit are replaced when steal is true.
func Claim(id DeviceID, workloadID string, steal bool) error {
	if id == "" || workloadID == "" {
		return fmt.Errorf("physical disk claim requires a device and workload")
	}
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		return err
	}
	path := lockPath(id)
	if raw, err := os.ReadFile(path); err == nil {
		owner := strings.TrimSpace(string(raw))
		if owner != "" && owner != workloadID && !steal {
			return fmt.Errorf("Physical disk %s is already claimed by another workload.", id)
		}
	}
	return os.WriteFile(path, []byte(workloadID+"\n"), 0o640)
}

// Release drops a runtime claim. Persistent assignment is unchanged.
func Release(id DeviceID, workloadID string) error {
	path := lockPath(id)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owner := strings.TrimSpace(string(raw))
	if owner != "" && workloadID != "" && owner != workloadID {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func lockPath(id DeviceID) string {
	name := strings.ReplaceAll(string(id), "/", "_")
	return filepath.Join(runDir, name+".lock")
}
