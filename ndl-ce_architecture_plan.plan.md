---
name: ndl-ce architecture plan
overview: "Architecture and phased implementation plan for No-dal Community Edition: a real single-node virtualization platform on Debian 13, with a Go control plane and node agent, QEMU and liblxc runtimes, and a Vite SPA UI. Optimized to reach a physical homelab dogfood install without becoming a frontend for libvirt or Incus."
todos:
  - id: phase-0-repo
    content: "Create ndl-ce GitHub repo: Go module, proto, Vite UI, Debian/systemd sketches, fast static CI, em dash file:line checker"
    status: pending
  - id: phase-1-cp-agent-auth
    content: Ship ndl-control + ndl-agent unix RPC, Postgres, first-run setup token, RBAC, local node enroll
    status: pending
  - id: phase-2-inventory-jobs
    content: Hardware inventory, operations/events, metrics scrape, dashboard against real host data
    status: pending
  - id: phase-3-storage-net-gate
    content: Directory pool, image library, isolated/LAN networks with management-NIC safety; gate workload create
    status: pending
  - id: phase-4-system-containers
    content: liblxc units, official LXC images, lifecycle, CT terminal/files
    status: pending
  - id: phase-5-vms
    content: QEMU/QMP systemd units, NoCloud, VNC/serial, qemu-ga channel, CP/agent-kill survival
    status: pending
  - id: phase-6-io-polish
    content: Ticketed Terminal/Files, admin-only host IO, privilege and path-jail tests
    status: pending
  - id: phase-7-snap-backup
    content: Honest snapshots plus optional local backup catalog and restore-as-new-UUID
    status: pending
isProject: false
---

# No-dal Community Edition Implementation Plan

This plan is for `ndl-ce` only. It does not implement Enterprise, No-dal Cloud, or the marketing site. It does not modify [NO-DAL_PROJECT_VISION.md](NO-DAL_PROJECT_VISION.md). Another coding agent should be able to execute phase by phase from this document.

**Vision alignment:** control plane and node agent are separate; UI is a client of the same API as CLI; single-node is first-class; clustering is an evolution; CE stays useful without Cloud or EE; AI later consumes structured actions; KVM/QEMU and Linux container primitives are used, not reinvented; No-dal owns orchestration.

---

## 1. Executive summary

Build a real hypervisor appliance, not a mocked dashboard.

A user installs Debian 13, adds the No-dal signed apt repo, runs `apt install ndl-ce`, opens the web UI, claims first-run with a console setup token, sees real hardware, creates a directory storage pool and a safe network, then creates a system container and a KVM virtual machine. Those workloads keep running if the control plane or UI dies.

**Locked stack**

- Host: Debian 13 (trixie) amd64, stock kernel 6.12
- Install: signed `.deb` packages first; installer ISO later
- Language: Go for `ndl-control`, `ndl-agent`, and `nodalctl`
- UI: Vite + React + TypeScript SPA, static files served by `ndl-control`
- Store: PostgreSQL 16 on localhost for desired state, operations, events, audit
- Agent scratch: SQLite for compact metrics only
- Southbound RPC: Connect + Protobuf over a systemd unix socket
- VMs: one QEMU process per VM, QMP, systemd `nodal-vm@<uuid>.service`. Not libvirt as the object model
- System containers: liblxc 6 + lxcfs, official linuxcontainers.org images. Not Incus
- Storage V1: Directory pool required; ZFS optional and honest about capabilities
- Network V1: systemd-networkd, Linux bridges, tap/veth, dnsmasq only on isolated bridges
- Auth: first-party appliance identity. Not Better Auth

**Hard invariants**

- Workloads are systemd units. They must not be children of `ndl-control` or `ndl-agent`.
- The web/API process never runs as root and never `exec`s user-controlled strings.
- The agent is a typed method table. There is no `Host.Exec` / `ExecHost`.
- Desired state lives in Postgres. Liveness lives on the host. The database must not claim a VM exists when the host disagrees.
- Every object has UUIDs plus `cluster_id` and `node_id` from first boot.
- CE never requires Cloud, a license, or EE modules.

---

## 2. CE V1 scope

**Foundation now (must decide and implement before first workload)**

- Repo, proto, CI, packages, systemd units
- Control plane, node agent, unix RPC, enroll of the local node
- First-run setup, users, sessions, API tokens, RBAC, audit
- Desired vs observed vs operations vs events
- Hardware inventory (reliable sysfs/udev sources)
- Directory storage pool + ISO/image library
- Isolated (+ NAT) network and optional LAN-bridge with lockout safety
- System-container lifecycle
- VM lifecycle with NoCloud, VNC/serial, qemu-ga channel attached
- Host and system-container Terminal and Files (privilege rules below)
- Tasks with stages; events; real metrics (or honest `Collecting`)
- `pg_dump` of the control-plane database

**Required for first dogfood (spare physical server)**

Everything above, plus: workload spec edit, start/stop/restart/delete, live task view, users/roles, cluster name settings. GPU inventory may be read-only.

