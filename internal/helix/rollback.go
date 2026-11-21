package helix

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	commitRE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	pullRE   = regexp.MustCompile(`^\s*(git|ch)\s+(pull|fetch)(\s|$)`)
)

// ValidCommit reports whether c is an abbreviated or full commit hash.
func ValidCommit(c string) bool { return commitRE.MatchString(c) }

// RollbackCommands turns a project's deploy commands into a rollback to
// commit: the folder is reset to that commit (fetching first if it is not
// there), then the project's own commands run without the steps that pull
// or fetch, so the old commit is what gets built and started.
func RollbackCommands(runs []string, commit string) ([]string, error) {
	if !ValidCommit(commit) {
		return nil, fmt.Errorf("%q is not a commit hash", commit)
	}
	out := []string{
		fmt.Sprintf("git cat-file -e %s^{commit} 2>/dev/null || git fetch --quiet", commit),
		fmt.Sprintf("git -c advice.detachedHead=false reset --hard %s", commit),
	}
	for _, run := range runs {
		var keep []string
		for _, part := range strings.Split(run, "&&") {
			if strings.TrimSpace(part) == "" || pullRE.MatchString(part) {
				continue
			}
			keep = append(keep, strings.TrimSpace(part))
		}
		if len(keep) > 0 {
			out = append(out, strings.Join(keep, " && "))
		}
	}
	return out, nil
}
