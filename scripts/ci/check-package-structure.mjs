import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";

const errors = [];
const required = [
  "packaging/debian/control",
  "packaging/debian/rules",
  "packaging/debian/changelog",
  "packaging/debian/copyright",
  "packaging/debian/source/format",
  "packaging/debian/nodal.install",
  "packaging/debian/ndl-control.install",
  "packaging/debian/ndl-agent.install",
  "packaging/debian/ndl-ui.install",
  "packaging/debian/nodalctl.install",
  "packaging/debian/ndl-guest.install",
  "packaging/debian/nodal-feature-oci.install",
  "packaging/debian/nodal-feature-gpu.install",
  "packaging/debian/nodal-feature-k8s.install",
  "packaging/debian/nodal-feature-distributed-storage.install",
  "packaging/debian/nodal-feature-ai.install",
  "packaging/debian/ndl-control.postinst",
  "packaging/debian/ndl-control.postrm",
  "packaging/debian/ndl-agent.postinst",
  "packaging/debian/ndl-agent.postrm",
  "packaging/e2e/check-maintainer-scripts.sh",
  "packaging/e2e/check-control-upgrade.sh",
  "packaging/e2e/check-ui-build.sh",
  "packaging/lib/ndl/postinst-control.sh",
  "packaging/lib/ndl/build-ui.sh",
  "packaging/e2e/lib/ensure-node.sh",
  "packaging/bootstrap/get-nodal.sh",
  "systemd/ndl-control.service",
  "systemd/ndl-agent.service",
  "systemd/ndl-agent.socket",
  "systemd/nodal-workloads.target",
  "systemd/ndl-network-rollback.service",
  "systemd/ndl-dnsmasq@.service",
  "systemd/ndl-nat@.service",
  "systemd/nodal-ct@.service",
  "systemd/nodal-vm@.service",
  "systemd/nodal-oci@.service",
  "cmd/ndl-qemu-launch/main.go",
  "cmd/ndl-oci-launch/main.go",
  "packaging/apparmor/usr.bin.qemu-system-x86_64.local",
  "docs/install.md",
  "docs/uninstall.md",
  "docs/recovery.md",
  "docs/ce-1.0.md",
  "docs/backup.md",
  "docs/cluster.md",
  "docs/store.md",
  "docs/ai.md",
  "docs/api-compatibility.md",
  "docs/checklists/ce-1.0-virt.md",
  "docs/checklists/ce-1.0-physical.md",
  "internal/hostos/hostos.go",
  "internal/hostos/debian/debian.go",
];

for (const rel of required) {
  if (!existsSync(rel)) errors.push(`missing ${rel}`);
}

