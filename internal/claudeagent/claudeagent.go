// Package claudeagent runs Claude Code in a dedicated system container and
// gives it access to the No-dal resources an operator grants: game server
// folders mounted into the container (with POSIX ACLs so it can write files
// the game owns) and, through a scoped token, a server's log, console and
// restart. Claude Code runs as "claude remote-control", so chats are started
// from claude.ai/code or the Claude app.
package claudeagent

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	// GuestUser runs Claude Code inside the container.
	GuestUser = "claude"
	// GuestUID is GuestUser's id inside the container. On the host it is
	// the container's mapped root plus GuestUID.
	GuestUID = 1000
	// GuestHome is GuestUser's home inside the container.
	GuestHome = "/home/claude"
	// ClaudeBin is where the native installer puts Claude Code.
	ClaudeBin = GuestHome + "/.local/bin/claude"
	// WorkDir is where Remote Control runs; granted folders are mounted
	// below it, one folder per resource.
	WorkDir = "/srv"
	// ConfigDir holds the API settings and the Remote Control environment.
	ConfigDir = "/etc/ndl-claude"
	// Service runs Remote Control inside the container.
	Service = "ndl-claude.service"
	// LoginSession is the tmux session the sign-in runs in.
	LoginSession = "ndl-login"
)

// Grant kinds.
const KindGameServer = "gameserver"

// Permission modes Remote Control may start sessions in.
var PermissionModes = []string{"default", "acceptEdits", "plan"}

// Folder is one granted folder, relative to the resource's data folder.
type Folder struct {
	Path  string `json:"path"`
	Write bool   `json:"write"`
}

// Grant is what the agent may use of one resource.
type Grant struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Slug    string   `json:"slug,omitempty"`
	Folders []Folder `json:"folders"`
	Log     bool     `json:"log,omitempty"`
	Console bool     `json:"console,omitempty"`
	Restart bool     `json:"restart,omitempty"`
}

// HostACL is one granted folder on the host, for the agent to give the
// container's user access to (or take it away).
type HostACL struct {
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
	Write    bool   `json:"write"`
}

// Request is the request_json of every agent action.
type Request struct {
	WorkloadID string `json:"workload_id"`
	// setup and grant: Remote Control settings, API access and the grants
	// that CLAUDE.md describes.
	Name           string  `json:"name,omitempty"`
	PermissionMode string  `json:"permission_mode,omitempty"`
	APIURL         string  `json:"api_url,omitempty"`
	APIToken       string  `json:"api_token,omitempty"`
	Grants         []Grant `json:"grants,omitempty"`
	// login-code
	Code string `json:"code,omitempty"`
	// grant and revoke
	ACLs []HostACL `json:"acls,omitempty"`
}

// Status is what the agent reports about the container.
type Status struct {
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	LoggedIn  bool   `json:"logged_in"`
	Account   string `json:"account,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Service   string `json:"service,omitempty"`
	LoginURL  string `json:"login_url,omitempty"`
	Message   string `json:"message,omitempty"`
}

// MountSource is a granted folder resolved on the host.
type MountSource struct {
	ServerID string `json:"server_id"`
	Path     string `json:"path"`
	Source   string `json:"source"`
	Write    bool   `json:"write"`
}

// GrantResult is the answer to grant: the host folders to mount.
type GrantResult struct {
	Mounts []MountSource `json:"mounts"`
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
	codeRe = regexp.MustCompile(`^[A-Za-z0-9_#.~-]{8,400}$`)
	slugRe = regexp.MustCompile(`[^a-z0-9_-]+`)
)

// ValidName checks a Remote Control name.
func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("name must be 1 to 40 letters, digits, spaces, dots, dashes or underscores")
	}
	return nil
}

// ValidMode checks a permission mode.
func ValidMode(mode string) error {
	for _, m := range PermissionModes {
		if m == mode {
			return nil
		}
	}
	return fmt.Errorf("permission mode must be one of %s", strings.Join(PermissionModes, ", "))
}

// ValidCode checks a sign-in code before it is typed into the login.
func ValidCode(code string) error {
	if !codeRe.MatchString(code) {
		return fmt.Errorf("that does not look like a sign-in code; copy the whole code from the page")
	}
	return nil
}

