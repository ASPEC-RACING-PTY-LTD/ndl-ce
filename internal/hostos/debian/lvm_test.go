package debian

import (
	"strings"
	"testing"
)

func TestLVMRuntimeArgvIsTyped(t *testing.T) {
	argv := LVMRuntimeInstallArgv(true)
	joined := strings.Join(argv, " ")
	if argv[0] != "/usr/bin/apt-get" || strings.Contains(joined, "bash") || strings.Contains(joined, "vgexport") {
		t.Fatal(joined)
	}
	if !strings.Contains(joined, "--dry-run") || !strings.Contains(joined, "lvm2") {
		t.Fatal(joined)
	}
}
