# No-dal Community Edition

Open-source infrastructure platform. Control plane, node agent, API,
CLI, and web UI.

This is Phase 0 foundation. It is not an installable product yet.

## Layout

- `cmd/` `ndl-control`, `ndl-agent`, `nodalctl` skeletons
- `proto/nodal/agent/v1` Connect plus Protobuf southbound RPC
- `api/openapi` northbound stub
- `ui` Vite plus React plus TypeScript skeleton
- `internal/hostos` host detection (Debian 13 amd64 only)
- `packaging/debian` package stubs and `nodal` metapackage
- `packaging/bootstrap/get-nodal.sh` small installer stub
- `systemd` unit drafts
- `.github/workflows/ci.yml` fast static CI

## Host support

Debian 13 amd64 is the initial Tier 1 host. Other distributions are
not supported in this phase.

## Docs

See `CONTRIBUTING.md`. Product vision and architecture live beside
this repository in the workspace planning documents.