// Slug is the folder a resource gets under WorkDir.
func Slug(name, id string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	if s == "" {
		s = id
		if len(s) > 8 {
			s = s[:8]
		}
	}
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// CleanFolder checks a granted folder: relative to the resource's data
// folder, below it, and usable as an LXC mount path.
func CleanFolder(p string) (string, error) {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return "", fmt.Errorf("choose a folder inside the server, not the whole server")
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("folder %q is outside the server", p)
	}
	if strings.ContainsAny(clean, " \t\n\r\x00\\,=#:") {
		return "", fmt.Errorf("folder %q has a character a mount cannot use (space, comma, =, #, :)", p)
	}
	return clean, nil
}

// NormalizeGrants cleans grants: known kinds, unique slugs, clean folders
// with no folder inside another of the same grant.
func NormalizeGrants(in []Grant) ([]Grant, error) {
	out := make([]Grant, 0, len(in))
	slugs := map[string]bool{}
	ids := map[string]bool{}
	for _, g := range in {
		if g.Kind != KindGameServer {
			return nil, fmt.Errorf("grant kind %q is not supported", g.Kind)
		}
		if strings.TrimSpace(g.ID) == "" || ids[g.ID] {
			return nil, fmt.Errorf("each grant needs its own resource")
		}
		ids[g.ID] = true
		g.Slug = Slug(g.Name, g.ID)
		for base, n := g.Slug, 2; slugs[g.Slug]; n++ {
			g.Slug = fmt.Sprintf("%s-%d", base, n)
		}
		slugs[g.Slug] = true
		seen := map[string]bool{}
		folders := make([]Folder, 0, len(g.Folders))
		for _, f := range g.Folders {
			p, err := CleanFolder(f.Path)
			if err != nil {
				return nil, err
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			folders = append(folders, Folder{Path: p, Write: f.Write})
		}
		sort.Slice(folders, func(i, j int) bool { return folders[i].Path < folders[j].Path })
		for i := 1; i < len(folders); i++ {
			for j := 0; j < i; j++ {
				if strings.HasPrefix(folders[i].Path, folders[j].Path+"/") {
					return nil, fmt.Errorf("%s is inside %s; grant one or the other", folders[i].Path, folders[j].Path)
				}
			}
		}
		g.Folders = folders
		out = append(out, g)
	}
	return out, nil
}

// Target is where a granted folder appears inside the container.
func Target(g Grant, f Folder) string {
	return path.Join(WorkDir, g.Slug, f.Path)
}

// DefaultFolders are offered for a game the first time it is granted.
func DefaultFolders(game string) []Folder {
	switch strings.ToLower(game) {
	case "gmod", "garrysmod":
		return []Folder{{Path: "garrysmod/gamemodes", Write: true}, {Path: "garrysmod/addons", Write: true}}
	}
	return nil
}

// ClaudeMD is the CLAUDE.md written to WorkDir: what is where and what the
// agent may do. It is regenerated on every grant change.
func ClaudeMD(grants []Grant) string {
	var b strings.Builder
	b.WriteString("# No-dal Claude Agent\n\n")
	b.WriteString("You run in a No-dal system container. Only the folders below are shared with you; ")
	b.WriteString("everything else on the host is out of reach. Read-only folders refuse writes.\n\n")
	if len(grants) == 0 {
		b.WriteString("Nothing is shared yet. The operator grants access in No-dal under Claude Agent.\n")
		return b.String()
	}
	for _, g := range grants {
		fmt.Fprintf(&b, "## %s (game server)\n\n", firstNonEmpty(g.Name, g.ID))
		for _, f := range g.Folders {
			mode := "read-only"
			if f.Write {
				mode = "read-write"
			}
			fmt.Fprintf(&b, "- `%s` is the server's `%s` (%s)\n", Target(g, f), f.Path, mode)
		}
		var cmds []string
		if g.Log {
			cmds = append(cmds, fmt.Sprintf("`ndl log %s [LINES]` reads the server log", g.Slug))
		}
		if g.Console {
			cmds = append(cmds, fmt.Sprintf("`ndl console %s COMMAND` runs a console command", g.Slug))
		}
		if g.Restart {
			cmds = append(cmds, fmt.Sprintf("`ndl restart %s` restarts the server", g.Slug))
		}
		if len(cmds) > 0 {
			b.WriteString("\nYou may also use:\n\n")
			for _, c := range cmds {
				b.WriteString("- " + c + "\n")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("Changes to files take effect when the game reloads them; ask before restarting a server people are playing on.\n")
	return b.String()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
