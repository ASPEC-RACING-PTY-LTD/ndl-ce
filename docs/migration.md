# Import, Export, and Migration

Copy-first. Source destruction is not a migration operation.

A completed migration means: **Migration verified. Source remains unchanged.**

The operator decides what happens to the original infrastructure afterward.
No-dal never deletes source workloads, disks, snapshots, backups, or
configuration. There is no "delete source after migration" control.

CE includes this capability. It is not gated on a license or Cloud.

## Architecture

Source Adapter
to Discovery
to Normalized No-dal Migration Manifest (`ndl.migration.manifest.v1`)
to Compatibility Analysis
to Migration Plan
to Transfer / Conversion
to No-dal Workload Creation
to Verification

Export uses the same manifest, then a destination adapter. Direct export (the
open No-dal bundle) is distinct from a compatible export package (OVF, Proxmox
notes plus disks). The UI and API label those honestly.

The engine is vendor-neutral. Adapters implement discovery, metadata, storage
read, capabilities, conversion, compatibility, and verification. The engine
does not delete source objects. Staging cleanup removes only No-dal-owned
temporary files.

## Manifest

Schema: `ndl.migration.manifest.v1`

A portable JSON document with typed VM and container sections. It records
identity, source metadata, CPU/memory, disks, firmware, NICs, cloud-init,
mount points, UID/GID maps, tags, checksums, and export metadata.

Third parties can import or export this document. It does not depend on No-dal
Cloud or Enterprise Edition.

Bundle layout:

- `manifest.json`
- `disks/`
- `rootfs/`
- `metadata/`
- `checksums/sha256.json`

## Supported V1 adapters

Listed only when a real path exists.

| Adapter | Role | What works | What does not |
| --- | --- | --- | --- |
| No-dal portable bundle | both | Round-trip import/export with checksums | Not a hypervisor remote create |
| Proxmox VE | source + compatible export | REST discovery, QEMU/LXC config translation, HTTP download of directory/NFS/CIFS file volumes, LXC vzdump tar/tar.gz/tar.zst | LVM-thin/ZFS zvol/RBD disks are not HTTP-downloadable; vma vzdump has no extractor; live and snapshot-assisted are unavailable; export does not call `qm create` |
| libvirt/KVM | source | Domain XML plus QEMU-compatible disks | No virsh, no libvirt runtime |
| Disk / archive | both | QCOW2, RAW, VMDK, VHD (vpc), VHDX via qemu-img; container tar/tar.gz/tar.zst | Missing VM hardware is not invented |
| OVF / OVA | both | Parse OVF/OVA, convert disks, write OVF package | Not a remote vSphere create |
| Existing backup | source | Completed artifacts this engine can validate, including local LXC tars | Unreadable formats fail closed |

VMware and Hyper-V guests are imported when they are already in an open disk
or OVF form. There is no proprietary VDDK or Hyper-V WMI adapter in V1.

## Modes

The operator selects the mode. No-dal does not silently fall back.

| Mode | Consistency | Notes |
| --- | --- | --- |
| Offline | SAFE | Source must already be stopped. No-dal will not stop it. For Proxmox, also requires a downloadable file volume. |
| Snapshot-assisted | LOW RISK | Listed so the risk model is visible. V1 adapters do not create source snapshots. Unavailable. |
| Live | RISKY, NO GUARANTEES | Listed so the risk is visible. V1 does not perform live transfer. Unavailable. Requires acknowledgement if a future adapter enables it. |
| Existing Backup | SAFE | Imports a captured artifact. Backup file is never deleted. |
| Disk / Archive | SOURCE SAFE | Destination compatibility depends on the input. |

Source safety is always PROTECTED and is independent of consistency.

## Compatibility

Per workload: READY, WARNING, REQUIRES MAPPING, UNSUPPORTED, BLOCKED.

Historical snapshot trees are not required for basic migration. If they are
not transferred, the plan says so before start.

## Transfer and jobs

Jobs use the existing task architecture (`migration.import`, `migration.export`).

Progress includes stage, bytes when known, conversion, verification, and
errors. Cancel removes No-dal staging only. Retry/resume is modeled on the
job; adapters that cannot resume restart the incomplete artifact after
re-validating source identity.

qemu-img convert runs as a typed agent method (`DiskConvert`). There is no
Host.Exec. Control plane remains unprivileged.

## Verification levels

Only observed levels are claimed:

- transfer complete
- configuration verified
- boot verified
- guest reachable
- application verified

Creating a workload row is not success by itself.

Destination boot is optional (`start_after`). If the source appears online and
the destination keeps the same MAC, start requires identity-conflict
acknowledgement.

## Permissions

- `migration.read`
- `migration.import`
- `migration.export`
- `migration.manage`

Viewing a workload does not grant migration rights. Viewer is read-only.
Operator may import, export, and manage sources. Credentials are stored in
`secrets.migration_source_credentials` and are never returned to the browser.

## Security

External sources are untrusted. Archives are extracted with path, symlink,
device, and size checks. Manifests are schema-validated. Disk convert
arguments are allowlisted. Endpoints are http(s) only. Tokens are redacted
from audit and JSON.

## Operator flow

Import / Export in the UI, or `nodalctl migration ...`.

CLI covers adapters, modes, sources, discover, compatibility, plan, start,
job status, cancel, retry, staging cleanup, disk/bundle import, and export.

1. Select source and connect (discovery does not start a transfer).
2. Select workloads.
3. Map storage and networks. Individual overrides are allowed.
4. Select a mode. Read the consistency rating.
5. Compatibility check.
6. Review the actual plan.
7. Start. Watch the job.
8. Read the verification report. Source remains unchanged.

## Failure

Failures name the stage, keep the source untouched, identify partial No-dal
destination/staging, and allow cleanup of No-dal-owned artifacts only.

## Tests and acceptance

See `packaging/e2e/phase44-accept.sh`. Disposable workload procedures (offline
VM, system container, backup, live/snapshot where supported, export round trip,
interrupt/failure) must run on a machine with real guests. Cloud unit tests
and `/health` are not that gate.
