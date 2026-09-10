# Guest baseline

Fresh Debian system containers get a No-DAL guest baseline that matches the
practical install compatibility of common Debian LXC helper scripts, without
embedding those scripts.

## LXC runtime

Unprivileged by default. Nesting (Docker/containerd/runc/BuildKit) stays the
complete nested-engine feature set: generated AppArmor, `allow_nesting`,
`seccomp.allow_nesting`, FUSE bind, `userns.conf` (keyctl stays), and the
start-host hook. That set is not reduced to a single `nesting=1` line.

Optional, off by default:

- TUN (`c 10:200 rwm` plus `/dev/net/tun`)
- `mknod`
- root SSH (`PermitRootLogin yes` after OpenSSH is installed)
- `python_system_pip` (removes Debian `EXTERNALLY-MANAGED`)

GPU and other host devices stay on Add Features / workload assignment.

## Rootfs sanity

Extract uses umask `022`. After extract, No-DAL validates `/` and `/etc` are
traversable by non-root service users such as `_apt`, and that `/root`, `/tmp`,
`/var/tmp`, and apt paths have expected Debian modes. Unprivileged guests are
checked against the mapped UID/GID (`u/g 0 100000 65536`), not host root 0.

A host umask that leaves `/` or `/etc` as `0750` is how `_apt` loses DNS while
root networking still works. Creation no longer assumes the template is sane.
Host-written No-DAL files (`etc/ndl`, DNS fallback drop-ins) are chowned onto
the mapped UID/GID so they are not `nobody` inside the guest.

## Readiness

Start does not succeed on `lxc-start` alone. No-DAL waits for an init PID and,
when IPv4 is enabled, an address on `eth0`. Guest bootstrap then checks DNS
for Debian repositories and GitHub names, and TCP/443 when ICMP is unusable.

DHCP guests keep systemd-resolved with `/etc/resolv.conf` pointing at
`/run/systemd/resolve/resolv.conf`. If that setup cannot resolve repositories,
a resolved `FallbackDNS` drop-in (`8.8.8.8`, `1.1.1.1`) is added. A healthy
resolved symlink is not replaced with a comment-only static file.

## Packages

Helper-script Debian list (literal): `sudo`, `curl`, `mc`, `gnupg2`, `jq`.

No-DAL extras (distinct, small): `ca-certificates`, `wget`, `iproute2`.

Nano is still copied from the host when missing. Git and Docker are not
installed by this baseline. Fresh containers run `apt-get update` and
`apt-get upgrade` once, noninteractively. Existing containers only install
missing packages. Interrupted `dpkg` is repaired before retry. Mandatory
first-boot package failure fails provisioning.

## Locale, timezone, boot, console

`LANG`, `LC_ALL`, and `LANGUAGE` are `C.UTF-8` in `/etc/environment`.
`/etc/timezone` exists and `/etc/localtime` points at the selected zone.
`systemd-networkd-wait-online.service` is masked. `systemd-homed` first-boot
units are masked so they cannot wait for interactive input.

The workload terminal is a root login shell. `container-getty` /
`console-getty` autologin as root with `TERM=linux`. SSH sessions may use
`xterm-256color`. Root SSH is not enabled unless requested.

## Python

Debian's `EXTERNALLY-MANAGED` marker is kept by default. Helper scripts remove
it so `pip install` works system-wide (PEP 668 would otherwise refuse). That
makes installers easier and weakens package-manager isolation. Set
`python_system_pip` on the spec only when an installer requires it.

## Existing containers

`ndl-ct-prepare`, start, and agent reconcile apply missing files idempotently.
They do not overwrite application config, secrets, repositories, databases,
Docker volumes, SSH keys, or user networking. Existing guests are not
OS-upgraded during reconcile. Deleting a system container removes its exclusive
container-root volume. Disks still attached to another workload are left alone.

## Storage accounting

Directory (and other thin/sparse) pools treat volume size as a logical limit,
not a reservation. Pool available space is physical StatFS free space minus a
16 MiB safety floor. Aggregate 50 GiB sparse roots may exceed the pool's
physical size. Workload start is not rejected for that overcommit. Physical
low-free warnings stay informational until the filesystem is actually exhausted.

Unprivileged nested Docker also needs host keyring quota. See `docs/docker.md`.
