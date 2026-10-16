package backuphost

import (
	"path"
	"strings"
)

// labelRules map well-known guest paths to a short, plain label. The first
// rule whose path is the item or one of its parents wins.
var labelRules = []struct {
	prefix string
	label  string
}{
	{"/var/lib/docker/volumes", "Docker data"},
	{"/var/lib/docker/overlay2", "Docker images"},
	{"/var/lib/docker/image", "Docker images"},
	{"/var/lib/docker/containers", "Docker logs"},
	{"/var/lib/docker/buildkit", "Docker cache"},
	{"/var/lib/docker", "Docker"},
	{"/var/lib/containerd", "Docker images"},
	{"/var/lib/postgresql", "Database"},
	{"/var/lib/mysql", "Database"},
	{"/var/lib/mariadb", "Database"},
	{"/var/lib/mongodb", "Database"},
	{"/var/lib/redis", "Database"},
	{"/var/lib/apt", "Packages"},
	{"/var/cache", "Cache"},
	{"/var/log", "Logs"},
	{"/var/tmp", "Temporary"},
	{"/var/mail", "Mail"},
	{"/var/spool", "Queues"},
	{"/var/www", "Website"},
	{"/var/lib", "App data"},
	{"/var/backups", "Old backups"},
	{"/tmp", "Temporary"},
	{"/etc", "Settings"},
	{"/home", "User files"},
	{"/root", "Admin files"},
	{"/srv", "Served files"},
	{"/opt", "Apps"},
	{"/usr/local", "Installed apps"},
	{"/usr", "System"},
	{"/bin", "System"},
	{"/sbin", "System"},
	{"/lib", "System"},
	{"/lib64", "System"},
	{"/lib32", "System"},
	{"/boot", "System"},
	{"/snap", "Snaps"},
	{"/proc", "Kernel"},
	{"/sys", "Kernel"},
	{"/dev", "Devices"},
	{"/run", "Runtime"},
	{"/mnt", "Mounts"},
	{"/media", "Mounts"},
	{"/.ndl-backup", "Backup rules"},
}

// nameLabels match a file or folder by its own name anywhere.
var nameLabels = map[string]string{
	"node_modules": "Packages", ".git": "Git", ".cache": "Cache", "__pycache__": "Cache",
	"vendor": "Packages", ".venv": "Packages", "venv": "Packages", "target": "Build output",
	"dist": "Build output", "build": "Build output", ".next": "Build output", "logs": "Logs",
	"log": "Logs", "cache": "Cache", "tmp": "Temporary", "backups": "Old backups",
	"docker-compose.yml": "Docker setup", "docker-compose.yaml": "Docker setup",
	"compose.yml": "Docker setup", "compose.yaml": "Docker setup", "Dockerfile": "Docker setup",
	".env": "Settings",
}

var extLabels = map[string]string{
	".log": "Log", ".gz": "Archive", ".zip": "Archive", ".tar": "Archive", ".zst": "Archive", ".xz": "Archive",
	".tgz": "Archive", ".7z": "Archive", ".sql": "Database dump", ".dump": "Database dump", ".db": "Database",
	".sqlite": "Database", ".sqlite3": "Database", ".iso": "Disk image", ".img": "Disk image", ".qcow2": "Disk image",
	".mp4": "Video", ".mkv": "Video", ".mov": "Video", ".jpg": "Image", ".jpeg": "Image", ".png": "Image",
	".pdf": "Document", ".conf": "Settings", ".yml": "Settings", ".yaml": "Settings", ".json": "Data",
}

// Label names what an item in a workload is, in a word or two. A Docker
// volume is named after its app: "Docker: nextcloud".
func Label(guest string, dir bool) string {
	name := path.Base(guest)
	if strings.HasPrefix(guest, "/var/lib/docker/volumes/") {
		rest := strings.TrimPrefix(guest, "/var/lib/docker/volumes/")
		vol := strings.SplitN(rest, "/", 2)[0]
		if vol != "" && vol != "metadata.db" && vol != "backingFsBlockDev" {
			app := strings.SplitN(strings.TrimSuffix(vol, "_data"), "_", 2)[0]
			if len(vol) == 64 {
				app = "unnamed volume"
			}
			return "Docker: " + app
		}
	}
	if l, ok := nameLabels[name]; ok {
		return l
	}
	for _, r := range labelRules {
		if guest == r.prefix || strings.HasPrefix(guest, r.prefix+"/") {
			if guest == r.prefix || dir {
				return r.label
			}
			break
		}
	}
	if !dir {
		if l, ok := extLabels[strings.ToLower(path.Ext(name))]; ok {
			return l
		}
		return "File"
	}
	for _, r := range labelRules {
		if strings.HasPrefix(guest, r.prefix+"/") {
			return r.label
		}
	}
	return "Folder"
}
