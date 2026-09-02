package migration

import (
	"fmt"
	"strings"
)

// ForbiddenSourceActions is the closed list of operations No-dal must never
// perform against source infrastructure. There is no API to enable them.
var ForbiddenSourceActions = []string{
	"delete source workload",
	"delete source disks",
	"delete source snapshots",
	"delete source backups",
	"clean up source environment",
	"remove source configuration",
	"decommission source after success",
	"delete source after migration",
}

// AssertNoSourceDestruction documents and enforces that a completed or failed
// migration leaves the original infrastructure intact.
func AssertNoSourceDestruction(sourceChanges string) error {
	if sourceChanges != "" && !strings.EqualFold(sourceChanges, "NONE") && !strings.EqualFold(sourceChanges, "UNCHANGED") {
		if looksDestructive(sourceChanges) {
			return fmt.Errorf("source destruction is not a migration operation")
		}
	}
	return nil
}

func looksDestructive(s string) bool {
	low := strings.ToLower(s)
	for _, n := range []string{"delete", "destroy", "decommission", "remove source", "cleanup source", "clean up source"} {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// DiscloseMutations returns the operator-visible temporary source-side
// operations required by a selected mode. Empty means read-only.
func DiscloseMutations(mode string, caps Caps) []string {
	if mode != ModeSnapshot {
		return nil
	}
	if len(caps.TemporaryMutations) > 0 {
		return append([]string{}, caps.TemporaryMutations...)
	}
	if caps.Snapshot {
		return []string{"Create a temporary snapshot named " + TempSnapshotPrefix + "<job> on the source. The original workload is not deleted. Pre-existing snapshots are not deleted."}
	}
	return nil
}

func SourceUnchanged() string { return "UNCHANGED" }

func SourceChangesNone() string { return "NONE" }
