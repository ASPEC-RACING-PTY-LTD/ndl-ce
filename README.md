# No-dal Community Edition

Open-source infrastructure platform. Control plane, node agent, API,
CLI, and web UI.

This tree is Phase 1: first boot. On a clean Debian 13 amd64 host the
bootstrap installs the `nodal` metapackage, starts services, and the
operator claims `/setup`. There are no workloads yet.

## Install

See `docs/install.md`. Public convenience command:

```text
curl -fsSL https://get.no-dal.com | sudo sh
```

The live URL may still be a placeholder. The in-repo script is the
same contract. Use `NODAL_APT_KEY_URL`, `NODAL_APT_REPO`, and
`NODAL_DEV_REPO=1` against a local test repository.

## Host support

Debian 13 amd64 is the only Tier 1 host. Other distributions fail
closed and install nothing.

## Docs

- `docs/install.md`
- `docs/uninstall.md`
- `docs/recovery.md`
- `CONTRIBUTING.md`
