# No-DAL CE physical dogfood progress

Host: Debian 13 amd64 (`no-dal`). Application data is disposable.

## Areas tested

- Live install present: `nodal` 1.0.1, `ndl-agent` and `ndl-control` running.
- Existing Directory pool `local` at `/var/lib/ndl/storage/local` is on `/` (`root_backed: true`) and currently `warning` (root filesystem headroom), not unavailable.
- Image cache exists at `/var/lib/ndl/cache/lxc-images/.../*.tar.xz` mode `0640` root:root.

## Failures reproduced

1. Root-backed Directory pools vs disappeared mounts
   - Observation currently fail-closes when `expected.RootBacked == false` and the covering filesystem is root, even when UUID identity still matches.
   - ReconcileStorage does not persist `root_backed` / `mount_point` from observe, so legacy records with a root UUID and missing `root_backed` become unavailable.
2. Unprivileged LXC rootfs extract
   - `lxc-usernsexec` tar cannot open a `0640` root-owned cache archive (Permission denied) unless an ACL `user:100000:r--` is present. That ACL is a permission weakening and must not be the product path.
   - Mapped tar also cannot write a `0750` root-owned dest, and cannot path-traverse `0750` pool directories. Privileged parent must open the archive and chdir/chown the dest.

## Root causes

- `observeRoot` treats "not marked root_backed" as "dedicated mount disappeared" even when the pool was always on `/`.
- Mapped extract uses pathname access to the protected cache.

## Fixes implemented

- See commits on this branch.

## Remaining areas

- Fresh package install/upgrade, setup, networks, CT lifecycle, memory/disk/MAC, persistence, UI, reboot remount.

## Blocked items

- None yet.