**Required before migrating important homelab workloads**

- Snapshots that tell the truth per backend
- Local backup to a second pool or disk (catalogued, restorable)
- Proven recovery: CP/agent kill, interrupted create, host reboot
- Safe platform update of `ndl-control` / `ndl-agent` without stopping guests
- Storage not on the root filesystem for real data
- Backup of `/var/lib/ndl` metadata

**Near-term after dogfood**

- ZFS as first-class optional pool
- GPU explicit assign (render or VFIO)
- Files/Terminal polish (Terminal Here, upload progress)
- Updates UI that applies packages
- Historical metrics range

**Long-term architecture (design now, build later)**

- Live migration, second node join, WireGuard overlay
- Incremental backup, S3/R2, encryption-before-upload
- OCI/Application workloads, Store manifests
- Custom No-dal Guest Agent
- VLAN-aware bridge, bonds, Kea
- LVM-thin driver
- AI operator on structured actions

**Enterprise later:** signed capability modules, SSO, advanced audit export. V1 has a deny-by-default capability table only. No root module loader.

**Cloud later:** licensing, hosted CP, fleet. CE must not call Cloud.

---

## 3. Explicit non-goals (V1)

- Kubernetes as a foundation
- Being a frontend for libvirt, Incus, Docker Compose, or another product
- Nested virt in GitHub CI on every commit
- Store marketplace, paid apps, helper-script execution
- AI chat or Operate mode
- Clustering, live migrate, HA control plane
- Custom installer ISO (until packages work)
- Ubuntu as a supported host (document as next OS, do not dual-maintain yet)
- arm64
- MFA enrollment UI (schema columns only)
- SSO / OIDC / LDAP
- License walls or fake locked EE features
- Prometheus + Grafana or ELK as required dependencies
- `guest-exec` as a shell
- Host Terminal or host Files for the `operator` role

---

## 4. Proposed architecture

```text
Browser / nodalctl / future AI
        |
  Northbound HTTPS or LAN HTTP + /api/v1 + /ws/v1
        |
  ndl-control  (unprivileged, Postgres, RBAC, jobs, SPA)
        |
  Connect RPC  unix:///run/ndl/agent.sock
        |
  ndl-agent  (root, typed methods, host journal)
        |
  systemd units + Linux primitives
        |-- nodal-vm@<uuid>.service  -> qemu-system-x86_64 + QMP
        |-- nodal-ct@<uuid>.service  -> liblxc start
        |-- systemd-networkd files
        |-- LVM/ZFS/directory volumes
        |-- journald, sysfs, udev
```

Single-node is a cluster of one: `cluster_id` minted at setup, `node_id` minted at local enroll.

---

## 5. Process and service topology

systemd units (no `BindsTo` / `PartOf` / `Requires` from workloads onto control or agent):

- `ndl-agent.socket` creates `/run/ndl/agent.sock` (`0660`, owner root, group `ndl-control`)
- `ndl-agent.service` (`Type=notify`, root, bounded capabilities, `DevicePolicy=closed` + allowlist)
- `ndl-control.service` (static user `ndl-control`, `DynamicUser` is rejected because `SO_PEERCRED` needs a stable uid)
- `postgresql` (localhost only, peer auth for `ndl-control`)
- `nodal-vm@<uuid>.service` and `nodal-ct@<uuid>.service` under `nodal-workloads.target`
- `ndl-network-rollback.service` (independent watchdog for dangerous net applies)

`ExecStop` on the agent is a no-op for workloads. Killing `ndl-control` and `ndl-agent` must leave guests running. Acceptance test for Phase 1 and again after first VM.

---

## 6. Host OS strategy

**Selected:** Debian 13 amd64, official kernel 6.12, systemd 257.

**Why:** one supported tuple, current virt stack, AppArmor default, 5-year LTS, no snap/netplan as the product format.

**Alternatives rejected for V1:** Ubuntu 24.04 (HWE/snap/netplan surface; better NVIDIA; second target later), Fedora/Arch (too fast), RHEL (homelab GPU and packaging cost), custom ISO first (delays dogfood).

**Host root:** ext4 or XFS on LVM. Never ZFS on `/` in V1. Never put the default Directory pool on `/` for important data; warn if `local` shares the root filesystem.

**Firmware:** enable `non-free-firmware` for Intel/AMD GPU firmware.

**NVIDIA:** optional, pinned, never unattended with kernel upgrades. Do not support host NVIDIA driver and VFIO on the same GPU.

**Decide now:** one OS, package-first, no backports kernel on supported nodes.

---

## 7. Control-plane design

`ndl-control` owns:

- Northbound HTTP (`/api/v1`, `/setup`, `/auth`, `/ws/v1`, static UI)
- Postgres desired state, operations, events, audit, secrets refs
- RBAC and session/token auth
- Job scheduler and reconciler (Observe, then enqueue Operations)
- Cluster CA material (host key on disk, not downloadable)
- OpenAPI for humans; same types as CLI

