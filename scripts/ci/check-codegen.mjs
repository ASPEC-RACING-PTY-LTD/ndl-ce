import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

function listTree(dir) {
  const files = [];
  function visit(current) {
    if (!existsSync(current)) return;
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const full = join(current, entry.name);
      if (entry.isDirectory()) visit(full);
      else files.push(full.replaceAll("\\", "/"));
    }
  }
  if (existsSync(dir) && statSync(dir).isDirectory()) visit(dir);
  return files.sort();
}

function snapshot(files) {
  return files.map((f) => `${f}\n${readFileSync(f, "utf8")}`).join("\n--\n");
}

const openAPIFiles = ["ui/src/generated/openapi.ts"];
const beforeOpenAPI = snapshot(openAPIFiles.filter((f) => existsSync(f)));
const beforeGo = snapshot(listTree("gen"));

execFileSync(process.execPath, ["scripts/generate-openapi.mjs"], { stdio: "inherit" });

try {
  execFileSync("buf", ["generate"], { stdio: "inherit" });
} catch {
  console.error("buf generate failed. Install buf, protoc-gen-go, and protoc-gen-connect-go.");
  process.exit(1);
}

const afterOpenAPI = snapshot(openAPIFiles);
const afterGo = snapshot(listTree("gen"));

if (beforeOpenAPI !== afterOpenAPI) {
  console.error("OpenAPI generated types drifted. Run: node scripts/generate-openapi.mjs");
  process.exit(1);
}
if (beforeGo !== afterGo) {
  console.error("Protobuf generated files drifted. Run: buf generate");
  process.exit(1);
}

console.log("Codegen is clean.");
