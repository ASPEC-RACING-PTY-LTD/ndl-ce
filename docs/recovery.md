# Recovery

## Control plane down

Stopping or killing `ndl-control` leaves the host unchanged.
Workloads are not bound to the control plane or the agent
(`BindsTo` / `PartOf` / `Requires` are forbidden on workload units).
`systemctl stop ndl-control` does not stop guests.

The agent socket and process can also stop without tearing down
workloads that have already been started by systemd.

## Replay setup

`/setup` is a single-use first-admin claim. After an admin exists,
replaying setup is refused. There is no factory password to reuse.

## recover-admin

`nodalctl recover-admin` is a local host operation. It requires
root (or equivalent) on the machine. It is not a remote reset and
it is not a default credential.

## Identity that persists

`cluster_id` and `node_id` are minted on first boot and stored on
disk under `/var/lib/ndl` and in the `nodal` database. Removing or
restarting packages does not mint new identities. Uninstall does
not delete those paths. A later destroy command is required to wipe
them.

## Product VMs (Phase 8)

Product VMs use `nodal-vm@<uuid>.service` and `/usr/sbin/ndl-qemu-launch`.
systemd owns the QEMU process. Stopping `ndl-control` or `ndl-agent` does
not stop a running VM.

Desired spec is compiled to a frozen launch artifact before start. Paths,
TAP names, unit names, QMP sockets, and PCI addresses are locators, not
product identity. MAC and PCI layout are persisted in the VM spec and
frozen launch config.

QEMU runs as `ndl-qemu`, not root. Phase 8 keeps a shared `ndl-qemu` uid
across VMs. AppArmor (when a parent QEMU profile exists) still allows
`/var/lib/ndl/storage/** rwk`, so one VM process can reach sibling disks.
That residual cross-VM isolation limit is accepted for Dogfood Host.

Delete removes VM configuration, TAP devices, cidata, and per-VM UEFI vars.
Attached persistent volumes are preserved unless a later phase adds an
explicit destructive confirm.

qemu-img convert is refused while the destination disk is attached to a
running unit. Live disk mutation is an error, not a warning.

Console uses `compute.console` and a short-lived ticket. It does not grant
`terminal.open`. VM Terminal and Files remain Phase 20.

### Recovery matrix (product VM)

1. Create and start a VM through the supported API, UI, or `nodalctl workload create --kind vm`.
2. QMP connects (`query-status` / `query-pci` when the unit is live).
3. Serial console works without a guest agent. Graphical console is a ticketed unix VNC socket.
4. `systemctl stop ndl-control` leaves the VM running.
5. `systemctl stop ndl-agent` leaves the VM running.
6. Restart control and agent. The VM is rediscovered from systemd plus last-applied state.
7. QMP reconnects.
8. Repeated start does not create a second `nodal-vm@` unit.
9. A QEMU crash is observed as crashed or failed, not healthy running.
10. Graceful stop and force-stop both work.
11. Restart reapplies the current desired spec to frozen argv while the VM is stopped, then starts it.
12. Autostart is systemd enablement of `nodal-vm@`. That is not the same proof as a live host reboot.
13. PCI addresses in last-applied are compared to live `query-pci` when QMP is up.
14. NIC MAC values persist across restart and non-replacing spec edits.
15. Unavailable storage refuses start and records an honest unavailable status.
16. qemu-img against a live attached disk is refused.
17. Failed prepare cleans TAP devices that No-dal can prove belong to that VM.
18. Expired or unauthorized console tickets are refused.