if (existsSync("packaging/ubuntu")) {
  errors.push("Ubuntu packaging tree must not exist until Ubuntu is qualified");
}
if (existsSync("internal/hostos/ubuntu")) {
  const ubuntuGo = readFileSync("internal/hostos/ubuntu/ubuntu.go", "utf8");
  if (!ubuntuGo.includes("QualificationGaps") || !/not a qualified/i.test(ubuntuGo)) {
    errors.push("Ubuntu adapter must document qualification gaps and stay unqualified");
  }
}
if (!existsSync("migrations/0027_phase30.sql")) {
  errors.push("missing migrations/0027_phase30.sql cluster join, tokens, and writer lease");
} else {
  const joinSql = readFileSync("migrations/0027_phase30.sql", "utf8");
  if (!joinSql.includes("join_tokens") || !joinSql.includes("cluster_leases") || !/role text/.test(joinSql)) {
    errors.push("phase 30 migration must add join_tokens, cluster_leases, and nodes.role");
  }
}
if (!existsSync("migrations/0028_phase31.sql")) {
  errors.push("missing migrations/0028_phase31.sql placement groups and maintenance");
}
if (!existsSync("migrations/0029_phase32.sql")) {
  errors.push("missing migrations/0029_phase32.sql migrate jobs and ownership epoch");
}
if (!existsSync("migrations/0030_phase33.sql")) {
  errors.push("missing migrations/0030_phase33.sql artifact locality and pull URL");
}
if (!existsSync("migrations/0031_phase34.sql")) {
  errors.push("missing migrations/0031_phase34.sql HA state and rolling plans");
}
if (!existsSync("migrations/0032_phase35.sql")) {
  errors.push("missing migrations/0032_phase35.sql feature modules");
} else {
  const featSql = readFileSync("migrations/0032_phase35.sql", "utf8");
  if (!featSql.includes("CREATE TABLE IF NOT EXISTS features") || !featSql.includes("kubelet")) {
    errors.push("phase 35 migration must add features and document that kubelet is not started");
  }
}
if (!existsSync("migrations/0033_phase36.sql")) {
  errors.push("missing migrations/0033_phase36.sql store packages");
} else {
  const storeSql = readFileSync("migrations/0033_phase36.sql", "utf8");
  if (!storeSql.includes("store_packages") || !storeSql.includes("store_installations")) {
    errors.push("phase 36 migration must add store_packages and store_installations");
  }
}
if (!existsSync("migrations/0034_phase37.sql")) {
  errors.push("missing migrations/0034_phase37.sql store signatures and scan results");
} else {
  const trustSql = readFileSync("migrations/0034_phase37.sql", "utf8");
  if (!trustSql.includes("store_package_signatures") || !trustSql.includes("store_scan_results")) {
    errors.push("phase 37 migration must add package signatures and scan results");
  }
}
if (!existsSync("migrations/0035_phase39.sql")) {
  errors.push("missing migrations/0035_phase39.sql distributed pools and OSDs");
} else {
  const distSql = readFileSync("migrations/0035_phase39.sql", "utf8");
  if (!distSql.includes("distributed_pools") || !distSql.includes("distributed_osds") || !distSql.includes("secrets.distributed_credentials")) {
    errors.push("phase 39 migration must add distributed pools, OSD rows, and secret keys");
  }
}
if (!existsSync("migrations/0036_phase40.sql")) {
  errors.push("missing migrations/0036_phase40.sql policies and policy_runs");
} else {
  const polSql = readFileSync("migrations/0036_phase40.sql", "utf8");
  if (!polSql.includes("policies") || !polSql.includes("policy_runs")) {
    errors.push("phase 40 migration must add policies and policy_runs");
  }
}
if (!existsSync("migrations/0037_phase41.sql")) {
  errors.push("missing migrations/0037_phase41.sql ai providers and profiles");
} else {
  const aiSql = readFileSync("migrations/0037_phase41.sql", "utf8");
  if (!aiSql.includes("ai_providers") || !aiSql.includes("ai_profiles") || !aiSql.includes("secrets.ai_credentials")) {
    errors.push("phase 41 migration must add ai_providers, ai_profiles, and secret keys");
  }
}
if (!existsSync("migrations/0038_phase42.sql")) {
  errors.push("missing migrations/0038_phase42.sql ai_plans");
} else {
  const planSql = readFileSync("migrations/0038_phase42.sql", "utf8");
  if (!planSql.includes("ai_plans") || !planSql.includes("ai_plan_steps")) {
    errors.push("phase 42 migration must add ai_plans and ai_plan_steps");
  }
}
if (!existsSync("migrations/0039_phase43.sql")) {
  errors.push("missing migrations/0039_phase43.sql license_state");
} else {
  const licSql = readFileSync("migrations/0039_phase43.sql", "utf8");
  if (!licSql.includes("license_state") || !licSql.includes("secrets.license_keys")) {
    errors.push("phase 43 migration must add license_state and secret keys");
  }
}
if (!existsSync("store/official/sample-web.yaml")) {
  errors.push("missing store/official/sample-web.yaml official sample manifest");
} else {
  const sample = readFileSync("store/official/sample-web.yaml", "utf8");
  if (/run:\s*bash|helper:/.test(sample) || !sample.includes("kind: oci")) {
    errors.push("official sample must be a declarative OCI manifest without helper scripts");
  }
}
if (!existsSync("packaging/iso/mkosi.conf")) {
  errors.push("missing packaging/iso/mkosi.conf Debian installer ISO contract");
} else {
  const iso = readFileSync("packaging/iso/mkosi.conf", "utf8");
  if (!iso.includes("Distribution=debian") || !iso.includes("Packages=nodal")) {
    errors.push("ISO must wrap Debian 13 and the nodal metapackage");
  }
}

const format = existsSync("packaging/debian/source/format")
  ? readFileSync("packaging/debian/source/format", "utf8").trim()
  : "";
if (format !== "3.0 (native)") {
  errors.push("packaging/debian/source/format must be 3.0 (native)");
}
const changelog = existsSync("packaging/debian/changelog")
  ? readFileSync("packaging/debian/changelog", "utf8")
  : "";
