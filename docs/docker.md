# Docker Management

Docker Management is an optional No-DAL feature. It is off on a fresh
install. Enable it from Add Features or `nodalctl feature enable docker`.
That installs the `nodal-feature-docker` marker package. It does not
install Docker Engine and does not start `dockerd`.

While the feature is disabled, the control plane does not probe Docker
sockets and the agent keeps no Docker event cache.

## What it discovers

When enabled, No-DAL looks for Docker Engine API sockets:

- Host: `/var/run/docker.sock` and `/run/docker.sock`
- Running system containers: `/proc/<init-pid>/root/run/docker.sock`
  and `/var/run/docker.sock` in the guest root

Virtual machines are not probed. Nested Docker in a VM needs a guest
agent path that this feature does not add.

## Nested Docker in system containers

Unprivileged system containers stay namespace-isolated from the host.
They are not privileged and not AppArmor-unconfined. Guest root gets the
LXC nested-engine feature set required to run Docker, containerd, runc,
BuildKit, and Compose inside that container:

```
lxc.apparmor.profile = generated
lxc.apparmor.allow_nesting = 1
lxc.seccomp.allow_nesting = 1
lxc.mount.auto = proc:mixed sys:rw cgroup:mixed
lxc.mount.entry = /dev/fuse dev/fuse none bind,optional,create=file 0 0
lxc.hook.version = 1
lxc.hook.start-host = /usr/lib/ndl/ndl-lxc-nesting-apparmor
```

Unprivileged guests also include `/usr/share/lxc/config/userns.conf`,
which leaves `lxc.cap.drop` and `lxc.cap.keep` empty so keyctl and the
rest of the user-namespace capability set stay available. No-DAL does
not add a keyctl-blocking seccomp filter. Debian `common.seccomp` still
blocks `kexec_load`, `open_by_handle_at`, and module load/unload.

Those guests share one host uid map (`0 -> 100000`). Nested Docker
creates session keyrings against that host uid. Debian's default
per-uid key quota (200) is shared across every unprivileged container
on that map. No-DAL ships `usr/lib/sysctl.d/ndl-lxc-keys.conf` and
raises `kernel.keys.maxkeys` / `kernel.keys.maxbytes` at agent start
so runc is not rejected with "unable to create session key: disk quota
exceeded". Guests cannot write those sysctls. The agent never lowers an
already sufficient host value.

The generated nesting profile is LXC's nested-container policy (overlay,
bind, rbind, cgroup, fuse). The start-host hook patches that generated
profile on liblxc versions that still emit `/proc` and `/sys`
write-denials when nesting is on. Current LXC omits those denials for
nesting because the guest can already mount its own proc and sys.
Without that, runc's detached procfs reconstructs namespaced sysctls
such as `net.ipv4.ip_unprivileged_port_start` as `/sys/net/...` and
AppArmor denies them. The hook is a no-op when the generated profile
already matches current LXC. No-DAL does not maintain a static mount
allowlist and does not add a blanket `mount,` rule of its own.

The feature set is stored as `nesting: true` on last-applied. Create,
start, restart, `ndl-ct-prepare`, and agent startup rewrite LXC config
from last-applied so reboot and reconcile cannot drop it. Existing
system containers are migrated the same way. A running guest keeps its
current kernel AppArmor label until the next start.

See `docs/guest-baseline.md` for Debian package, DNS, locale, console,
and Python-compat behaviour that runs around this feature set.

Nested Docker can create containers, namespaces, cgroups, overlay
mounts, bind/rbind mounts, networking, volumes, BuildKit workloads,
and namespaced sysctls. It cannot:

- Load kernel modules or change host-global kernel state (`sys_module`,
  `sys_time`, host-scoped sysctls outside the guest network namespace)
- `mknod` arbitrary host devices (No-DAL has no seccomp-notify device
  daemon)
- See host filesystems or devices that were not explicitly attached

## Hierarchy

The Docker page is Machine → Compose project → containers.

Compose grouping uses engine labels:

- `com.docker.compose.project`
- `com.docker.compose.service`
- `com.docker.compose.project.working_dir`
- `com.docker.compose.project.config_files`

Containers without those labels sit in a Standalone group for that
machine. A flat container view is on the same page.

## Health

Container health uses Docker state, healthchecks, OOM, exit codes,
restart loops, and recent Engine events. Update failures (pull or
recreate) are a separate qualifier: `Running · Update Failed`.

Projects and machines roll up to Healthy, Degraded, or Critical. A
unreachable daemon is Critical for that machine.

Near-real-time updates come from the Docker events API with a periodic
list/inspect reconciliation.

## Actions

Operators can start, stop, restart, read logs, open a terminal (`docker
exec`), pull, and recreate. Terminals open in the existing Terminal
workspace.

CLI:

```
nodalctl feature enable docker
nodalctl docker show
nodalctl docker logs host CONTAINER
nodalctl docker restart MACHINE CONTAINER
```

Disable with `nodalctl feature disable docker --confirm disable-feature`.
Existing containers keep running. Discovery stops.
