package lxc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Device rules from LXC 6.0.4 common.conf and userns.conf (Debian 13).
const (
	lxcCommonConfDevices = `lxc.cgroup2.devices.deny = a
lxc.cgroup2.devices.allow = c *:* m
lxc.cgroup2.devices.allow = b *:* m
lxc.cgroup2.devices.allow = c 1:3 rwm
lxc.cgroup2.devices.allow = c 1:5 rwm
lxc.cgroup2.devices.allow = c 1:7 rwm
lxc.cgroup2.devices.allow = c 5:0 rwm
lxc.cgroup2.devices.allow = c 5:1 rwm
lxc.cgroup2.devices.allow = c 5:2 rwm
lxc.cgroup2.devices.allow = c 1:8 rwm
lxc.cgroup2.devices.allow = c 1:9 rwm
lxc.cgroup2.devices.allow = c 136:* rwm
lxc.cgroup2.devices.allow = c 10:229 rwm
`
	lxcUsernsConfDevices = `lxc.cgroup2.devices.deny =
lxc.cgroup2.devices.allow =
`
)

type lxcDeviceRule struct {
	allow        bool
	typ          byte
	major, minor int
	access       string
}

// lxcDevicePolicy models liblxc 6.0.x cgroup2 device handling: config lines
// apply in order, an empty value clears that key, the "a" rule resets the list
// and picks the default, rules against the default are skipped, and an empty
// list attaches no eBPF program at all.
type lxcDevicePolicy struct {
	attached     bool
	defaultAllow bool
	rules        []lxcDeviceRule
}

func evalLXCDevicePolicy(t *testing.T, cfg string) lxcDevicePolicy {
	t.Helper()
	type entry struct{ key, val string }
	var entries []entry
	for _, line := range strings.Split(cfg, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "lxc.cgroup2.devices.allow" && k != "lxc.cgroup2.devices.deny" {
			continue
		}
		if v == "" {
			kept := entries[:0]
			for _, e := range entries {
				if e.key != k {
					kept = append(kept, e)
				}
			}
			entries = kept
			continue
		}
		entries = append(entries, entry{k, v})
	}
	var p lxcDevicePolicy
	for _, e := range entries {
		r, err := parseLXCDeviceRule(strings.HasSuffix(e.key, ".allow"), e.val)
		if err != nil {
			t.Fatalf("liblxc rejects %s = %s (lxc-start fails): %v", e.key, e.val, err)
		}
		if r.typ == 'a' && r.major < 0 && r.minor < 0 && r.access == "" {
			p.defaultAllow = r.allow
			p.rules = nil
			continue
		}
		p.rules = append(p.rules, r)
	}
	p.attached = len(p.rules) > 0
	return p
}

// parseLXCDeviceRule mirrors device_cgroup_rule_parse in liblxc cgfsng.c.
func parseLXCDeviceRule(allow bool, val string) (lxcDeviceRule, error) {
	r := lxcDeviceRule{allow: allow, major: -1, minor: -1}
	if val == "a" {
		r.typ = 'a'
		return r, nil
	}
	fields := strings.Split(val, " ")
	if len(fields) != 3 || len(fields[0]) != 1 || !strings.Contains("abc", fields[0]) {
		return r, fmt.Errorf("malformed rule")
	}
	r.typ = fields[0][0]
	maj, min, ok := strings.Cut(fields[1], ":")
	if !ok {
		return r, fmt.Errorf("missing major:minor")
	}
	var err error
	if r.major, err = parseLXCDeviceNumber(maj); err != nil {
		return r, err
	}
	if r.minor, err = parseLXCDeviceNumber(min); err != nil {
		return r, err
	}
	if len(fields[2]) > 3 {
		return r, fmt.Errorf("access %q is longer than rwm", fields[2])
	}
	for _, c := range fields[2] {
		if c != 'r' && c != 'w' && c != 'm' {
			return r, fmt.Errorf("access %q is not r, w, or m", fields[2])
		}
	}
	r.access = fields[2]
	return r, nil
}

func parseLXCDeviceNumber(s string) (int, error) {
	if s == "*" {
		return -1, nil
	}
	if !digitsOnly(s) {
		return 0, fmt.Errorf("device number %q", s)
	}
	return strconv.Atoi(s)
}