It must not: open PTYs, talk QMP, write networkd files, hold `CAP_SYS_ADMIN`, parent QEMU.

Listen policy: bind management addresses chosen at first-run. Do not default to all interfaces if a default route looks like a WAN. Document LAN HTTP for V1; TLS later.

Reconcile rules: Observe first on start. Never auto-kill orphans. Never invent a replacement disk. Stale `RUNNING` ops: attach or mark STALE after Observe.

---

## 8. Node-agent design

`ndl-agent` is deterministic infrastructure software. Not an AI agent.

**Method enum** (illustrative, freeze in proto): `Host.GetInventory`, `Host.Observe`, `Vm.*`, `Container.*`, `Volume.*`, `Net.EnsureBridge`, `Net.ApplyNetworkd`, `IO.AttachTerminal`, `IO.Files*`, `Guest.Call`. Unknown method is a hard error.

**Validation:** treat the CP as hostile. Regex names, UUID existence, path under expected parents, PCI BDF from inventory, bridge name allowlist.

**Spawn:** libraries first (netlink, libudev, go-lxc, QMP JSON). If a binary is required (`qemu-img`, `zfs`, `lvm`), `execve` with a compile-time path and a validated argv vector. No `system()`, no `sh -c`.

**Durable host state**

- `/var/lib/ndl/cluster.json`, `/var/lib/ndl/node.json`
- `/var/lib/ndl/workloads/<uuid>/last-applied.json`
- `/var/lib/ndl/agent/ops/<operation_id>.json` (no secret plaintext)
- Sockets: `/run/ndl/vm/<uuid>/{qmp,qga,vnc,serial}.sock` mode `0600`

**LXC and QEMU must be started as systemd units**, not held as live library objects inside the agent process. Agent restart must not kill containers.

---

## 9. Privilege and security model

| Process | Privilege |
|---|---|
| UI (static) | none |
| `ndl-control` | none; unix + loopback |
| `ndl-agent` | root, bounded caps, device allowlist |
| QEMU | user `qemu`, cgroup, Debian AppArmor |
| liblxc guests | unprivileged user ns by default |

**Host Terminal and host Files are admin-only in V1.** Operator gets Terminal and Files on system containers. Agent deny-list even for admin Files: host key dir, agent certs, Postgres data, `/etc/ndl` secrets, setup-token path.

Path jail: `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_MAGICLINKS`. Writes use `O_NOFOLLOW`. Terminal `cwd` must open beneath the same jail.

Tickets: hashed, 2 minutes to connect, header only, Origin checked on WS. Never query-string.

Setup token: at least 128 bits, hashed, single-use, rate-limited, printed to `/dev/console`, not persisted in the journal. After claim, `/setup/*` is dead.

Destructive HTTP: `409 confirmation_required` then `X-Nodal-Confirm`. Does not cover PTY keystrokes.

No EE `dlopen` loader in CE.

---

## 10. RPC and API architecture

**Southbound:** `proto/nodal/agent/v1/*.proto` via Connect. V1: CP dials unix socket. Proto includes `Enroll`, `Hello`, `Observe`, `Execute`, `WatchOperation`, `Cancel`, and `OpenSession` (implemented later for remote nodes).

Identity: local enroll via peercred with empty join token. Join tokens and mTLS certs (SPIFFE-shaped URI SAN) are proto + on-disk files after first VM, not a SPIRE dependency.

**Northbound:** REST+JSON `/api/v1` plus ticketed WebSockets. Generate OpenAPI and TypeScript from the same spec. `nodalctl` uses Bearer tokens on the same routes.

**Structured actions for later AI** (names exist now; AI does not):

- `workload.create` -> `compute.create`
- `workload.restart` -> `compute.lifecycle`
- `workload.migrate` -> `compute.migrate`
- `backup.create` / `backup.restore`
- `storage.mount` / `network.attach`

Every action is RBAC + audit + (if mutating) an Operation.

---

## 11. Database and state model

**Selected:** PostgreSQL 16 localhost. **Rejected:** SQLite as the CP store (writer contention, painful CP move).

Five classes:

- Desired: `workloads`, `disks`/`volumes`, `nics`, `storage_pools`, `networks`, user intent
- Observed: `*_observations`, `hardware_inventory` (overwrite cache)
- Historical: `events`, `audit_events`
- Transient: `operations` with leases
- Host-native: QEMU pid, units, files, last-applied

Every row: UUID, `cluster_id`. Node-owned rows: `node_id`. Names unique per `(cluster_id, type, name)`.

Volume identity is UUID + `backend_ref` (ZFS GUID, fs UUID, xattr). Paths are locators, never primary keys.

Metrics samples never go in Postgres.

**DB loss:** `pg_dump` in V1; adopt mode from `cluster.json` + `last-applied.json` if dump is gone. Never mint new workload UUIDs for existing host artifacts.

---

## 12. Job and reconciliation architecture

