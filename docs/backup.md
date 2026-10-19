# Backup

Snapshots are not backups. A snapshot is a point-in-time restore on the
same pool. A backup copies data to a destination.

## Policies

A policy is the operational object. Workload scope is `selected` (an
explicit workload list; the default) or `all` (the current eligible
fleet, including workloads created later). All must be chosen
deliberately. An `all` policy can leave workloads out
(`excluded_workload_ids`, "Leave out" in the policy editor, shown only for
All workloads): they are never backed up by it and are not counted as
unprotected in recovery health, while every other workload, new ones
included, stays covered. Switching a policy to `selected` drops its
exclusions. One policy can cover many workloads, and one workload can
belong to more than one policy.

Directory system containers also have a capture mode: `full` (default,
complete recoverable filesystem), `custom` (detected categories plus
include/exclude), or `smart` (application data only). Full Machine is
the normal path because later backups reuse chunks instead of storing
another full OS copy. All three modes use Backup Engine V2. Smart and
Full do not walk guest filesystems in the policy editor. Custom discovery
loads only when the operator configures one selected workload.
Run now executes the policy against its current scope. One policy
execution runs at a time. A second Run now (or a nightly tick) against
any policy returns 409 while another execution is in progress. Different
workloads in that policy capture concurrently up to the workspace
capture-concurrency limit. The same workload cannot capture twice.
Eligible means
the root disk can be copied with any supported method: ZFS send when the
pool is ZFS, qcow2 flatten for Directory VMs, or Backup Engine V2 content-addressed capture
for Directory system containers. A running Directory container is copied
live and stays running. No-DAL never freezes, pauses, suspends, or stops
a guest for backup. Directory copies are crash-consistent. Stronger
consistency is optional and application-aware: if
`/etc/ndl/hooks/backup-pre` or `/etc/ndl/hooks/backup-post` exist and
are executable in the guest, they run via `lxc-attach` around the live
copy. Extra disks, iSCSI, and distributed
volumes are skipped as resources. They do not make a backupable root
ineligible. A run with nothing eligible returns 422.

`last_run_at` is stamped once when a policy execution starts, not after
each workload finishes. Nightly due-ness is therefore "about a day after
the policy began", not "a day after the last guest completed".

On control restart, leftover `running` rows become `interrupted` so a
crashed execution cannot block nightly forever.

Current Directory backups never write cgroup freeze leases. Agent
startup still thaws leftover `/run/ndl/backup-freeze` leases from older
agents that froze guests. Freezes without a lease are left alone. A
watchdog thaws owned leftover leases older than 30 minutes.

Object packs reuse one HTTP client per agent socket and one S3
transport per pack. Chunk PUT and HEAD overlap with a bound of 4
in-flight object RPCs inside a single backup. That does not start a
second guest.

## Snapshots

Create snapshots from a workload Snapshots tab or `nodalctl`. Rollback
and flatten require confirm headers. Directory VM snapshots are qcow2
overlays. ZFS and LVM-thin use their native snapshot mechanisms when
those pools are in use.

## Destinations

Phase 11 local and NFS/SMB destinations, plus Phase 23 object storage
(S3, R2, B2, MinIO). Credentials stay in secrets. Encrypt-before-upload
is a backup-engine option, not a promise that the destination is empty.

## Restore

Restore as new creates a new workload UUID. Restore replace overwrites
the existing workload and requires confirm. System container archives
restore the rootfs tree plus recorded config (cpus, memory, idmap,
privileged, image pin, NICs). Cross-node restore uses the dest node
chosen at restore time. Restore onto a worker whose dest
agent is not connected stays unavailable and does not copy disks onto
the control node. Failed restore does not delete the source backup.

Backup of additional VM data disks is not implemented. Those disks are
skipped and listed on the run plan. The supported root disk is still
copied. Restore replace of a workload that still has extra disks is
refused so generations are not mixed. Restore as new restores the
included root only.

See also [recovery.md](recovery.md).

## Workload files and backup rules

**Backups → Workload files and backup rules** shows a system container's
folders and files with their size, when they changed and a plain label
("Database", "Logs", "Docker: nextcloud", "System"…). Sort by name (default),
size or date, and open folders to go deeper. Folder sizes are measured on
request and shown as "≥" when measuring ran out of time.

Each item can be set to:

- **Policy**: follow the policy's backup scope (the default);
- **Skip**: never back it up;
- **Keep**: always back it up, even inside a skipped folder or outside the
  policy's scope.

The most specific path wins, so a kept folder inside a skipped one is still
backed up. **Save rules** writes them into the container itself at
`/.ndl-backup/rules.json`, so they travel with the workload and its backups.
Every backup of the workload reads that file first and applies it on top of
the policy. A damaged rules file is reported in the agent log and ignored,
which only means more is backed up. The file is written without following
links, so a container cannot use it to reach the host.

## Deleting backups

- **Delete** on a restore point removes it here and in cloud storage.
- **Delete all** in the Backup size details, or **Delete all backups** in the
  workload files box, removes every backup of one workload in one step
  (`POST /api/v1/backups/workloads/{id}/delete`). Protected backups are kept
  unless `include_protected=true` is given.
- Space is reclaimed in the background by the next cleanup.

SuperAdmins are not asked to confirm plain "are you sure" prompts; prompts that
collect something, such as a password, still open.
