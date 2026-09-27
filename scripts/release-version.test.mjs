import test from "node:test";
import assert from "node:assert/strict";
import { nextReleaseVersion } from "./release-version.mjs";

const changelog = "nodal (1.0.8) UNRELEASED; urgency=medium\n";

test("only a commit subject beginning DEPLOY: can create a release version", () => {
  assert.throws(() => nextReleaseVersion("v1.0.8", "fix: DEPLOY: patch", changelog));
  assert.throws(() => nextReleaseVersion("v1.0.8", "deploy: patch", changelog));
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: patch release", changelog), "v1.0.9");
});

test("defaults DEPLOY releases to patch and supports explicit semantic bumps", () => {
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: build", changelog), "v1.0.9");
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: minor", changelog), "v1.1.0");
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: major", changelog), "v2.0.0");
});

test("minor and patch roll over after 9", () => {
  assert.equal(nextReleaseVersion("v1.0.9", "DEPLOY: patch", changelog), "v1.1.0");
  assert.equal(nextReleaseVersion("v1.4.9", "DEPLOY: patch", changelog), "v1.5.0");
  assert.equal(nextReleaseVersion("v1.9.9", "DEPLOY: patch", changelog), "v2.0.0");
  assert.equal(nextReleaseVersion("v1.9.3", "DEPLOY: minor", changelog), "v2.0.0");
});

test("rolls a legacy two-digit baseline forward to the next minor", () => {
  assert.equal(
    nextReleaseVersion("", "DEPLOY: patch", "nodal (1.0.12) trixie; urgency=medium\n"),
    "v1.1.0",
  );
  assert.equal(nextReleaseVersion("v1.0.12", "DEPLOY: patch", changelog), "v1.1.0");
});

test("accepts an explicit greater semver and uses the Debian changelog as first baseline", () => {
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: 1.1.0 publish", changelog), "v1.1.0");
  assert.equal(nextReleaseVersion("", "DEPLOY: patch", "nodal (1.0.7) UNRELEASED; urgency=medium\n"), "v1.0.8");
  assert.throws(() => nextReleaseVersion("v1.1.0", "DEPLOY: 1.0.9", changelog));
});

test("refuses explicit versions with a minor or patch above 9", () => {
  assert.throws(() => nextReleaseVersion("v1.0.8", "DEPLOY: 1.0.10", changelog), /go up to 9/);
  assert.throws(() => nextReleaseVersion("v1.0.8", "DEPLOY: 1.10.0", changelog), /go up to 9/);
  assert.equal(nextReleaseVersion("v1.0.8", "DEPLOY: 10.0.0", changelog), "v10.0.0");
});

test("does not calculate a release below the current Debian package version", () => {
  assert.equal(
    nextReleaseVersion("v1.0.7", "DEPLOY: patch", "nodal (1.0.8) UNRELEASED; urgency=medium\n"),
    "v1.0.9",
  );
});
