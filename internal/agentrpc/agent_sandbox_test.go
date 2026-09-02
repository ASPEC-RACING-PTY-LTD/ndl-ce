package agentrpc

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Least-privilege bounding set for typed lxc-attach of unprivileged CTs on Debian 13.
// CAP_SETFCAP: kernel >= 5.12 rejects uid_map writes that map host uid 0 without it.
// LXC userns_exec_minimal maps the agent's uid 0 into a helper user namespace
// before moving the attach process into the container cgroup.
// CAP_SYS_PTRACE: setns into another process requires PTRACE_MODE_ATTACH_REALCREDS.
const requiredAgentCapabilityBoundingSet = "CAP_NET_ADMIN CAP_CHOWN CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SETFCAP CAP_SYS_ADMIN CAP_SYS_PTRACE"

func agentServiceUnit(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "systemd", "ndl-agent.service")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAgentUnitKeepsLeastPrivilegeAttachSandbox(t *testing.T) {
	unit := agentServiceUnit(t)

	if !strings.Contains(unit, "User=root") {
		t.Fatal("agent must remain a privileged node agent")
	}
	if !regexp.MustCompile(`(?m)^NoNewPrivileges=yes$`).MatchString(unit) {
		t.Fatal("NoNewPrivileges=yes must remain; disabling it alone does not fix lxc-attach")
	}
	if regexp.MustCompile(`(?m)^NoNewPrivileges=no$`).MatchString(unit) {
		t.Fatal("NoNewPrivileges must not be disabled")
	}
	if !regexp.MustCompile(`(?m)^DevicePolicy=closed$`).MatchString(unit) {
		t.Fatal("DevicePolicy=closed must remain; uid_map and cgroup.procs are not device nodes")
	}
	if strings.Contains(unit, "DevicePolicy=auto") {
		t.Fatal("DevicePolicy=auto is not required for typed lxc-attach")
	}
	want := regexp.MustCompile(`(?m)^CapabilityBoundingSet=` + regexp.QuoteMeta(requiredAgentCapabilityBoundingSet) + `$`)
	if !want.MatchString(unit) {
		t.Fatalf("CapabilityBoundingSet must be the typed attach set %q", requiredAgentCapabilityBoundingSet)
	}
	if strings.Contains(unit, "CapabilityBoundingSet=~") {
		t.Fatal("must not ship an unrestricted CapabilityBoundingSet")
	}
	for _, allow := range []string{
		"DeviceAllow=char-pts rw",
		"DeviceAllow=/dev/ptmx rw",
		"DeviceAllow=/dev/pts rw",
	} {
		if !strings.Contains(unit, allow) {
			t.Fatalf("missing %s", allow)
		}
	}
	if strings.Contains(unit, "Host.Exec") || strings.Contains(unit, "/bin/bash -c") {
		t.Fatal("unit must not introduce generic host execution")
	}
}

func TestDebianRulesInstallsAgentUnitFromSystemdTree(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	rules, err := os.ReadFile(filepath.Join(root, "packaging", "debian", "rules"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(rules)
	if !strings.Contains(text, "systemd/ndl-agent.service") {
		t.Fatal("debian/rules must install systemd/ndl-agent.service")
	}
	install, err := os.ReadFile(filepath.Join(root, "packaging", "debian", "ndl-agent.install"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(install), "lib/systemd/system/ndl-agent.service") {
		t.Fatal("ndl-agent.install must ship ndl-agent.service")
	}
}

func TestAgentUnitDocumentsSETFCAPAndPTRACE(t *testing.T) {
	unit := agentServiceUnit(t)
	if !strings.Contains(unit, "CAP_SETFCAP") || !strings.Contains(unit, "uid mapping") {
		t.Fatal("unit must document CAP_SETFCAP for lxc-attach uid_map")
	}
	if !strings.Contains(unit, "CAP_SYS_PTRACE") || !strings.Contains(unit, "setns") {
		t.Fatal("unit must document CAP_SYS_PTRACE for setns")
	}
}
