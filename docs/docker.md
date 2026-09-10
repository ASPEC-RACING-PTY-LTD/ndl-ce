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

Unprivileged system containers use the `lxc-container-ndl-nesting`
AppArmor profile. That profile allows Docker Engine, containerd, and
BuildKit bind mounts, including read-only rbind of containerd overlay
snapshots onto `/var/lib/docker/tmp/buildkit-mount*`. Containers are
not unconfined.

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
