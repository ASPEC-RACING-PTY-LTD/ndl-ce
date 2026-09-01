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
if (/nvidia|cuda|kubernetes|kubeadm|kubelet|zfs-dkms|zfsutils|ceph|ollama|vllm/i.test(dependLines)) {
  errors.push("Phase 1 Depends must not include GPU, Kubernetes, ZFS DKMS, or AI");
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
if (!controlUnit.includes("RuntimeDirectory=ndl")) {
  errors.push("ndl-control.service must set RuntimeDirectory=ndl");
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
if (!/^CapabilityBoundingSet=\s*$/m.test(agentUnit)) {
  errors.push("ndl-agent.service must use an empty CapabilityBoundingSet");
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

if (errors.length) {
  console.error("Package structure failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}

console.log("Package structure passed.");
