import { readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const SKIP_DIRS = new Set([
  ".git",
  "node_modules",
  "ui/dist",
  "ui/src/generated",
  "coverage",
  "vendor",
  "bin",
  "gen",
  "packaging/e2e/out",
  "packaging/e2e/gocache",
]);

export function walkFiles(root, { extensions = null, skipFiles = new Set() } = {}) {
  const results = [];

  function visit(dir) {
    let entries;
    try {
      entries = readdirSync(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      const full = join(dir, entry.name);
      const rel = relative(root, full).replaceAll("\\", "/");
      if (entry.isDirectory()) {
        if (SKIP_DIRS.has(entry.name) || SKIP_DIRS.has(rel)) continue;
        visit(full);
        continue;
      }
      if (skipFiles.has(rel)) continue;
      if (extensions) {
        const dot = entry.name.lastIndexOf(".");
        const ext = dot >= 0 ? entry.name.slice(dot) : "";
        if (!extensions.has(ext)) continue;
      }
      if (statSync(full).isFile()) results.push({ full, rel });
    }
  }

  visit(root);
  return results;
}
