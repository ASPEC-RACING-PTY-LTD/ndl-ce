# CE 1.0 physical checklist

Run on a real Debian 13 amd64 host. This document is the checklist.
It is not executed proof and is not evidence that this Cloud agent
ran it. Ticking items here does not mark CE 1.0 reached.

The executable version of this checklist is the automated harness in
`packaging/cert/` (see [ce-1.0-certification.md](ce-1.0-certification.md)).
Prefer running that harness on two physical nodes: it records
PASS / FAIL / BLOCKED-PHYSICAL per gate with evidence and refuses to
report success from mocks. The manual items below map to those gates.

1. One-line or manual repo install of `nodal`, open `/setup`
2. Create a Directory pool, a network, a VM, a system container, an OCI app
3. Snapshot and a local or object backup restore
4. GPU assign if hardware is present; skip honestly if not
5. Join a second node, migrate a VM, restore to the dest
6. Store official sample install
7. Ask "Why did this workload restart?" after a restart event
8. `systemctl stop ndl-control` and `ndl-agent` leave guests running
9. Uninstall does not delete `/var/lib/ndl`
10. License page with no key stays CE. Entering a key with the API down
    does not stop workloads
