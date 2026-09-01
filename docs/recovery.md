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
