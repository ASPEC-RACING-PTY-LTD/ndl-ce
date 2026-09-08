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
| Proxmox VE | source + compatible export | REST discovery, QEMU/LXC config translation, HTTP download of directory/NFS/CIFS file volumes, LXC vzdump tar/tar.gz/tar.zst, automatic temporary vzdump for LXC on LVM-thin/ZFS when a downloadable backup store exists | LVM-thin/ZFS zvol/RBD VM disks are not HTTP-downloadable; PBS backups cannot be downloaded; vma vzdump has no extractor; live and snapshot-assisted are unavailable; export does not call `qm create` |
| libvirt/KVM | source | Domain XML plus QEMU-compatible disks | No virsh, no libvirt runtime |
| Disk / archive | both | QCOW2, RAW, VMDK, VHD (vpc), VHDX via qemu-img; container tar/tar.gz/tar.zst | Missing VM hardware is not invented |
| OVF / OVA | both | Parse OVF/OVA, convert disks, write OVF package | Not a remote vSphere create |
| Existing backup | source | Completed artifacts this engine can validate, including local LXC tars | Unreadable formats fail closed |

VMware and Hyper-V guests are imported when they are already in an open disk
or OVF form. There is no proprietary VDDK or Hyper-V WMI adapter in V1.

## Strategy and methods

The operator chooses one global risk profile for the selection. That is
intent, not a per-workload transfer method.

| Strategy | Consistency | What the engine does |
| --- | --- | --- |
| Consistent copy (default) | SAFE | Prefer Offline on a stopped guest, then an existing backup, then disk import. Running guests that need Offline must be stopped on the source. |
| Leave sources running | SOURCE SAFE | Prefer existing backups or disk import. Offline is used only when the guest is already stopped. |
| Existing backups | SAFE | Backup import only. Workloads without a usable backup are blocked. |
| Minimal interruption | RISKY | Would use snapshot or live. Unavailable in V1. |

After the strategy is chosen, No-dal picks a compatible method, storage
map, network map, and compatibility result for every selected workload.
Proxmox LXC guests whose rootfs is LVM-thin, ZFS, or another
non-downloadable backend are planned as a temporary vzdump when a
directory, NFS, or CIFS backup store exists. Those guests never enter
Transfer with a raw rootfs download. If a temporary vzdump cannot be
created, Review blocks that guest with the exact reason. The temporary
archive is deleted after verification. Operator backups are not deleted.
Live and snapshot-assisted are never auto-selected. Per-workload method
overrides stay in Advanced. No-dal does not silently fall back to a
riskier method than the plan.

| Mode | Consistency | Notes |
| --- | --- | --- |
| Offline | SAFE | Source must already be stopped. No-dal will not stop it. For Proxmox VMs, also requires a downloadable file volume. LXC on LVM-thin or ZFS uses a temporary vzdump instead of Offline. |
| Snapshot-assisted | LOW RISK | Listed so the risk model is visible. V1 adapters do not create source snapshots. Unavailable. |
| Live | RISKY, NO GUARANTEES | Listed so the risk is visible. V1 does not perform live transfer. Unavailable. Requires acknowledgement if a future adapter enables it. |
| Existing Backup | SAFE | Imports a captured artifact. Operator backups are never deleted. A temporary vzdump created for LXC on non-downloadable storage is deleted after verification. |
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
arguments are allowlisted. Endpoints are http(s) only. A Proxmox token must be
user@realm!tokenid=secret (example root@pam!nodal=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx),
not the secret UUID alone. Tokens are redacted from audit and JSON.

## Operator flow

Import / Export in the UI, or `nodalctl migration ...`.

CLI covers adapters, modes, sources, discover, compatibility, plan, start,
job status, cancel, retry, staging cleanup, disk/bundle import, and export.

1. Connect a source. Discovery runs automatically and does not start a transfer.
2. Select workloads (Select all for a bulk move).
3. Choose one global strategy.
4. Review the automatic plan for every selected workload. Warnings and
   mapping choices appear only when a decision is ambiguous or blocked.
   Advanced keeps per-workload method and mapping overrides.
5. Submit. Watch progress.
6. Read the verification report. Source remains unchanged.

## Failure

Failures name the stage, keep the source untouched, identify partial No-dal
destination/staging, and allow cleanup of No-dal-owned artifacts only.

## Tests and acceptance

See `packaging/e2e/phase44-accept.sh`. Disposable workload procedures (offline
VM, system container, backup, live/snapshot where supported, export round trip,
interrupt/failure) must run on a machine with real guests. Cloud unit tests
and `/health` are not that gate.