```text
API validate
  -> insert Desired (maybe power=pending)
  -> insert Operation (UUIDv7, idempotency_key)
  -> take DB lease on resource
  -> Execute on agent
  -> stages + OperationEvent stream
  -> commit stage
  -> Desired updated; last-applied written
```

States: `PENDING -> ACCEPTED -> RUNNING -> SUCCEEDED | FAILED | CANCELED | TIMED_OUT`.

Before commit: cancel may compensate. After commit: undo is a new operation.

Agent journals stage start/finish on disk. CP on boot Observes, reattaches or marks STALE. Duplicate `operation_id` returns the stored result.

Concurrency: exclusive lock per workload. Default max 4 ops per node.

---

## 13. VM architecture

**Selected:** No-dal `VmSpec` + QEMU/QMP. One process per VM.

**Rejected as identity:** libvirt Domain XML, libvirt networks, libvirt storage pools, inter-node `qemu+ssh` migrate.

**Why:** the vision requires No-dal to own storage, network, backup, and cluster join. Libvirt is a second control plane. Copy libvirt lessons (ABI expansion, confinement), not its object model.

**V1 compiler rules**

- Machine type pinned: `pc-q35-X.Y`, never alias `q35`
- Persist expanded PCI/controller addresses after first start
- Disks: `-blockdev` with stable `node-name`; qcow2 on Directory; zvol on ZFS
- Never `qemu-img` a live disk
- NIC: tap + virtio-net onto a No-dal bridge
- Console: VNC unix + serial unix, ticket-proxied
- Always attach qemu-ga chardev `org.qemu.guest_agent.0`
- NoCloud cidata seed (user-data, meta-data, optional network-config)
- OVMF optional with per-VM vars volume; BIOS allowed
- User `qemu`, cgroup, Debian qemu AppArmor
- CPU: `-cpu host` allowed only as a recorded single-node default that will not live-migrate

**Guest usable without qemu-ga:** power, disks, VNC/serial. Shutdown falls back to ACPI. Backup may be crash-consistent.

---

## 14. System-container architecture

**Selected:** liblxc 6.0 LTS + lxcfs + `go-lxc`. Isolated `lxcpath` under `/var/lib/ndl/runtime/lxc`.

**Rejected:** Incus daemon (second CP), systemd-nspawn as the product runtime, libvirt-lxc, runc for OS containers.

**V1**

- Official `images.linuxcontainers.org` LXC unprivileged tarballs (`rootfs.tar.xz` + `meta.tar.xz`), GPG verified
- Catalog pin: Debian stable, Ubuntu LTS, Alpine, amd64, variant `default`
- Unprivileged default; privileged is explicit + audited
- Store uid/gid map on the instance record
- Volume created by No-dal; `lxc.rootfs.path` consumes it
- veth onto a No-dal bridge; CP allocates MAC
- cgroup v2 limits; lxcfs mounted in
- PTY via `console_getfd` / attach with `LXC_ATTACH_TERMINAL`
- Files via host rootfs + idmap translation
- Started by `nodal-ct@<uuid>.service`

Live CRIU migration is not a product promise.

---

## 15. Storage architecture

**V1 backends:** Directory (required default), ZFS (optional, capability-honest).

**Deferred:** LVM-thin, NFS/SMB, iSCSI, Ceph, encryption, qcow2-on-ZFS (driver refuse), internal qcow2 snapshots.

**Objects:** Pool, Volume (`block` | `filesystem`), Snapshot, LibraryItem, Attachment. Compute stores `volume_id`. Attach returns ephemeral `VolumeHandle`.

**Directory:** qcow2 files for VM disks (external overlay snaps only, chain cap ~16, flatten action). Container roots are directories (snapshot is copy or hidden). Library is files.

**ZFS:** zvol for VM disks (`volmode=dev`); dataset for CT roots and library. `zfs snapshot` / clone / send. Import by pool GUID, never force-import by default.

**TRIM:** QEMU `discard=unmap` by default.

**Missing disk:** mark unavailable. Do not delete DB rows. Do not create an empty replacement.

**Default install:** create pool `local` under a data path (prefer a non-root mount if present). UI must say when the pool shares the OS filesystem.

---

## 16. Networking architecture

**V1 types:** `isolated`, `isolated-nat`, `lan-bridge`.

**Persistence:** agent writes only `/etc/systemd/network/50-nodal-*.netdev` and `*.network`. Apply with `networkctl reload` / `reconfigure`, not `systemctl restart systemd-networkd`. Do not write Netplan as source of truth. Import existing management config; do not rewrite it on first start.

**Attach:** TAP for VMs, veth for containers. One bridge per L2 domain.

**DHCP:** dnsmasq bound to isolated bridges only. Reservations are “static” in Guided. Never a second DHCP server on a LAN-bridged uplink. Never DHCP on the management NIC.

**Firewall V1:** nftables `inet` host INPUT. Allow UI/SSH only on the management ifindex. No `br_netfilter`.

