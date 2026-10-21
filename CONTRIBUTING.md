# Contributing to No-dal Community Edition

This repository is the open-source infrastructure platform. It is not
the marketing site.

## Phase order

Implement one numbered phase from `ndl-ce_complete_roadmap.plan.md` at
a time. Phase 0 is foundation only.

## Commands

From the repository root:

```text
go test ./...
go vet ./...
gofmt -w .
node scripts/ci/check-em-dashes.mjs
node scripts/ci/check-bootstrap-size.mjs
node scripts/ci/check-package-structure.mjs
node scripts/ci/check-ci-policy.mjs
node scripts/ci/check-codegen.mjs
node scripts/generate-openapi.mjs
buf generate
buf lint
```

From `ui/`:

```text
pnpm install
pnpm lint
pnpm typecheck
pnpm test
```

## Rules

- No Unicode em dash characters.
- No generic host command RPC.
- The control plane is unprivileged. The agent is the typed privileged
  process.
- Do not add Ubuntu as a supported host until Phase 29.
- Do not put PostgreSQL or service-user logic in the bootstrap script.
- UI and CLI consume the supported API. Do not hide important actions
  in the frontend only.
- Do not add fake infrastructure data.

## Test package repositories

The bootstrap fetches from GitHub and installs signed packages from the
documented HTTPS repository and keyring path. Use `NODAL_APT_KEY_URL`,
`NODAL_APT_REPO` and `NODAL_DEV_REPO=1` against a local test repository.
The release workflow signs the APT repository with a persistent key held in
the repository secrets; no signing key is stored in this tree.

## Releases

GitHub evaluates the release workflow on pushes to `main`, but its build and
release job is skipped unless the commit subject starts with `DEPLOY:`. It
defaults to the next patch version; use
`DEPLOY: minor` or `DEPLOY: major` for those increments, or include an
explicit greater version such as `DEPLOY: 1.1.0`. Minor and patch versions
are single digits and roll over after 9: 1.0.9 is followed by 1.1.0, and
1.9.9 by 2.0.0. Explicit versions such as `1.0.10` are refused. The workflow tests and
builds the project, stamps the top `UNRELEASED` entry of
`packaging/debian/changelog` with the release version, tags it, pushes the
stamped changelog back to `main`, and publishes the Debian packages as a
GitHub prerelease whose notes are that changelog entry. Other commits do not
create releases or advance the published version.

Record user-visible changes in the top `UNRELEASED` changelog entry before a
`DEPLOY:` commit; a release without one fails. Keep that entry's version at
the latest released version; the release calculates the next version from it.

## Host support

No-DAL runs on Debian 13 amd64. Other distributions fail closed and
install nothing.

## Release checklists

- `docs/ce-1.0.md`
- `docs/checklists/ce-1.0-virt.md`
- `docs/checklists/ce-1.0-physical.md`
