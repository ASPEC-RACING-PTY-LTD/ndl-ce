import { readFileSync } from "node:fs";

const workflow = readFileSync(".github/workflows/ci.yml", "utf8");
const errors = [];
const forbidden = [
  [/\bdocker\s+build\b/, "docker build"],
  [/\bdocker\s+buildx\b/, "docker buildx"],
  [/\bbuildx\s+build\b/, "buildx build"],
  [/\bdocker\s+compose\b/, "docker compose"],
  [/\buses:\s*docker\//, "docker/* action"],
  [/\bbuild-push-action\b/, "build-push-action"],
  [/\bsetup-buildx\b/, "setup-buildx"],
  [/\bghcr\.io\b/, "ghcr.io"],
  [/\bnext\s+build\b/, "next build"],
  [/\bplaywright\b/, "playwright"],
  [/\bsudo\b/, "sudo"],
  [/\bprivileged:\s*true\b/, "privileged container"],
  [/\buseradd\b|\badduser\b/, "useradd"],
  [/\bsystemctl\b/, "systemctl"],
  [/\bqemu-system\b/, "qemu-system"],
  [/bash\s+<\(curl/, "curl-pipe actionlint install"],
  [/curl\s+[^\n]*\|\s*(sudo\s+)?(sh|bash)/, "curl pipe"],
];

const active = workflow
  .split("\n")
  .filter((line) => {
    const t = line.trim();
    return t && !t.startsWith("#");
  })
  .join("\n");

for (const [re, label] of forbidden) {
  if (re.test(active)) errors.push(`CI must not run ${label}`);
}
if (!workflow.includes("concurrency:")) {
  errors.push("CI must set concurrency");
}

if (errors.length) {
  console.error("CI policy failed:");
  for (const e of errors) console.error(`  ${e}`);
  process.exit(1);
}
console.log("CI policy passed.");