**Dangerous changes:** detect management address, default route, and their masters. Dry-run + typed confirm + 120s probe + independent rollback watchdog. First-run must not silently enslave the management NIC.

**Later:** VLAN-aware bridge, bonds, Kea, WireGuard, guest `bridge` family policy.

---

## 17. GPU and hardware architecture

Inventory is an observed cache, not desired state. Sources: sysfs + udev always. Optional: SMBIOS DIMMs, smartctl, nvme-cli, NVML. Missing optional tools show `Not reported`, not a red error.

**GPU V1:** list PCI/DRM/IOMMU. Do not assign in the first dogfood unless inventory and exclusive locks are done. Never default GPUs into containers. Never `NVIDIA_VISIBLE_DEVICES=all`.

**Later assign modes:** `render` (one render node) or `vfio` (whole IOMMU group, list every BDF, refuse ACS override as a product default). Shared NVIDIA ctl/uvm is documented as machine-global.

---

## 18. Terminal and Files architecture

**V1 targets:** host (admin-only), system containers (operator+). VMs: compatibility console only.

Protocol: `nodal.term.v1` binary WS frames (INPUT/OUTPUT/RESIZE/PING/CWD). xterm.js in the SPA. PTY opened only by the agent.

Files: REST + chunked upload (8 MiB, partial file + rename). SHA-256 optional. Same-target copy/move only.

VM later: custom No-dal guest agent for PTY and Files. qemu-ga is never the Files/Terminal implementation.

---

## 19. Guest Agent strategy

**V1:** attach qemu-ga channel on every No-dal VM. Use ping, info, shutdown, fsfreeze, osinfo, guest NICs. VMs work without the guest package.

**Later:** `org.nodal.guest.0` for Terminal/Files. Keep qemu-ga for freeze/ACPI.

Do not require the custom agent to create or start a VM.

---

## 20. Observability architecture

Agent scrape: CPU, mem, disk, net, temps, optional GPU, cgroup per workload. 1s in-memory ring, 15s persist to agent SQLite (14d raw, 90d 5-min rollup). CP reads via RPC. Never open the SQLite file from the CP.

Logs: journald via `sd_journal_*`, filter by unit. No ELK.

Events and audit stay in Postgres. Thin alerts: write events on SMART critical, thermal trip, heartbeat loss.

UI: one events WebSocket plus REST history. Empty series render `Collecting` or `Unavailable`, never fake charts.

---

## 21. Backup architecture

**Snapshot ≠ backup.** Same words in API and UI as the vision example.

V1 after compute: local snapshot API (ZFS snap or qcow2 external overlay). Directory CT snapshot is copy or hidden via capability flags.

Recommended soon after: one local backup target on a second disk/pool, full copy from a snapshot, catalog row, restore as a **new** workload UUID (`restore.mode = new | replace`).

Later: NFS/SMB adapters, S3/R2, encrypt-before-upload (No-dal keys, not bucket SSE alone), ZFS incremental send, QEMU bitmaps on qcow2, verify jobs.

Per-volume datasets/zvols or per-UUID directories from day one so `zfs send` can target one disk.

---

## 22. Authentication and RBAC

First-party module in `ndl-control`.

Bootstrap: console setup token -> first admin username + password (Argon2id) -> session cookie. No factory password. Last admin cannot be demoted. Offline `nodalctl recover-admin` requires the host key.

Roles: `admin`, `operator`, `viewer`, plus custom. Permission catalog as researched (`compute.*`, `terminal.open`, `files.*`, `storage.*`, `network.*`, `backup.*`, `identity.*`, `cluster.*`, `secret.*`, `audit.read`, `events.read`, `metrics.read`).

Host `terminal.open` and host Files: `admin` only in V1.

Node principals: enroll then cert; may `secret.use` for workloads on self only.

MFA: columns and `aal` on sessions. No UI in V1.

---

## 23. Frontend architecture

**Selected:** Vite + React 19 + TypeScript in `ui/`, TanStack Router + Query, React Aria or Base UI for a11y behavior, uPlot only when real samples exist. Served as static files from `ndl-control`.

**Rejected:** Next.js (SSR/SaaS stack), shadcn-as-identity, sharing `no-dal` marketing components.

**Dogfood routes:** `/setup`, `/login`, `/`, `/nodes/:id/{summary,hardware,workloads,storage,network,gpu,terminal,files,metrics,events,settings}`, `/workloads`, `/workloads/new/{system-container,vm}`, `/workloads/:id/{summary,spec,storage,network,console,terminal,files,snapshots,metrics,events,settings}`, `/storage`, `/network`, `/tasks`, `/events`, `/users`, `/settings`, `/me`.

Hide Applications and Backups nav until those APIs exist.

**UX:** one create/edit form with progressive disclosure (`More options`). Persist `ux_level` on `/me`. Expert raw editor and command palette wait. Expert acknowledgement API can exist unused.

Same generated types as `nodalctl`. If a button is not an API the CLI can call, it does not ship.

