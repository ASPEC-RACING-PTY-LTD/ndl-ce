//go:build linux

package qemu

import (
	"os"
	"os/exec"
	"testing"
)

func TestCreateTAPDevicePersistsAfterClose(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a TAP")
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		t.Skip(err)
	}
	name := "nvcerttap0"
	if err := createTAPDevice(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "delete", "dev", name).Run()
	})
	if _, err := os.Stat("/sys/class/net/" + name); err != nil {
		t.Fatalf("TAP %s vanished after createTAPDevice returned: %v", name, err)
	}
}
