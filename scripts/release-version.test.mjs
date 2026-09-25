import test from "node:test";
import assert from "node:assert/strict";
import { nextReleaseVersion } from "./release-version.mjs";

const changelog = "nodal (1.0.9) UNRELEASED; urgency=medium\n";

test("only a commit subject beginning DEPLOY: can create a release version", () => {
  assert.throws(() => nextReleaseVersion("v1.0.9", "fix: DEPLOY: patch", changelog));
  assert.throws(() => nextReleaseVersion("v1.0.9", "deploy: patch", changelog));
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: patch release", changelog), "v1.0.10");
});

test("defaults DEPLOY releases to patch and supports explicit semantic bumps", () => {
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: build", changelog), "v1.0.10");
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: minor", changelog), "v1.1.0");
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: major", changelog), "v2.0.0");
});

test("accepts an explicit greater semver and uses the Debian changelog as first baseline", () => {
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: 1.1.0 publish", changelog), "v1.1.0");
  assert.equal(nextReleaseVersion("", "DEPLOY: patch", "nodal (1.0.10) UNRELEASED; urgency=medium\n"), "v1.0.11");
  assert.throws(() => nextReleaseVersion("v1.1.0", "DEPLOY: 1.0.10", changelog));
});

test("does not calculate a release below the current Debian package version", () => {
  assert.equal(
    nextReleaseVersion("v1.0.9", "DEPLOY: patch", "nodal (1.0.10) UNRELEASED; urgency=medium\n"),
    "v1.0.11",
  );
});