# Production e2e audit handoff

This is the continuation brief for the live appliance audit of host `no-dal`.
Resume from here. Do not re-do completed areas unless a regression is
suspected. Existing production workloads stay read-only.

## Goal

Finish the original production e2e audit on the **live** appliance:

- Operate the real UI like an administrator.
- Create only disposable resources, then fully delete them before the next
  area. Do not pile guests up.
- Fix every No-dal bug found, run focused tests, deploy with
  `packaging/e2e/rebuild-packages.sh` then `apt-get install` the debs, and
  re-verify live.
- Restarting `ndl-control` is allowed. Guests must keep running.
- Never attach test guests to `lan`.
- Never `systemctl start` / `reset-failed` production units, including
  Rustdesk.
- Do not run the production backup policy named `Backup` (R2 target).
- Do not mass-delete historical unavailable `container-root` volume rows.

When the audit is actually complete: remove every disposable resource **and**
the audit user, verify production health, and write one consolidated report.

## Appliance

- Host: Debian 13 `no-dal`, AMD Ryzen 9 9900X, 62 GiB RAM, `/` has hundreds
  of GiB free.
- Production UI/API: `http://127.0.0.1:8080` via `/usr/sbin/ndl-control`.
- Packages live at wrap-up: `ndl-control` / `ndl-agent` / `ndl-ui` / `nodal`
  **1.0.9**.
- Cluster `local` (`5248e1c6-7df9-4bdc-8dd9-ba1d7d524090`).
- LXC path: `-P /var/lib/ndl/runtime/lxc`.
- Postgres: `runuser -u ndl-control -- psql -d nodal`.
- Deploy:

```bash
SRC=/root/ndl-ce OUT=/tmp/ndl-debs bash /root/ndl-ce/packaging/e2e/rebuild-packages.sh
apt-get install /tmp/ndl-debs/ndl-control_*.deb /tmp/ndl-debs/ndl-agent_*.deb \
  /tmp/ndl-debs/ndl-ui_*.deb /tmp/ndl-debs/nodal_*.deb /tmp/ndl-debs/nodalctl_*.deb
```

Wait about 10s for `:8080` after install. Confirm production CTs still
running.

UI testing: Chromium + `puppeteer-core` in `/tmp/ndl-gs-shot`
(`executablePath: /usr/bin/chromium`). Firefox ESR cannot render oklch.
Use `waitUntil: "domcontentloaded"`. `/events` hangs under `networkidle0`
because of SSE `/api/v1/events/stream`. Confirm dialogs are native
`<dialog>` / `dialog[open]`. Puppeteer `click()` on a disabled button is a
no-op. Files waits must not use `/usr/i` (it matches production CT `cursor`).

## Audit login (keep until the audit is finished)

Disposable admin, created for this audit. **Delete only at final cleanup.**

- Username: `ndl-audit-e2e`
- User ID: `1271d746-a260-4496-b40c-8345d4075631`
- Password file: `/tmp/ndl-audit-pass` (mode 600)
- Cookie file: `/tmp/ndl-audit-cookie`

```bash
PASS=$(cat /tmp/ndl-audit-pass)
curl -sS -c /tmp/ndl-audit-cookie -H 'Content-Type: application/json' \
  -d "{\"username\":\"ndl-audit-e2e\",\"password\":\"$PASS\"}" \
  http://127.0.0.1:8080/api/v1/auth/login
```

Do not use `nodalctl recover-admin` on `Skila`. Do not reset other users.

## Production inventory (read-only)

Do not modify, restart, stop, delete, or reconfigure these.

System containers (19): agolive, AspecRacing, bunnyz, ChriFinance,
CodeHold, cursor, cyberm, DepenDash, Everos, FixerAi, Gitea, helix,
jobsentinels, kkr, MyAutoRecords, nodal, **Rustdesk**, SoundDock, ViewDock.

At wrap-up, 18 were `running`. **Rustdesk** is `status=stopped`,
`desired_power=running`, systemd unit `nodal-ct@f2b85ee3-708e-4f1d-aaf5-a83f4742be68`
`inactive` / `Result=success`. It previously showed `failed` because
`lxc-stop` exited 2 when the guest was already gone. After 1.0.7, new
stops use `ExecStop=-`. Do **not** start or reset-failed Rustdesk.

