package gameserver

import (
	"fmt"
	"strings"
)

// BuildOption is one build of a game a server can run, such as Garry's
// Mod's 32-bit and 64-bit builds. A template lists them with the default
// first. A server's build is whichever launcher its startup command runs.
type BuildOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Beta is the Steam branch holding this build's files; empty is the
	// default branch.
	Beta string `json:"beta,omitempty"`
	// Run is the launcher at the start of the startup command, such as
	// "./srcds_run". Its file being present means the build is installed.
	Run string `json:"run"`
}

// BuildFor reports which build a startup command runs.
func (t Template) BuildFor(startup string) (BuildOption, bool) {
	cmd := strings.TrimSpace(startup)
	var best BuildOption
	found := false
	for _, b := range t.Builds {
		if cmd == b.Run || strings.HasPrefix(cmd, b.Run+" ") {
			if !found || len(b.Run) > len(best.Run) {
				best, found = b, true
			}
		}
	}
	return best, found
}

// BuildByID finds one of the template's builds.
func (t Template) BuildByID(id string) (BuildOption, bool) {
	for _, b := range t.Builds {
		if b.ID == id {
			return b, true
		}
	}
	return BuildOption{}, false
}

// WithBuild rewrites a startup command to run another build's launcher,
// keeping every argument.
func (t Template) WithBuild(startup string, to BuildOption) (string, error) {
	cur, ok := t.BuildFor(startup)
	if !ok {
		return "", fmt.Errorf("the startup command does not start with a known launcher (%s)", t.buildRuns())
	}
	cmd := strings.TrimSpace(startup)
	return to.Run + strings.TrimPrefix(cmd, cur.Run), nil
}

// LauncherFile is the build's launcher as a path inside the server folder.
func (b BuildOption) LauncherFile() string {
	return strings.TrimPrefix(b.Run, "./")
}

func (t Template) buildRuns() string {
	runs := make([]string, 0, len(t.Builds))
	for _, b := range t.Builds {
		runs = append(runs, b.Run)
	}
	return strings.Join(runs, " or ")
}

// gmodBuilds are Garry's Mod's builds. The x86-64 branch carries both
// launchers, so going back to 32-bit needs no download.
func gmodBuilds() []BuildOption {
	return []BuildOption{
		{ID: "x86", Label: "32-bit", Run: "./srcds_run"},
		{ID: "x86-64", Label: "64-bit", Beta: "x86-64", Run: "./srcds_run_x64"},
	}
}
