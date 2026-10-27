package claudeagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ACLRunner runs setfacl; tests replace it.
type ACLRunner func(ctx context.Context, args ...string) ([]byte, error)

// Host gives the agent container's user access to granted folders on the
// host. Ownership is never changed: the game keeps owning its files, and a
// named-user ACL (with a default ACL, so new files inherit it) lets the
// container's user read or write them.
type Host struct {
	// DataDir is a game server's data folder on this node.
	DataDir func(serverID string) string
	// SetFACL runs setfacl; nil runs /usr/bin/setfacl.
	SetFACL ACLRunner
}

func (h Host) setfacl(ctx context.Context, args ...string) ([]byte, error) {
	if h.SetFACL != nil {
		return h.SetFACL(ctx, args...)
	}
	return exec.CommandContext(ctx, "/usr/bin/setfacl", args...).CombinedOutput()
}

// Resolve finds a granted folder on the host. It must be an existing
// folder whose real path is inside the server's data folder, so a symlink
// cannot point a mount or an ACL somewhere else.
func (h Host) Resolve(a HostACL) (string, error) {
	rel, err := CleanFolder(a.Path)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(a.ServerID) == "" || strings.ContainsAny(a.ServerID, "/\\") || a.ServerID == "." || a.ServerID == ".." {
		return "", fmt.Errorf("server id is not valid")
	}
	root, err := filepath.EvalSymlinks(h.DataDir(a.ServerID))
	if err != nil {
		return "", fmt.Errorf("the server's files are not on this node")
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("folder %s does not exist on the server", rel)
	}
	if !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("folder %s leads outside the server", rel)
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a folder", rel)
	}
	if strings.ContainsAny(real, " \t\n\r\x00\\,=#:") {
		return "", fmt.Errorf("folder %s has a character a mount cannot use", rel)
	}
	return real, nil
}

// Grant resolves each folder and gives uid access to it and everything in it.
func (h Host) Grant(ctx context.Context, uid int, acls []HostACL) ([]MountSource, error) {
	if uid <= 0 {
		return nil, fmt.Errorf("the agent container's user id is unknown")
	}
	out := make([]MountSource, 0, len(acls))
	for _, a := range acls {
		real, err := h.Resolve(a)
		if err != nil {
			return nil, err
		}
		perm := "rX"
		if a.Write {
			perm = "rwX"
		}
		spec := fmt.Sprintf("u:%d:%s,d:u:%d:%s", uid, perm, uid, perm)
		if b, err := h.setfacl(ctx, "-R", "-P", "-m", spec, real); err != nil {
			return nil, fmt.Errorf("giving access to %s failed: %v: %s", a.Path, err, strings.TrimSpace(string(b)))
		}
		out = append(out, MountSource{ServerID: a.ServerID, Path: a.Path, Source: real, Write: a.Write})
	}
	return out, nil
}

// Revoke takes uid's access away again. Folders that are gone are skipped.
func (h Host) Revoke(ctx context.Context, uid int, acls []HostACL) error {
	if uid <= 0 {
		return nil
	}
	var errs []string
	for _, a := range acls {
		real, err := h.Resolve(a)
		if err != nil {
			continue
		}
		spec := fmt.Sprintf("u:%d,d:u:%d", uid, uid)
		if b, err := h.setfacl(ctx, "-R", "-P", "-x", spec, real); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v: %s", a.Path, err, strings.TrimSpace(string(b))))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("taking access away failed: %s", strings.Join(errs, "; "))
	}
	return nil
}
