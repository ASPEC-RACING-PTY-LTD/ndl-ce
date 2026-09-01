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
if (/nvidia|cuda|kubernetes|kubeadm|kubelet|zfs-dkms|zfsutils|ceph|ollama|vllm|qemu-system|lvm2|nfs-kernel-server|samba/i.test(dependLines)) {
  errors.push("Depends must not include GPU, Kubernetes, ZFS, LVM, NFS/SMB servers, QEMU system, or AI");
}
if (!/^Package: ndl-agent[\s\S]*?qemu-utils/m.test(control)) {
  errors.push("ndl-agent must Depend on qemu-utils for offline qemu-img");
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

const proto = existsSync("proto/nodal/agent/v1/agent.proto")
  ? readFileSync("proto/nodal/agent/v1/agent.proto", "utf8")
  : "";
if (/rpc\s+(HostExec|ExecHost|Exec)\s*\(/.test(proto)) {
  errors.push("agent proto must not define a generic exec RPC");
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
if (!/^CapabilityBoundingSet=CAP_NET_ADMIN CAP_CHOWN\s*$/m.test(agentUnit)) {
  errors.push("ndl-agent.service must grant CAP_NET_ADMIN and CAP_CHOWN");
}
if (/CAP_SYS_ADMIN/.test(agentUnit)) {
  errors.push("ndl-agent.service must not grant CAP_SYS_ADMIN");
}
const agentInstall = existsSync("packaging/debian/ndl-agent.install")
  ? readFileSync("packaging/debian/ndl-agent.install", "utf8")
  : "";
if (!agentInstall.includes("nodal-ct@.service")) {
  errors.push("ndl-agent.install must install nodal-ct@.service");
}
const rules = existsSync("packaging/debian/rules")
  ? readFileSync("packaging/debian/rules", "utf8")
  : "";
if (!rules.includes("nodal-ct@.service")) {
  errors.push("debian/rules must install nodal-ct@.service");
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
const postinst = existsSync("packaging/debian/ndl-agent.postinst")
  ? readFileSync("packaging/debian/ndl-agent.postinst", "utf8")
  : "";
if (!postinst.includes("lxc-net.service")) {
  errors.push("ndl-agent.postinst must mask lxc-net.service");
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

if (errors.length) {
  console.error("Package structure failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}

console.log("Package structure passed.");
