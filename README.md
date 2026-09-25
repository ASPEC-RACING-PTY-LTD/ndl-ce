# No-dal Community Edition

Open-source infrastructure platform. Control plane, node agent, API,
CLI, and web UI.

CE 1.0 is the target milestone and is not reached until Debian 13
Homelab and cluster hardware gates pass. Debian 13 amd64 is the only
Tier 1 host. No Cloud or EE key is required. See `docs/ce-1.0.md`.

On a clean Debian 13 amd64 host the bootstrap installs the `nodal`
metapackage, starts services, and the operator claims `/setup`.

## Install

See `docs/install.md`. Public convenience command:

```text
curl -fsSL https://raw.githubusercontent.com/ASPEC-RACING-PTY-LTD/ndl-ce/main/packaging/bootstrap/get-nodal.sh | sudo sh
```

The command fetches the bootstrap directly from GitHub. Use
`NODAL_APT_KEY_URL`, `NODAL_APT_REPO`, and
`NODAL_DEV_REPO=1` against a local test repository. Signed production
packages use the documented HTTPS repo and keyring path. This tree
does not mint those signatures here.

## Alpha builds

GitHub evaluates the release workflow on pushes to `main`, but its build and
release job is skipped unless the commit subject starts with `DEPLOY:`. It
defaults to the next patch version; use
`DEPLOY: minor` or `DEPLOY: major` for those increments, or include an
explicit greater version such as `DEPLOY: 1.1.0`. The workflow tests and
builds the project, updates the Debian package version, and publishes the
Debian packages as a GitHub prerelease. Other commits do not create releases
or advance the published version.

## Host support

Debian 13 amd64 is the only Tier 1 host. Other distributions fail
closed and install nothing. Ubuntu is not claimed as Tier 1.

## Docs

- `docs/ce-1.0.md`
- `docs/install.md`
- `docs/uninstall.md`
- `docs/recovery.md`
- `docs/backup.md`
- `docs/cluster.md`
- `docs/store.md`
- `docs/ai.md`
- `docs/api-compatibility.md`
- `docs/checklists/ce-1.0-virt.md`
- `docs/checklists/ce-1.0-physical.md`
- `CONTRIBUTING.md`
