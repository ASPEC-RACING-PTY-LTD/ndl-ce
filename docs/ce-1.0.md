# No-dal CE 1.0

CE 1.0 means you can install No-dal with one command on Debian 13 amd64
(or the manual repo path, or the installer ISO), finish first-run in the
browser, run VMs, system containers, and OCI apps, protect them with
snapshots and backups, read metrics/logs/events, open Terminal and Files,
assign GPUs, join a second node, migrate and restore, use the Store
without root scripts, and optionally use BYO-AI through structured
actions. No Cloud or EE key is required.

That definition is the milestone. The 28-gate physical certification
harness is the executable gate. Run `ce10-phys-9` produced useful
single-node and virtual two-node integration evidence on this Debian
13 homelab, but three required physical gates were too permissive and
are no longer a CE 1.0 PASS:

- gate 12 (second physical node)  -  Node B was a KVM guest
- gate 20 (host reboot/autostart)  -  only the guest was rebooted
- gate 21 (package upgrade)  -  `1.0.6 -> 1.0.6` is not a version upgrade

CE 1.0 is not physically certified until those gates PASS honestly.
The installer ISO is still not booted in this tree.

## Docs

- [install.md](install.md) one-line, manual repo, ISO
- [uninstall.md](uninstall.md) remove does not delete workload data
- [recovery.md](recovery.md) control plane and agent stop leave guests
- [workload-lifecycle.md](workload-lifecycle.md) live vs pending restart, Edit, disk growth
- [backup.md](backup.md)
- [migration.md](migration.md)
- [cluster.md](cluster.md)
- [store.md](store.md)
- [ai.md](ai.md)
- [docker.md](docker.md)
- [guest-baseline.md](guest-baseline.md)
- [management.md](management.md)
- [api-compatibility.md](api-compatibility.md)
- [checklists/ce-1.0-virt.md](checklists/ce-1.0-virt.md)
- [checklists/ce-1.0-physical.md](checklists/ce-1.0-physical.md)

## License surface

Management, License can store an EE key for a later upgrade without
reinstall. The License page lives under Management. Activation talks to a licensing API only when a key is
present. If that API is unreachable, grace applies and workloads keep
running. CE does not ship EE blobs or private repo credentials.

## Honesty

The CE 1.0 physical hardware gates are the 28-gate harness in
`packaging/cert/ce-1.0-certify.sh`. Cloud unit tests, API fixtures,
and unchecked documents are not that gate. A KVM/QEMU/container Node B,
an unarmed Node A reboot, or a same-version reinstall must record
`BLOCKED-PHYSICAL` (or `BLOCKED-PHYSICAL/RELEASE-ARTIFACT`) rather than
PASS. Virtual two-node testing may be recorded as integration evidence
only.

This tree is honest about the following gaps:

- Dest-listen offline migrate, dest object pull, dest boot, and dest
  guest reboot/autostart passed on physical node A plus a disposable
  Debian 13 KVM worker (`192.168.2.183`, worker
  `7d07167c-f939-4bda-a90a-2a433245de88`). That is integration evidence,
  not two physical chassis, not a Node A reboot, and not a real package
  upgrade. Live CRIU LXC migrate remains post-1.0.
- Operate must use existing APIs. Approve must not Host.Exec. Restart
  and Store install now invoke those handlers. Policy create still
  writes the store and is not the finished engine.
- SkipHostCmds must fail closed. Cloud engines that skip host commands
  must stay unavailable or unverified and must not invent success.
- The installer ISO is not booted in this tree. mkosi config is not a
  spare-PC install.
- Signed install remains the documented HTTPS repo and keyring path.
  Package signatures produced under `out/` are build artifacts, not
  the production download repo.
- Ubuntu is not Tier 1.

This tree also does not claim multi-master HA, live Trivy on this host,
live kubelet, or live Ceph/`rbd map` on this Cloud agent host.
Official Store trust is the pinned publisher public key in
`store/official.pub`, not a hosted CA. The physical checklist records
`ce10-phys-9` as integration evidence; gates 12, 20, and 21 remain
BLOCKED until a second bare-metal node, an armed Node A reboot, and a
real supported version transition are proven.
