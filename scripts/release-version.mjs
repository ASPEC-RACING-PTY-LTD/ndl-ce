import { readFileSync } from "node:fs";

const VERSION_PATTERN = /^v?(\d+)\.(\d+)\.(\d+)$/;
// Minor and patch are single digits: 1.0.9 is followed by 1.1.0, and 1.9.9
// by 2.0.0. Older baselines such as 1.0.12 are read, then rolled over.
const DIGIT_LIMIT = 9;

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

function carry([major, minor, patch]) {
  if (patch > DIGIT_LIMIT) {
    minor += 1;
    patch = 0;
  }
  if (minor > DIGIT_LIMIT) {
    major += 1;
    minor = 0;
    patch = 0;
  }
  return [major, minor, patch];
}

function bumpVersion([major, minor, patch], bump) {
  if (bump === "major") return [major + 1, 0, 0];
  if (bump === "minor") return carry([major, minor + 1, 0]);
  return carry([major, minor, patch + 1]);
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
    if (target[1] > DIGIT_LIMIT || target[2] > DIGIT_LIMIT) {
      throw new Error(`Minor and patch versions go up to ${DIGIT_LIMIT}; use the next minor or major version instead`);
    }
    if (compareVersions(target, current) <= 0) {
      throw new Error("The requested release version must be newer than the current version");
    }
    return `v${target.join(".")}`;
  }

  const bump = /^(major|minor|patch)\b/i.exec(directive)?.[1]?.toLowerCase() ?? "patch";
  return `v${bumpVersion(current, bump).join(".")}`;
}

if (/(?:^|[\\/])release-version\.mjs$/.test(process.argv[1] ?? "")) {
  const [latestTag = "", commitMessage = ""] = process.argv.slice(2);
  const changelog = readFileSync("packaging/debian/changelog", "utf8");
  process.stdout.write(`${nextReleaseVersion(latestTag, commitMessage, changelog)}\n`);
}