No production VMs, OCI workloads, or game servers existed at wrap-up.

Storage pool `local` (`1389f9b3-cabe-43c8-ba4a-07a8dc859a39`) is directory
backend at `/var/lib/ndl/storage/local`, status `warning`
(`root_filesystem`, `thin_overcommit`).

Networks: `lan` (dangerous lan-bridge), plus isolated-nat
`cert-ce10-phys-4-net` (`f8c04077-18d8-4776-8d4e-d687f1426e5c`) and
`cert-ce10-phys-5-net`. Worker node `cert-lab-nodeb` exists, status
`collecting`, no inventory. Do not delete it.

GPU feature: enabled, `package_status=unavailable`. Do not toggle GPU.

Failed CodeHold `workload.setup-extras` tasks from 2026-09-13 are old;
CodeHold itself is running. Do not mutate CodeHold.

SoundDock Docker healthcheck failed is guest-side. Do not mutate
SoundDock or ViewDock.

Overlay backup target `ndl-overlay-test-local`
(`7a93069b-0155-48c6-b1eb-aa79961c3809`,
`/var/lib/ndl/backup-overlay-test`) is the only safe extra backup target.
Do not dump to R2 / mutate policy `Backup`.

One VM library image: debian-13 genericcloud qcow2
`d645e05b-f9f4-404a-991c-d96d88c5d1b1`.

## What already shipped (1.0.7 through 1.0.9)

These are **deployed on the appliance** and the commits belong on `main`.

### 1.0.7

- `ExecStop=-` on CT/OCI units; delete `reset-failed` leftovers.
- Add Features counts CT/VM instead of always zero.
- Wizards prefer isolated-nat, not LAN.
- Workloads table shows loading vs empty cluster.

### 1.0.8

- Header `TaskIndicator` only flags failures from the last hour.
- Workload detail loading state; Start/Stop re-enable when lifecycle
  returns; 4s poll is `getWorkload` only (not `getDocker` of every CT).
- Sidebar follows live GET status, not catalog `targetIsLive`.
- Files shows `Loading files` instead of `This directory is empty`.
- App test typing so `tsc --noEmit` in the UI package build succeeds.

### 1.0.9

- `DeleteVolume` detaches `workload_disks` and `snapshots` first, so CT
  delete removes the exclusive root volume row.
- Dashboard shows Collecting until inventory loads, not `0 running`.

## Completed live testing

### System container `ndl-audit-ct1` (done and deleted)

- ID `b0707d78-5b54-4a49-be99-fe1091e8cc4f`. Alpine, 1 CPU, 1 GiB,
  isolated-nat `cert-ce10-phys-4-net`.
- Terminal, files (after 1.0.8), snapshots (honest unsupported on
  directory CT), stop/start (after 1.0.8), engine-v2 smart+full backup to
  overlay target, UI delete.
- Backup artifacts/runs/restore-points for this guest were removed.
  Original production `a4fbf86c-....tar.zst` was left untouched.
- After delete: workload 404, runtime dir gone, unit inactive
  `Result=success`.
- Volume leftover bug found and fixed in 1.0.9. The leftover row
  `5951746c-423b-4958-8d23-8f5df79fc8d9` was deleted manually. Do not
  mass-delete the other historical unavailable `container-root` rows.

Scripts: `/tmp/ndl-gs-shot/prod-audit-lxc-lifecycle.mjs`,
`prod-audit-lxc-lifecycle2.mjs`, `prod-audit-lxc-delete.mjs`.

### VM `ndl-audit-vm1` (partial, then cleaned)

- ID `e2c11358-136d-40eb-a6b7-c4a002106287`. Debian-13 cloud image, 1 CPU,
  1 GiB, isolated-nat, autostart off. Created through `/workloads/new/vm`
  guided wizard (7 steps). Volume
  `f799cd2e-de33-4679-9ac1-b0ef6ebbf247`.
