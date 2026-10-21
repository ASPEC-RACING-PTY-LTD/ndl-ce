# No-DAL Community Edition

No-DAL, also written NoDAL, No DAL or Nodal, is an open-source,
self-hosted Linux hypervisor and infrastructure platform built on stock
Debian 13 primitives. Community Edition (No-DAL CE) manages KVM/QEMU
virtual machines, LXC system containers, native OCI application
containers (via containerd), game servers, storage, networking and
encrypted, deduplicated backups. A lean node agent does the work,
managed by an unprivileged control plane with its state in PostgreSQL,
through one API, CLI (`nodalctl`) and web UI.

The Debian metapackage is `nodal` and the CLI is `nodalctl`. No-DAL CE
is in production, past 1.0, and runs a homelab of more than 40 projects
every day. No Cloud or EE key is required. Website: https://no-dal.com

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
packages use the documented HTTPS repo and keyring path. The release
workflow signs the APT repository with a persistent key held in the
repository secrets; no signing key is stored in this tree.

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

## Docs

- `docs/ce-1.0.md`
- `docs/install.md`
- `docs/uninstall.md`
- `docs/management.md`
- `docs/workload-lifecycle.md`
- `docs/docker.md`
- `docs/guest-baseline.md`
- `docs/migration.md`
- `docs/recovery.md`
- `docs/backup.md`
- `docs/backup-engine-v2.md`
- `docs/cluster.md`
- `docs/store.md`
- `docs/ai.md`
- `docs/api-compatibility.md`
- `docs/checklists/ce-1.0-virt.md`
- `docs/checklists/ce-1.0-physical.md`
- `CONTRIBUTING.md`
