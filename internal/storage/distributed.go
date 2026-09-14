package storage

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	BackendDistributed = "distributed"
	FormatRBD          = "rbd"
	FormatNBD          = "nbd"
	RBDDevPrefix       = "/dev/rbd/"
	NBDDevPrefix       = "/dev/nbd"
	RBDBin             = "/usr/bin/rbd"
	CephVolumeBin      = "/usr/sbin/ceph-volume"
	DistributedSecret  = "/var/lib/ndl/secrets/distributed"
	DistributedUnsup   = "Distributed storage runtime uses the Debian 13 adapter. This host is not Debian 13 amd64."
	DistributedMissing = "Ceph client tools are not installed. Directory storage remains first-class."
	ClusterDownMsg     = "The distributed cluster is down. Volumes stay unavailable and are not deleted."
	OSDRootRefuse      = "Ceph OSDs must be created on extra disks, not the host root disk"
	OSDNotStarted      = "OSD bring-up is a typed action. Enabling distributed storage does not start ceph-osd."
	OSDStopVMMsg       = "ceph-osd stop was requested. Virtual machines and system containers were not stopped."
	StartOSDConfirm    = "start-ceph-osd"
	DefaultCephUser    = "admin"
	DefaultMonPort     = "6789"
)

var (
	cephPoolRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)
	cephUserRe  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)
	cephKeyRe   = regexp.MustCompile(`^[A-Za-z0-9+/=._-]{16,256}$`)
	nbdDevRe    = regexp.MustCompile(`^/dev/nbd[0-9]{1,2}$`)
	cephImageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,126}$`)
)

// DistributedCapabilities is honest RBD: create and clustered block, no incremental send.
func DistributedCapabilities() Capabilities {
	return Capabilities{
		VolumeCreate:    true,
		SparseFiles:     false,
		Snapshots:       false,
		IncrementalSend: false,
		XattrIdentity:   false,
		SharedWarning:   true,
		SupportedClasses: []string{
			ClassVMDisk,
		},
	}
}

// ParseDistributedLocator accepts mon1[:port][,mon2[:port]]/pool.
// The pool name is a locator, never a UUID.
func ParseDistributedLocator(loc string) (mons []string, pool string, err error) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return nil, "", fmt.Errorf("distributed locator is required")
	}
	if strings.Contains(loc, "..") || strings.ContainsAny(loc, " \n\r\t") {
		return nil, "", fmt.Errorf("distributed locator is invalid")
	}
	monPart, pool, ok := strings.Cut(loc, "/")
	if !ok || monPart == "" || pool == "" {
		return nil, "", fmt.Errorf("distributed locator must be mon[:port][,mon2[:port]]/pool")
	}
	if strings.Contains(pool, "/") {
		return nil, "", fmt.Errorf("distributed locator must be mon[:port][,mon2[:port]]/pool")
	}
	pool, err = ParseCephPool(pool)
	if err != nil {
		return nil, "", err
	}
	for _, part := range strings.Split(monPart, ",") {
		mon, err := parseMonitor(part)
		if err != nil {
			return nil, "", err
		}
		mons = append(mons, mon)
	}
	if len(mons) == 0 {
		return nil, "", fmt.Errorf("at least one monitor is required")
	}
	return mons, pool, nil
}

func parseMonitor(part string) (string, error) {
	part = strings.TrimSpace(part)
	if part == "" {
		return "", fmt.Errorf("monitor locator is invalid")
	}
	host, port, err := splitHostPortDefault(part, DefaultMonPort)
	if err != nil {
		return "", err
	}
	if err := parseHost(host); err != nil {
		return "", fmt.Errorf("monitor host is invalid")
	}
	return host + ":" + port, nil
}

func splitHostPortDefault(in, defPort string) (host, port string, err error) {
	in = strings.TrimSpace(in)
	if strings.Count(in, ":") > 1 {
		return "", "", fmt.Errorf("monitor locator is invalid")
	}
	if !strings.Contains(in, ":") {
		return in, defPort, nil
	}
	host, port, ok := strings.Cut(in, ":")
	if !ok || host == "" || port == "" {
		return "", "", fmt.Errorf("monitor locator is invalid")
	}
	n, convErr := strconv.Atoi(port)
	if convErr != nil || n < 1 || n > 65535 {
		return "", "", fmt.Errorf("monitor port is invalid")
	}
	return host, port, nil
}

// ParseCephPool validates a Ceph pool name. It is a locator, not identity.
func ParseCephPool(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("ceph pool is required")
	}
	if !cephPoolRe.MatchString(name) {
		return "", fmt.Errorf("ceph pool is invalid")
	}
	return name, nil
}

// ParseCephUser validates a cephx client name without the client. prefix.
func ParseCephUser(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "client.")
	if name == "" {
		name = DefaultCephUser
	}
	if !cephUserRe.MatchString(name) {
		return "", fmt.Errorf("ceph user is invalid")
	}
	return name, nil
}

// ParseCephxKey accepts a cephx key. It must never appear in argv or list JSON.
func ParseCephxKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("cephx key is required")
	}
	if strings.ContainsAny(key, " \n\r\t") || !cephKeyRe.MatchString(key) {
		return "", fmt.Errorf("cephx key is invalid")
	}
	return key, nil
}

// ParseCephImage validates an RBD image name. Volume UUIDs are allowed locators.
func ParseCephImage(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("rbd image is required")
	}
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", fmt.Errorf("rbd image is invalid")
	}
	if !cephImageRe.MatchString(name) {
		return "", fmt.Errorf("rbd image is invalid")
	}
	return name, nil
}

// ParseOSDDisk allows extra-disk locators only. The host root disk is refused.
func ParseOSDDisk(p, rootDev string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("disk locator is required")
	}
	if p == "/" || p == "/dev/root" {
		return "", fmt.Errorf(OSDRootRefuse)
	}
	loc, err := ParseDiskLocator(p, rootDev)
	if err != nil {
		if strings.Contains(err.Error(), "host root") || strings.Contains(err.Error(), "ZFS") {
			return "", fmt.Errorf(OSDRootRefuse)
		}
		return "", err
	}
	return loc, nil
}

// KeyringPath is the host secret file for one attached cluster. Pool UUID is identity.
func KeyringPath(poolID string) (string, error) {
	id := strings.TrimSpace(poolID)
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, " \n\r\t/") {
		return "", fmt.Errorf("pool id is invalid")
	}
	return path.Join(DistributedSecret, id+".keyring"), nil
}

// ValidateRBDPath allows /dev/rbd/<pool>/<image> mapped handles only.
func ValidateRBDPath(p string) error {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, RBDDevPrefix) {
		return fmt.Errorf("rbd locator is invalid")
	}
	cleaned := path.Clean(p)
	if cleaned != p || strings.Contains(p, "..") || strings.ContainsAny(p, " \n\r\t,=") {
		return fmt.Errorf("rbd locator is invalid")
	}
	rest := strings.TrimPrefix(cleaned, RBDDevPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("rbd locator is invalid")
	}
	if _, err := ParseCephPool(parts[0]); err != nil {
		return fmt.Errorf("rbd locator is invalid")
	}
	if _, err := ParseCephImage(parts[1]); err != nil {
		return fmt.Errorf("rbd locator is invalid")
	}
	return nil
}

// ValidateNBDPath allows /dev/nbdN mapped handles. It is a documented RBD equivalent.
func ValidateNBDPath(p string) error {
	p = strings.TrimSpace(p)
	if !nbdDevRe.MatchString(p) {
		return fmt.Errorf("nbd locator is invalid")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(p, NBDDevPrefix))
	if err != nil || n < 0 || n > 15 {
		return fmt.Errorf("nbd locator is invalid")
	}
	return nil
}

// RBDDevicePath is the mapped host locator. Volume UUID remains desired identity.
func RBDDevicePath(pool, image string) (string, error) {
	pool, err := ParseCephPool(pool)
	if err != nil {
		return "", err
	}
	image, err = ParseCephImage(image)
	if err != nil {
		return "", err
	}
	return RBDDevPrefix + pool + "/" + image, nil
}

// MonitorArg is the rbd -m value. Keys never appear here.
func MonitorArg(mons []string) (string, error) {
	if len(mons) == 0 {
		return "", fmt.Errorf("at least one monitor is required")
	}
	for _, m := range mons {
		if _, err := parseMonitor(m); err != nil {
			return "", err
		}
	}
	return strings.Join(mons, ","), nil
}

func rbdBaseArgv(user, keyring, monitors string) ([]string, error) {
	user, err := ParseCephUser(user)
	if err != nil {
		return nil, err
	}
	if keyring == "" || !strings.HasPrefix(keyring, DistributedSecret+"/") || strings.Contains(keyring, "..") {
		return nil, fmt.Errorf("keyring locator is invalid")
	}
	if path.Clean(keyring) != keyring {
		return nil, fmt.Errorf("keyring locator is invalid")
	}
	mon, err := MonitorArg(strings.Split(monitors, ","))
	if err != nil {
		return nil, err
	}
	return []string{RBDBin, "-m", mon, "--id", user, "--keyring", keyring}, nil
}

// RBDListArgv observes images in a pool. The cephx key is never an argument.
func RBDListArgv(user, keyring, monitors, pool string) ([]string, error) {
	pool, err := ParseCephPool(pool)
	if err != nil {
		return nil, err
	}
	base, err := rbdBaseArgv(user, keyring, monitors)
	if err != nil {
		return nil, err
	}
	return append(base, "ls", pool), nil
}

// RBDCreateArgv creates one image. Size is megabytes, never a shell expression.
func RBDCreateArgv(user, keyring, monitors, pool, image string, sizeBytes int64) ([]string, error) {
	if sizeBytes < MinBlockBytes {
		return nil, fmt.Errorf("volume size is too small")
	}
	pool, err := ParseCephPool(pool)
	if err != nil {
		return nil, err
	}
	image, err = ParseCephImage(image)
	if err != nil {
		return nil, err
	}
	base, err := rbdBaseArgv(user, keyring, monitors)
	if err != nil {
		return nil, err
	}
	mb := sizeBytes / (1 << 20)
	if mb < 1 {
		mb = 1
	}
	return append(base, "create", pool+"/"+image, "--size", strconv.FormatInt(mb, 10)), nil
}

// RBDMapArgv maps an image to /dev/rbd/<pool>/<image>.
func RBDMapArgv(user, keyring, monitors, pool, image string) ([]string, error) {
	pool, err := ParseCephPool(pool)
	if err != nil {
		return nil, err
	}
	image, err = ParseCephImage(image)
	if err != nil {
		return nil, err
	}
	base, err := rbdBaseArgv(user, keyring, monitors)
	if err != nil {
		return nil, err
	}
	return append(base, "map", pool+"/"+image), nil
}

// OSDCreateArgv runs ceph-volume on one extra disk. It is not feature-install.
func OSDCreateArgv(disk string) ([]string, error) {
	disk, err := ParseOSDDisk(disk, "")
	if err != nil {
		return nil, err
	}
	return []string{CephVolumeBin, "lvm", "create", "--data", disk}, nil
}

// WriteKeyring writes a cephx keyring file. The key is never placed in argv.
func WriteKeyring(poolID, user, key string) (string, error) {
	user, err := ParseCephUser(user)
	if err != nil {
		return "", err
	}
	key, err = ParseCephxKey(key)
	if err != nil {
		return "", err
	}
	p, err := KeyringPath(poolID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(DistributedSecret, 0o700); err != nil {
		return "", err
	}
	body := "[client." + user + "]\n\tkey = " + key + "\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// ArgvContainsSecret reports whether a cephx key leaked into argv.
func ArgvContainsSecret(argv []string, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, a := range argv {
		if a == key || strings.Contains(a, key) {
			return true
		}
	}
	return false
}

// ProcComm lists /proc/*/comm. Missing /proc is treated as no ceph-osd process.
func ProcComm() []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(path.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(b))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ObserveOSD reports whether a ceph-osd process is present. Default install has none.
func ObserveOSD(list func() []string) (running bool, names []string) {
	if list == nil {
		list = ProcComm
	}
	for _, n := range list() {
		if n == "ceph-osd" {
			running = true
			names = append(names, n)
		}
	}
	return running, names
}
