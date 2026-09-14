package storage

import (
	"fmt"
	"net"
	"path"
	"regexp"
	"strings"
)

const (
	BackendNFS     = "nfs"
	BackendSMB     = "smb"
	BackendISCSI   = "iscsi"
	NFSMountRoot   = "/var/lib/ndl/storage/nfs"
	SMBMountRoot   = "/var/lib/ndl/storage/smb"
	ISCSIByPath    = "/dev/disk/by-path/"
	MountBin       = "/usr/bin/mount"
	ISCSIAdmBin    = "/usr/sbin/iscsiadm"
	NFSMissing     = "NFS is not mounted. Desired rows remain."
	SMBMissing     = "SMB share is not mounted. Desired rows remain."
	ISCSIMissing   = "iSCSI session is not logged in. Desired rows remain."
	DatastoreUnsup = "Network datastore runtime uses the Debian 13 adapter. This host is not Debian 13 amd64."
	CredDir        = "/var/lib/ndl/secrets/datastore"
	NFSShareMsg    = "This pool is a network share. It does not provide clustered or distributed storage semantics. If the share is down, volumes stay unavailable and are not deleted."
	ISCSIBlockMsg  = "This iSCSI LUN is a block handle. It is not a clustered filesystem."
)

var (
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,253}$`)
	iqnRe   = regexp.MustCompile(`^iqn\.[0-9]{4}-[0-9]{2}\.[a-z0-9.-]+(:[A-Za-z0-9._:-]+)?$`)
	shareRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._$-]{0,80}$`)
)

// NFSCapabilities is Directory-like on a mounted share. Incremental send is false.
func NFSCapabilities() Capabilities {
	c := DirectoryCapabilities(true, true)
	c.SharedWarning = true
	return c
}

// SMBCapabilities is Directory-like on a mounted CIFS share.
func SMBCapabilities() Capabilities {
	return NFSCapabilities()
}

// ISCSICapabilities is a raw LUN. Snapshots and incremental send are false.
func ISCSICapabilities() Capabilities {
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

func capForKind(kind string) Capabilities {
	switch kind {
	case BackendSMB:
		return SMBCapabilities()
	case BackendISCSI:
		return ISCSICapabilities()
	default:
		return NFSCapabilities()
	}
}

// ParseNFSLocator accepts server:/export. It is a locator, not identity.
func ParseNFSLocator(loc string) (server, export string, err error) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return "", "", fmt.Errorf("nfs locator is required")
	}
	if strings.Contains(loc, "..") || strings.ContainsAny(loc, " \n\r\t") {
		return "", "", fmt.Errorf("nfs locator is invalid")
	}
	server, export, ok := strings.Cut(loc, ":")
	if !ok || server == "" || export == "" {
		return "", "", fmt.Errorf("nfs locator must be server:/export")
	}
	if err := parseHost(server); err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(export, "/") {
		return "", "", fmt.Errorf("nfs export must be an absolute path")
	}
	cleaned := path.Clean(export)
	if cleaned != export {
		return "", "", fmt.Errorf("nfs export is not a clean locator")
	}
	return server, export, nil
}

