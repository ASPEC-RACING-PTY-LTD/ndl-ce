package debian

import (
	"strings"
	"testing"
)

func TestZFSRuntimeArgvIsTyped(t *testing.T) {
	argv := ZFSRuntimeInstallArgv(true)
	joined := strings.Join(argv, " ")
	if argv[0] != "/usr/bin/apt-get" || strings.Contains(joined, "bash") || strings.Contains(joined, "zfs-dkms") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "--dry-run") || !strings.Contains(joined, "zfsutils-linux") {
		t.Fatal(joined)
	}
}
