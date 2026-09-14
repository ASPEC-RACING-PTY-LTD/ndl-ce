# CE 1.0 physical checklist

The executable version of this checklist is the automated harness in
`packaging/cert/` (see [ce-1.0-certification.md](ce-1.0-certification.md)).
It records PASS / FAIL / BLOCKED-PHYSICAL per gate with evidence and
refuses to report success from mocks.

## Executed proof

Harness run `ce10-phys-9` on Debian GNU/Linux 13 (trixie) host `no-dal`
is **integration evidence only**. Node B was KVM guest `cert-lab-nodeb`
(`192.168.2.183`, worker `7d07167c-f939-4bda-a90a-2a433245de88`).
Gate 20 rebooted that guest, not Node A. Gate 21 reinstalled `1.0.6`.
Those three required physical gates are `BLOCKED-PHYSICAL` /
`BLOCKED-PHYSICAL/RELEASE-ARTIFACT` until proven honestly. Report:
`/var/lib/ndl/cert/ce10-phys-9/report.md`.

Node A is this physical Debian 13 appliance (`192.168.2.82`).
Production `nodal-ct@` count stayed 18. The harness must not treat a
guest as the second physical node, must not PASS without a Node A
reboot, and must not count same-version reinstall as an upgrade.

The manual items below map to those gates.

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