func (p lxcDevicePolicy) allows(typ byte, major, minor int, access string) bool {
	if !p.attached {
		return true
	}
	for _, r := range p.rules {
		if r.allow == p.defaultAllow {
			continue
		}
		if r.typ != 'a' && r.typ != typ {
			continue
		}
		if (r.major >= 0 && r.major != major) || (r.minor >= 0 && r.minor != minor) {
			continue
		}
		granted := true
		for _, c := range access {
			if !strings.ContainsRune(r.access, c) {
				granted = false
			}
		}
		if granted {
			return r.allow
		}
	}
	return p.defaultAllow
}

// hostConfig is what writeConfig produces on a Debian 13 host that ships
// common.conf and userns.conf.
func hostConfig(spec Spec) string {
	cfg := lxcCommonConfDevices
	if !spec.Privileged {
		cfg += lxcUsernsConfDevices
	}
	return cfg + RenderConfig(spec)
}

type deviceCheck struct {
	name         string
	typ          byte
	major, minor int
	access       string
}

var standardGuestDevices = []deviceCheck{
	{"/dev/null", 'c', 1, 3, "rw"},
	{"/dev/zero", 'c', 1, 5, "rw"},
	{"/dev/full", 'c', 1, 7, "rw"},
	{"/dev/random", 'c', 1, 8, "rw"},
	{"/dev/urandom", 'c', 1, 9, "rw"},
	{"/dev/tty", 'c', 5, 0, "rw"},
	{"/dev/console", 'c', 5, 1, "rw"},
	{"/dev/ptmx", 'c', 5, 2, "rw"},
	{"/dev/pts/0", 'c', 136, 0, "rw"},
	{"/dev/pts/7", 'c', 136, 7, "rw"},
}

func assertAllowed(t *testing.T, p lxcDevicePolicy, checks ...deviceCheck) {
	t.Helper()
	for _, c := range checks {
		if !p.allows(c.typ, c.major, c.minor, c.access) {
			t.Errorf("%s (%c %d:%d %s) must be allowed", c.name, c.typ, c.major, c.minor, c.access)
		}
	}
}

func assertDenied(t *testing.T, p lxcDevicePolicy, checks ...deviceCheck) {
	t.Helper()
	for _, c := range checks {
		if p.allows(c.typ, c.major, c.minor, c.access) {
			t.Errorf("%s (%c %d:%d %s) must be denied", c.name, c.typ, c.major, c.minor, c.access)
		}
	}
}

func nvidiaSpec() Spec {
	return Spec{
		WorkloadID: "22222222-2222-4222-8222-222222222222", Name: "gpu", RootfsPath: "/vol/root",
		BridgeName: "br0",
		GPUDevices: []string{"/dev/nvidia0", "/dev/nvidiactl", "/dev/nvidia-uvm", "/dev/nvidia-uvm-tools", "/dev/nvidia-modeset"},
	}
}

var unassignedHostDevices = []deviceCheck{
	{"/dev/sda", 'b', 8, 0, "rw"},
	{"/dev/mem", 'c', 1, 1, "rw"},
	{"/dev/kmsg", 'c', 1, 11, "rw"},
	{"/dev/kvm", 'c', 10, 232, "rw"},
	{"/dev/nvidia1", 'c', 195, 1, "rw"},
	{"/dev/dri/card0", 'c', 226, 0, "rw"},
	{"mknod arbitrary char", 'c', 4, 0, "m"},
	{"mknod arbitrary block", 'b', 8, 0, "m"},
}

