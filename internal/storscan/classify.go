package storscan

import (
	"path/filepath"
	"strings"
	"time"
)

// Importance levels, from least to most careful.
const (
	ImportanceLow       = "low"       // usually safe to delete
	ImportanceReview    = "review"    // check before deleting
	ImportanceImportant = "important" // needed by apps or No-dal; delete through its own tool
	ImportanceCritical  = "critical"  // deleting it loses data or breaks the system
)

// Confidence says how the answer was reached. Unknown items are never
// marked safe.
const (
	ConfidenceKnown   = "known"   // No-dal created or tracks this item
	ConfidenceLikely  = "likely"  // matched a well-known location or file type
	ConfidenceUnknown = "unknown" // no rule matched
)

// Insight explains what an item is for and how careful to be with it.
type Insight struct {
	Purpose    string `json:"purpose"`
	Importance string `json:"importance"`
	Confidence string `json:"confidence"`
	Advice     string `json:"advice,omitempty"`
	// Protected items are refused by Delete.
	Protected bool `json:"protected,omitempty"`
}

// SecretDirs hold keys and credentials. Their total size is shown but their
// contents are never listed.
var SecretDirs = []string{"/var/lib/ndl/secrets", "/var/lib/ndl/keys", "/var/lib/ndl/control/keys", "/root/.ssh", "/home/*/.ssh", "/etc/ssh", "/etc/ndl"}

// Secret reports whether abs is inside a folder whose contents are hidden.
func Secret(abs string) bool {
	return protectedBy(filepath.Clean(abs), SecretDirs) != "" && !isAncestorOnly(filepath.Clean(abs))
}

// isAncestorOnly is true when abs only contains a secret folder (such as /root)
// rather than being one or being inside one.
func isAncestorOnly(abs string) bool {
	for _, p := range SecretDirs {
		matches := []string{p}
		if strings.Contains(p, "*") {
			matches, _ = filepath.Glob(p)
		}
		for _, m := range matches {
			if within(abs, m) {
				return false
			}
		}
	}
	return true
}

// Known labels absolute paths No-dal tracks, such as container disks.
type Known struct {
	Path      string
	Purpose   string
	Advice    string
	Protected bool
}

type rule struct {
	match      func(abs, name string, dir bool) bool
	purpose    string
	importance string
	advice     string
}

func under(prefix string) func(abs, name string, dir bool) bool {
	return func(abs, _ string, _ bool) bool { return within(abs, prefix) }
}

func suffix(exts ...string) func(abs, name string, dir bool) bool {
	return func(_, name string, dir bool) bool {
		if dir {
			return false
		}
		n := strings.ToLower(name)
		for _, e := range exts {
			if strings.HasSuffix(n, e) {
				return true
			}
		}
		return false
	}
}

func named(names ...string) func(abs, name string, dir bool) bool {
	return func(_, name string, _ bool) bool {
		for _, n := range names {
			if name == n {
				return true
			}
		}
		return false
	}
}