if (!changelog.includes("nodal (0.1.6)") || !changelog.includes("Phase 7 QEMU/QMP supervisory prototype")) {
  errors.push("changelog must include nodal (0.1.6) Phase 7 QEMU/QMP supervisory prototype");
}
if (!changelog.includes("nodal (0.1.7)") || !changelog.includes("Phase 8 product virtual machines")) {
  errors.push("changelog must include nodal (0.1.7) Phase 8 product virtual machines");
}
if (!changelog.includes("nodal (0.1.10)") || !changelog.includes("Phase 11 backup engine")) {
  errors.push("changelog must include nodal (0.1.10) Phase 11 backup engine");
}
if (!changelog.includes("nodal (0.1.11)") || !changelog.includes("Phase 12 platform updates")) {
  errors.push("changelog must include nodal (0.1.11) Phase 12 platform updates");
}
if (!changelog.includes("nodal (0.1.12)") || !changelog.includes("Phase 13 identity completion")) {
  errors.push("changelog must include nodal (0.1.12) Phase 13 identity completion");
}
if (!changelog.includes("nodal (0.1.13)") || !changelog.includes("Phase 14 GPU runtime and assignment")) {
  errors.push("changelog must include nodal (0.1.13) Phase 14 GPU runtime and assignment");
}
if (!changelog.includes("nodal (0.1.14)") || !changelog.includes("Phase 15 ZFS storage")) {
  errors.push("changelog must include nodal (0.1.14) Phase 15 ZFS storage");
}
if (!changelog.includes("nodal (0.1.15)") || !changelog.includes("Phase 16 observability complete")) {
  errors.push("changelog must include nodal (0.1.15) Phase 16 observability complete");
}
if (!changelog.includes("nodal (0.1.16)") || !changelog.includes("Phase 17 operator UX")) {
  errors.push("changelog must include nodal (0.1.16) Phase 17 operator UX");
}
if (!changelog.includes("nodal (0.1.17)") || !changelog.includes("Phase 18 VM advanced")) {
  errors.push("changelog must include nodal (0.1.17) Phase 18 VM advanced");
}
if (!changelog.includes("nodal (0.1.18)") || !changelog.includes("Phase 19 No-dal Guest Agent")) {
  errors.push("changelog must include nodal (0.1.18) Phase 19 No-dal Guest Agent");
}
if (!changelog.includes("nodal (0.1.19)") || !changelog.includes("Phase 20 guest Terminal and Files")) {
  errors.push("changelog must include nodal (0.1.19) Phase 20 guest Terminal and Files");
}
if (!changelog.includes("nodal (0.1.20)") || !changelog.includes("Phase 21 OCI application workloads")) {
  errors.push("changelog must include nodal (0.1.20) Phase 21 OCI application workloads");
}
if (!changelog.includes("nodal (0.1.21)") || !changelog.includes("Phase 22 application stacks")) {
  errors.push("changelog must include nodal (0.1.21) Phase 22 application stacks");
}
if (!changelog.includes("nodal (0.1.22)") || !changelog.includes("Phase 23 object-storage backup")) {
  errors.push("changelog must include nodal (0.1.22) Phase 23 object-storage backup");
}
if (!changelog.includes("nodal (0.1.23)") || !changelog.includes("Phase 24 backup verification")) {
  errors.push("changelog must include nodal (0.1.23) Phase 24 backup verification");
}
if (!changelog.includes("nodal (0.1.24)") || !changelog.includes("Phase 25 LVM-thin")) {
  errors.push("changelog must include nodal (0.1.24) Phase 25 LVM-thin");
}
if (!changelog.includes("nodal (0.1.25)") || !changelog.includes("Phase 26 NFS/SMB/iSCSI")) {
  errors.push("changelog must include nodal (0.1.25) Phase 26 NFS/SMB/iSCSI");
}
if (!changelog.includes("nodal (0.1.26)") || !changelog.includes("Phase 27 advanced networking")) {
  errors.push("changelog must include nodal (0.1.26) Phase 27 advanced networking");
}
if (!changelog.includes("nodal (0.1.27)") || !changelog.includes("Phase 28 WireGuard")) {
  errors.push("changelog must include nodal (0.1.27) Phase 28 WireGuard");
}
if (!changelog.includes("nodal (0.1.28)") || !changelog.includes("Phase 29 host platform")) {
  errors.push("changelog must include nodal (0.1.28) Phase 29 host platform");
}
if (!changelog.includes("nodal (0.1.29)") || !changelog.includes("Phase 30 cluster join")) {
  errors.push("changelog must include nodal (0.1.29) Phase 30 cluster join");
}
if (!changelog.includes("nodal (0.1.30)") || !changelog.includes("Phase 31 placement")) {
  errors.push("changelog must include nodal (0.1.30) Phase 31 placement");
}
if (!changelog.includes("nodal (0.1.31)") || !changelog.includes("Phase 32 workload migration")) {
  errors.push("changelog must include nodal (0.1.31) Phase 32 workload migration");
}
if (!changelog.includes("nodal (0.1.32)") || !changelog.includes("Phase 33 cluster storage")) {
  errors.push("changelog must include nodal (0.1.32) Phase 33 cluster storage");
}
if (!changelog.includes("nodal (0.1.33)") || !changelog.includes("Phase 34 HA foundations")) {
  errors.push("changelog must include nodal (0.1.33) Phase 34 HA foundations");
}
if (!changelog.includes("nodal (0.1.34)") || !changelog.includes("Phase 35 feature modules")) {
  errors.push("changelog must include nodal (0.1.34) Phase 35 feature modules");
}
if (!changelog.includes("nodal (0.1.35)") || !changelog.includes("Phase 36 No-dal Store")) {
  errors.push("changelog must include nodal (0.1.35) Phase 36 No-dal Store");
}
if (!changelog.includes("nodal (0.1.36)") || !changelog.includes("Phase 37 Store trust")) {
  errors.push("changelog must include nodal (0.1.36) Phase 37 Store trust");
}
if (!changelog.includes("nodal (0.1.37)") || !changelog.includes("Phase 38 optional Kubernetes")) {
  errors.push("changelog must include nodal (0.1.37) Phase 38 optional Kubernetes");
}
if (!changelog.includes("nodal (0.1.38)") || !changelog.includes("Phase 39 optional distributed storage")) {
  errors.push("changelog must include nodal (0.1.38) Phase 39 optional distributed storage");
}
if (!changelog.includes("nodal (0.1.39)") || !changelog.includes("Phase 40 automation engine")) {
  errors.push("changelog must include nodal (0.1.39) Phase 40 automation engine");
}
if (!changelog.includes("nodal (0.1.40)") || !changelog.includes("Phase 41 AI Ask")) {
  errors.push("changelog must include nodal (0.1.40) Phase 41 AI Ask");
}
if (!changelog.includes("nodal (0.1.41)") || !changelog.includes("Phase 42 AI Plan")) {
  errors.push("changelog must include nodal (0.1.41) Phase 42 AI Plan");
}
if (!changelog.includes("nodal (1.0.0)") || !changelog.includes("Phase 43 CE 1.0")) {
  errors.push("changelog must include nodal (1.0.0) Phase 43 CE 1.0");
}

