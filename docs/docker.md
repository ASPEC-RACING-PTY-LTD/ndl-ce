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

## Device access

Every unprivileged system container gets a complete cgroup2 device
allowlist that starts from deny-all:

```
lxc.cgroup2.devices.deny = a
lxc.cgroup2.devices.allow = c 1:3 rwm     # /dev/null
lxc.cgroup2.devices.allow = c 1:5 rwm     # /dev/zero
lxc.cgroup2.devices.allow = c 1:7 rwm     # /dev/full
lxc.cgroup2.devices.allow = c 1:8 rwm     # /dev/random
lxc.cgroup2.devices.allow = c 1:9 rwm     # /dev/urandom
lxc.cgroup2.devices.allow = c 5:0 rwm     # /dev/tty
lxc.cgroup2.devices.allow = c 5:1 rwm     # /dev/console
lxc.cgroup2.devices.allow = c 5:2 rwm     # /dev/ptmx
lxc.cgroup2.devices.allow = c 136:* rwm   # /dev/pts/*
lxc.cgroup2.devices.allow = c 10:229 rwm  # /dev/fuse, nesting only
```

TUN adds `c 10:200 rwm`. `allow_mknod` adds `c *:* m` and `b *:* m`
(mknod only, no read or write). Each assigned GPU node adds one exact
`c MAJOR:MINOR rwm` rule read from the host node, so dynamic majors such
as `nvidia-uvm` are never hardcoded. A `/dev/dri/by-path/*` locator also
mounts the canonical `renderD*` or `card*` node it points at, because
VAAPI, NVENC, and libdrm enumerate those names. When a node is missing on
the host, well-known names (`renderD<N>`, `card<N>`, `nvidia<N>`,
`nvidiactl`) keep an exact rule derived from the name; any other missing
node gets no rule and stays denied. There are no `195:*`, `226:*`, or `a`
allow rules.

The list is written in full because Debian's `userns.conf` clears the
`common.conf` device rules for unprivileged guests, and liblxc attaches a
default-deny eBPF device program as soon as one rule is present. Appending
only GPU or TUN rules therefore denied `/dev/null`, `/dev/zero`, and
`/dev/pts`, which broke `nvidia-smi`, `dockerd`, and interactive shells.
Privileged containers keep the `common.conf` allowlist and only append
their feature and GPU rules.

The LXC config is generated from last-applied on every start and restart
(`ndl-ct-prepare`), on agent startup, on GPU assign or unassign, and on
Reapply from the workload's Diagnostics page. Unassign keeps the
workload's other GPUs. Do not hand-edit
`/var/lib/ndl/runtime/lxc/<uuid>/config`; the next start overwrites it. A
running container keeps its loaded device program until it restarts, so
Reapply never restarts on its own and reports when a restart is still
needed.

### NVIDIA with Docker and CDI

Assign the GPU in render, compute, or encode mode; the node list is
derived for you. An NVIDIA claim includes the per-GPU `/dev/nvidia<N>`
(the minor the driver reports in
`/proc/driver/nvidia/gpus/<pci>/information`), `/dev/nvidiactl`,
`/dev/nvidia-uvm`, `/dev/nvidia-uvm-tools`, `/dev/nvidia-modeset`, and the
GPU's DRI render and card nodes. `nvidia-caps` is never included, because
`caps/nvidia-cap1` grants MIG configuration of every GPU on the host.
Assign is refused until inventory reports the NVIDIA minor, so refresh
node inventory after the driver loads. The host must have created those
nodes before the container starts; `nvidia-uvm` nodes appear only after the module loads
(for example after `nvidia-modprobe -u -c=0` or the first CUDA call on the
host). A node created later is picked up on the next container start.

Inside the container, install the NVIDIA userspace matching the host
driver without its kernel module, install `nvidia-container-toolkit`, and
generate the CDI spec with `nvidia-ctk cdi generate`. Nested Docker
containers can only use devices the system container itself is allowed,
so `docker run --device nvidia.com/gpu=all` exposes exactly the assigned
nodes.

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
