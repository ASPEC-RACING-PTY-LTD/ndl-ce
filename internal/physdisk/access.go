package physdisk

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

// GrantRuntimeAccess gives ndl-qemu read-write on the device node without
// changing disk contents, partitioning, or udev ownership permanently.
func GrantRuntimeAccess(devicePath string) error {
	if err := validateDeviceNode(devicePath); err != nil {
		return err
	}
	if _, err := os.Stat(devicePath); err != nil {
		return missingError{ID: devicePath}
	}
	if setfacl, err := exec.LookPath("setfacl"); err == nil {
		cmd := exec.Command(setfacl, "-m", "u:"+qemuUser+":rw", "--", devicePath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("could not grant %s access to %s: %s", qemuUser, devicePath, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if accessibleByQEMU(devicePath) {
		return nil
	}
	return fmt.Errorf("Physical disk %s is not accessible to %s. Grant read-write to that user or add it to the disk group.", devicePath, qemuUser)
}

// RevokeRuntimeAccess removes a previously granted ACL. It never wipes the disk.
func RevokeRuntimeAccess(devicePath string) error {
	if err := validateDeviceNode(devicePath); err != nil {
		return err
	}
	if _, err := os.Stat(devicePath); err != nil {
		return nil
	}
	setfacl, err := exec.LookPath("setfacl")
	if err != nil {
		return nil
	}
	_ = exec.Command(setfacl, "-x", "u:"+qemuUser, "--", devicePath).Run()
	return nil
}

func validateDeviceNode(p string) error {
	if !strings.HasPrefix(p, ByIDDir+"/") && !strings.HasPrefix(p, "/dev/") {
		return fmt.Errorf("physical disk path is invalid")
	}
	if strings.Contains(p, "..") || strings.ContainsAny(p, " \n\r,=") {
		return fmt.Errorf("physical disk path is invalid")
	}
	return nil
}

func accessibleByQEMU(p string) bool {
	u, err := user.Lookup(qemuUser)
	if err != nil {
		return false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return false
	}
	st, err := os.Stat(p)
	if err != nil {
		return false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	mode := st.Mode()
	if sys.Uid == uint32(uid) && mode&0o600 == 0o600 {
		return true
	}
	if mode&0o006 == 0o006 {
		return true
	}
	gid, err := strconv.Atoi(u.Gid)
	if err == nil && sys.Gid == uint32(gid) && mode&0o060 == 0o060 {
		return true
	}
	groups, _ := u.GroupIds()
	for _, g := range groups {
		if id, err := strconv.Atoi(g); err == nil && sys.Gid == uint32(id) && mode&0o060 == 0o060 {
			return true
		}
	}
	return false
}
