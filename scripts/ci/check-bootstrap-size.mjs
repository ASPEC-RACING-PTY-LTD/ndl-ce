import { readFileSync } from "node:fs";

const rel = "packaging/bootstrap/get-nodal.sh";
const text = readFileSync(rel, "utf8");
const lines = text.split(/\r?\n/);
const maxLines = 80;
const errors = [];

if (lines.length > maxLines) {
  errors.push(`${rel} has ${lines.length} lines; max is ${maxLines}`);
}

const banned = [
  [/postgres/i, "PostgreSQL"],
  [/useradd|adduser|groupadd/, "service-user creation"],
  [/systemctl\s+(start|enable|restart|stop)/, "service mutation"],
  [/createdb|psql\b/i, "database initialization"],
  [/\bwget\b/, "wget download"],
  [/\beval\b/, "eval"],
  [/curl\s+[^\n]*\|\s*(sudo\s+)?(sh|bash)/, "nested unsigned pipe"],
  [/wget\s+[^\n]*\|\s*(sudo\s+)?(sh|bash)/, "wget pipe"],
];

for (const [re, label] of banned) {
  if (re.test(text)) errors.push(`${rel} must not contain ${label}`);
}

if (/\bapt(-get)?\s+install\b/.test(text) && !/Would run:.*apt-get install/.test(text)) {
  errors.push(`${rel} must not run apt install; echo the command only`);
}

if (!text.includes("os-release")) {
  errors.push(`${rel} must detect os-release`);
}
if (!text.includes("uname")) {
  errors.push(`${rel} must detect architecture`);
}
if (!text.includes('id != "debian"') && !text.includes("[ \"$id\" != \"debian\" ]")) {
  errors.push(`${rel} must fail closed unless ID is debian`);
}
if (!text.includes("13")) {
  errors.push(`${rel} must require Debian 13`);
}
if (!text.includes("amd64")) {
  errors.push(`${rel} must require amd64`);
}
if (!text.includes("nodal")) {
  errors.push(`${rel} must mention the nodal metapackage`);
}
if (/id.*=.*ubuntu|ubuntu.*supported/i.test(text) && !/not currently support/.test(text)) {
  errors.push(`${rel} must not treat Ubuntu as a supported host`);
}

if (errors.length) {
  console.error("Bootstrap policy failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}
console.log("Bootstrap size and policy passed.");