var rules = []rule{
	{under("/var/lib/postgresql"), "PostgreSQL databases (No-dal's own state)", ImportanceCritical, "Never delete these files."},
	{under("/var/lib/mysql"), "MySQL or MariaDB databases", ImportanceCritical, "Remove data from inside the database instead."},
	{under("/var/lib/docker/volumes"), "Docker volumes (application data)", ImportanceCritical, "Remove unused volumes from the Docker page or with docker volume prune."},
	{under("/var/lib/docker"), "Docker images, layers and container files", ImportanceImportant, "Free space with docker system prune or the Docker page; deleting files here breaks Docker."},
	{under("/var/lib/containerd"), "Container runtime images and snapshots", ImportanceImportant, "Remove unused images through the runtime, not by deleting files."},
	{under("/var/lib/ndl/storage"), "No-dal storage pool data", ImportanceImportant, "Delete workloads, volumes or images from No-dal instead."},
	{under("/var/lib/ndl/backup-repo"), "No-dal backup repository", ImportanceImportant, "Prune backups from the Backups page; deleting files here can break restores."},
	{under("/storage/backups"), "Backups", ImportanceImportant, "Prune backups from the Backups page."},
	{under("/var/lib/ndl/control/tmp"), "No-dal staging files from backups and restores", ImportanceLow, "Old items here are leftovers from finished jobs."},
	{under("/var/lib/ndl"), "No-dal data", ImportanceImportant, "Manage it from No-dal."},
	{under("/var/log/journal"), "System journal (logs)", ImportanceLow, "Shrink it with journalctl --vacuum-size=500M rather than deleting files."},
	{func(abs, name string, dir bool) bool {
		return within(abs, "/var/log") && !dir && (strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".old") || isRotated(name))
	}, "Rotated old log file", ImportanceLow, "Old rotated logs are safe to delete."},
	{under("/var/log"), "Log files", ImportanceReview, "Current logs may still be written to; prefer rotating them."},
	{under("/var/cache/apt"), "Downloaded package files", ImportanceLow, "apt-get clean removes these safely."},
	{under("/var/cache"), "Application caches", ImportanceLow, "Caches are rebuilt when needed."},
	{under("/var/tmp"), "Temporary files", ImportanceLow, "Usually safe once nothing is using them."},
	{under("/tmp"), "Temporary files", ImportanceLow, "Usually safe once nothing is using them."},
	{named("swapfile", "swap.img"), "Swap file", ImportanceCritical, "Turn swap off before removing it."},
	{named("node_modules"), "Node.js dependencies (can be reinstalled)", ImportanceReview, "Reinstalled by npm install; the app stops working until then."},
	{named(".cache", "__pycache__", ".npm", ".yarn", ".gradle", ".m2"), "Build or tool cache", ImportanceLow, "Rebuilt on demand."},
	{named(".Trash", "Trash", ".local/share/Trash"), "Trash", ImportanceLow, "Already deleted by its owner."},
	{func(_, name string, dir bool) bool {
		return !dir && (name == "core" || strings.HasPrefix(name, "core."))
	}, "Crash dump", ImportanceLow, "Only useful for debugging a crash."},
	{suffix(".iso"), "Disk image (ISO)", ImportanceReview, "Keep it if workloads boot or install from it."},
	{suffix(".img", ".qcow2", ".raw", ".vmdk", ".vdi", ".vhd", ".vhdx"), "Virtual disk image", ImportanceImportant, "Could be a virtual machine's or container's disk; check before deleting."},
	{suffix(".tar", ".tar.gz", ".tgz", ".zip", ".7z", ".tar.zst", ".zst", ".xz", ".bz2"), "Archive", ImportanceReview, "Could be a backup or a download."},
	{suffix(".log"), "Log file", ImportanceReview, "Check it is not still being written."},
	{suffix(".sql", ".dump", ".bak"), "Database dump or backup copy", ImportanceReview, "Could be the only copy of a backup."},
	{under("/home"), "Personal files", ImportanceReview, "Belongs to a user; ask before deleting."},
	{under("/root"), "Administrator's files", ImportanceReview, "Check what these are before deleting."},
	{under("/opt"), "Installed applications", ImportanceImportant, "Uninstall the application instead."},
	{under("/srv"), "Served site or service data", ImportanceImportant, "Likely live application data."},
}

func isRotated(name string) bool {
	i := strings.LastIndexByte(name, '.')
	if i < 0 || i == len(name)-1 {
		return false
	}
	for _, r := range name[i+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Classify explains an item by its absolute path. Paths No-dal tracks win;
// then well-known locations and file types; anything else is unknown and
// must be reviewed.
func Classify(abs string, n *Node, known []Known, now time.Time) Insight {
	abs = filepath.Clean(abs)
	name := filepath.Base(abs)
	for _, k := range known {
		if abs == k.Path || within(abs, k.Path) {
			return Insight{Purpose: k.Purpose, Importance: ImportanceCritical, Confidence: ConfidenceKnown, Advice: k.Advice, Protected: k.Protected}
		}
		if within(k.Path, abs) && n != nil && n.Dir {
			return Insight{Purpose: "Contains " + k.Purpose, Importance: ImportanceCritical, Confidence: ConfidenceKnown,
				Advice: "This folder holds data No-dal tracks; open it to see which.", Protected: k.Protected}
		}
	}
	if p := protectedBy(abs, SystemProtected); p != "" && p != "/" {
		return Insight{Purpose: "Operating system files", Importance: ImportanceCritical, Confidence: ConfidenceLikely, Advice: "Protected; deleting it would break the host.", Protected: true}
	}
	dir := n != nil && n.Dir
	for _, r := range rules {
		if r.match(abs, name, dir) {
			in := Insight{Purpose: r.purpose, Importance: r.importance, Confidence: ConfidenceLikely, Advice: r.advice}
			if in.Importance == ImportanceLow && n != nil && n.MTime > 0 && now.Sub(time.Unix(n.MTime, 0)) < 24*time.Hour && (within(abs, "/tmp") || within(abs, "/var/tmp")) {
				in.Importance, in.Advice = ImportanceReview, "Changed in the last day, so something may still be using it."
			}
			return in
		}
	}
	return Insight{Purpose: "Unknown", Importance: ImportanceReview, Confidence: ConfidenceUnknown, Advice: "No rule recognises this item; find out what it is before deleting it."}
}
