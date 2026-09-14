import { readFileSync } from "node:fs";
import { walkFiles } from "./walk-files.mjs";

const root = process.cwd();
const skipFiles = new Set([
  "scripts/ci/check-em-dashes.mjs",
]);

const extensions = new Set([
  ".ts",
  ".tsx",
  ".js",
  ".mjs",
  ".cjs",
  ".go",
  ".css",
  ".md",
  ".mdc",
  ".yml",
  ".yaml",
  ".json",
  ".sql",
  ".proto",
  ".sh",
  ".service",
  ".socket",
  ".target",
]);

const patterns = [
  { label: "em dash", re: /\u2014/g },
  { label: "&mdash;", re: /&mdash;/gi },
  { label: "&#8212;", re: /&#8212;/g },
  { label: "&#x2014;", re: /&#x2014;/gi },
];

const hits = [];
for (const file of walkFiles(root, { extensions, skipFiles })) {
  if (file.rel.endsWith("pnpm-lock.yaml")) continue;
  const text = readFileSync(file.full, "utf8");
  const lines = text.split(/\r?\n/);
  for (const { label, re } of patterns) {
    lines.forEach((line, index) => {
      re.lastIndex = 0;
      if (re.test(line)) {
        hits.push(`${file.rel}:${index + 1}: ${label}`);
      }
    });
  }
}

if (hits.length) {
  console.error("Em dash policy failed:");
  for (const hit of hits) console.error(`  ${hit}`);
  process.exit(1);
}

console.log("Em dash policy passed.");
