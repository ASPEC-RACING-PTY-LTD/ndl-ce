package agentrpc

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Least-privilege bounding set for typed lxc-attach of unprivileged CTs on Debian 13
// and dest chmod/lchown/fchownat during Local Host Migration of UID-mapped rootfs.
// CAP_FOWNER: chmod/lchown/fchownat on files the agent does not own return EPERM
// without it, even as root. Privileged filesystem work stays in ndl-agent.
// CAP_SETFCAP: kernel >= 5.12 rejects uid_map writes that map host uid 0 without it.
// LXC userns_exec_minimal maps the agent's uid 0 into a helper user namespace
// before moving the attach process into the container cgroup.
// CAP_SYS_PTRACE: setns into another process requires PTRACE_MODE_ATTACH_REALCREDS.
const requiredAgentCapabilityBoundingSet = "CAP_NET_ADMIN CAP_CHOWN CAP_FOWNER CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SETFCAP CAP_SYS_ADMIN CAP_SYS_PTRACE"

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
		"DeviceAllow=/dev/loop-control rw",
		"DeviceAllow=block-loop rw",
		"DeviceAllow=/dev/net/tun rw",
	} {
		if !strings.Contains(unit, allow) {
			t.Fatalf("missing %s", allow)
		}
	}
	if strings.Contains(unit, "Host.Exec") || strings.Contains(unit, "/bin/bash -c") {
		t.Fatal("unit must not introduce generic host execution")
	}
}

func TestCTUnitPreparesDirectoryRootMount(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	unit, err := os.ReadFile(filepath.Join(root, "systemd", "nodal-ct@.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(unit)
	if !strings.Contains(text, "ExecStartPre=/usr/lib/ndl/ndl-ct-prepare %i") {
		t.Fatal("nodal-ct@.service must remount a sized directory root before lxc-start")
	}
	prep, err := os.ReadFile(filepath.Join(root, "cmd", "ndl-ct-prepare", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prep), "RewriteRuntimeConfig") {
		t.Fatal("ndl-ct-prepare must rewrite LXC config from last-applied before lxc-start")
	}
	if !strings.Contains(string(prep), "ApplyGuestFiles") {
		t.Fatal("ndl-ct-prepare must apply guest files after remount so nano lands without ndl-agent")
	}
	if strings.Contains(text, "BindsTo=ndl-agent") || strings.Contains(text, "Requires=ndl-agent") {
		t.Fatal("container units must not bind to ndl-agent")
	}
	install, err := os.ReadFile(filepath.Join(root, "packaging", "debian", "ndl-agent.install"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(install), "usr/lib/ndl/ndl-ct-prepare") {
		t.Fatal("ndl-agent.install must ship ndl-ct-prepare")
	}
	if !strings.Contains(string(install), "usr/lib/ndl/ndl-lxc-nesting-apparmor") {
		t.Fatal("ndl-agent.install must ship ndl-lxc-nesting-apparmor")
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
	if !strings.Contains(text, "./cmd/ndl-lxc-nesting-apparmor") {
		t.Fatal("debian/rules must build ndl-lxc-nesting-apparmor")
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

func TestAgentUnitIncludesFOWNERForMappedRootfsChmod(t *testing.T) {
	unit := agentServiceUnit(t)
	if !strings.Contains(requiredAgentCapabilityBoundingSet, "CAP_FOWNER") {
		t.Fatal("required agent bounding set must include CAP_FOWNER")
	}
	if !strings.Contains(unit, "CAP_FOWNER") {
		t.Fatal("ndl-agent.service must include CAP_FOWNER for dest chmod/lchown/fchownat")
	}
	if !strings.Contains(unit, "fchownat") && !strings.Contains(unit, "lchown") {
		t.Fatal("unit must document CAP_FOWNER for Local Host dest chmod/lchown/fchownat")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	control, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "ndl-control.service"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(control)
	if strings.Contains(text, "CAP_FOWNER") {
		t.Fatal("ndl-control must not gain CAP_FOWNER; mapped-rootfs chmod stays in ndl-agent")
	}
	if !regexp.MustCompile(`(?m)^CapabilityBoundingSet=CAP_NET_BIND_SERVICE$`).MatchString(text) {
		t.Fatal("ndl-control CapabilityBoundingSet must stay CAP_NET_BIND_SERVICE only")
	}
}