const control = existsSync("packaging/debian/control")
  ? readFileSync("packaging/debian/control", "utf8")
  : "";
for (const pkg of ["Package: nodal", "Package: ndl-control", "Package: ndl-agent", "Package: ndl-ui", "Package: nodalctl"]) {
  if (!control.includes(pkg)) errors.push(`debian/control missing ${pkg}`);
}
if (!control.includes("Package: ndl-guest")) {
  errors.push("debian/control missing Package: ndl-guest");
}
for (const feat of [
  "Package: nodal-feature-oci",
  "Package: nodal-feature-gpu",
  "Package: nodal-feature-k8s",
  "Package: nodal-feature-distributed-storage",
  "Package: nodal-feature-ai",
]) {
  if (!control.includes(feat)) errors.push(`debian/control missing ${feat}`);
}
if (!/Depends:\s*ndl-control,\s*ndl-agent,\s*ndl-ui,\s*nodalctl/.test(control)) {
  errors.push("nodal metapackage must depend on ndl-control, ndl-agent, ndl-ui, nodalctl");
}
if (/^Package: nodal\n(?:(?!\nPackage:)[\s\S])*Depends:[^\n]*nodal-feature/m.test(control)) {
  errors.push("nodal metapackage must not Depend on feature packages");
}
function stanza(src, name) {
  const parts = src.split(/\n(?=Package: )/);
  return parts.find((p) => new RegExp(`^Package: ${name}\\b`).test(p)) ?? "";
}
const coreControl = ["nodal", "ndl-control", "ndl-agent", "ndl-ui", "nodalctl", "ndl-guest"]
  .map((n) => stanza(control, n))
  .join("\n");
if (!/postgresql-16\s*\|\s*postgresql/.test(control)) {
  errors.push("ndl-control must Depend on postgresql-16 | postgresql");
}
if (!/^Package: ndl-control[\s\S]*?adduser/m.test(control)) {
  errors.push("ndl-control must Depend on adduser");
}
const dependLines = coreControl
  .split(/\r?\n/)
  .filter((line) => /^\s*Depends:/.test(line))
  .join("\n");
if (/nvidia|cuda|kubernetes|kubeadm|kubelet|zfs-dkms|zfsutils|ceph|ollama|vllm|lvm2|nfs-kernel-server|samba/i.test(dependLines)) {
  errors.push("Depends must not include GPU, Kubernetes, ZFS, LVM, NFS/SMB servers, or AI");
}
if (/libvirt/i.test(control)) {
  errors.push("Depends must not include libvirt");
}
if (!/^Package: ndl-agent[\s\S]*?qemu-utils/m.test(control)) {
  errors.push("ndl-agent must Depend on qemu-utils for offline qemu-img");
}
if (!/^Package: ndl-agent[\s\S]*?qemu-system-x86/m.test(control)) {
  errors.push("ndl-agent must Depend on qemu-system-x86");
}
if (!/^Package: ndl-agent[\s\S]*?dnsmasq-base/m.test(control)) {
  errors.push("ndl-agent must Depend on dnsmasq-base, not the system-wide dnsmasq daemon");
}
if (/^Package: ndl-agent[\s\S]*?Depends:[^\n]*\bdnsmasq,/m.test(control)) {
  errors.push("ndl-agent must not Depend on the system-wide dnsmasq package");
}
if (!/^Package: ndl-agent[\s\S]*?nftables/m.test(control)) {
  errors.push("ndl-agent must Depend on nftables for NAT and INPUT validation");
}
if (!/^Package: ndl-agent[\s\S]*?\blxc\b/m.test(control)) {
  errors.push("ndl-agent must Depend on lxc");
}
if (!/^Package: ndl-agent[\s\S]*?lxcfs/m.test(control)) {
  errors.push("ndl-agent must Depend on lxcfs");
}
if (!/^Package: ndl-agent[\s\S]*?\bgpgv\b/m.test(control)) {
  errors.push("ndl-agent must Depend on gpgv to verify official LXC image signatures");
}
if (!/^Package: ndl-agent[\s\S]*?uidmap/m.test(control)) {
  errors.push("ndl-agent must Depend on uidmap");
}
if (!/^Package: ndl-agent[\s\S]*?iproute2/m.test(control)) {
  errors.push("ndl-agent must Depend on iproute2 for typed TAP operations");
}

const proto = existsSync("proto/nodal/agent/v1/agent.proto")
  ? readFileSync("proto/nodal/agent/v1/agent.proto", "utf8")
  : "";
