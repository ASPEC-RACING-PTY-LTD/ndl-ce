# No-DAL CE physical dogfood progress

Host: Debian 13 amd64 (`no-dal`). Application data is disposable. Disposable admin is `dogfood-admin`.

Branch: `cursor/root-backed-pools-and-lxc-extract-f531`

## Areas tested

- Package rebuild and overlay install of `nodal` 1.0.1 on this host
- Bootstrap wipe and setup with disposable `dogfood-admin` (not the operator's account)
- Service start/restart of `ndl-agent` and `ndl-control`
- Intentionally root-backed Directory pools `local` (`/var/lib/ndl/storage/local`) and `dogfood-root` (`/srv/ndl-dogfood`): status `warning` with `root_filesystem`, not unavailable
- Isolated network `dogfood-iso` (`10.64.0.0/24`, bridge `ndl1d83a55c`, dnsmasq DHCP)
- Image cache remains `0640` root:root with no `user:100000` ACL
- Unprivileged Debian 13 system-container create (stdin extract), start, stop, restart, remount after umount
- Bounded `.img` loop volume (2 GiB then grown to 3 GiB)
- Memory cgroup `memory.max=536870912` on `lxc.payload.<id>`
- MAC persist `02:00:00:00:00:d1`, duplicate MAC rejected `409`
- Isolated DHCP via guest `ndl-dhclient` (`10.64.0.156`)
- Persistence across `ndl-agent` / `ndl-control` restart
- Task success/failure recording for create/start/stop
- nodalctl and HTTP API
- UI login, dashboard, workloads, storage (local is warning)
- Dedicated tmpfs pool fail-closed to `unavailable` after backing unmount
- `ndl-ct-prepare` remounts the loop image before `lxc-start`

## Failures reproduced

1. Root-backed Directory pools treated as disappeared dedicated mounts
2. Unprivileged extract could not open `0640` cache by pathname
3. Loop `.img` not remounted after reboot/umount
4. `mount -o loop,nouuid` failed: Debian 13 ext4 rejects XFS `nouuid` via `fsconfig`
5. Invalid MAC still reached volume allocate (before reorder)
6. `lxc.net.0.ipv6.address = none` rejected by LXC 6; create reported running while `nodal-ct@` failed
7. Unprivileged Debian 13 `systemd-networkd` crash-loop `226/NAMESPACE`; no IPv4 DHCP
8. `resize2fs` refused until `e2fsck -f`; truncate-then-fail left a 3 GiB image with a 2 GiB filesystem
9. System-container API `spec` parsed empty JSON as VM qcow2 defaults

## Root causes

- Observation used `RootBacked == false` as "dedicated mount lost" even when identity still matched `/`
- Observe did not persist backing onto pool rows
- Mapped tar opened the cache by path inside the user namespace
- Directory roots were mkfs/mounted only at create
- `nouuid` is not an ext4 mount option on util-linux fsconfig
- MAC validation ran after `prepareRoot`
- LXC 6 does not accept address sentinel `none`
- systemd-networkd sandbox (RestrictNamespaces, ProtectKernel*, BindReadOnlyPaths=/sys) cannot unshare mounts in mapped LXC; even after a drop-in, `.network` files were not applied
- resize2fs requires a force check; same-size retry skipped fsck after a partial grow
- `specJSON` called `vmspec.Parse` on `{}` for every kind

## Fixes implemented (commits)

- `4cdfa8c` Keep intentionally root-backed Directory pools available
- `8553412` Remount sized Directory container roots; privileged stdin extract
- `1de5a09` Mount Directory container roots without XFS nouuid
- `7842a87` Stop writing LXC address=none for disabled IP families
- `30b78cd` Enable IPv4 DHCP in unprivileged Debian 13 containers
- `6726a0a` Keep system-container API spec empty and chown guest net files
- `b4a7ad5` Run e2fsck before resize2fs when growing Directory roots

## Tests/builds performed

- `go test ./internal/storage ./internal/lxc ./internal/httpapi` targeted runs pass, including live ext4 loop mount, live userns extract of a `0640` archive, MAC-before-volume, e2fsck-before-resize
- Packages rebuilt with `packaging/e2e/rebuild-packages.sh` and installed over 1.0.1
- Physical create/start/stop/restart/grow/DHCP/remount/agent-restart verified on this host

## Physical-host verification

- CT `dogfood-ct` (`ae69160a-862d-4cef-acfe-d643e824f677`) Debian 13, unprivileged, running
- Root loop mount `/dev/loop0` on `.../c6d7e30c-....img`, guest `df` 2.9G after grow
- Cache file mode `0640`, no extra ACL
- UI: login, dashboard 1 running workload, workloads list shows dogfood-ct, storage local=warning

## Remaining areas

- No pool-delete API, so `dedicated-tmpfs` and `dedicated-bind` stay `unavailable` as leftover fail-closed evidence
- Full host reboot not executed (loop remount proven via umount + `ndl-ct-prepare` / start)
- VM/OCI/backup/migration/HA/Kubernetes not dogfooded in this pass
- GitHub PR create is blocked (`must be a collaborator`); branch is pushed
- Phase 18 UEFI tests fail on this host because OVMF is installed (unrelated)

## Blocked items

- Opening a GitHub pull request: forge API returns `must be a collaborator`
- Operator personal account is off-limits and was not used