// The model must reproduce the reported failure, or the other tests prove nothing.
func TestDevicePolicyModelReproducesAppendOnlyGPURules(t *testing.T) {
	cfg := lxcCommonConfDevices + lxcUsernsConfDevices +
		"lxc.cgroup2.devices.allow = c 195:0 rwm\n" +
		"lxc.cgroup2.devices.allow = c 195:255 rwm\n"
	p := evalLXCDevicePolicy(t, cfg)
	assertDenied(t, p, standardGuestDevices[0], standardGuestDevices[1], standardGuestDevices[8])
	assertAllowed(t, p, deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"})

	noRules := evalLXCDevicePolicy(t, lxcCommonConfDevices+lxcUsernsConfDevices)
	if noRules.attached {
		t.Fatal("userns.conf must leave no device program when nothing is appended")
	}
}

func TestUnprivilegedNVIDIAKeepsStandardDevices(t *testing.T) {
	spec := nvidiaSpec()
	p := evalLXCDevicePolicy(t, hostConfig(spec))
	if !p.attached || p.defaultAllow {
		t.Fatal("unprivileged GPU guest must run a default-deny device program")
	}
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p,
		deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"},
		deviceCheck{"/dev/nvidiactl", 'c', 195, 255, "rw"},
		deviceCheck{"/dev/fuse (nesting)", 'c', 10, 229, "rw"},
	)
	assertDenied(t, p, unassignedHostDevices...)
	assertDenied(t, p, deviceCheck{"/dev/net/tun (not requested)", 'c', 10, 200, "rw"})
}

func TestUnprivilegedTUNKeepsStandardDevices(t *testing.T) {
	spec := Spec{WorkloadID: uuid.NewString(), Name: "vpn", RootfsPath: "/vol/root", TUN: true}
	p := evalLXCDevicePolicy(t, hostConfig(spec))
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p, deviceCheck{"/dev/net/tun", 'c', 10, 200, "rw"})
	assertDenied(t, p, unassignedHostDevices...)
}

func TestUnprivilegedWithoutDevicesGetsBaselineOnly(t *testing.T) {
	off := false
	spec := Spec{WorkloadID: uuid.NewString(), Name: "plain", RootfsPath: "/vol/root", Nesting: &off}
	p := evalLXCDevicePolicy(t, hostConfig(spec))
	assertAllowed(t, p, standardGuestDevices...)
	assertDenied(t, p, unassignedHostDevices...)
	assertDenied(t, p,
		deviceCheck{"/dev/fuse (nesting off)", 'c', 10, 229, "rw"},
		deviceCheck{"/dev/net/tun", 'c', 10, 200, "rw"},
	)
}

func TestAllowMknodRendersValidRules(t *testing.T) {
	spec := Spec{WorkloadID: uuid.NewString(), Name: "mk", RootfsPath: "/vol/root", AllowMknod: true}
	cfg := RenderConfig(spec)
	if strings.Contains(cfg, "mknod") {
		t.Fatalf("liblxc access is r, w, m only: %s", cfg)
	}
	p := evalLXCDevicePolicy(t, hostConfig(spec))
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p, deviceCheck{"mknod char", 'c', 4, 0, "m"}, deviceCheck{"mknod block", 'b', 8, 0, "m"})
	assertDenied(t, p, deviceCheck{"/dev/sda", 'b', 8, 0, "rw"}, deviceCheck{"/dev/mem", 'c', 1, 1, "r"})
}

func TestPrivilegedGPUKeepsCommonConfAllowlist(t *testing.T) {
	spec := nvidiaSpec()
	spec.Privileged = true
	cfg := RenderConfig(spec)
	if strings.Contains(cfg, "lxc.cgroup2.devices.deny") {
		t.Fatalf("privileged guests must not reset the common.conf allowlist: %s", cfg)
	}
	p := evalLXCDevicePolicy(t, hostConfig(spec))
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p, deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"})
	assertDenied(t, p, deviceCheck{"/dev/sda", 'b', 8, 0, "rw"}, deviceCheck{"/dev/nvidia1", 'c', 195, 1, "rw"})
}

func TestDevicePolicyIsSelfContainedWithoutHostIncludes(t *testing.T) {
	p := evalLXCDevicePolicy(t, RenderConfig(nvidiaSpec()))
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p, deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"})
	assertDenied(t, p, unassignedHostDevices...)
}

