package storage

import (
	"bufio"
	"strings"
)

var sharedFSTypes = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smb": true,
	"fuse.sshfs": true, "fuse.rclone": true, "glusterfs": true,
	"ceph": true, "cephfs": true, "9p": true, "afs": true, "lustre": true,
}

// Mount is one /proc/self/mountinfo record.
type Mount struct {
	MountPoint string
	Root       string
	FSType     string
	Source     string
	FSUUID     string
}

// ParseMountinfo parses Linux mountinfo text.
func ParseMountinfo(text string) []Mount {
	var out []Mount
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		m, ok := parseMountLine(line)
		if ok {
			out = append(out, m)
		}
	}
	return out
}

func parseMountLine(line string) (Mount, bool) {
	// mountinfo: ... mountpoint mountopts - fstype source superopts
	sep := strings.Index(line, " - ")
	if sep < 0 {
		return Mount{}, false
	}
	left := strings.Fields(line[:sep])
	right := strings.Fields(line[sep+3:])
	if len(left) < 5 || len(right) < 2 {
		return Mount{}, false
	}
	m := Mount{
		Root:       unescapeMount(left[3]),
		MountPoint: unescapeMount(left[4]),
		FSType:     right[0],
		Source:     unescapeMount(right[1]),
	}
	if len(right) >= 3 {
		m.FSUUID = fsUUIDFromSuper(right[2])
	}
	return m, m.MountPoint != ""
}

func unescapeMount(s string) string {
	s = strings.ReplaceAll(s, `\040`, " ")
	s = strings.ReplaceAll(s, `\011`, "\t")
	s = strings.ReplaceAll(s, `\012`, "\n")
	s = strings.ReplaceAll(s, `\134`, `\`)
	return s
}

func fsUUIDFromSuper(opts string) string {
	for _, part := range strings.Split(opts, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "uuid=") {
			return strings.TrimPrefix(part, "uuid=")
		}
	}
	return ""
}

// CoveringMount returns the longest mountpoint prefix for cleaned.
func CoveringMount(cleaned string, mounts []Mount) (Mount, bool) {
	var best Mount
	bestLen := -1
	for _, m := range mounts {
		mp := m.MountPoint
		if mp == "" {
			continue
		}
		if cleaned == mp || strings.HasPrefix(cleaned, mp+"/") || mp == "/" {
			if len(mp) > bestLen {
				best = m
				bestLen = len(mp)
			}
		}
	}
	if bestLen < 0 {
		return Mount{}, false
	}
	return best, true
}

// SharedFS reports network/shared filesystem types that do not add clustered semantics.
func SharedFS(fstype string) bool {
	return sharedFSTypes[strings.ToLower(fstype)]
}

// SameBacking reports whether observed backing still matches the recorded identity.
func SameBacking(expected, observed BackingIdentity) bool {
	if expected.FSUUID != "" && observed.FSUUID != "" {
		return expected.FSUUID == observed.FSUUID
	}
	if expected.Dev != 0 && observed.Dev != 0 {
		return expected.Dev == observed.Dev
	}
	if expected.Device != "" && observed.Device != "" && expected.MountPoint != "" {
		return expected.Device == observed.Device && expected.MountPoint == observed.MountPoint
	}
	return false
}

func backingFromMount(m Mount, dev uint64, uuidFallback string, rootDev uint64, rootUUID string) BackingIdentity {
	uuid := m.FSUUID
	if uuid == "" {
		uuid = uuidFallback
	}
	rootBacked := m.MountPoint == "/"
	if rootDev != 0 && dev == rootDev {
		rootBacked = true
	}
	if rootUUID != "" && uuid != "" && uuid == rootUUID {
		rootBacked = true
	}
	return BackingIdentity{
		FSUUID:     uuid,
		FSType:     m.FSType,
		MountPoint: m.MountPoint,
		Device:     m.Source,
		Dev:        dev,
		RootBacked: rootBacked,
		Shared:     SharedFS(m.FSType),
	}
}