---

## 24. Update and recovery architecture

Updates V1: signed apt repo, packages `ndl-agent`, `ndl-control`, `ndl-ui` split so a CP bump does not restart QEMU. Gate kernel / `zfs-dkms` / NVIDIA. Rollback = previous `.deb` + previous kernel in GRUB.

Recovery:

- CP die: guests stay; UI down
- Agent die: guests stay; systemd restarts agent; journal resume
- Host reboot: `nodal-workloads.target` autostarts desired-on units
- Interrupted create: same `operation_id`, idempotent disk allocate
- DB loss: restore dump or adopt from last-applied
- License/Cloud unreachable: no effect (CE has no license)

Document this as `docs/recovery.md` in the CE repo.

---

## 25. Cluster-readiness decisions (do not build clustering)

Implement now:

- `cluster_id`, `node_id`, `owner_node_id`, `desired_node_id` on workloads
- Volume/pool/snapshot/task UUIDs
- DB leases, not process mutexes
- Agent must not share a filesystem with the CP except the socket
- Postgres DSN is config, not hardcoded forever
- Metrics over RPC
- Feature flags in `Hello`
- Proto `OpenSession` reserved
- CPU model recorded; `-cpu host` marked non-migratable
- qemu-ga channel attached from first VM
- uid maps stored on containers

Do not implement: Raft, join UI, live migrate, WireGuard, placement AUTO.

---

## 26. CE / EE extension boundary

CE is the full infrastructure platform.

V1: table `capabilities` default empty; settings page says `Community Edition. License activation is not required.`

Later EE: signed artifacts, short-lived entitlements, grace if licensing is unreachable, never stop workloads on expiry.

Forbidden in V1: private repo credentials, root plugin loader, crippled node limits, fake locked tiles.

---

## 27. Testing strategy

**Every GitHub commit**

- Go: `go test ./...`, `go vet`, golangci-lint
- Race on auth, jobs, path jail only
- UI: eslint, `tsc --noEmit`, vitest (MSW: setup redirect, confirm challenge, empty metrics, RBAC-hidden actions)
- Privilege matrix tests
- Fake agent: persist job, kill mid-stage, restart CP, assert no second disk
- Postgres 16 service: apply all migrations
- `buf generate` / OpenAPI generate + `git diff --exit-code`
- Em dash check with `file:line`

**Write now, skip on `ubuntu-latest`:** virt-tagged QEMU/LXC tests.

**Later dedicated KVM runner:** VM and CT lifecycle, storage, net apply/rollback, recovery, interrupted ops.

**Physical host checklist:** install `.deb`, setup token, create pool/net, SC, VM, kill CP+agent, reboot, restore from local backup.

Mocks are for tests only, never for production inventory or charts.

---

## 28. CI strategy

GitHub is authoritative. Gitea is archival/mirror only.

Four parallel jobs, target under 10 minutes, cancel superseded runs, cache Go modules and pnpm.

- **policy:** em dash `file:line` (skip `node_modules`, `ui/dist`, `vendor`, generated stubs), gofmt, prettier or equivalent on `ui/`, actionlint
- **go:** vet, lint, unit tests
- **ui:** eslint, tsc, vitest
- **schema:** buf lint/breaking vs main, migrate on Postgres, generate diff

Do not: `next build`, `docker build`, Compose up, Playwright on every commit, `pnpm audit` as a required gate, invent a Dockerfile just to run Hadolint.

Virt workflow: `workflow_dispatch` + nightly on a labeled runner.

---

## 29. Repository structure

New GitHub repo for Community Edition (empty `ndl-ce/` today). Monorepo until a split is forced.

```text
ndl-ce/
  cmd/ndl-control/
  cmd/ndl-agent/
  cmd/nodalctl/
  proto/nodal/agent/v1/
  api/openapi/
  ui/
  internal/ops/
  internal/auth/
  internal/store/
  internal/transport/
  packaging/debian/
  systemd/
  scripts/ci/
  docs/
  .github/workflows/ci.yml
```

Do not nest this inside the marketing `no-dal/` app.

---

## 30. Technology decisions and reasoning

- **Go (CP+agent+CLI):** one language, `connect-go`, `go-lxc`, boring unix/systemd, larger infra contributor pool. Rust is safer at the privilege boundary but splits the tree and delays dogfood. Python is rejected for the agent.
- **Vite React SPA:** appliance UI, no Node on the host at runtime. Next.js is the Cloud/marketing product.
- **Postgres 16:** network client from day one; UUID/JSONB; later CP move without a store rewrite.
- **Connect/Protobuf:** typed southbound, HTTP-debuggable, gRPC-compatible later.
- **QEMU not libvirt identity:** No-dal owns cluster/storage/net/backup. Cost: we own the supervisor. Mitigation: pin QEMU, persist ABI, copy confinement lessons.
- **liblxc not Incus:** avoid a second desired-state store.
- **Directory + ZFS:** always-works default plus snapshot-native optional. LVM-thin waits.
- **systemd-networkd + dnsmasq:** Debian-native persistence; do not write a DHCP server.
- **First-party auth:** appliance bootstrap and node identity are not SaaS email auth.

