# No-dal Community Edition

Open-source infrastructure platform. Control plane, node agent, API,
CLI, and web UI.

CE 1.0. Debian 13 amd64 is the only Tier 1 host. No Cloud or EE key is
required. See `docs/ce-1.0.md`.

On a clean Debian 13 amd64 host the bootstrap installs the `nodal`
metapackage, starts services, and the operator claims `/setup`.

## Install

See `docs/install.md`. Public convenience command:

```text
curl -fsSL https://get.no-dal.com | sudo sh
```

The live URL may still be a placeholder. The in-repo script is the
same contract. Use `NODAL_APT_KEY_URL`, `NODAL_APT_REPO`, and
`NODAL_DEV_REPO=1` against a local test repository. Signed production
packages use the documented HTTPS repo and keyring path. This tree
does not mint those signatures here.

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
