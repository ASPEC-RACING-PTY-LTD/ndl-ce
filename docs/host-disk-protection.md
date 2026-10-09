# Host disk protection

No-dal and PostgreSQL usually share the host's root disk. If that disk
fills, PostgreSQL stops and No-dal goes down with it. The agent now guards
against that.

## What is watched

Every 30 seconds `ndl-agent` checks the filesystems holding `/`, the data
directory (`/var/lib/ndl`) and PostgreSQL (`/var/lib/postgresql`). Each is
rated:

| Level     | Free space below                                   |
| --------- | -------------------------------------------------- |
| Warning   | 15% of the disk, between 10 GiB and 200 GiB        |
| Critical  | 8% of the disk, between 5 GiB and 30 GiB           |
| Emergency | 3% of the disk, between 2 GiB and 10 GiB           |

On an 868 GiB disk that is roughly 130 GiB, 30 GiB and 10 GiB free, on
top of the 50 GiB reserved for No-dal (below).

## What happens

- **Warning**: an event is raised and the Storage page shows it.
- **Critical**: bulk writes to that disk are refused with a message saying
  why: backups, backup downloads, file restores, disk conversions,
  migrations, archive extraction, image and file uploads, new volumes and
  containers, VM snapshots, update checkpoints, package installs and game
  server installs. A banner shows on every page. Anything that frees space
  (deleting, expiring backups, cancelling, uploading to remote storage) is
  never refused. Writes to a different disk, such as a ZFS pool on its own
  HDD, are not affected.
- **Emergency**: the agent deletes its reserve file, so No-dal and
  PostgreSQL have room to keep running and you can free space from the UI.

## Space reserved for No-dal

50 GiB of the disk holding `/var/lib/ndl` is permanently reserved for
No-dal (at most 10% of a small disk). The reserve
(`/var/lib/ndl/reserve/ballast`) is a preallocated file with no data in it:
nothing else, including workloads on a pool that shares the root disk, can
use that space. If the disk fills anyway, the agent releases the reserve
automatically, so the control plane keeps working and you never need SSH to
recover.

The reserve is taken in steps: on a fuller disk it holds what fits above
the critical threshold and grows as space frees up. After an emergency it
is taken again once the disk is healthy. Filesystems that cannot
preallocate (ZFS root) do not keep one. **Release reserve now** on the
Storage page deletes it immediately; it then stays released for six hours.

## Freeing space

**Storage, Host disk protection, What is using space** measures the data
directory by category and shows how much of the disk is used outside
No-dal. **Clean up** is offered only for disposable files:

- Update checkpoints: keeps the newest.
- Backup, migration and file-restore staging: removes entries whose newest
  file is older than 24 hours, and never the staging of a running backup or
  migration.
- Container image cache: images are downloaded again when needed.

Storage pools, workload disks, the backup repository, local backup targets
and game server data are never cleaned up here.

## Update checkpoints

A checkpoint holds control-plane state (configuration, certificates,
secrets, container and network definitions) and a PostgreSQL dump. It no
longer copies workload disks, pools, backups, staging, game data, image
caches or earlier checkpoints, and it stays on one filesystem. Only the
three newest checkpoints are kept.
