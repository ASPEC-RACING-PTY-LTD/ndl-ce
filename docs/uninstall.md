# Uninstall No-dal

The package manager owns installed files. Dangerous cleanup is a
separate, explicit command. The bootstrap script does not uninstall.

## apt remove

```text
apt-get remove nodal ndl-control ndl-agent ndl-ui nodalctl
```

This stops `ndl-control`, `ndl-agent`, and `ndl-agent.socket`.
Binaries and units are removed.

It does **not** delete:

- `/var/lib/ndl` (control-plane state, later workload disks)
- the PostgreSQL `nodal` database
- workload data, container roots, backups, or storage pools

## apt purge

```text
apt-get purge nodal ndl-control ndl-agent ndl-ui nodalctl
```

Purge may remove No-dal config under `/etc/ndl` (including
`control.env`). Workload disks, `/var/lib/ndl` data, and the
database still stay.

## Data wipe

Wiping state is a separate explicit command with a typed confirm.
That destroy path is not implemented in Phase 1. Until it exists,
do not delete `/var/lib/ndl` or drop the `nodal` database unless
you intend to destroy the appliance by hand.
