# Workload lifecycle and spec edits

Workloads are host-supervised systemd units (`nodal-ct@`, `nodal-vm@`,
`nodal-oci@`). They do not `BindsTo`, `PartOf`, or `Requires` the agent
or control plane. Stopping, restarting, or overlaying `ndl-agent` /
`ndl-control` must not stop guests. Autostart is systemd enablement
(`WantedBy=nodal-workloads.target`) and applies on **host boot**, not
when the agent process returns.

A second start of an already-running unit is a no-op. The agent adopts
the live unit and does not launch a duplicate.

## Edit vs Summary

Summary is a monitoring surface: CPU, memory, storage, network,
runtime, Guest Agent, and Docker health when the Docker feature is on.
Clone, migrate, USB, and spec fields are not on Summary.

Edit is a header control (pencil). Save never restarts unless **Restart
after save** is checked (default off). Changes that cannot apply live
become Pending Restart.

## System containers

| Field | Running | Stopped |
| --- | --- | --- |
| CPUs | Live (`lxc-cgroup` `cpu.max`) | Written for next start |
| Memory | Live (`lxc-cgroup` `memory.max`) | Written for next start |
| Disk grow (directory) | Live grow, optional guest filesystem expand (default on) | Grow without unmount |
| Hostname / name | Pending restart (`lxc.uts.name` and `/etc/hostname`) | Written for next start |
| IP / DNS | Pending restart | Written for next start |
| MAC | Requires stop | Applied |
| Disk shrink | Unsupported | Unsupported |
| Autostart | Live systemd enable/disable | Same |

GPU, nesting, and TUN last-applied flags are preserved across spec
apply. `CreateCT` is not used to patch a running container.

## System container storage

Open a system container and choose **Storage** in its sidebar to mount extra
folders into it, for example a large HDD for a media server's library.

- **New folder on a storage pool** creates an empty folder on a directory,
  ZFS, NFS or SMB pool and gives it to the container's root user. On ZFS it
  is its own dataset, created on the pool's disks, and the size is its
  quota. The folder is recorded as an operator-owned volume, so orphan
  cleanup never removes it.
- **Existing folder on the host** mounts a folder that is already there.
  Its files and ownership are not changed. In an unprivileged container,
  root is host UID 100000 by default, so files must be owned by that UID
  for the container to write to them.

Saving only edits the container's mount list. It does not stop, restart or
rebuild the container, and it never deletes, formats or re-owns existing
data. A running container loads new mounts the next time it restarts; the
page offers a restart and asks before doing it. Unmounting removes the
mount from the container only: the host folder and its files stay.

Pool folders that are their own filesystem (a ZFS dataset or a pool image)
are checked at every start. If one is not mounted, the container does not
start, so it can never write into the empty folder on the host disk
underneath it. Clones do not inherit mounts.

## Virtual machines

CPU, memory, firmware, and most spec fields are Pending Restart while
the VM is running. Save records desired spec against the frozen launch
config. Optional **Restart after save** stops, reprepares from the
**updated** spec, then starts. QEMU CPU/memory hotplug is not claimed.

## OCI applications

CPU/memory patches update desired spec. Power changes still go through
OCI lifecycle. Clone and migrate stay on Operations.

## Disk growth

Directory container roots can grow while running. The image is
extended and `resize2fs` can run on the mounted filesystem. The guest
is not unmounted. ZFS and LVM-thin grow go through their pool APIs.
Shrink is refused.

Multi-disk VM growth is not a live CT cgroup path. Stopped shrink is
only allowed when a later path can prove it is safe; it is not
implemented here.

## Management updates

Package updates restart the management plane only. Guests keep running.
The Updates page reports management as temporarily unavailable during an
apply. It does not say the infrastructure is restarting.