// ParseSMBLocator accepts //server/share.
func ParseSMBLocator(loc string) (server, share string, err error) {
	loc = strings.TrimSpace(loc)
	loc = strings.ReplaceAll(loc, `\`, "/")
	if strings.Contains(loc, "..") || strings.ContainsAny(loc, " \n\r\t") {
		return "", "", fmt.Errorf("smb locator is invalid")
	}
	loc = strings.TrimPrefix(loc, "//")
	parts := strings.Split(loc, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("smb locator must be //server/share")
	}
	if err := parseHost(parts[0]); err != nil {
		return "", "", err
	}
	if !shareRe.MatchString(parts[1]) {
		return "", "", fmt.Errorf("smb share name is invalid")
	}
	return parts[0], parts[1], nil
}

// ParseIQN validates an iSCSI target name. It is a locator, not a UUID.
func ParseIQN(iqn string) (string, error) {
	iqn = strings.TrimSpace(iqn)
	if iqn == "" {
		return "", fmt.Errorf("iqn is required")
	}
	if strings.ContainsAny(iqn, " \n\r\t") || strings.Contains(iqn, "..") {
		return "", fmt.Errorf("iqn is invalid")
	}
	if !iqnRe.MatchString(iqn) {
		return "", fmt.Errorf("iqn is invalid")
	}
	return iqn, nil
}

// ParsePortal validates host:port for iSCSI.
func ParsePortal(portal string) (string, error) {
	portal = strings.TrimSpace(portal)
	if portal == "" {
		return "", fmt.Errorf("iscsi portal is required")
	}
	host, port, err := net.SplitHostPort(portal)
	if err != nil {
		if err := parseHost(portal); err != nil {
			return "", fmt.Errorf("iscsi portal is invalid")
		}
		return portal + ":3260", nil
	}
	if err := parseHost(host); err != nil {
		return "", err
	}
	if port != "3260" && port != "3261" {
		return "", fmt.Errorf("iscsi portal port is invalid")
	}
	return net.JoinHostPort(host, port), nil
}

func parseHost(h string) error {
	h = strings.TrimSpace(h)
	if h == "" || strings.Contains(h, "..") {
		return fmt.Errorf("host is invalid")
	}
	if ip := net.ParseIP(h); ip != nil {
		return nil
	}
	if !hostRe.MatchString(h) {
		return fmt.Errorf("host is invalid")
	}
	return nil
}

// DatastoreMountPath is the local mount locator for NFS/SMB. Pool UUID is identity.
func DatastoreMountPath(kind, poolID string) (string, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || strings.Contains(poolID, "/") || strings.Contains(poolID, "..") {
		return "", fmt.Errorf("pool_id must be a UUID")
	}
	switch kind {
	case BackendNFS:
		return NFSMountRoot + "/" + poolID, nil
	case BackendSMB:
		return SMBMountRoot + "/" + poolID, nil
	default:
		return "", fmt.Errorf("kind does not mount a filesystem")
	}
}

// CredPath is a 0600 credentials file. It is not a systemd unit.
func CredPath(poolID string) (string, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || strings.Contains(poolID, "/") || strings.Contains(poolID, "..") {
		return "", fmt.Errorf("pool_id must be a UUID")
	}
	return CredDir + "/" + poolID + ".cred", nil
}

// NFSMountArgv mounts an NFS export under the NFS storage root.
func NFSMountArgv(server, export, mount string) ([]string, error) {
	if err := parseHost(server); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(export, "/") || strings.Contains(export, "..") {
		return nil, fmt.Errorf("nfs export is invalid")
	}
	cleaned := path.Clean(mount)
	if cleaned != mount || !strings.HasPrefix(mount, NFSMountRoot+"/") {
		return nil, fmt.Errorf("nfs mount must be under the NFS storage root")
	}
	src := server + ":" + export
	return []string{MountBin, "-t", "nfs", "-o", "vers=4,soft,timeo=30,retrans=2,nconnect=1", src, mount}, nil
}

// SMBMountArgv mounts a CIFS share using a credentials file. Password is never on argv.
func SMBMountArgv(server, share, credFile, mount string) ([]string, error) {
	if err := parseHost(server); err != nil {
		return nil, err
	}
	if !shareRe.MatchString(share) {
		return nil, fmt.Errorf("smb share name is invalid")
	}
	cleaned := path.Clean(mount)
	if cleaned != mount || !strings.HasPrefix(mount, SMBMountRoot+"/") {
		return nil, fmt.Errorf("smb mount must be under the SMB storage root")
	}
	credClean := path.Clean(credFile)
	if credClean != credFile || !strings.HasPrefix(credFile, CredDir+"/") {
		return nil, fmt.Errorf("smb credentials file is invalid")
	}
	src := "//" + server + "/" + share
	return []string{MountBin, "-t", "cifs", "-o", "credentials=" + credFile + ",vers=3.0,noserverino", src, mount}, nil
}

// ISCSILoginArgv logs into a target. Password is never on argv.
func ISCSILoginArgv(iqn, portal string) ([]string, error) {
	iqn, err := ParseIQN(iqn)
	if err != nil {
		return nil, err
	}
	portal, err = ParsePortal(portal)
	if err != nil {
		return nil, err
	}
	return []string{ISCSIAdmBin, "-m", "node", "-T", iqn, "-p", portal, "--login"}, nil
}

// ISCSIDiscoveryArgv discovers sendtargets on a portal.
func ISCSIDiscoveryArgv(portal string) ([]string, error) {
	portal, err := ParsePortal(portal)
	if err != nil {
		return nil, err
	}
	return []string{ISCSIAdmBin, "-m", "discovery", "-t", "sendtargets", "-p", portal}, nil
}

// ISCSIDevicePath is a by-path locator for a logged-in LUN.
func ISCSIDevicePath(portal, iqn string) (string, error) {
	portal, err := ParsePortal(portal)
	if err != nil {
		return "", err
	}
	iqn, err = ParseIQN(iqn)
	if err != nil {
		return "", err
	}
	host, port, _ := net.SplitHostPort(portal)
	return fmt.Sprintf("%sip-%s:%s-iscsi-%s-lun-0", ISCSIByPath, host, port, iqn), nil
}

func refuseDatastoreArgv(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("datastore argv is not typed")
	}
	if argv[0] != MountBin && argv[0] != ISCSIAdmBin {
		return fmt.Errorf("datastore argv is not typed")
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "bash") || strings.Contains(joined, "/bin/sh") {
		return fmt.Errorf("shell is not a typed datastore action")
	}
	if strings.Contains(joined, "password=") || strings.Contains(joined, "Password=") {
		return fmt.Errorf("password must not appear on mount argv")
	}
	return nil
}
