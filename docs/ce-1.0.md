# No-dal CE 1.0

CE 1.0 means you can install No-dal with one command on Debian 13 amd64
(or the manual repo path, or the installer ISO), finish first-run in the
browser, run VMs, system containers, and OCI apps, protect them with
snapshots and backups, read metrics/logs/events, open Terminal and Files,
assign GPUs, join a second node, migrate and restore, use the Store
without root scripts, and optionally use BYO-AI through structured
actions. No Cloud or EE key is required.

That definition is the milestone. This tree has not reached it.

## Docs

- [install.md](install.md) one-line, manual repo, ISO
- [uninstall.md](uninstall.md) remove does not delete workload data
- [recovery.md](recovery.md) control plane and agent stop leave guests
- [backup.md](backup.md)
- [migration.md](migration.md)
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

The CE 1.0 definition is not reached until Debian 13 Homelab and
cluster hardware gates pass. Cloud unit tests, API fixtures, and
checklist documents are not that gate.

This tree is honest about the following gaps:

- Migrate is unwired until a dest agent is connected (`Migrate` stays
  nil and returns dest-agent-not-connected). Two-box live migrate is
  not proven here.
- Operate must use existing APIs. Approve must not Host.Exec. Restart
  and Store install now invoke those handlers. Policy create still
  writes the store and is not the finished engine.
- SkipHostCmds must fail closed. Cloud engines that skip host commands
  must stay unavailable or unverified and must not invent success.
- The installer ISO is not booted in this tree. mkosi config is not a
  spare-PC install.
- Packages are unsigned here. Signed install remains the documented
  HTTPS repo and keyring path.
- Ubuntu is not Tier 1.

This tree also does not claim multi-master HA, live Trivy on this host,
a production Store CA, live kubelet, or live Ceph/`rbd map` on this
Cloud agent host. Virt and physical checklists are documents, not
executed proof.
