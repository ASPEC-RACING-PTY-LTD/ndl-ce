import { existsSync, readFileSync, readdirSync } from "node:fs";

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
  "packaging/debian/ndl-control.postinst",
  "packaging/debian/ndl-control.postrm",
  "packaging/debian/ndl-agent.postinst",
  "packaging/debian/ndl-agent.postrm",
  "packaging/lib/ndl/postinst-control.sh",
  "packaging/bootstrap/get-nodal.sh",
  "systemd/ndl-control.service",
  "systemd/ndl-agent.service",
  "systemd/ndl-agent.socket",
  "systemd/nodal-workloads.target",
  "systemd/ndl-network-rollback.service",
  "systemd/ndl-dnsmasq@.service",
  "systemd/nodal-ct@.service",
  "systemd/nodal-vm@.service",
  "cmd/ndl-qemu-launch/main.go",
  "packaging/apparmor/usr.bin.qemu-system-x86_64.local",
  "docs/install.md",
  "docs/uninstall.md",
  "docs/recovery.md",
  "internal/hostos/hostos.go",
  "internal/hostos/debian/debian.go",
];

for (const rel of required) {
  if (!existsSync(rel)) errors.push(`missing ${rel}`);
}

if (existsSync("internal/hostos/ubuntu") || existsSync("packaging/ubuntu")) {
  errors.push("Ubuntu host adapter must not exist yet");
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

const control = existsSync("packaging/debian/control")
  ? readFileSync("packaging/debian/control", "utf8")
  : "";
for (const pkg of ["Package: nodal", "Package: ndl-control", "Package: ndl-agent", "Package: ndl-ui", "Package: nodalctl"]) {
  if (!control.includes(pkg)) errors.push(`debian/control missing ${pkg}`);
}
if (!/Depends:\s*ndl-control,\s*ndl-agent,\s*ndl-ui,\s*nodalctl/.test(control)) {
  errors.push("nodal metapackage must depend on ndl-control, ndl-agent, ndl-ui, nodalctl");
}
if (!/postgresql-16\s*\|\s*postgresql/.test(control)) {
  errors.push("ndl-control must Depend on postgresql-16 | postgresql");
}
if (!/^Package: ndl-control[\s\S]*?adduser/m.test(control)) {
  errors.push("ndl-control must Depend on adduser");
}
const dependLines = control
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
if (!/^CapabilityBoundingSet=CAP_NET_ADMIN CAP_CHOWN CAP_DAC_OVERRIDE CAP_SETUID CAP_SETGID CAP_SYS_ADMIN\s*$/m.test(agentUnit)) {
  errors.push("ndl-agent.service must use the Phase 6 typed capability set");
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
if (!agentInstall.includes("nodal-ct@.service")) {
  errors.push("ndl-agent.install must install nodal-ct@.service");
}
if (!agentInstall.includes("usr/sbin/ndl-qemu-launch")) {
  errors.push("ndl-agent.install must install usr/sbin/ndl-qemu-launch");
}
if (!agentInstall.includes("nodal-vm@.service")) {
  errors.push("ndl-agent.install must install nodal-vm@.service");
}
if (!agentInstall.includes("etc/apparmor.d/local/usr.bin.qemu-system-x86_64")) {
  errors.push("ndl-agent.install must install the QEMU AppArmor local profile");
}
const rules = existsSync("packaging/debian/rules")
  ? readFileSync("packaging/debian/rules", "utf8")
  : "";
if (!rules.includes("nodal-ct@.service")) {
  errors.push("debian/rules must install nodal-ct@.service");
}
if (!rules.includes("./cmd/ndl-qemu-launch")) {
  errors.push("debian/rules must build ndl-qemu-launch");
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
const postinstControl = existsSync("packaging/lib/ndl/postinst-control.sh")
  ? readFileSync("packaging/lib/ndl/postinst-control.sh", "utf8")
  : "";
if (!/chmod 0751 \/var\/lib\/ndl/.test(postinstControl)) {
  errors.push("postinst-control.sh must leave /var/lib/ndl traversable (0751) for unprivileged containers");
}

if (errors.length) {
  console.error("Package structure failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}

console.log("Package structure passed.");
