import { readFileSync } from "node:fs";

const server = readFileSync("internal/httpapi/server.go", "utf8");
const yaml = readFileSync("api/openapi/nodal.v1.yaml", "utf8");

const mux = new Set();
for (const m of server.matchAll(/mux\.HandleFunc\("([A-Z]+) (\/api\/v1\/[^"]+)"/g)) {
  mux.add(`${m[1]} ${m[2].slice("/api/v1".length)}`);
}

const oa = new Set();
let cur = "";
for (const line of yaml.split("\n")) {
  const path = line.match(/^  (\/[^:]+):$/);
  if (path) {
    cur = path[1];
    continue;
  }
  const method = line.match(/^    (get|post|put|patch|delete):$/);
  if (method && cur) {
    oa.add(`${method[1].toUpperCase()} ${cur}`);
  }
}

const missingSpec = [...mux].filter((item) => !oa.has(item)).sort();
const missingMux = [...oa].filter((item) => !mux.has(item)).sort();
const errors = [];
if (missingSpec.length) {
  errors.push("HTTP routes missing from OpenAPI:");
  for (const item of missingSpec) errors.push(`  ${item}`);
}
if (missingMux.length) {
  errors.push("OpenAPI paths missing from the HTTP mux:");
  for (const item of missingMux) errors.push(`  ${item}`);
}

if (errors.length) {
  console.error("OpenAPI route freeze failed:");
  for (const line of errors) console.error(line);
  process.exit(1);
}
console.log(`OpenAPI route freeze passed (${mux.size} methods).`);