---

## 31. Security threat considerations

- LAN first-run race: high-entropy setup token, bind policy, journal hygiene
- Compromised CP: agent still validates; no generic exec
- Host PTY: admin-only; deny-list for key material
- Path escape: `openat2`, no host `/` for operators
- Network lockout: management freeze + rollback watchdog
- GPU leakage: explicit `gpu_id` only when assign ships
- Secret leakage: hashed tokens, redacted events, no `secret.reveal` UI in V1
- QEMU consoles: unix sockets only
- EE loader: not present (supply-chain)

---

## 32. Development phases

Do not start compute before a real pool and a safe network exist. Do not implement SPIFFE, ZFS, or ISO until a VM survives `systemctl stop ndl-control ndl-agent`.

### Phase 0. Repository and tooling

- **Objective:** empty CE repo that can merge safely
- **Work:** Go module, proto skeleton, UI Vite skeleton, Debian packaging sketch, systemd unit drafts, CI four jobs, em dash checker with `file:line`, CONTRIBUTING
- **Tests:** CI green on hello + em dash
- **Deferred:** ISO, Docker
- **Acceptance:** `ci.yml` required on GitHub default branch; `go test` and `ui` vitest pass

### Phase 1. Install, control plane, agent, auth

- **Objective:** `apt install` on Debian 13, open UI, claim setup, login
- **Backend:** HTTP, Postgres migrate, setup token, sessions, RBAC seed, audit writer, unix RPC `Hello`/`Enroll`/`Observe` stub
- **Agent:** socket activation, peercred, local enroll, write `cluster.json`/`node.json`
- **Frontend:** `/setup`, `/login`, shell, `/me`
- **Security:** listen bind, lockout, last-admin, recover-admin CLI
- **Tests:** setup single-use, privilege deny, fake Observe
- **Acceptance:** kill CP, nothing else was started yet; setup cannot be replayed; `nodalctl` login via token
- **Deferred:** compute, TLS required

### Phase 2. Inventory, jobs, events, dashboard

- **Objective:** real node page
- **Backend:** inventory RPC, `hardware_inventory` upsert, operations table, events, metrics scrape + SQLite, WS ticket
- **Agent:** sysfs/udev collect, scrape loop
- **Frontend:** dashboard, node summary/hardware/metrics/events, `/tasks`
- **Acceptance:** CPU/RAM/disks/NICs match the host; empty GPU is valid; meters say `Collecting` until samples exist
- **Deferred:** SMART optional enrich

### Phase 3. Storage and network minimum (gate)

- **Objective:** create-workload is possible
- **Backend:** Directory pool, volume create, ISO/image library upload, isolated + isolated-nat networks, lan-bridge with dangerous pipeline, nftables management allow
- **Agent:** VolumeHandle, qemu-img create (offline), networkd writers, dnsmasq instance per isolated bridge, rollback watchdog
- **Frontend:** first-run steps 2-4, `/storage` create pool, `/network` create isolated net, image picker
- **Security:** operator cannot apply management-NIC changes; admin confirm + rollback
- **Acceptance:** API rejects workload create without pool+network; single-NIC bridge dry-run required; no second DHCP on LAN-bridge
- **Deferred:** ZFS wizard, VLAN-aware, bonds

### Phase 4. System-container lifecycle

- **Objective:** create/start/stop/restart/delete a real unprivileged CT
- **Backend:** image fetch+verify, `WORKLOAD_CREATE` plan, `nodal-ct@` unit
- **Agent:** unpack rootfs, write LXC config, lxcfs, cgroup limits, Observe pid
- **Frontend:** create form, workload summary, start/stop, spec edit (CPU/RAM)
- **Security:** unprivileged default
- **Tests:** fake liblxc; virt-tagged later
- **Acceptance:** Debian or Alpine CT gets an IP on the isolated net; agent restart does not kill it; CP stop does not kill it
- **Deferred:** privileged, GPU, macvlan

### Phase 5. VM lifecycle

- **Objective:** create/start/stop/restart/delete a real KVM VM
- **Backend:** VmSpec compiler, NoCloud, ISO attach
- **Agent:** `nodal-vm@` unit, QMP, sockets, qemu user
- **Frontend:** create VM, compat console (serial then VNC), spec edit
- **Acceptance:** Linux cloud image or ISO boots; VNC works; qemu-ga channel present; `systemctl stop ndl-control ndl-agent` leaves VM running; reboot autostarts if desired_power=running
- **Deferred:** VFIO, swtpm, hugepages, live migrate

### Phase 6. Terminal, Files, console polish

