package debian

import (
	"strings"
	"testing"
)

func TestGPURuntimeArgvIsTyped(t *testing.T) {
	argv := GPURuntimeInstallArgv(true)
	if argv[0] != "/usr/bin/apt-get" || strings.Contains(strings.Join(argv, " "), "bash") {
		t.Fatalf("%v", argv)
	}
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "NVIDIA_VISIBLE_DEVICES") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "--dry-run") {
		t.Fatal("dry-run required for Cloud-safe default")
	}
	restore := GPUDriverctlRestoreArgv("0000:02:00.0")
	if restore[0] != "/usr/sbin/driverctl" || restore[1] != "unset-override" {
		t.Fatalf("%v", restore)
	}
}
