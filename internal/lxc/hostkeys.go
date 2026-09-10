package lxc

import (
	"os"
	"strconv"
	"strings"
)

const (
	hostKeysMaxKeys  = 200000
	hostKeysMaxBytes = 2000000
)

// EnsureHostKeyringQuota raises the host keyring ceiling so unprivileged
// containers that share the default uid map can create nested session
// keyrings (Docker/runc). Guests cannot write these sysctls.
func EnsureHostKeyringQuota() {
	raiseSysctl("/proc/sys/kernel/keys/maxkeys", hostKeysMaxKeys)
	raiseSysctl("/proc/sys/kernel/keys/maxbytes", hostKeysMaxBytes)
}

func raiseSysctl(path string, min int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	cur, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || cur >= min {
		return
	}
	_ = os.WriteFile(path, []byte(strconv.Itoa(min)+"\n"), 0o644)
}