- Proven: create, Start, serial Console (boot logs), Stop, Start again,
  confirm-delete.
- **Not finished before wrap-up:** snapshot create/rollback on qcow2,
  graphical console, guest-agent Terminal/Files (ndl-guest is not on the
  cloud image; qemu-ga also did not reply during first boot), clone,
  migrate, backup of the VM.
- Delete API: `POST /workloads/{id}/delete` without
  `X-Nodal-Confirm: delete` returns 409. With the header it deletes the
  workload and **preserves the attached volume**
  (`volumes_preserved: true`). The empty runtime dir
  `/var/lib/ndl/runtime/qemu/<id>` was also left behind.
- Wrap-up cleaned **only** that audit volume row, the qcow2, and the
  runtime dir. There is no HTTP DELETE for volumes.

Scripts: `/tmp/ndl-gs-shot/prod-audit-vm-create.mjs`,
`prod-audit-vm-lifecycle.mjs`.

Wizard defaults isolated-nat correctly (`cert-ce10-phys-4-net`).

## Found bugs not yet fixed in code

Fix these while continuing, then deploy, then re-verify.

1. **VM summary banner leaks a unix error.** Running VMs without ndl-guest
   show `read unix @->/var/lib/ndl/runtime/qemu/<id>/guest.sock: i/o timeout`
   as the page banner. `Probe()` maps dial failure to
   `nodal guest is not connected`, but `CallTimeout` returns `err.Error()`.
   qemu-ga already says `qemu-guest-agent did not reply`. Do the same for
   nodal_ga. File: `internal/guest/client.go`. UI fallback:
   `ui/src/pages/WorkloadDetailPage.tsx` should not put a raw syscall
   string in the banner.

2. **VM delete leaves the boot volume and an empty runtime dir.** Product
   currently preserves volumes by design. There is still no volume DELETE
   API, so disposable VMs leak qcow2 + `volumes` rows unless an operator
   deletes the row by hand. Runtime `delete-runtime` also left an empty
   directory. Decide whether exclusive VM disks should be destroyed like
   exclusive CT roots, and make runtime cleanup remove the directory.

3. **Snapshots page can sit on Collecting.** The page waits on unused
   `getWorkload(id)` in parallel with `listWorkloadSnapshots`. Guest
   probes make GET slow; the screenshot caught Collecting even though
   `GET /workloads/{id}/snapshots` already returned
   `supported: true`, `mechanism: qcow2-overlay`. File:
   `ui/src/pages/SnapshotsPage.tsx`. Re-test Create snapshot after this.

4. **Engine v2 `checksum_sha256` can be the backup UUID.**
   `internal/httpapi/backup_v2.go` uses
   `firstNonEmpty(res.SHA256, out.BackupID)`. Leave it empty when v2 has
   no SHA.

5. **IMAGE `Not reported (not verified)`** on a VM created from a cloud
   image. `image_pin` was empty. Confirm whether cloud-image VMs should
   surface the library item name.

6. **Historical unavailable `container-root` volume rows** (~38) from
   prior deletes. 1.0.9 prevents new CT leftovers. Do not mass-delete.
   Operator GC is a product question.

## What has not been tested yet (do these next)

Work **one area at a time**. Clean it before the next. Watch `uptime`,
`free`, `df`. Smallest viable guests. Isolated-nat only.

### 1. Finish VM lifecycle (one disposable VM)

Recreate `ndl-audit-vm1` the same way (1 CPU, 1 GiB, debian-13 cloud
image, isolated-nat, autostart off). Then:

- Wait until Collecting leaves the Snapshots page (or fix the load hang).
- Create a qcow2 snapshot, rollback, flatten if offered.
- Serial console is proven; try Graphical / Reconnect.
- Terminal/Files stay disabled until ndl-guest. Record that honestly
  unless you install ndl-guest inside this disposable VM.
- Stop / Start / Delete with `X-Nodal-Confirm: delete`.
- Verify qemu unit gone **and** decide/fix volume leftover. Do not leave
  another `vm-disk` row.

Do not keep a VM and an OCI guest running together.

### 2. One OCI workload

