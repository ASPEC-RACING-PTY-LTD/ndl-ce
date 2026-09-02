# CE 1.0 virt checklist

These items are the in-tree virt/e2e scripts under `packaging/e2e`.
This document is the checklist, not executed proof. This Cloud agent
host is not a Debian 13 KVM appliance. Do not tick these as proven on
Ubuntu Cloud VMs. Ticking items here does not mark CE 1.0 reached.

1. Package build via `packaging/e2e/rebuild-packages.sh`
2. Phase 31-43 accept scripts where a local control plane can log in.
   Health-only or SQL-only runs print SMOKE_OK and are not roadmap
   acceptance.
3. `go test ./...` and UI vitest in CI
4. Control plane stop does not stop `nodal-vm@` units (recovery.md)

Unsigned or skipped virt cases stay skipped. Do not invent a green
KVM run.
