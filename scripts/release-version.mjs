import { readFileSync } from "node:fs";

const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)$/;

function parseVersion(value) {
  const match = VERSION_PATTERN.exec(value ?? "");
  if (!match) throw new Error(`Invalid semantic version: ${value}`);
  return match.slice(1).map(Number);
}

function compareVersions(left, right) {
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) return left[index] - right[index];
  }
  return 0;
}

export function nextReleaseVersion(latestTag, commitMessage, changelog = "") {
  const subject = String(commitMessage).split(/\r?\n/, 1)[0];
  if (!subject.startsWith("DEPLOY:")) {
    throw new Error("Release commits must start with DEPLOY:");
  }

  const taggedVersion = latestTag ? parseVersion(latestTag.replace(/^v/, "")) : null;
  const changelogText = /^nodal \(([^)]+)\)/m.exec(changelog)?.[1];
  const changelogVersion = changelogText ? parseVersion(changelogText) : null;
  const current = !taggedVersion
    ? changelogVersion
    : !changelogVersion || compareVersions(taggedVersion, changelogVersion) >= 0
      ? taggedVersion
      : changelogVersion;
  if (!current) throw new Error("No current version found in tags or Debian changelog");
  const directive = subject.slice("DEPLOY:".length).trim().split(/\s+/, 1)[0] ?? "";
  const explicit = VERSION_PATTERN.exec(directive);

  if (explicit) {
    const target = explicit.slice(1).map(Number);
    if (compareVersions(target, current) <= 0) {
      throw new Error("The requested release version must be newer than the current version");
    }
    return `v${target.join(".")}`;
  }

  const bump = /^(major|minor|patch)\b/i.exec(directive)?.[1]?.toLowerCase() ?? "patch";
  const [major, minor, patch] = current;
  const target = bump === "major"
    ? [major + 1, 0, 0]
    : bump === "minor"
      ? [major, minor + 1, 0]
      : [major, minor, patch + 1];
  return `v${target.join(".")}`;
}

if (/(?:^|[\\/])release-version\.mjs$/.test(process.argv[1] ?? "")) {
  const [latestTag = "", commitMessage = ""] = process.argv.slice(2);
  const changelog = readFileSync("packaging/debian/changelog", "utf8");
  process.stdout.write(`${nextReleaseVersion(latestTag, commitMessage, changelog)}\n`);
}