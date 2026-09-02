# Backup

Snapshots are not backups. A snapshot is a point-in-time restore on the
same pool. A backup copies data to a destination.

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
the existing workload and requires confirm. Cross-node restore uses the
dest node chosen at restore time. Restore onto a worker whose dest
agent is not connected stays unavailable and does not copy disks onto
the control node. Failed restore does not delete the source backup.

See also [recovery.md](recovery.md).
