# CE 1.0 physical certification procedure

This is the executable CE 1.0 release-gate procedure for two physical Debian 13
amd64 machines. It is driven by the automated harness in `packaging/cert/`, not
by ticking boxes. The harness records `PASS`, `FAIL`, or `BLOCKED-PHYSICAL` for
each gate with evidence and refuses to report success from mocks.

CE 1.0 is not certified until every required gate is `PASS` on real hardware.
A `BLOCKED-PHYSICAL` gate means the harness could not execute it here; it is
never a pass.

## Harness

- `packaging/cert/ce-1.0-certify.sh` runs the 28 gates on node A.
- `packaging/cert/lib.sh` is the shared library: results ledger, safety guards,
  disposable-resource registry and cleanup, and control-plane wrappers.
- `packaging/cert/selftest.sh` validates the harness itself in any environment
  (guards refuse non-disposable and denylisted names; the verdict fails loudly
  on `FAIL` or `BLOCKED-PHYSICAL`). Run it in CI: `bash packaging/cert/selftest.sh`.

Exit status of the main harness:

- `0` every required gate `PASS` (no `FAIL`, no `BLOCKED-PHYSICAL`).
- `1` at least one `FAIL` (a real defect); CE 1.0 is not certified.
- `2` no defects, but `BLOCKED-PHYSICAL` gates remain; not yet certified.

## Absolute safety

The harness only mutates resources it creates, all prefixed `cert-<run-id>`. It
refuses any name that is not so prefixed and any name containing a
production/denylisted token (for example `Skila`). It never inspects secrets,
never freezes/pauses/stops/restarts a production workload, never restores over
production, and never fills a filesystem. Workspace and host-reserve limits are
exercised with small artificial settings, not by filling real storage. Cleanup
removes only tracked disposable resources.

Management-plane restart (gate 19) is permitted because workloads are
independent systemd units; the harness restarts `ndl-control`/`ndl-agent` and
asserts the disposable workload's `MainPID` is unchanged. It never restarts a
production `nodal-ct@`/`nodal-vm@` unit.

## Prerequisites

- Two machines running a fresh, supported Debian 13 amd64 install (gate 1).
- No-dal installed from the signed repository on both (gate 2), first-run setup
  completed in a browser on node A (gate 3).
- For the remote-protection and DR gates: an R2/S3 backup target already created
  in No-dal. Provide its id and coordinates by environment variable. Secret
  values are read by the product from its own secret store; never pass secret
  values to the harness.
- For KVM: `/dev/kvm` present and a small disposable cloud image.

## Environment variables

Set on node A before running. Missing optional variables make their gates
`BLOCKED-PHYSICAL` rather than silently skipped.

| Variable | Purpose |
|---|---|
| `CERT_I_UNDERSTAND=disposable-only` | required acknowledgement to run |
| `NODAL_URL` | control-plane URL (default `http://127.0.0.1:8080`) |
| `NODAL_TOKEN` | API token for gates that use the HTTP API |
| `CERT_NODE_B` | second node address (join, dest-agent, migration) |
| `CERT_R2_TARGET_ID` | existing backup target id for R2/S3 |
| `CERT_R2_BUCKET`, `CERT_R2_ENDPOINT` | target coordinates (names only) |
| `CERT_VM_IMAGE` | small disposable cloud image for the KVM gate |

## Run

On node A, after both machines are installed and node A setup is complete:

```sh
# Inspect the plan first.
packaging/cert/ce-1.0-certify.sh --plan

# Create an API token and an R2/S3 target in No-dal, then:
export CERT_I_UNDERSTAND=disposable-only
export NODAL_TOKEN=...            # from nodalctl access token create
export CERT_NODE_B=10.0.0.2       # second node
export CERT_R2_TARGET_ID=...      # from nodalctl backup target create
export CERT_R2_BUCKET=... CERT_R2_ENDPOINT=...
export CERT_VM_IMAGE=/var/lib/ndl/images/debian-13-genericcloud-amd64.qcow2

sudo -E packaging/cert/ce-1.0-certify.sh
```

The report is written to `/var/lib/ndl/cert/<run-id>/report.md` with a per-gate
evidence directory. Re-run after resolving any `FAIL`. CE 1.0 is certified only
when a run reports `PASS` for every required gate and exits `0`.

## Gates

1. Fresh supported Debian 13 installation
2. No-dal package/repository installation
3. Browser first-run setup
4. Management services healthy
5. Disposable LXC creation/start/network
6. Disposable KVM/QEMU VM creation/start/network
7. Docker/nested-container functionality where supported
8. Snapshots
9. Backup Engine V2 Smart/Custom/Full (plus a second incremental capture and
   the zero-freeze invariant asserted from `cgroup.freeze`)
10. R2/S3 upload to remote verification to Protected
11. Destroy disposable source, restore-as-new, validate the seeded canary data
12. Second physical node join
13. Authenticated mTLS/WireGuard destination-agent connectivity
14. Offline/live migration per actual CE 1.0 support
15. Destination object pull
16. Restored/migrated workload boots and runs on the destination
17. Source is not duplicated after migration
18. Failed migration leaves the source safely running
19. Management-plane restart does not terminate workloads
20. Host reboot/autostart
21. Package upgrade preserving workloads/config/auth
22. License/API outage grace behavior
23. Store signed application install
24. Monitoring/events/tasks
25. Terminal/Files/guest-agent functionality
26. Network safety/rollback
27. GPU/IOMMU path if hardware exists
28. Final cleanup of disposable certification resources

## Backup and DR chain (gates 9 to 11)

The harness proves the full chain on a disposable workload:

live disposable workload -> V2 capture -> Local Complete -> upload queue ->
R2/S3 upload -> remote verification -> Protected -> destroy disposable original
-> restore-as-new -> start -> verify the seeded disposable canary data.

It also exercises Smart, Custom, and Full capture modes, a second incremental
capture, and asserts the zero-freeze invariant by sampling the workload's
`cgroup.freeze` throughout capture. Bandwidth limiting, retention and GC against
the disposable repository only, workspace-limit and host-reserve behavior (with
small artificial limits), and interrupted-upload recovery are validated on the
appliance where the physical repository and target exist.
