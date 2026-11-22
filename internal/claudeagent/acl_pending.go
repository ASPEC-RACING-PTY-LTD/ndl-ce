package claudeagent

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Granting access must not rewrite an ACL that already says the same
// thing. setfacl -R rewrites every file's ACL, and every rewrite changes
// the file's change time: a game watching its files (the JvS gamemode
// reloads on each change) sees a folder-wide edit each time the agent's
// grants are saved. So the grant walks the folder itself and calls
// setfacl only for the files and folders that lack the access.

const (
	aclXattrAccess  = "system.posix_acl_access"
	aclXattrDefault = "system.posix_acl_default"
	aclTagUser      = 0x02
	aclTagMask      = 0x10
	aclPermRead     = 4
	aclPermWrite    = 2
	aclPermExec     = 1
)

// ACLReader reads an extended attribute without following links; tests
// replace it.
type ACLReader func(path, name string) ([]byte, error)

func readXattr(path, name string) ([]byte, error) {
	buf := make([]byte, 256)
	for {
		n, err := unix.Lgetxattr(path, name, buf)
		if errors.Is(err, unix.ERANGE) {
			buf = make([]byte, len(buf)*4)
			continue
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
}

func (h Host) readACL(path, name string) ([]byte, error) {
	if h.ReadACL != nil {
		return h.ReadACL(path, name)
	}
	return readXattr(path, name)
}

// aclEntry is one named-user entry and the mask, from an ACL xattr.
type aclEntry struct {
	userPerm int // -1 when the user has no entry
	mask     int // -1 when there is no mask
}

func parseACL(raw []byte, uid int) aclEntry {
	out := aclEntry{userPerm: -1, mask: -1}
	// Header: a 4-byte version (2), then 8-byte entries: tag, perm, id.
	if len(raw) < 4 || binary.LittleEndian.Uint32(raw[:4]) != 2 {
		return out
	}
	for i := 4; i+8 <= len(raw); i += 8 {
		tag := binary.LittleEndian.Uint16(raw[i:])
		perm := int(binary.LittleEndian.Uint16(raw[i+2:]))
		id := int(binary.LittleEndian.Uint32(raw[i+4:]))
		switch {
		case tag == aclTagUser && id == uid:
			out.userPerm = perm
		case tag == aclTagMask:
			out.mask = perm
		}
	}
	return out
}

// has reports whether the entry grants want, through the mask too.
func (e aclEntry) has(want int) bool {
	if e.userPerm < 0 || e.userPerm&want != want {
		return false
	}
	return e.mask < 0 || e.mask&want == want
}

// wantPerm is what "rX" or "rwX" means for one path: X is execute for a
// folder, or for a file someone may already execute.
func wantPerm(write, dir bool, mode fs.FileMode) int {
	p := aclPermRead
	if write {
		p |= aclPermWrite
	}
	if dir || mode&0o111 != 0 {
		p |= aclPermExec
	}
	return p
}

// pendingGrant lists the files and folders under real (itself included)
// whose ACL does not yet give uid the access. Links are skipped, as
// setfacl -P does.
func (h Host) pendingGrant(real string, uid int, write bool) (files, dirs []string, err error) {
	err = filepath.WalkDir(real, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := wantPerm(write, d.IsDir(), info.Mode())
		raw, _ := h.readACL(p, aclXattrAccess)
		ok := parseACL(raw, uid).has(want)
		if d.IsDir() {
			// New files inherit the default ACL, so folders need it too.
			if ok {
				rawDef, _ := h.readACL(p, aclXattrDefault)
				ok = parseACL(rawDef, uid).has(want)
			}
			if !ok {
				dirs = append(dirs, p)
			}
			return nil
		}
		if info.Mode().IsRegular() && !ok {
			files = append(files, p)
		}
		return nil
	})
	return files, dirs, err
}

// hasSearch reports whether uid may already pass through dir.
func (h Host) hasSearch(dir string, uid int) bool {
	raw, _ := h.readACL(dir, aclXattrAccess)
	return parseACL(raw, uid).has(aclPermExec)
}

// pathBatches splits paths so one setfacl call stays well under the
// argument limit.
func pathBatches(paths []string, n int) [][]string {
	var out [][]string
	for len(paths) > n {
		out = append(out, paths[:n])
		paths = paths[n:]
	}
	if len(paths) > 0 {
		out = append(out, paths)
	}
	return out
}
