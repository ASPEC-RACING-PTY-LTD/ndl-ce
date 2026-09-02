# No-dal CE 1.0

CE 1.0 means you can install No-dal with one command on Debian 13 amd64
(or the manual repo path, or the installer ISO), finish first-run in the
browser, run VMs, system containers, and OCI apps, protect them with
snapshots and backups, read metrics/logs/events, open Terminal and Files,
assign GPUs, join a second node, migrate and restore, use the Store
without root scripts, and optionally use BYO-AI through structured
actions. No Cloud or EE key is required.

## Docs

- [install.md](install.md) one-line, manual repo, ISO
- [uninstall.md](uninstall.md) remove does not delete workload data
- [recovery.md](recovery.md) control plane and agent stop leave guests
- [backup.md](backup.md)
- [cluster.md](cluster.md)
- [store.md](store.md)
- [ai.md](ai.md)
- [api-compatibility.md](api-compatibility.md)
- [checklists/ce-1.0-virt.md](checklists/ce-1.0-virt.md)
- [checklists/ce-1.0-physical.md](checklists/ce-1.0-physical.md)

## License surface

Settings, License can store an EE key for a later upgrade without
reinstall. Activation talks to a licensing API only when a key is
present. If that API is unreachable, grace applies and workloads keep
running. CE does not ship EE blobs or private repo credentials.

## Honesty

This CE 1.0 tree does not claim Ubuntu as Tier 1, multi-master HA,
live Trivy on this host, a production Store CA, live kubelet, live
Ceph/`rbd map` on this Cloud agent host, or that production apt
packages were signed in this environment. Signed install continues to
use the documented HTTPS repo and keyring path.