`/workloads/new` OCI wizard, isolated-nat, smallest image that already
exists locally if possible. Start, logs, stop, delete. If images/runtime
are missing, record the blocker after trying the real wizard. Confirm
`containerd` units are gone after delete.

### 3. Remaining UI / admin pages

Users, roles, API access, security, MFA, groups, audit log. Backups page
read-only on the production `Backup` policy. Tasks, events, alerts,
import-export, templates, stacks (cert stacks are read-only). Docker
inventory (do not mutate SoundDock/ViewDock).

Add Features: Game Servers software-only with 0 servers may be
disabled/re-enabled if that cannot destroy data. Do not disable
docker/oci/gpu if production CTs depend on docker discovery.

### 4. One real Minecraft game server

Not 25 templates. Vanilla or Paper, no extra credentials. Override the
default 2048 MiB / 8 GiB down (512–768 MiB if the API allows). Create,
install, confirm a process is **actually listening**, console, files,
stop/start, delete. Game-server backups have a DELETE API. Feature is
enabled with `package_status=not_configured`.

### 5. Storage / networking / schedules (beyond CT+VM smoke)

Pool warning copy, volume list honesty, isolated-nat vs LAN danger
copy, snapshot flatten/rollback (VM), backup schedules **without**
touching the production R2 policy. Overlay target only.

### 6. Automated regression and production smoke

Focused tests for every remaining fix. Then `go test` as appropriate and
`pnpm --dir ui test`. Then a final smoke pass on `:8080`.

### 7. Final cleanup

- Delete every audit guest, volume, snapshot, backup artifact, schedule,
  and network **this audit created**.
- Delete user `ndl-audit-e2e`.
- Confirm production CT names unchanged. Rustdesk may still be stopped
  with `desired_power=running`. Leave it.
- Confirm host health (`uptime`, `free`, `df`, `ndl-control` active).
- Consolidated final report (version, areas, resources, bugs/fixes,
  tests, leftover questions).

## Non-blocking product questions

Leave these until the final report unless a fix is required to continue.

- Should historical unavailable `container-root` rows be garbage-collected
  by a one-shot operator tool, or left as tombstones?
- Should backup artifacts have an API delete, or is engine-v2 namespace
  `rm` plus DB delete the intended cleanup?
- Should `checksum_sha256` stay empty when v2 has no SHA, rather than
  substituting `BackupID`?
- Game Servers `package_status=not_configured` while the feature is
  enabled: expected on this appliance?
- Should exclusive VM disks be destroyed on VM delete the way exclusive
  CT roots are after 1.0.9, or is volume preserve-by-default the product
  rule (and then a volume DELETE API is required)?
- Rustdesk: desired running, actually stopped. Operator start vs leave
  it? Do not start it during the audit.

## Branch consolidation (done at wrap-up)

Unique unreleased product code lived only on `cursor/prod-e2e-audit-8bc4`
(the 1.0.7–1.0.9 commits). Other leftover branches were **behind**
`main` (already merged via PRs #8, #10, #11, #12). Two-dot diffs against
`main` were deletions, not extra features.

Do not resurrect:

- `cursor/backup-engine-v2-integration-8bc4`
- `cursor/backup-engine-redesign-73b7`
- `cursor/directory-ct-backup-f531`
- `cursor/game-servers-native-8bc4`
- `cursor/lan-bridge-uplink-mac-f531`
- `cursor/root-backed-pools-and-lxc-extract-f531`

Untracked local junk, do not commit: `cmd/ndl-gs-demo/`, `ndl-control`,
`out/`.

## Screenshots from this pass

Under the cloud-agent artifacts store:

- `audit_ct_files_108.png`, `audit_ct_snapshots_108.png`,
  `audit_ct_stopped_108.png` (1.0.8 CT evidence)
- `audit_vm_wizard_review.png`, `audit_vm_created.png`,
  `audit_vm_running.png`, `audit_vm_console.png`,
  `audit_vm_snapshots.png`

`audit_vm_snapshots.png` is Collecting (bug 3). `audit_vm_running.png`
shows the guest.sock banner (bug 1).
