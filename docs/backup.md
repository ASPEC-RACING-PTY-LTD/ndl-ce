# Backup

Snapshots are not backups. A snapshot is a point-in-time restore on the
same pool. A backup copies data to a destination.

## Policies

A policy is the operational object. Scope is `all` (the current eligible
fleet, including workloads created later) or `selected` (an explicit
workload list). All is the default. One policy can cover many workloads.
Run now executes the policy against its current scope. One policy
execution runs at a time. A second Run now (or a nightly tick) against
any policy returns 409 while another execution is in progress. Fleet
workload concurrency stays at 1. Eligible means
the root disk can be copied with any supported method: ZFS send when the
pool is ZFS, qcow2 flatten for Directory VMs, or a filesystem archive
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
