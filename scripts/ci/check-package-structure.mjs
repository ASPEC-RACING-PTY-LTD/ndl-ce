import { existsSync, readFileSync, readdirSync } from "node:fs";

const errors = [];
const required = [
  "packaging/debian/control",
  "packaging/debian/rules",
  "packaging/debian/changelog",
  "packaging/bootstrap/get-nodal.sh",
  "systemd/ndl-control.service",
  "systemd/ndl-agent.service",
  "systemd/ndl-agent.socket",
  "systemd/nodal-workloads.target",
  "internal/hostos/hostos.go",
  "internal/hostos/debian/debian.go",
];

for (const rel of required) {
  if (!existsSync(rel)) errors.push(`missing ${rel}`);
}

if (existsSync("internal/hostos/ubuntu")) {
  errors.push("Ubuntu host adapter must not exist in Phase 0");
}

const control = existsSync("packaging/debian/control")
  ? readFileSync("packaging/debian/control", "utf8")
  : "";
for (const pkg of ["Package: nodal", "Package: ndl-control", "Package: ndl-agent", "Package: ndl-ui", "Package: nodalctl"]) {
  if (!control.includes(pkg)) errors.push(`debian/control missing ${pkg}`);
}

const proto = existsSync("proto/nodal/agent/v1/agent.proto")
  ? readFileSync("proto/nodal/agent/v1/agent.proto", "utf8")
  : "";
if (/rpc\s+(HostExec|ExecHost|Exec)\s*\(/.test(proto)) {
  errors.push("agent proto must not define a generic exec RPC");
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
