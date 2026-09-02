//go:build linux

package qemu

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func createTAPDevice(name string) error {
	if name == "" || len(name) > 15 || strings.ContainsAny(name, " \n./") {
		return fmt.Errorf("tap name is invalid")
	}
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open tun: %w", err)
	}
	defer unix.Close(fd)
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		return err
	}
	ifr.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI)
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		return fmt.Errorf("TUNSETIFF %s: %w", name, err)
	}
	u, err := user.Lookup(QEMUUser)
	if err != nil {
		return nil
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	if err := unix.IoctlSetInt(fd, unix.TUNSETOWNER, uid); err != nil {
		return fmt.Errorf("TUNSETOWNER: %w", err)
	}
	return nil
}