func TestDevicePolicyRulesAreExactAndOrdered(t *testing.T) {
	spec := nvidiaSpec()
	spec.TUN = true
	cfg := RenderConfig(spec)
	deny := strings.Index(cfg, "lxc.cgroup2.devices.deny = a\n")
	allow := strings.Index(cfg, "lxc.cgroup2.devices.allow")
	if deny < 0 || allow < 0 || deny > allow {
		t.Fatalf("deny-all must precede every allow rule: %s", cfg)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(cfg, "\n") {
		if !strings.HasPrefix(line, "lxc.cgroup2.devices.allow") {
			continue
		}
		if seen[line] {
			t.Fatalf("duplicate rule %q", line)
		}
		seen[line] = true
		for _, broad := range []string{"= a", "c *:*", "b *:*", "195:*", "226:*", " 1:* ", " 5:* ", " 10:* "} {
			if strings.Contains(line, broad) {
				t.Fatalf("broad rule %q", line)
			}
		}
	}
}

func TestRestartRegeneratesDevicePolicyForExistingGPUContainer(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	root := filepath.Join(e.DataDir, "rootfs", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	spec := nvidiaSpec()
	spec.WorkloadID = id
	spec.RootfsPath = root
	spec.ImagePin = "imported"
	spec.SkipImage = true
	spec.UIDMap = DefaultUIDMap
	spec.GIDMap = DefaultGIDMap
	// Written by an earlier release: no nesting field, GPU nodes assigned.
	if err := e.writeApplied(spec, true, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(e.configPath(id)), 0o750); err != nil {
		t.Fatal(err)
	}
	stale := "lxc.uts.name = gpu\n" +
		"lxc.mount.entry = /dev/nvidia0 dev/nvidia0 none bind,optional,create=file\n" +
		"lxc.cgroup2.devices.allow = c 195:0 rwm\n" +
		"lxc.cgroup2.devices.allow = c 1:* rwm\n" +
		"lxc.cgroup2.devices.allow = c 5:* rwm\n" +
		"lxc.cgroup2.devices.allow = c 136:* rwm\n"
	if err := os.WriteFile(e.configPath(id), []byte(stale), 0o640); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := e.Restart(ctx, id); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(e.configPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "c 1:* rwm") || strings.Contains(string(first), "c 5:* rwm") {
		t.Fatalf("restart must regenerate from last-applied, not keep hand edits: %s", first)
	}
	p := evalLXCDevicePolicy(t, lxcCommonConfDevices+lxcUsernsConfDevices+string(first))
	assertAllowed(t, p, standardGuestDevices...)
	assertAllowed(t, p, deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"}, deviceCheck{"/dev/fuse", 'c', 10, 229, "rw"})
	assertDenied(t, p, unassignedHostDevices...)

	applied, err := e.readApplied(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Spec.GPUDevices) != len(spec.GPUDevices) || !SpecWantsNesting(applied.Spec) {
		t.Fatalf("restart must keep GPU nodes and nesting: %+v", applied.Spec)
	}

	for _, step := range []func() error{
		func() error { return e.Restart(ctx, id) },
		func() error { return e.Stop(ctx, id) },
		func() error { return e.Start(ctx, id) },
		func() error { return e.RewriteRuntimeConfig(id) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		next, err := os.ReadFile(e.configPath(id))
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("config drifted across lifecycle:\n%s\n---\n%s", first, next)
		}
	}
}

func TestApplyGPUDevicesKeepsBaselineOnAssignAndUnassign(t *testing.T) {
	e := testEngine(t)
	id := uuid.NewString()
	if _, err := e.Create(context.Background(), Spec{
		WorkloadID: id, Name: "alpine", ImagePin: "alpine/3.21/amd64/default",
		VolumeID: uuid.NewString(), RootfsPath: filepath.Join(e.DataDir, "rootfs", id), BridgeName: "br0",
	}); err != nil {
		t.Fatal(err)
	}
	for _, nodes := range [][]string{{"/dev/nvidia0", "/dev/nvidiactl"}, nil} {
		if err := e.ApplyGPUDevices(id, nodes); err != nil {
			t.Fatal(err)
		}
		cfg, err := os.ReadFile(e.configPath(id))
		if err != nil {
			t.Fatal(err)
		}
		p := evalLXCDevicePolicy(t, lxcCommonConfDevices+lxcUsernsConfDevices+string(cfg))
		assertAllowed(t, p, standardGuestDevices...)
		gpu := deviceCheck{"/dev/nvidia0", 'c', 195, 0, "rw"}
		if nodes == nil {
			assertDenied(t, p, gpu)
		} else {
			assertAllowed(t, p, gpu)
		}
	}
}
