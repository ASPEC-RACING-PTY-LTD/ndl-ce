import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { walkFiles } from "./walk-files.mjs";

const root = existsSync(join(process.cwd(), "ui/src")) ? process.cwd() : join(process.cwd(), "..");
const skipFiles = new Set(["scripts/ci/check-ui-copy.mjs"]);
const extensions = new Set([".ts", ".tsx"]);
const patterns = [
  { label: "phase number", re: /Phase \d+/g },
  { label: "later phase", re: /later phase/gi },
  { label: "not invented", re: /not invented/gi },
  { label: "license activation", re: /License activation is not required/g },
];

const hits = [];
for (const file of walkFiles(root, { extensions, skipFiles })) {
  if (!file.rel.startsWith("ui/src/")) {
    continue;
  }
  if (file.rel.endsWith(".test.tsx") || file.rel.endsWith(".test.ts")) {
    continue;
  }
  const text = readFileSync(file.full, "utf8");
  for (const pattern of patterns) {
    const matches = text.match(pattern.re);
    if (matches) {
      hits.push(`${file.rel}: ${pattern.label}`);
    }
  }
}

if (hits.length > 0) {
  console.error("UI copy lint failed:");
  for (const hit of hits) {
    console.error(`  ${hit}`);
  }
  process.exit(1);
}

console.log("UI copy lint: ok");