if (/rpc\s+(HostExec|ExecHost|Exec)\s*\(/.test(proto)) {
  errors.push("agent proto must not define a generic exec RPC");
}
if (/Host\.Exec/.test(proto)) {
  errors.push("agent proto must not define Host.Exec");
}

const controlUnit = existsSync("systemd/ndl-control.service")
  ? readFileSync("systemd/ndl-control.service", "utf8")
  : "";
if (!controlUnit.includes("User=ndl-control")) {
  errors.push("ndl-control.service must run as ndl-control");
}
if (!controlUnit.includes("EnvironmentFile=-/etc/ndl/control.env")) {
  errors.push("ndl-control.service must load /etc/ndl/control.env");
}
if (!controlUnit.includes("After=network.target postgresql.service")) {
  errors.push("ndl-control.service must start after network and postgresql");
}
if (/^RuntimeDirectory=ndl\s*$/m.test(controlUnit)) {
  errors.push("ndl-control.service must not claim RuntimeDirectory=ndl; the agent socket owns /run/ndl");
}

const agentUnit = existsSync("systemd/ndl-agent.service")
  ? readFileSync("systemd/ndl-agent.service", "utf8")
  : "";
if (!agentUnit.includes("User=root")) {
  errors.push("ndl-agent.service must run as root");
}
if (!agentUnit.includes("NoNewPrivileges=yes")) {
  errors.push("ndl-agent.service must set NoNewPrivileges");
}
if (!agentUnit.includes("DevicePolicy=closed")) {
  errors.push("ndl-agent.service must set DevicePolicy=closed");
}
if (!/^CapabilityBoundingSet=CAP_NET_ADMIN CAP_CHOWN CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SETFCAP CAP_SYS_ADMIN CAP_SYS_PTRACE\s*$/m.test(agentUnit)) {
  errors.push("ndl-agent.service must use the typed lxc-attach capability set including CAP_SETFCAP and CAP_SYS_PTRACE");
}
if (/CapabilityBoundingSet=~/.test(agentUnit) || /DevicePolicy=auto/.test(agentUnit) || /NoNewPrivileges=no/.test(agentUnit)) {
  errors.push("ndl-agent.service must not ship an unrestricted bounding set, DevicePolicy=auto, or NoNewPrivileges=no");
}
if (/qemu-system|ndl-qemu-launch/.test(agentUnit)) {
  errors.push("ndl-agent.service must not parent QEMU");
}
if (!/DeviceAllow=char-pts rw/.test(agentUnit) || !/DeviceAllow=\/dev\/ptmx rw/.test(agentUnit)) {
  errors.push("ndl-agent.service must allow PTY devices");
}
const agentInstall = existsSync("packaging/debian/ndl-agent.install")
  ? readFileSync("packaging/debian/ndl-agent.install", "utf8")
  : "";
if (!agentInstall.includes("ndl-nat@.service")) {
  errors.push("ndl-agent.install must install ndl-nat@.service");
}
if (!agentInstall.includes("nodal-ct@.service")) {
  errors.push("ndl-agent.install must install nodal-ct@.service");
}
if (!agentInstall.includes("usr/sbin/ndl-qemu-launch")) {
  errors.push("ndl-agent.install must install usr/sbin/ndl-qemu-launch");
}
if (!agentInstall.includes("usr/sbin/ndl-oci-launch")) {
  errors.push("ndl-agent.install must install usr/sbin/ndl-oci-launch");
}
if (!agentInstall.includes("nodal-vm@.service")) {
  errors.push("ndl-agent.install must install nodal-vm@.service");
}
if (!agentInstall.includes("nodal-oci@.service")) {
  errors.push("ndl-agent.install must install nodal-oci@.service");
}
if (!agentInstall.includes("etc/apparmor.d/local/usr.bin.qemu-system-x86_64")) {
  errors.push("ndl-agent.install must install the QEMU AppArmor local profile");
}
const rules = existsSync("packaging/debian/rules")
  ? readFileSync("packaging/debian/rules", "utf8")
  : "";
if (!rules.includes("ndl-nat@.service")) {
  errors.push("debian/rules must install ndl-nat@.service");
}
if (!rules.includes("nodal-ct@.service")) {
  errors.push("debian/rules must install nodal-ct@.service");
}
if (!rules.includes("./cmd/ndl-qemu-launch")) {
  errors.push("debian/rules must build ndl-qemu-launch");
}
if (!rules.includes("./cmd/ndl-oci-launch")) {
  errors.push("debian/rules must build ndl-oci-launch");
}
if (!rules.includes("usr/sbin/ndl-oci-launch")) {
  errors.push("debian/rules must install ndl-oci-launch to usr/sbin/ndl-oci-launch");
}
if (!rules.includes("usr/share/ndl/features")) {
  errors.push("debian/rules must install optional feature markers");
}
if (!rules.includes("nodal-oci@.service")) {
  errors.push("debian/rules must install nodal-oci@.service");
}
if (!rules.includes("./cmd/ndl-guest")) {
  errors.push("debian/rules must build ndl-guest");
}
if (!rules.includes("usr/sbin/ndl-guest")) {
  errors.push("debian/rules must install ndl-guest");
}
if (!existsSync("cmd/ndl-guest/main.go")) {
  errors.push("missing cmd/ndl-guest/main.go");
}
if (!existsSync("systemd/ndl-guest.service")) {
  errors.push("missing systemd/ndl-guest.service");
}
const qemuGo =
  (existsSync("internal/qemu/launch.go") ? readFileSync("internal/qemu/launch.go", "utf8") : "") +
  (existsSync("internal/qemu/types.go") ? readFileSync("internal/qemu/types.go", "utf8") : "") +
  (existsSync("internal/qemu/argv.go") ? readFileSync("internal/qemu/argv.go", "utf8") : "");
if (!qemuGo.includes("org.nodal.guest.0")) {
  errors.push("CompileLaunch must attach org.nodal.guest.0");
}
if (!rules.includes("usr/sbin/ndl-qemu-launch")) {
  errors.push("debian/rules must install ndl-qemu-launch to usr/sbin/ndl-qemu-launch");
}
if (!rules.includes("nodal-vm@.service")) {
  errors.push("debian/rules must install nodal-vm@.service");
}
if (!rules.includes("etc/apparmor.d/local/usr.bin.qemu-system-x86_64")) {
  errors.push("debian/rules must install the QEMU AppArmor local profile");
}
if (/libvirt/i.test(rules) || /libvirt/i.test(agentInstall)) {
  errors.push("packaging must not use libvirt");
}
const ctUnit = existsSync("systemd/nodal-ct@.service")
  ? readFileSync("systemd/nodal-ct@.service", "utf8")
  : "";
if (!/Type=simple/.test(ctUnit) || !/lxc-start[^\n]* -F/.test(ctUnit)) {
  errors.push("nodal-ct@.service must run lxc-start in the foreground as Type=simple");
}
if (/^Type=forking/m.test(ctUnit) || /^ExecStart=.*lxc-start.* -d/m.test(ctUnit)) {
  errors.push("nodal-ct@.service must not use Type=forking or lxc-start -d");
}
const ociUnit = existsSync("systemd/nodal-oci@.service")
  ? readFileSync("systemd/nodal-oci@.service", "utf8")
  : "";
if (!/Type=simple/.test(ociUnit) || !ociUnit.includes("ExecStart=/usr/sbin/ndl-oci-launch")) {
  errors.push("nodal-oci@.service must run ndl-oci-launch as Type=simple");
}
if (/^(After|BindsTo|Requires|PartOf|Wants)=.*(ndl-control|ndl-agent)/m.test(ociUnit)) {
  errors.push("nodal-oci@.service must not bind to ndl-control or ndl-agent");
}
if (!/WantedBy=nodal-workloads.target/.test(ociUnit)) {
  errors.push("nodal-oci@.service must be WantedBy nodal-workloads.target");
}
const vmUnit = existsSync("systemd/nodal-vm@.service")
  ? readFileSync("systemd/nodal-vm@.service", "utf8")
  : "";
if (!vmUnit.includes("User=ndl-qemu")) {
  errors.push("nodal-vm@.service must set User=ndl-qemu");
}
if (!vmUnit.includes("ExecStart=/usr/sbin/ndl-qemu-launch")) {
  errors.push("nodal-vm@.service must start ndl-qemu-launch");
}
if (/libvirt/i.test(vmUnit)) {
  errors.push("nodal-vm@.service must not use libvirt");
}
const postinst = existsSync("packaging/debian/ndl-agent.postinst")
  ? readFileSync("packaging/debian/ndl-agent.postinst", "utf8")
  : "";
if (!postinst.includes("lxc-net.service")) {
  errors.push("ndl-agent.postinst must mask lxc-net.service");
}
if (!/adduser[^\n]*ndl-qemu|useradd[^\n]*ndl-qemu/.test(postinst)) {
  errors.push("ndl-agent.postinst must create system user ndl-qemu");
}
if (!postinst.includes("/var/lib/ndl/runtime/qemu")) {
  errors.push("ndl-agent.postinst must set ndl-qemu home to /var/lib/ndl/runtime/qemu");
}
if (!/nologin/.test(postinst) || !postinst.includes("ndl-qemu")) {
  errors.push("ndl-agent.postinst must create ndl-qemu with no shell");
}
if (!/getent group kvm/.test(postinst) || !/adduser ndl-qemu kvm|usermod[^\n]*kvm/.test(postinst)) {
  errors.push("ndl-agent.postinst must add ndl-qemu to kvm when that group exists");
}
if (/systemctl\s+enable --now[^\n]*nodal-vm@|systemctl\s+start[^\n]*nodal-vm@/.test(postinst)) {
  errors.push("ndl-agent.postinst must not start a VM");
}
if (/libvirt/i.test(postinst)) {
  errors.push("ndl-agent.postinst must not use libvirt");
}
if (/^RuntimeDirectory=ndl\s*$/m.test(agentUnit)) {
  errors.push("ndl-agent.service must not claim RuntimeDirectory=ndl; the agent socket owns /run/ndl");
}

const natUnit = existsSync("systemd/ndl-nat@.service")
  ? readFileSync("systemd/ndl-nat@.service", "utf8")
  : "";
if (!natUnit.includes("ExecStart=/usr/sbin/nft -f /var/lib/ndl/net/nft/%i.nft")) {
  errors.push("ndl-nat@.service must apply persisted nftables with nft -f");
}
if (/flush ruleset/.test(natUnit) || /ens18/.test(natUnit)) {
  errors.push("ndl-nat@.service must not flush ruleset or hardcode ens18");
}
if (!natUnit.includes("WantedBy=multi-user.target")) {
  errors.push("ndl-nat@.service must remain enabled across reboot");
}

const socket = existsSync("systemd/ndl-agent.socket")
  ? readFileSync("systemd/ndl-agent.socket", "utf8")
  : "";
if (!socket.includes("ListenStream=/run/ndl/agent.sock")) {
  errors.push("ndl-agent.socket must listen on /run/ndl/agent.sock");
}
if (!socket.includes("SocketMode=0660")) {
  errors.push("ndl-agent.socket must be 0660");
}
if (!socket.includes("SocketGroup=ndl-control")) {
  errors.push("ndl-agent.socket must be group ndl-control");
}

const unitDir = "systemd";
if (existsSync(unitDir)) {
  for (const name of readdirSync(unitDir)) {
    const unit = readFileSync(`${unitDir}/${name}`, "utf8");
    if (name !== "ndl-agent.service" && /Requires=ndl-agent\.service/.test(unit)) {
      errors.push(`${name} must not Require the agent process`);
    }
    if (/BindsTo=ndl-(control|agent)\.service/.test(unit) || /PartOf=ndl-(control|agent)\.service/.test(unit)) {
      errors.push(`${name} must not bind workloads or peers to the control plane or agent`);
    }
    if (name.startsWith("nodal-") && /Requires=ndl-(control|agent)\.service/.test(unit)) {
      errors.push(`${name} must not Require the control plane or agent`);
    }
    if (/libvirt/i.test(unit)) {
      errors.push(`${name} must not use libvirt`);
    }
  }
}

const buildRepo = existsSync("packaging/e2e/build-repo.sh")
  ? readFileSync("packaging/e2e/build-repo.sh", "utf8")
  : "";
if (buildRepo.includes("quick-gen-key")) {
  errors.push("build-repo.sh must not generate a new signing key; use packaging/e2e/lib/sign-repo.sh");
}
if (buildRepo && !buildRepo.includes("sign_release")) {
  errors.push("build-repo.sh must sign with the persistent key helper");
}
const rebuildRepo = existsSync("packaging/e2e/rebuild-packages.sh")
  ? readFileSync("packaging/e2e/rebuild-packages.sh", "utf8")
  : "";
if (rebuildRepo.includes("quick-gen-key")) {
  errors.push("rebuild-packages.sh must not generate a new signing key; use packaging/e2e/lib/sign-repo.sh");
}
if (rebuildRepo && !rebuildRepo.includes('rm -rf "$OUT/debs"')) {
  errors.push("rebuild-packages.sh must clean $OUT/debs before copying new packages");
}
if (rebuildRepo && !rebuildRepo.includes("check-maintainer-scripts.sh")) {
  errors.push("rebuild-packages.sh must inspect generated maintainer scripts from built debs");
}
if (rebuildRepo.includes("ui/dist is missing")) {
  errors.push("rebuild-packages.sh must not require a prebuilt ui/dist");
}
if (rebuildRepo && !rebuildRepo.includes('rm -rf "$BUILD/ui/dist"')) {
  errors.push("rebuild-packages.sh must discard copied ui/dist so the package build cannot reuse stale assets");
}
if (rebuildRepo && !rebuildRepo.includes("ensure_node")) {
  errors.push("rebuild-packages.sh must provide Node so debian/rules can build Vite assets");
}
if (buildRepo.includes("ui/dist is missing")) {
  errors.push("build-repo.sh must not require a prebuilt ui/dist");
}
if (buildRepo && !buildRepo.includes('rm -rf "$BUILD/ui/dist"')) {
  errors.push("build-repo.sh must discard copied ui/dist so the package build cannot reuse stale assets");
}
if (buildRepo && !buildRepo.includes("ensure_node")) {
  errors.push("build-repo.sh must provide Node so debian/rules can build Vite assets");
}

const debianRules = existsSync("packaging/debian/rules")
  ? readFileSync("packaging/debian/rules", "utf8")
  : "";
if (!debianRules.includes("build-ui.sh")) {
  errors.push("debian/rules must build fresh Vite assets via build-ui.sh");
}
if (!debianRules.includes("rm -rf $(MODROOT)/ui/dist")) {
  errors.push("debian/rules must delete ui/dist on clean so a rebuild cannot pack leftovers");
}
if (/if \[ -d \$\(MODROOT\)\/ui\/dist \]/.test(debianRules)) {
  errors.push("debian/rules must not skip ndl-ui when ui/dist is missing");
}
if (!debianRules.includes("test -f $(MODROOT)/ui/dist/index.html")) {
  errors.push("debian/rules must fail if Vite did not write dist/index.html");
}
if (!debianRules.includes("test -d $(MODROOT)/ui/dist/assets")) {
  errors.push("debian/rules must fail if Vite did not write dist/assets");
}
if (!debianRules.includes("test -f debian/ndl-ui/usr/share/ndl/ui/index.html")) {
  errors.push("debian/rules must fail if ndl-ui did not receive Vite assets");
}

const buildUi = existsSync("packaging/lib/ndl/build-ui.sh")
  ? readFileSync("packaging/lib/ndl/build-ui.sh", "utf8")
  : "";
if (!buildUi.includes("pnpm build") || !buildUi.includes('rm -rf "$UI/dist"')) {
  errors.push("build-ui.sh must delete dist and run pnpm build");
}
if (!buildUi.includes("/assets/") || !buildUi.includes("dist/assets")) {
  errors.push("build-ui.sh must reject a placeholder dist that is not a Vite build");
}
if (!buildUi.includes("Vite dist has no CSS assets") || !buildUi.includes("Vite dist has no JS assets")) {
  errors.push("build-ui.sh must reject a dist that is missing hashed JS or CSS");
}

const debhelperToken = "#DEBHELPER#";
const maintainerScript = /\.(postinst|postrm|preinst|prerm)$/;
const debhelperMentionAllow = new Set([
  "packaging/e2e/check-maintainer-scripts.sh",
  "scripts/ci/check-package-structure.mjs",
]);

function walkRelFiles(dir, acc = []) {
  if (!existsSync(dir)) return acc;
  for (const name of readdirSync(dir)) {
    const rel = `${dir}/${name}`;
    let st;
    try {
      st = statSync(rel);
    } catch {
      continue;
    }
    if (st.isDirectory()) {
      if (name === "out" || name === "signing" || name === "gocache" || name === "node_modules") {
        continue;
      }
      walkRelFiles(rel, acc);
      continue;
    }
    if (st.isFile()) acc.push(rel);
  }
  return acc;
}

for (const rel of [...walkRelFiles("packaging"), ...walkRelFiles("scripts/ci")]) {
  const text = readFileSync(rel, "utf8");
  if (!text.includes(debhelperToken)) continue;
  if (debhelperMentionAllow.has(rel)) continue;
  const lines = text.split("\n");
  lines.forEach((line, idx) => {
    if (!line.includes(debhelperToken)) return;
    if (!maintainerScript.test(rel) || line.trim() !== debhelperToken) {
      errors.push(
        `${rel}:${idx + 1} accidental ${debhelperToken} token; maintainer scripts may only use a lone token line`,
      );
    }
  });
}

for (const name of [
  "ndl-control.postinst",
  "ndl-control.postrm",
  "ndl-agent.postinst",
  "ndl-agent.postrm",
]) {
  const rel = `packaging/debian/${name}`;
  if (!existsSync(rel)) continue;
  const lone = readFileSync(rel, "utf8")
    .split("\n")
    .filter((line) => line.trim() === debhelperToken);
  if (lone.length !== 1) {
    errors.push(`${rel} must contain exactly one lone ${debhelperToken} token`);
  }
}

const generatedCheck = existsSync("packaging/e2e/check-maintainer-scripts.sh")
  ? readFileSync("packaging/e2e/check-maintainer-scripts.sh", "utf8")
  : "";
if (!generatedCheck.includes("dpkg-buildpackage") || !generatedCheck.includes("dpkg-deb")) {
  errors.push("check-maintainer-scripts.sh must build a real .deb and extract generated maintainer scripts");
}
if (!generatedCheck.includes("sh -n") || !generatedCheck.includes("restarts it once after configure")) {
  errors.push("check-maintainer-scripts.sh must syntax-check generated scripts and reject stray prose");
}
if (/\bsed\s+-i\b/.test(generatedCheck) || /sed\s+[^\n]*#DEBHELPER#/.test(generatedCheck)) {
  errors.push("check-maintainer-scripts.sh must not rewrite packaging source with sed");
}
if (generatedCheck.includes("<title>ndl-ui</title>")) {
  errors.push("check-maintainer-scripts.sh must not plant a placeholder ui/dist; debian/rules must run Vite");
}
if (!generatedCheck.includes("ensure_node")) {
  errors.push("check-maintainer-scripts.sh must call ensure_node so the UI package can be built");
}
if (!generatedCheck.includes('rm -rf "$BUILD/ui/dist"')) {
  errors.push("check-maintainer-scripts.sh must discard copied ui/dist so the package build cannot reuse stale assets");
}
if (!generatedCheck.includes("usr/share/ndl/ui/assets")) {
  errors.push("check-maintainer-scripts.sh must inspect ndl-ui Vite assets in the built deb");
}

const postinstControlPkg = existsSync("packaging/debian/ndl-control.postinst")
  ? readFileSync("packaging/debian/ndl-control.postinst", "utf8")
  : "";
if (/systemctl\s+start[^\n]*ndl-control/.test(postinstControlPkg)) {
  errors.push("ndl-control.postinst must not start ndl-control; the automatic systemd helper restarts it once");
}
if (/cluster_leases/.test(postinstControlPkg)) {
  errors.push("ndl-control.postinst must not delete writer lease rows");
}
const postrmControlPkg = existsSync("packaging/debian/ndl-control.postrm")
  ? readFileSync("packaging/debian/ndl-control.postrm", "utf8")
  : "";
if (/remove\|upgrade\|deconfigure/.test(postrmControlPkg)) {
  errors.push("ndl-control.postrm must not stop ndl-control on upgrade");
}
if (/cluster_leases/.test(postrmControlPkg)) {
  errors.push("ndl-control.postrm must not delete writer lease rows");
}
const postrmAgentPkg = existsSync("packaging/debian/ndl-agent.postrm")
  ? readFileSync("packaging/debian/ndl-agent.postrm", "utf8")
  : "";
if (/remove\|upgrade\|deconfigure/.test(postrmAgentPkg)) {
  errors.push("ndl-agent.postrm must not stop ndl-agent on upgrade");
}

const postinstControl = existsSync("packaging/lib/ndl/postinst-control.sh")
  ? readFileSync("packaging/lib/ndl/postinst-control.sh", "utf8")
  : "";
if (!/chmod 0751 \/var\/lib\/ndl/.test(postinstControl)) {
  errors.push("postinst-control.sh must leave /var/lib/ndl traversable (0751) for unprivileged containers");
}

const uiBuild = spawnSync("sh", ["packaging/e2e/check-ui-build.sh"], { encoding: "utf8" });
if (uiBuild.status !== 0) {
  errors.push(`check-ui-build.sh failed: ${(uiBuild.stderr || uiBuild.stdout || "").trim()}`);
}

if (errors.length) {
  console.error("Package structure failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}

console.log("Package structure passed.");