- **Objective:** daily admin without SSH for host (admin) and CTs
- **Backend:** tickets, `NodeIO`, Files jail, chunked upload
- **Frontend:** xterm.js, Files browser, disabled VM Terminal/Files with reason
- **Security:** host IO admin-only; path escape tests
- **Acceptance:** CT terminal + upload/download; host terminal as admin; viewer cannot open terminal
- **Deferred:** Terminal Here polish, folder download, custom guest agent

### Phase 7. Snapshots and local backup

- **Objective:** snapshot honesty + one real backup path
- **Backend:** snapshot API with capability flags; optional local backup target; restore `mode=new`
- **Frontend:** workload Snapshots tab; `/backups` only when target exists
- **Acceptance:** ZFS snap or qcow2 overlay works; Directory CT snap is not faked; restore creates a new UUID; snapshot button is not labeled Backup
- **Deferred:** S3/R2, encryption, incremental

### Phase 8. OCI / applications

- After dogfood. containerd or a thin OCI runtime. Compose import later. Store later. Architecture: `workloads.kind=oci` already in the schema.

### Phase 9. Second-node foundations

- Join token UI, mTLS, `OpenSession`, placement field used. No live migrate required.

---

## 33. Dogfood milestone

**Name:** Dogfood Host

**When:** Phases 0-6 complete on a spare physical Debian 13 server.

**You can:** install packages, claim setup, see real hardware, configure one pool and one network, import an ISO or CT image, run a system container and a VM, open CT terminal/files and VM console, watch a task finish, add a second user, kill the control plane without stopping guests.

**You must not claim:** production readiness, backup/DR, clustering, GPU passthrough, or Store.

**Prove first:** CP+agent stop test, host reboot autostart, interrupted create does not allocate a second disk, network rollback, RBAC denials.

---

## 34. Important-workload migration milestone

**Name:** Homelab Migration Candidate

**When:** Phase 7 plus weeks of dogfood, `pg_dump` + `/var/lib/ndl` backup, data pool off root, documented recovery, package update that does not bounce guests, snapshots you have actually rolled back.

**Still not:** live migrate, HA, R2-only backups without a local copy, GPU unless explicitly tested.

---

## 35. Deferred functionality

ISO installer, Ubuntu support, ZFS-on-root, LVM-thin, VLAN-aware bridging, bonds, WireGuard, Kea, encryption, S3/R2, incremental backup, custom guest agent, OCI/Store, AI, Expert raw editor, command palette, mobile IA, MFA UI, SSO, EE modules, SPIRE, ostree, Prometheus stack.

---

## 36. Risks and open questions

**Risks**

- QEMU supervisor work is large (reviewer: years of libvirt-hard parts). Mitigate: pin QEMU, small VmSpec, no hotplug in V1.
- One-NIC home servers will try LAN-bridge and lock out the UI. Mitigate: isolated default, dangerous pipeline mandatory.
- Directory vs ZFS snapshot inequality will confuse users. Mitigate: capability flags and honest UI.
- NVIDIA on Debian is weaker than Ubuntu. Accept or delay NVIDIA.
- Schema drift if UI types are hand-written. Mitigate: generate or fail CI.

**Open questions (do not block Phase 0)**

- GitHub org/repo name for `ndl-ce` (vision: `no-dal/ndl-ce`)
- Apt repo hosting (GitHub releases vs dedicated repo host)
- Exact QEMU package version pin after Debian 13 qemu audit
- Whether a hidden transient libvirt launcher is allowed only for confinement (default: no, unless AppArmor-as-root is a ship blocker)

---

## 37. Phase acceptance criteria (checklist)

**Phase 0:** CI green; em dash fails with `file:line`; no Docker required.

**Phase 1:** first-run claim works; replay fails; peercred enroll; CP user cannot open `/dev/kvm`; recover-admin works offline.

**Phase 2:** inventory matches `lscpu`/`lsblk` reality; WS live or poll chip; no fake charts.

**Phase 3:** no pool or no network => create workload 400; isolated DHCP works; lan-bridge on management NIC requires typed confirm and rolls back.

**Phase 4:** CT running after `systemctl stop ndl-control ndl-agent`; image GPG verified; Files jail cannot escape to host.

**Phase 5:** VM running after the same stop; QMP reconnect after agent restart; machine type persisted; ga channel present; VNC ticket required.

**Phase 6:** admin host PTY; operator CT PTY; viewer denied; upload checksum mismatch deletes partial.

**Phase 7:** snapshot objects distinct from backup; restore new UUID; capability-hidden CT snap on Directory.

**Dogfood Host:** physical checklist signed off by you on real hardware.

---

## Reviewer resolutions folded in

- Workload units outlive **both** CP and agent; LXC is not in-process.
- Host IO is admin-only; `ExecHost` does not exist.
- Storage and network write plus image import are a hard gate before compute.
- One form with more-options, not three UX products in V1.
- SPIFFE/ZFS/ISO wait until the VM survives daemon kills.
- GitHub CI stays fast and virt-free on `ubuntu-latest`.
- EE hook is a stub, not a root loader.
- CE remains fully functional without Cloud or EE.
