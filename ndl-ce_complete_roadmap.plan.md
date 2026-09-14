---
name: ndl-ce complete roadmap
overview: Numbered end-to-end implementation roadmap for a feature-complete No-dal Community Edition. Debian 13 is the initial Tier 1 host, not a permanent exclusive host. Preserves the existing architecture plan as the HOW reference. Assigns every CE vision capability to a phase, with MVP through CE 1.0 milestones, HTTPS before Homelab Migration, a QEMU prototype gate, and concrete S3/R2 and GPU assignment phases.
todos:
  - id: preserve-arch-plan
    content: Keep ndl-ce_architecture_plan as HOW reference. Do not overwrite it or the vision document.
    status: pending
  - id: phase-0-2-mvp
    content: "When implementing: Phases 0-2 to MVP (repo, one-line first boot, host visibility)."
    status: pending
  - id: phase-3-8-dogfood
    content: "When implementing: Phases 3-8 to Dogfood Host, including the QEMU prototype gate before VMs."
    status: pending
  - id: phase-9-12-homelab
    content: "When implementing: Phases 9-12 HTTPS, snapshots, backup engine, guest-safe updates (Homelab Migration)."
    status: pending
  - id: phase-13-42-beta
    content: "When implementing: Phases 13-42 remaining CE systems through Feature-Complete Beta."
    status: pending
  - id: phase-43-ce10
    content: "When implementing: Phase 43 CE 1.0 hardening, docs, soak, license-activation surface."
    status: pending
isProject: false
---

# No-dal CE complete implementation roadmap

This is the **authoritative phased implementation roadmap** for Community Edition.

The existing architecture plan remains the **HOW** reference: [ndl-ce_architecture_plan.plan.md](ndl-ce_architecture_plan.plan.md).

The product source of **WHAT** is [NO-DAL_PROJECT_VISION.md](NO-DAL_PROJECT_VISION.md).

Do not overwrite the architecture plan. Do not edit the vision document. Do not implement in this pass.

Relationship:
- Architecture plan: process split, privilege model, QEMU/liblxc/storage/net contracts, recovery invariants.
- This roadmap: ordered phases from empty repo to CE 1.0, with every CE capability assigned a number.

---

## How to use this document

A future coding agent should:
1. Read the architecture plan for interfaces and invariants.
2. Implement **one numbered phase** from this roadmap.
3. Meet that phase's acceptance criteria before starting the next.
4. When a later phase is named in an earlier phase (example: "Live migration is Phase 32"), implement it only in that later phase.

There is no undefined later bucket. If work is not in this phase, it has a phase number.

---

## Architecture preserved (no casual redesign)

Unless a phase gate **fails with evidence**, keep:

- Debian 13 amd64 as the **initial Tier 1** supported **host** (not a permanent exclusive host)
- Go `ndl-control`, `ndl-agent`, `nodalctl`
- Vite + React + TypeScript SPA served by the control plane
- PostgreSQL 16 for desired state, operations, events, audit
- Unprivileged control plane
- Privileged agent as a typed method table (no `Host.Exec`)
- Connect + Protobuf southbound RPC
- API-first northbound HTTP + generated types
- systemd-supervised workloads that outlive CP and agent
- QEMU/QMP (not libvirt as object model)
- liblxc + lxcfs (not Incus)
- Desired vs observed vs host-native vs operations
- UUID identity plus `cluster_id` / `node_id` from first boot
- Directory default storage, ZFS as first-class optional backend
- systemd-networkd as the **Debian 13 host-platform** persistence implementation, plus a management-NIC rollback watchdog. Shared Linux primitives (netlink, bridges, nftables) stay shared.
- Browser Terminal and Files
- GitHub authoritative fast CI
- Em dash ban with `file:line`
- CE independent of Cloud and EE

## Architecture amendments vs the previous plan

These are the only intentional changes. Stack choices did not change.

1. **HTTPS is Phase 9**, required before Homelab Migration Candidate. The architecture plan allowed plaintext HTTP through early dogfood and did not number TLS. Reason: the management plane holds credentials, terminals, and files. Evidence: user amendment plus appliance threat model. Consequence: Dogfood Host may still use LAN HTTP. Important-workload management must be HTTPS.

2. **QEMU prototype is Phase 7**, a hard gate before the full VM subsystem (Phase 8). The architecture plan jumped to VM lifecycle. Reason: direct QEMU/QMP ownership is the largest technical risk. Consequence: if the prototype fails, stop and document a decision. Do not silently switch to libvirt identity.

3. **S3-compatible / R2 backups are Phase 23**, not an unnamed future item. Local backup engine is Phase 11 so restore is proven before object storage.

4. **GPU assignment is Phase 14**. Inventory stays in Phase 2. Assignment does not gate Homelab Migration.

5. **Every former deferred item now has a phase number** (LVM-thin, NFS/SMB/iSCSI, VLAN/bonds, WireGuard, Guest Agent, OCI, Store, clustering, AI, ISO, host-platform compatibility including Ubuntu LTS, optional Kubernetes, optional distributed storage, automation engine).

6. **Backup Phase 11 is an engine**, not a catalog-only table. Schedule, retention, snapshot-based copy, local plus NFS/SMB destinations, and a restore you have actually run.

7. **Host platforms are additive.** Debian 13 first, not Debian 13 forever. Distro-specific host behavior is isolated behind a host-platform boundary. Phase 29 establishes the mechanism for additional qualified hosts. This does not delay Phases 0 to 12 and does not require a multi-distro implementation now.

8. **One-line host install is a Phase 1 product path.** Existing supported Linux hosts install with a small HTTPS bootstrap that only configures the signed repo and installs the `nodal` metapackage. Authoritative install logic lives in versioned packages, not in a giant remote shell script. The installer ISO remains Phase 29. All install paths converge on the same packages, services, and `/setup` flow.

No other architecture decision was changed.

---

## Host platform model

**Rule:** Debian 13 first, not Debian 13 forever.

Debian 13 amd64 remains the first and primary supported No-dal **host**. Early phases, dogfood, and Homelab Migration are implemented and tested there. Do not delay those phases to support other hosts. Do not claim that every Linux distribution is supported.

The architecture must still allow additional Linux distributions to become explicitly supported hosts later **without** a major control-plane or node-agent rewrite, and **without** forcing users onto a different No-dal object model.

### Four OS surfaces (do not mix them)

1. **No-dal HOST operating system.** The Linux distribution that runs `ndl-control` and `ndl-agent`. This matrix is intentionally narrow. Initial Tier 1: Debian 13 amd64. Ubuntu LTS is added in Phase 29 if qualified. Further hosts are added only after explicit qualification and testing.
2. **Guest operating systems inside VMs.** Independent of the host matrix. A Debian 13 host can run Windows, other Linux, and other guests. Broad guest support is allowed from Phase 8 onward.
3. **System-container distributions.** LXC/liblxc images (Debian, Alpine, and others). Independent of the host matrix. Phase 5.
4. **OCI application images.** Registry images for application workloads. Independent of the host matrix. Phase 21.

A host that is not supported must not be confused with a guest, container, or OCI image that is.

### What must stay distro-neutral

These models and APIs must not assume Debian-only paths, package names, or tooling:

- Workload, VM, system-container, and OCI objects
- Storage pool/volume/class identity
- Network objects (bridges, attachments, reservations, policies)
- Backup, snapshot, restore, and catalog objects
- Scheduling, placement, cluster membership
- Identity, RBAC, API, database schema
- Shared Linux primitives: sysfs, udev, netlink, nftables, cgroups, KVM, QEMU, liblxc, systemd **workload** units

### What is a host-platform concern

Isolate these behind a host-platform interface or adapter. Implement the Debian 13 adapter first. Do not scatter `if debian` / `if ubuntu` through unrelated infrastructure code.

- Package management and repositories (`apt` on Debian)
- Host network persistence (Debian 13: systemd-networkd drop-ins)
- Firewall integration that differs by distro (shared nftables where possible)
- Kernel and module packaging (including ZFS DKMS/kmod variants)
- GPU driver and runtime **installation**
- Bootloader and host recovery (GRUB, previous kernel)
- Service configuration that is distro-specific (not the workload unit contract)
- Installer media and host provisioning

### How much abstraction to build now

Do **not** invent a large plugin framework in Phase 0.

In Phases 0 to 2, leave a clean seam:

- Detect `/etc/os-release` (`ID`, `VERSION_ID`, `ID_LIKE`)
- Report `host_platform` plus capability flags on `Hello` / inventory
- Keep Debian-specific package, repo, network-persistence, and update code in a named package such as `internal/hostos/debian`
- Shared Linux code lives in shared packages
- Unsupported hosts fail closed with a clear message naming the detected OS and the supported list
- An unsupported or experimental mode may be added later. It must be explicit. It is not the default. It is not a claim of support.

Phase 29 turns that seam into a second qualified adapter (Ubuntu LTS) plus the documented process for adding more. Additional adapters are additive.

### Support tiers

- **Tier 1:** Installed, tested, documented. Debian 13 amd64 from Phase 1. Ubuntu LTS only after Phase 29 qualification.
- **Unsupported:** Agent refuses to enroll or start privileged host mutation, with a clear error.
- **Experimental (optional, later):** Only if an explicit flag exists. No support claim. Same APIs. May still refuse dangerous host mutations.

---

## Installation product model

Installation is part of the product. A clean supported host should not need a long manual guide to reach the web UI.

**Goal:** one command, then finish setup in the browser.

The public convenience command is:

```text
curl -fsSL https://get.no-dal.com | sudo sh
```

The exact URL may stay a placeholder until the endpoint exists. Architect around this experience from Phase 1. Do not treat a developer-only setup script as the only tested path.

### Three paths, one installation

A. **One-line bootstrap.** Best path for an existing supported Linux host. Phase 1.

B. **Manual repository and package install.** Same packages, for cautious admins, automation, and users who refuse `curl | sh`. Phase 1 docs.

C. **Installer ISO.** Best path for a fresh dedicated physical server. Phase 29. Not Phase 1.

All three must produce the same package layout, systemd units, control plane, node agent, database/state model, first-run `/setup`, and update system. There must not be three kinds of No-dal.

### Bootstrapper stays small

The remote script only:

- detects OS, version, architecture from os-release and uname
- verifies the host is currently supported
- runs basic preflight (root, disk space, systemd, network to the repo)
- installs repository signing material over HTTPS
- configures the official signed package repository
- installs the user-facing metapackage (`nodal` or equivalent)
- invokes package postinst / `nodalctl` install helpers
- verifies service health
- prints the management URL and next step

Authoritative work lives in versioned packages: service users, systemd units, PostgreSQL preparation, migrations, listen bind, setup-token generation. A cautious admin must be able to read the bootstrap script before running it. Do not download and execute extra unsigned payloads.

### User-facing package vs internal split

Normal users run `apt install nodal` (or the bootstrap that does that). Internally the metapackage depends on `ndl-control`, `ndl-agent`, `ndl-ui`, `nodalctl`, and the core runtime dependencies required for the currently shipped platform. Split packages stay so Phase 12 can update the control plane without bouncing guests.

Phase 1 installs **core** dependencies only (PostgreSQL 16, systemd units, and whatever those packages require). Do not pull GPU runtimes, Kubernetes, distributed storage, optional storage backends, or AI components into the base install. Feature phases add those through Phase 35 modules.

### Host dispatch

The same public URL can later serve more than Debian. The script detects the host and selects the adapter. Phase 1: Debian 13 amd64 only. Phase 29: Ubuntu LTS if qualified. Future hosts: additive. Unsupported hosts fail closed and list supported platforms. Never force Debian packages onto Fedora or another unsupported OS.

### HTTPS honesty

Phase 1 prints an HTTP management URL if TLS is not shipped yet. After Phase 9, install and completion messages use HTTPS. Do not advertise HTTPS before Phase 9.

### Uninstall

The package manager owns installed files. `nodalctl` or an explicit package helper owns dangerous cleanup.

- Remove packages: binaries and services stop. Workload data stays.
- Purge: No-dal config files may go. Workload disks, CT roots, backups, and storage pools stay.
- Destroy state / wipe data: explicit separate command plus typed confirm. The bootstrap script does not own uninstall.

A normal uninstall must not delete user workloads.

---

## Roadmap at a glance

Format: Phase number. Name. Major capability. Complexity. Milestone reached at end of phase if any.

- **0** Foundation. Repo, proto, CI, packaging stubs, small bootstrap script, module boundary. Small. Pre-MVP
- **1** First boot. One-line install, CP, agent, auth, RBAC, API, CLI, reconciliation. Large.
- **2** Host visibility. Inventory, jobs, events, metrics spine. Medium. **MVP**
- **3** Directory storage. Pools, classes, image library. Medium.
- **4** Safe networking. Isolated, NAT, LAN-bridge, DHCP/DNS, rollback. Large.
- **5** System containers. liblxc lifecycle, images, clone, devices. Large.
- **6** Terminal and Files. Host and CT PTY/Files quality bar. Large.
- **7** QEMU prototype gate. Supervisor proof. Large.
- **8** Virtual machines. Lifecycle, disks, NICs, cloud-init, console, qemu-ga. Very Large. **Dogfood Host**
- **9** HTTPS and certificates. TLS, WSS, cookies, ACME path. Medium.
- **10** Snapshots. VM and CT, honest capabilities. Medium.
- **11** Backup engine. Local plus NFS/SMB destinations, schedule, retention, restore. Large.
- **12** Platform updates. Channels, preflight, guest-safe apply. Medium. **Homelab Migration Candidate**
- **13** Identity completion. Groups, MFA, tokens, audit, secrets, encryption at rest. Medium.
- **14** GPU runtime and assignment. NVIDIA, AMD, Intel, render, VFIO. Large.
- **15** ZFS storage. Pools, zvols, datasets, send/recv. Large.
- **16** Observability complete. History, logs, local alerts, timeline, capacity. Medium.
- **17** Operator UX. Guided/Advanced/Expert, search, palette, responsive. Medium.
- **18** VM advanced. Clone, templates, import/export, USB, UEFI, PCI, hotplug. Large.
- **19** No-dal Guest Agent. Linux and Windows guests. Large.
- **20** Guest Terminal and Files. VM PTY/Files via Guest Agent. Medium.
- **21** OCI workloads. Runtime, registries, ports, volumes, health, GPU. Large.
- **22** Application stacks. Compose import, multi-container apps. Large.
- **23** Object-storage backup. S3, R2, B2, MinIO, encrypt-before-upload, incrementals. Very Large.
- **24** Backup verification and file restore. Verify jobs, restore tests, file-level. Medium.
- **25** LVM and LVM-thin. Local thin pools. Medium.
- **26** Network-attached storage. NFS, SMB, iSCSI datastores. Large.
- **27** Advanced networking. VLANs, bonds, firewall policies, overlays, traffic visibility. Large.
- **28** WireGuard and remote nodes. Secure node connectivity. Medium.
- **29** Host platform compatibility and installer. Host adapter mechanism, Ubuntu LTS qualification, bare-metal ISO. Large.
- **30** Cluster join. Enrollment, cluster inventory, one-writer Postgres. Large.
- **31** Placement and maintenance. Auto/specific/group, affinity, drain. Large.
- **32** Workload migration. Offline then live. Very Large.
- **33** Cluster storage and DR. Cross-node restore, replication, DR workflow. Large.
- **34** HA and rolling updates. CP failover foundations, cluster-aware updates. Very Large.
- **35** Feature modules. Optional install of heavy features. Small.
- **36** No-dal Store. Manifest, Community apps, lifecycle, hooks. Very Large.
- **37** Store trust. Verified/Official, signatures, scans, provenance. Large.
- **38** Optional Kubernetes. Feature module, not the foundation. Very Large.
- **39** Optional distributed storage. Ceph-class module. Very Large.
- **40** Automation engine. Deterministic policies/workflows (not an LLM). Large.
- **41** AI Ask. Provider-neutral gateway, read-only context. Large.
- **42** AI Plan, Operate, Automate. Structured actions, approvals, app actions. Very Large. **Feature-Complete Beta**
- **43** CE 1.0 release. Docs, soak, license-activation surface, hardening. Large. **No-dal CE 1.0**

---

## Milestones (checkpoints, not scope cuts)

### MVP (end of Phase 2)

Earliest real system. One-line or manual package install on clean Debian 13, claim setup, login, see **this** host's hardware, watch jobs/events. No workloads yet.

### Dogfood Host (end of Phase 8)

Spare physical server. Create a directory pool and a safe network. Run a system container and a KVM VM. CT Terminal/Files and VM compatibility console. Guests survive CP/agent stop. LAN HTTP is still allowed.

### Homelab Migration Candidate (end of Phase 12)

HTTPS is on. Snapshots work. Backup engine has a restore you have run. Platform update does not bounce guests. Recovery runbook exists. GPU and ZFS are **not** required to pass this gate.

### Feature-Complete Beta (end of Phase 42)

All major CE product systems exist: VM, CT, OCI, Store, clustering, object backup, Guest Agent, GPU assign, AI Operate. Hardening and soak remain.

### No-dal CE 1.0 (end of Phase 43)

Vision CE capabilities listed in the CE 1.0 definition below are implemented, integrated, documented, tested, and hardened. Not "a polished MVP."

---

## CE 1.0 definition

CE 1.0 means a user can install No-dal with one command on a supported host (or via the manual repo path, or the installer ISO), finish first-run in the browser, run VMs, system containers, and OCI apps, protect them with native snapshots and backups (local and S3-compatible including R2), understand problems from metrics/logs/events, open Terminal and Files from the browser, assign GPUs explicitly, join a second node, migrate and restore across nodes, use the Store without root scripts, and optionally use BYO-AI through structured actions. No Cloud or EE required.

**Included at 1.0**
- Workloads: system container, VM, OCI, application stacks
- Storage: Directory, ZFS, LVM-thin, NFS, SMB, iSCSI, storage classes
- Networking: bridges, isolated, NAT, LAN-bridge, DHCP/DNS/static, VLAN, bonds, firewall policies, WireGuard remote nodes, overlays
- Backup: snapshots, local, NFS/SMB destinations, S3/R2/B2/MinIO, encrypt-before-upload, retention, verify, restore, file-level where possible, cross-node restore
- Clustering: join, inventory, placement, groups, maintenance, migration, HA foundations, rolling updates
- GPU: inventory, runtime readiness, explicit render/compute/VFIO assignment for CT, OCI, VM
- Store: Community plus Verified/Official pipeline, signatures, lifecycle, hooks
- AI: Ask, Plan, Operate, Automate on structured actions
- Terminal/Files: host, CT, VM via Guest Agent
- Guest Agent: Linux and Windows, optional, VMs usable without it
- Security: local users, groups, roles, MFA, tokens, service and node identities, secrets, HTTPS, audit, rate limits
- API/CLI: complete supported API, `nodalctl`, generated schemas
- Observability: live and historical metrics, journald logs, local alerts, events, change timeline
- Updates/recovery: signed packages, channels, preflight, rollback, guest survival, recovery runbook
- UX: Guided, Advanced, Expert with acknowledgement, search, command palette, responsive where practical
- Optional modules: Kubernetes, distributed storage (installable, not default)
- Host OS: Debian 13 amd64 Tier 1. Ubuntu LTS as a second host if Phase 29 qualification passed. Host-platform adapter mechanism present so further distros are additive. Guest, system-container, and OCI images remain independently broad.
- Install: one-line HTTPS bootstrap, documented manual repo/package path, and installer ISO. All three converge on the `nodal` metapackage, the same services, and `/setup`. Uninstall does not delete workload data.

**Intentionally post-1.0 CE**
- Full SDN controller / flow-table fabric (vision: "as the platform matures")
- ostree / immutable host
- arm64 as a supported **host**
- Additional explicitly qualified **host** distributions beyond Ubuntu LTS
- Host-root ZFS
- CRIU live migration of system containers (offline migrate in 1.0)

**Intentionally EE (not in this roadmap)**
- SSO / SAML / OIDC / LDAP
- Advanced compliance / SIEM export / immutable off-box audit
- Commercial support, SLAs, fleet-as-a-service product
- Enterprise deployment tooling beyond CE ISO/packages

**Intentionally Cloud (not in this roadmap)**
- Hosted control plane, hosted relay, hosted alert SaaS
- Paid marketplace transaction fees and licensing commerce
- Managed update channels as a hosted service

**CE still includes** a Settings license-activation **surface** so an EE key can be entered later without reinstall (Phase 43). Activation must never stop workloads. CE does not require a key.

---

## Phase template note

Every phase below uses the same headings. "See Phase N" is a cross-reference, not a deferral bucket.

---

## Phase 0. Foundation

**Objective.** Empty `ndl-ce` becomes a mergeable monorepo with CI and the install-layout stubs Phase 1 will fill.

**Why now.** Nothing else can land safely. Package and bootstrap structure must exist so Phase 1 does not invent a second install path.

**Dependencies.** None. Architecture plan section 29 structure.

**Architecture.** Go module, proto skeleton (`Hello`/`Observe`/`Execute`), Vite skeleton, Debian 13 packaging drafts, `capabilities` table stub, no `Host.Exec` in the proto. Add a thin `internal/hostos` seam: os-release detection types, a `debian` implementation package, and a supported-host list that currently contains Debian 13 only. Do not implement other distros. Do not build a plugin loader.

Packaging stubs in-repo: metapackage `nodal`, split packages `ndl-control` / `ndl-agent` / `ndl-ui` / `nodalctl`, systemd unit drafts, a **small** bootstrap script (detect, refuse unsupported, add repo placeholder, install metapackage). The script is not the platform. No live `get.no-dal.com` required in this phase.

**Control plane / Agent / Frontend.** Hello binaries and a static CI-works page only.

**Database.** Migration runner exists. No product schema yet.

**Security.** Em dash `file:line` check. CI cannot run privileged host mutations. Bootstrap script has no secrets.

**CLI/API.** Proto and OpenAPI stubs generate.

**Tests.** `go test`, UI vitest hello, codegen diff, actionlint. Bootstrap size/sanity check so it cannot grow into an unsigned installer.

**Recovery tests.** Not applicable.

**Acceptance.** GitHub `ci.yml` required on default branch. Em dash violation fails with file and line. No Docker required. `internal/hostos` exists with Debian types and a supported-host list. No Ubuntu adapter yet. Packaging and bootstrap stubs exist. Bootstrap does not contain PostgreSQL or service-user implementation.

**User capability.** Developers can open a PR. Operators cannot install a product yet.

**Complexity.** Small. Agent effort: hours to 1 day. Human: 1 hour (create GitHub repo). Uncertainty: org/repo name.

**Follow-on.** Phase 1.

---

## Phase 1. First boot

**Objective.** On a clean Debian 13 amd64 host, one bootstrap command installs No-dal. The user opens the printed URL, claims `/setup`, and logs in. `nodalctl` works. Debian 13 is the only supported host in this phase.

**Why now.** Identity, RPC, reconciliation, and the install path must exist before inventory or compute. Installation is a product surface, not a leftover.

**Dependencies.** Phase 0.

**Architecture.** Unprivileged `ndl-control`, root `ndl-agent`, unix socket plus `SO_PEERCRED`, Postgres, first-party auth, deny-by-default RBAC (`admin`/`operator`/`viewer`), operation table, Observe-first reconciler (idle), `cluster_id`/`node_id` on disk and in DB. LAN HTTP allowed. Agent detects host OS from os-release and reports it on `Hello`. Unsupported hosts fail closed with a clear error. Debian-specific package/repo helpers live only in `internal/hostos/debian`.

User-facing install is the `nodal` metapackage. Package postinst (not the curl script) creates service users, systemd units, prepares local PostgreSQL 16, runs migrations, generates the console setup token, and starts `ndl-control` and `ndl-agent`. Core deps only. No GPU, K8s, Ceph, ZFS DKMS, or AI packages.

Bootstrap script (`https://get.no-dal.com` or a Phase 1 placeholder with the same contract): detect OS/arch, verify Debian 13 amd64, preflight, install signing key and signed repo, `apt install nodal`, health-check units, pick a management address, print completion. Idempotent re-run where practical. Warn on conflicting existing PostgreSQL or No-dal config. Fail closed on unsupported OS/arch. Do not force Debian packages onto another distro.

Manual path is documented and must produce the same result: add signing key, add repo, `apt update`, `apt install nodal`, open the URL.

**Control plane.** Setup token claim, sessions, lockout, audit writer, `/api/v1`, static UI, OpenAPI. Stores `host_platform` from the agent. Does not encode Debian package names in the northbound API. Listens after package start. Health endpoint for the bootstrap check.

**Agent.** Socket, `Hello` (includes `host_platform` id, version, family, support tier, capabilities), local `Enroll`, empty `Observe`. Refuse enroll on an unsupported host OS. Started by systemd from the package.

**Frontend.** `/setup`, `/login`, shell, `/me`, empty dashboard state.

**Database.** Package prepares a local PostgreSQL 16 instance and role for No-dal. Schema: `clusters`, `nodes`, `users`, `roles`, `role_bindings`, `sessions`, `api_tokens`, `operations`, `events`, `audit_events`, `secrets` (schema), `capabilities`. Existing unused Postgres is adopted only after an explicit safe check. Conflicting clusters fail with a useful error.

**Security.** HTTPS bootstrap and signed repo. Package signatures verified. No factory password. No embedded secrets in the script. Safe temp files. Setup token printed or journaled, not a default admin. Last-admin protection, `nodalctl recover-admin`, brute-force lockout, rate limits on login. No silent destructive host changes.

**CLI/API.** `nodalctl setup`, `login`, `whoami`, `token create`. Every privileged route calls `authorize()`. Install helpers used by postinst live in `nodalctl` or a package helper, not in curl.

**Tests.** Setup single-use, RBAC deny, peercred reject, recover-admin. Bootstrap: supported Debian 13 amd64, unsupported distro rejection, unsupported arch rejection, repo and signature failure, dependency install, database init, systemd start, health check, URL discovery, first-admin setup, rerun/idempotency, interrupted install, package failure, existing Postgres/config conflict. Clean-Debian VM/physical test **must** use the one-line path (or the same script invoked as `sh get.sh` against a test repo). Dev scripts are extra, not a substitute.

**Recovery tests.** Kill CP: host unchanged. Replay setup: refused. Interrupted `apt install` can be retried. Uninstall packages: services gone, no workload data yet so the "do not delete disks" contract is still documented and tested with a fake data dir if needed.

**Acceptance.**
- Clean Debian 13: bootstrap, services healthy, printed URL opens `/setup`, first admin created, replay claim fails.
- User did not manually install PostgreSQL, individual `ndl-*` packages, repos, users, units, or config for the happy path.
- Second claim fails. CP user cannot open `/dev/kvm`. Token login works.
- Fedora (or any non-Debian-13) run prints that No-dal does not currently support this host platform, lists Debian 13 amd64, and installs nothing.
- Manual repo path documented and produces the same units and `/setup`.
- Completion message uses HTTP until Phase 9. Uninstall docs state that workload data is not removed by `apt remove`.

**User capability.** You run one command on Debian 13, open the URL, and have an appliance login and API. No workloads.

**Complexity.** Large. Agent: several days. Human: half to one day on a **clean** Debian 13 VM using the bootstrap path. Uncertainty: packaging, local Postgres, signed-repo hosting in early development.

**Follow-on.** Phase 2. HTTPS URL in install output is Phase 9. Additional host distributions and the ISO are Phase 29. Optional feature packages are Phase 35 plus later feature phases.

---

## Phase 2. Host visibility (MVP)

**Objective.** Real hardware, jobs, events, and live meters from this machine.

**Why now.** Proves Observe, operations, and no fake data before storage.

**Dependencies.** Phase 1.

**Architecture.** Inventory cache (not desired). Agent scrape. Jobs with stages. Events vs audit. Metrics in agent SQLite, read over RPC. Host OS identity and capability flags are observed facts, not desired state.

**Control plane.** Inventory upsert, operation watch, events WS, metrics query.

**Agent.** sysfs/udev collect: CPU topology, RAM, disks, NVMe, SATA, NIC, PCI, USB, GPU list, IOMMU groups, hwmon temps, DMI firmware, os-release host platform. Optional SMART/nvme-cli/DIMMs as `Not reported` if missing.

**Frontend.** Dashboard, node Summary/Hardware/Metrics/Events, `/tasks`. Empty series show `Collecting` or `Unavailable`.

**Database.** `hardware_inventory`, `node_observations`, operation progress, events.

**Security.** `node.read`, `events.read`, `metrics.read`. No host mutation yet.

**CLI/API.** `nodalctl node show`, `task list`, `event list`.

**Tests.** Fixture sysfs. No fake chart unit test.

**Recovery tests.** Agent restart refreshes inventory. Stale cache marked.

**Acceptance.** CPU/RAM/disks/NICs match the host. GPU absence is valid. SMART unknown is not a green icon. Node page shows the detected host OS (Debian 13) as inventory, not as a workload or guest.

**User capability.** You can see the server you will virtualize. **MVP reached.**

**Complexity.** Medium. Agent: 2 to 4 days. Human: 2 hours on hardware. Uncertainty: odd OEM sysfs.

**Follow-on.** Phase 3. GPU assignment is Phase 14.

---

## Phase 3. Directory storage and image library

**Objective.** Workloads will have volumes and boot media.

**Why now.** Gate before compute (architecture plan).

**Dependencies.** Phase 2.

**Architecture.** `VolumeHandle`. UUID plus `backend_ref`. Storage **classes** (content: vm-disk, container-root, iso, template, backup-staging). Directory driver. ISO/cloud-image library. Capability flags.

**Control plane.** Pool/volume/library APIs. Reject create-workload until a usable pool exists (enforced in Phases 5 and 8, schema ready now).

**Agent.** Create directory pool, `qemu-img` offline, xattr `user.ndl.volume_id`, capacity (usable/allocated/provisioned), shared-filesystem warning.

**Frontend.** `/storage`, first-run pool step, image upload/list.

**Database.** `storage_pools`, `volumes`, `library_items`.

**Security.** `storage.*`. Paths never stored as desired identity.

**CLI/API.** `nodalctl storage pool create`, `volume create`, `image upload`.

**Tests.** Fake filesystem. Capability: Directory has no incremental send.

**Recovery tests.** Missing disk: pool `unavailable`, objects not deleted.

**Acceptance.** Upload an ISO. Create a volume by UUID. Headroom warning if pool is on `/`.

**User capability.** You can store images and disks. You cannot start a guest yet.

**Complexity.** Medium. Agent: 3 to 5 days. Human: 2 hours.

**Follow-on.** Phase 4. ZFS is Phase 15. LVM-thin is Phase 25. NFS/SMB/iSCSI datastores are Phase 26.

---

## Phase 4. Safe networking

**Objective.** Isolated, isolated-NAT, and LAN-bridge networks without locking out the UI.

**Why now.** Guests need L2. Management-NIC safety is a ship blocker.

**Dependencies.** Phase 3.

**Architecture.** Control-plane network objects are distro-neutral (bridge, isolated, isolated-nat, lan-bridge, reservations). On Debian 13, the host-platform adapter persists No-dal-owned units as `50-ndl-*.network(d)` and adopts existing management config. Shared primitives: TAP/veth later, dnsmasq on isolated bridges only, DHCP plus DNS plus static reservations, nftables `inet` host INPUT, rollback watchdog independent of CP. Do not store networkd paths as desired identity. Other persistence backends are Phase 29.

**Control plane.** Network objects, dry-run, danger flag, confirm. No Debian-only fields in the public API.

**Agent.** Debian adapter: networkd apply. Shared: dnsmasq instance per isolated bridge, `nft -c`, 120s probe, rollback.

**Frontend.** `/network`, first-run NIC step, typed confirm for dangerous ops.

**Database.** `networks`, `addresses`, `dhcp_reservations`.

**Security.** Operator cannot enslave management NIC. Admin plus confirm plus watchdog.

**CLI/API.** `nodalctl network create`, `network apply --dry-run`.

**Tests.** Fake netlink. Danger classification unit tests.

**Recovery tests.** Failed apply rolls back. CP death does not restart networkd.

**Acceptance.** Isolated DHCP works. LAN-bridge does not run a second DHCP. Management ifindex unchanged after isolated create. Single-NIC enslave requires typed ifname and rolls back on probe fail.

**User capability.** You can give guests a network. Overlay, VLAN-aware, bonds: Phase 27. WireGuard: Phase 28.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: half day (lockout drills). Uncertainty: hosts that are not systemd-networkd. Those are unsupported until a Phase 29 adapter exists.

---

## Phase 5. System containers

**Objective.** Create, start, stop, restart, delete, clone unprivileged liblxc containers.

**Why now.** Proves workload units, volumes, NICs, images without QEMU ABI risk.

**Dependencies.** Phases 3 and 4.

**Architecture.** liblxc plus lxcfs. Official LXC images, GPG verified. `nodal-ct@<uuid>.service`. Unprivileged default. Persisted uid map. Agent-owned storage/net. No Incus. No `lxc-net`. Container image names (Debian, Alpine, and others) are **system-container distributions**, not No-dal host OS support.

**Control plane.** `WORKLOAD_CREATE` for `kind=system_container`. Spec: image pin, CPU/RAM, volume, NIC, devices list (empty until Phase 14/18).

**Agent.** Fetch/unpack, write LXC config artifact, start/stop, Observe pid, cgroup limits.

**Frontend.** Create form (one form, more-options), workload summary, lifecycle, spec edit.

**Database.** `workloads` plus disks/nics. `last-applied` on disk.

**Security.** Unprivileged default. Privileged flag is explicit and audited.

**CLI/API.** `nodalctl workload create --kind system-container`.

**Tests.** Fake liblxc in CI. Virt-tagged later.

**Recovery tests.** `systemctl stop ndl-control ndl-agent`: CT stays up. Interrupted create: same `operation_id`, no second volume UUID.

**Acceptance.** Debian or Alpine CT gets a DHCP address. Image verified. Files jail root is the CT rootfs (wired in Phase 6). Snapshots: Phase 10. GPU: Phase 14. Migration readiness fields exist. Offline migrate: Phase 32.

**User capability.** You can run a real OS container.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 6. Terminal and Files (host and CT)

**Objective.** Daily admin without SSH for host (admin) and system containers (operator+).

**Why now.** Vision pillar. Also proves tickets before VM console.

**Dependencies.** Phase 5 (CT target). Host can start after Phase 2 but ships here.

**Architecture.** Tickets. Agent opens PTY (`openat2` jail for Files). `nodal.term.v1`. Dual console: CT attach PTY (Terminal) and lxc-console (compatibility). Permissions: `terminal.open` vs `files.*`. Upload Here / Terminal Here / Open Files.

**Control plane.** IO session APIs, audit of open/upload/download/delete.

**Agent.** PTY, resize, cwd poll, chunked upload, checksum optional.

**Frontend.** xterm.js, Files browser, paste confirm at 3+ lines, tabs, reconnect.

**Database.** `io_sessions`, ticket hashes.

**Security.** Host IO admin-only. Viewer: `files.read` only, no download, no terminal. Path escape tests.

**CLI/API.** `nodalctl node terminal`, `workload files ls`.

**Tests.** Jail `../` and symlink. RBAC matrix.

**Recovery tests.** Agent death: PTY gone, UI says session ended. Workload unaffected.

**Acceptance.** CT terminal plus upload/download. Admin host terminal. Viewer denied. Folder archive download can wait until this phase's stretch or Phase 17. VM Terminal/Files: Phase 20.

**User capability.** You can operate the host and CTs from the browser.

**Complexity.** Large. Agent: 1 week. Human: half day.

---

## Phase 7. QEMU prototype gate

**Objective.** Prove direct QEMU/QMP supervision is acceptable **before** building the full VM product.

**Why now.** Largest architecture risk. Must fail fast.

**Dependencies.** Phases 3 and 4 (disk plus tap). May overlap Phase 5.

**Architecture.** One pinned QEMU, one systemd unit, qemu user, AppArmor, QMP unix, serial/VNC unix, qemu-ga chardev, versioned machine type, persisted PCI addresses, frozen argv artifact. No libvirt object model.

**Control plane.** Minimal `Execute` to start/stop the prototype VM.

**Agent.** Supervisor: start, QMP connect/reconnect, crash detect, graceful ACPI, autostart after reboot.

**Frontend.** A lab page or CLI-only is acceptable. No product VM wizard yet.

**Database.** One prototype workload row or a lab flag.

**Security.** Non-root qemu. Unix sockets only. No human monitor.

**CLI/API.** `nodalctl lab qemu-proto` or equivalent.

**Tests.** Scripted: CP stop, agent stop, QEMU kill, host reboot, interrupted start, QMP reconnect.

**Recovery tests.** Those **are** this phase.

**Acceptance (all required).**
- Unit independent of CP/agent
- QMP reconnect after agent restart
- QEMU crash observed, not silent
- Stable machine ABI across restart
- Disk attach via VolumeHandle
- Confinement: not root
- AppArmor does not break QMP/sockets
- Console sockets work
- Graceful shutdown path
- qemu-ga channel present (guest package optional)
- Autostart after reboot when desired
- Interrupted start does not leak a second unit

**If this fails.** Stop. Write a decision record: keep QEMU and fix the gap, or adopt a hidden launcher that still forbids libvirt identity in the API. Do not silently switch. Phase 8 must not start until the record says proceed.

**User capability.** Engineers trust the supervisor. Users do not have a VM wizard yet.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 to 2 days on KVM hardware. Uncertainty: **highest in the roadmap**.

**Follow-on.** Phase 8 only if the gate passes.

---

## Phase 8. Virtual machines (Dogfood Host)

**Objective.** Product VM create/start/stop/restart/delete with real disks, NICs, cloud-init, and console.

**Why now.** Gate passed. Completes first useful compute with Phase 5.

**Dependencies.** Phase 7 proceed decision. Phases 3, 4, 6 (console tickets).

**Architecture.** VmSpec compiler from Phase 7 lessons. NoCloud seed. UEFI optional with vars volume. Compatibility console (serial plus VNC). qemu-ga channel always. Dynamic resize where QEMU allows (memory balloon / CPU later in Phase 18 if unsafe now). Guest OS inside the VM is independent of the No-dal **host** OS. A Debian 13 host may run Windows and other Linux guests.

**Control plane.** Full VM APIs. Spec edit.

**Agent.** Production `nodal-vm@` path.

**Frontend.** Create VM, Console tab, spec, disabled Terminal/Files with reason.

**Database.** VM spec JSON, firmware volumes, cidata.

**Security.** `compute.console` vs `terminal.open`. Ticketed VNC.

**CLI/API.** `nodalctl workload create --kind vm`.

**Tests.** Fake QMP in CI. Physical: cloud image boots.

**Recovery tests.** Repeat Phase 7 matrix on a product VM.

**Acceptance.** Cloud image or ISO boots. Console works without guest agent. `systemctl stop ndl-control ndl-agent` leaves VM running. PCI addresses persist. No `qemu-img` on live disks. **Dogfood Host reached** (with Phases 0 to 8).

**User capability.** You can run a real VM on a spare server.

**Complexity.** Very Large. Agent: 2 to 3 weeks. Human: 2 days. Uncertainty: firmware/cloud-init edge cases.

**Follow-on.** Clone/templates/import/USB/hotplug: Phase 18. Guest Agent: Phase 19. Live migrate: Phase 32.

---

## Phase 9. HTTPS and certificates

**Objective.** Management plane is TLS before important workloads move.

**Why now.** Required before Homelab Migration. Dogfood may still have used HTTP.

**Dependencies.** Phase 1 listeners. Should run after Dogfood so UX exists.

**Architecture.** Control plane TLS. Secure cookies. WSS for events and IO. HTTP to HTTPS redirect when TLS is enabled. Modes: generated self-signed, imported PEM, later ACME.

**Control plane.** Cert store, listen 443, redirect, ACME client hook.

**Agent.** No change except health URL scheme.

**Frontend.** Certificate settings, trust instructions, ACME fields.

**Database.** `certificates`, renewal timestamps.

**Security.** Private keys 0600. No key download to browser. Session `Secure` flag when HTTPS.

**CLI/API.** `nodalctl cert generate`, `cert import`, `cert acme`.

**Tests.** Cookie Secure, WS reject cleartext when TLS required.

**Recovery tests.** Bad cert: keep last good, do not drop to open HTTP automatically on a hardened install. First enable has a confirm.

**Acceptance.** UI, API, Terminal, Files, VNC tickets all work over TLS. Renewal/replace documented. ACME for public names and step-ca for private names both work or are explicitly documented as supported in this phase. Bootstrap and postinst completion messages print the HTTPS URL after this phase. Do not keep advertising HTTP as the normal management URL once TLS is the default.

**User capability.** You can manage the host without plaintext HTTP.

**Complexity.** Medium. Agent: 3 to 5 days. Human: 2 hours.

---

## Phase 10. Snapshots

**Objective.** Point-in-time restore on the same pool. Snapshot is not backup.

**Why now.** Needed before the backup engine and before Homelab Migration.

**Dependencies.** Phases 5 and 8.

**Architecture.** Snapshot objects. External qcow2 overlays on Directory. ZFS snaps when Phase 15 exists. Until then Directory/qcow2 only. Capability flags. CT Directory snap is copy or hidden. Purpose tags `ndl-user-*`.

**Control plane.** Snapshot API. Rollback.

**Agent.** Quiesce hook (qemu-ga freeze best-effort). Never `qemu-img` live.

**Frontend.** Workload Snapshots tab. Button is not labeled Backup.

**Database.** `snapshots` with GUIDs.

**Security.** `compute.snapshot` / `storage.snapshot`.

**CLI/API.** `nodalctl snapshot create|rollback`.

**Tests.** Chain cap. Capability hidden on Directory CT.

**Recovery tests.** Rollback after a bad package inside the guest.

**Acceptance.** VM overlay snap works. CT on Directory does not pretend to be ZFS. Backup is Phase 11.

**User capability.** You can undo a bad change on the same disk.

**Complexity.** Medium. Agent: 4 to 6 days. Human: half day.

---

## Phase 11. Backup engine (local, NFS, SMB destinations)

**Objective.** Independent copies with schedule, retention, and a restore you have run.

**Why now.** Homelab Migration requires a proven restore. Object storage is Phase 23.

**Dependencies.** Phase 10. Secrets schema from Phase 1.

**Architecture.** Snapshot then copy. Destinations: local other pool/directory, NFS, SMB **as backup targets** (datastores as compute backends are Phase 26). Schedule. Retention GFS. Compression. Catalog. `restore.mode = new | replace`. Incremental where the backend allows (full copy is honest if not). Dedup: only if an engine exists without a rewrite. Else full/incremental and Phase 23 revisits.

**Control plane.** Targets, policies, runs, restore jobs.

**Agent.** Read snapshot, write artifact, checksum.

**Frontend.** `/backups` appears now.

**Database.** `backup_targets`, `backup_policies`, `backup_runs`, `backup_artifacts`.

**Security.** Target credentials in secrets. `backup.restore` is destructive confirm.

**CLI/API.** `nodalctl backup run`, `backup restore`.

**Tests.** Fake target. Restore new UUID.

**Recovery tests.** **Must restore a workload on the same node and boot it.** This is the Homelab gate, not Phase 24.

**Acceptance.** Nightly policy works. Retention prunes backups, not live snaps needed for the chain. Snapshot button remains distinct.

**User capability.** You can survive a deleted VM if the target disk is intact.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day including a real restore.

**Follow-on.** S3/R2 Phase 23. Verify jobs Phase 24. Cross-node Phase 33.

---

## Phase 12. Platform updates (Homelab Migration Candidate)

**Objective.** Update No-dal without stopping guests.

**Why now.** Closes the Homelab bundle with HTTPS, snapshots, and backup.

**Dependencies.** Phase 8 (split packages). Phase 9 recommended first.

**Architecture.** Update API is host-platform-neutral (channels, preview, changelog, preflight, checkpoint, apply, rollback). On Debian 13 the adapter uses the **same** signed apt repo and `nodal` metapackage as Phase 1. Split `.deb` packages (`ndl-control` / `ndl-agent` / `ndl-ui`) so a CP bump does not restart QEMU. GRUB previous-kernel rollback. Preflight covers kernel / ZFS module / NVIDIA packages via the Debian adapter. Checkpoint `/var/lib/ndl` plus `pg_dump`. Store compatibility check becomes real in Phase 36 (hook interface now). Do not call `apt` from workload or API packages. An older No-dal package must upgrade through this path. The one-line bootstrap is not a second updater.

**Control plane.** Update API. No `apt` verbs in the public contract.

**Agent.** `Host.PrepareUpdate` and apply go through the host-platform adapter. Debian 13: apt. Other package managers: Phase 29.

**Frontend.** Settings / Updates.

**Database.** Update history events.

**Security.** Signed InRelease. `node.update`.

**CLI/API.** `nodalctl update check|apply`.

**Tests.** Dry-run preflight. Upgrade from the previous No-dal package version on a host that was installed with the Phase 1 bootstrap or the manual repo path.

**Recovery tests.** Apply CP-only bump. Guests stay. Roll back CP package.

**Acceptance.** Documented recovery.md. Upgrade uses the signed repo, not a one-off script. **Homelab Migration Candidate reached** if Phases 9 to 12 pass on hardware.

**User capability.** You can patch the control plane while VMs stay up.

**Complexity.** Medium. Agent: 4 to 6 days. Human: half day.

**Follow-on.** Cluster-aware rolling updates: Phase 34.

---

## Phase 13. Identity completion

**Objective.** Groups, MFA, service identities, full audit UX, encryption at rest for secrets/volumes-as-configured.

**Why now.** After Homelab gate so MFA does not block migration. Schema existed from Phase 1.

**Dependencies.** Phase 1.

**Architecture.** TOTP/WebAuthn, `aal`, recovery codes. Groups. Service principals. Volume LUKS or ZFS native encryption fields (ZFS encrypt create-time: usable after Phase 15).

**Control plane.** MFA enroll/challenge, group APIs, audit browser (`audit.read`).

**Agent.** Unlock encrypted volumes with `secret.use` only.

**Frontend.** MFA screens, groups, audit log.

**Database.** `mfa_methods`, `groups`, encryption fields.

**Security.** Step-up for `secret.reveal`, `cluster.destroy`.

**CLI/API.** `nodalctl user mfa`, `group add`.

**Tests.** MFA challenge, token cannot exceed creator.

**Recovery tests.** Lost last MFA: recover-admin still works.

**Acceptance.** Viewer cannot read audit. Operator can use MFA. License activation UI is Phase 43.

**User capability.** You can run a multi-user homelab with MFA.

**Complexity.** Medium. Agent: 4 to 6 days. Human: 2 hours.

---

## Phase 14. GPU runtime and assignment

**Objective.** Install GPU support once per node. Workloads consume GPUs only when assigned.

**Why now.** After backup/update so a bad VFIO bind is recoverable. Inventory already exists (Phase 2).

**Dependencies.** Phases 2, 5, 8, 11 recommended.

**Architecture.** Explicit `gpu_id`. Modes: `render`, `compute`, `encode`, `vfio`. Assignment uses shared Linux device nodes, IOMMU groups, cgroup device BPF, and VFIO. NVIDIA/AMD/Intel **runtime install** (packages, DKMS, persistenced) is a host-platform concern. Debian 13 adapter only until Phase 29. Never `NVIDIA_VISIBLE_DEVICES=all`. IOMMU group shown in full. Refuse ACS override as default. Conflict locks. CUDA/ROCm readiness as node feature flags, not silent.

**Control plane.** Device claims. Placement later uses caps (Phase 31).

**Agent.** Bind-mount plus cgroup device BPF for CT. VFIO bind/unbind for VM. OCI assignment consumes same claims in Phase 21.

**Frontend.** Node GPU tab assign. Workload GPU tab. Store GPU picker is Phase 36.

**Database.** `gpu_assignments`.

**Security.** No default GPU. Exclusive vs shared documented.

**CLI/API.** `nodalctl gpu assign`.

**Tests.** Reject `gpu=all`. Group completeness.

**Recovery tests.** Failed VFIO: restore host driver. Snapshot first.

**Acceptance.** Create without GPU gets no `/dev/dri`. Two exclusive claims fail. HDMI audio in group is listed.

**User capability.** You can give a transcode workload one GPU and keep isolation.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day per vendor you own.

**Follow-on.** OCI GPU in Phase 21 uses this API. MIG discovery in this phase if NVML offers it. Full licensed vGPU stacks are post-1.0 CE.

---

## Phase 15. ZFS storage

**Objective.** First-class ZFS pools, zvols, datasets, snapshot/send.

**Why now.** Optional for Homelab. Required for CE 1.0 storage breadth and cheap snaps.

**Dependencies.** Phase 3 interfaces.

**Architecture.** Import by GUID. Create on extra disks (not root). zvol for VM disks. dataset for CT roots. Per-UUID datasets. `zfs send`/`recv` for BackupSource. Replication hooks used in Phase 33.

**Control plane.** ZFS pool wizard. Capability: incremental send = true.

**Agent.** `libzfs_core` or validated `zfs` argv. ZFS userland operations are shared. Kernel/module install remains a host-platform concern (Debian adapter in this phase).

**Frontend.** Import/create ZFS. Honest vs Directory.

**Database.** `zpool_guid`, dataset GUIDs.

**Security.** No auto `zpool import -f`.

**CLI/API.** `nodalctl storage zfs import`.

**Tests.** Capability matrix.

**Recovery tests.** Pulled disk: pool faulted, rows remain.

**Acceptance.** zvol VM plus dataset CT. Directory remains default for hosts without ZFS.

**User capability.** You can use a real ZFS pool.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 16. Observability complete

**Objective.** Historical metrics, logs, local alerts, change timeline, capacity, storage latency, per-workload traffic.

**Why now.** Spine exists (Phase 2). Now make it a product.

**Dependencies.** Phase 2. Workloads from 5/8.

**Architecture.** SQLite TS plus downsample. journald follow. Alert rules write events. Local delivery: webhook and optional local SMTP (not Cloud). Capacity forecast from pool samples. SMART health surface.

**Control plane.** Alert rules, notification channels (self-hosted).

**Agent.** io.stat, net counters, latency histograms where cheap.

**Frontend.** Range picker, log viewer, timeline, alert settings.

**Database.** `alert_rules`, `notification_channels`.

**Security.** Webhook URLs are secrets. No metric samples in Postgres.

**CLI/API.** `nodalctl logs`, `metrics query`, `alert list`.

**Tests.** Empty series still not fake.

**Recovery tests.** Agent down: UI shows stale, not zeros.

**Acceptance.** "What changed before this broke?" answerable from events plus metrics plus audit.

**User capability.** You can diagnose without SSH and without a required extra monitoring stack.

**Complexity.** Medium. Agent: 1 week. Human: half day.

---

## Phase 17. Operator UX

**Objective.** Guided / Advanced / Expert, search, command palette, responsive chrome, live status polish.

**Why now.** APIs exist. Presentation only.

**Dependencies.** Phases 8 and 6 at minimum.

**Architecture.** One form, progressive disclosure. Expert acknowledgement one-time. Same APIs.

**Control plane.** `PATCH /me` ux_level, expert_ack.

**Agent.** None.

**Frontend.** Palette (`Create VM`, jump to task), search, tablet layout, onboarding empty states.

**Database.** User prefs.

**Security.** Expert does not grant permissions.

**CLI/API.** No new infra APIs.

**Tests.** Viewer plus Expert is read-only.

**Recovery tests.** Not applicable.

**Acceptance.** Guided create still posts the same body as Advanced. Palette only lists authorized actions.

**User capability.** The UI feels like a product, not three products.

**Complexity.** Medium. Agent: 4 to 6 days. Human: 3 hours.

---

## Phase 18. VM advanced

**Objective.** Templates, clone, import/export, USB passthrough, UEFI/Secure Boot polish, generic PCI passthrough, hotplug where safe.

**Why now.** Dogfood VMs exist. These are CE section 7 VM capabilities.

**Dependencies.** Phase 8. GPU VFIO path from Phase 14 for PCI.

**Architecture.** Template = volume snapshot plus spec. Import via `qemu-img convert`. Export artifact. USB `usb-host` or VFIO. PCI non-GPU devices use the same IOMMU rules.

**Control plane.** Template/clone/import APIs.

**Agent.** Convert, attach USB, hotplug device_add when ABI allows.

**Frontend.** Template library, import wizard.

**Database.** `templates`.

**Security.** Import is privileged. PCI group listing required.

**CLI/API.** `nodalctl workload clone|import|export`.

**Tests.** Clone gets new UUIDs and new MAC.

**Recovery tests.** Failed import leaves no half-adopted volume.

**Acceptance.** Clone boots. Import of a qcow2 works. USB attach listed in inventory.

**User capability.** You can duplicate and import VMs.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 19. No-dal Guest Agent

**Objective.** Optional guest package for Linux and Windows.

**Why now.** Needed before VM Files/Terminal. qemu-ga remains freeze/shutdown.

**Dependencies.** Phase 8 channel strategy. Second virtio-serial `org.nodal.guest.0` or vsock.

**Architecture.** Guest daemon implements Files plus PTY RPCs the node agent already speaks. Not required to create/start a VM. IP reporting, metrics, freeze coordination (may call qemu-ga), application awareness hooks for Store (Phase 36).

**Control plane.** `ga_state` including `nodal_ga`.

**Agent.** Multiplex guest channel.

**Frontend.** Install instructions, badge.

**Database.** Guest agent version on observation.

**Security.** Channel is not a host root shell. Same `files.*` / `terminal.open`.

**CLI/API.** `nodalctl guest status`.

**Tests.** Fake guest channel in CI.

**Recovery tests.** Missing agent: VM still usable via Console.

**Acceptance.** Linux guest PTY proof. Windows guest at least shutdown/IP/files subset. Full Windows Files if feasible. If Windows Files slip, they stay in this phase until done (not a new unnamed bucket).

**User capability.** Guests can be administered like CTs when the agent is installed.

**Complexity.** Large. Agent: 2 weeks. Human: 1 to 2 days (two guest OSes).

---

## Phase 20. Guest Terminal and Files

**Objective.** VM Terminal and Files meet the Phase 6 quality bar.

**Why now.** Guest Agent exists.

**Dependencies.** Phases 6 and 19.

**Architecture.** Same tickets, jail inside guest `/`.

**Control plane / Frontend.** Enable VM tabs when `nodal_ga` is ok.

**Agent.** Proxy to guest.

**Security.** Same permission verbs. Audit paths as `vm:/...`.

**CLI/API.** Same `workload files` / `terminal` with VM ids.

**Tests.** Permission split.

**Recovery tests.** Agent disconnect disables tabs with a reason.

**Acceptance.** Terminal Here / Upload Here work on a Linux VM.

**User capability.** You do not need SSH into the guest for routine files.

**Complexity.** Medium. Agent: 3 to 5 days. Human: 3 hours.

---

## Phase 21. OCI application workloads

**Objective.** First-class OCI containers (not a separate product).

**Why now.** Compute kinds already reserved. After core virt so the runtime is a module.

**Dependencies.** Phases 3, 4, 14 (GPU claims).

**Architecture.** containerd or equivalent. Kind `oci`. Registries plus private auth (secrets). Ports, volumes (No-dal volume UUIDs, no anonymous volumes), networks, env, secrets, health, logs, resource limits, GPU via Phase 14, updates/rollback of the image pin. Console = logs plus exec (not a system systemd). OCI image names are **application images**, not No-dal host OS support.

**Control plane.** OCI spec APIs.

**Agent.** Pull, run, cgroup, journal.

**Frontend.** Create OCI, logs tab.

**Database.** `registries`, image pins.

**Security.** No privileged-by-default. No host bind to `/`.

**CLI/API.** `nodalctl workload create --kind oci`.

**Tests.** Fake runtime.

**Recovery tests.** Unit survives CP stop.

**Acceptance.** Private registry pull with stored creds. Healthcheck visible. GPU optional assign.

**User capability.** You can run app containers next to VMs/CTs.

**Complexity.** Large. Agent: 2 weeks. Human: 1 day.

---

## Phase 22. Application stacks and Compose import

**Objective.** Multi-container apps with one desired-state document.

**Why now.** OCI exists. Store (Phase 36) will deploy stacks.

**Dependencies.** Phase 21.

**Architecture.** Stack object. Compose import to No-dal stack (inspectable, not a hidden compose process as source of truth). Shared volumes and networks.

**Control plane.** Stack CRUD, import.

**Agent.** Apply as N OCI units plus attachments.

**Frontend.** Stack view.

**Database.** `stacks`, `stack_members`.

**Security.** Import is declarative. Reject `privileged: true` unless authorized.

**CLI/API.** `nodalctl stack import compose.yml`.

**Tests.** Compose fixture.

**Recovery tests.** Partial stack apply resumes.

**Acceptance.** Imported stack is editable as No-dal objects.

**User capability.** You can bring a compose file without making Compose the platform.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 23. Object-storage backup (S3, R2, B2, MinIO)

**Objective.** Encrypt-before-upload backups to S3-compatible stores, including Cloudflare R2.

**Why now.** Local engine exists. Object storage is a CE north-star feature.

**Dependencies.** Phase 11.

**Architecture.** One destination class. Providers: generic S3, R2, AWS S3, Backblaze B2, MinIO. Client-side encryption (No-dal keys). Multipart, resume. Incrementals (ZFS send -i and/or qcow2 bitmaps). Compression. Dedup if practical without a second product UX. R2: no Object Lock assumption. `no_check_bucket` compatible tokens.

**Control plane.** Target wizard per provider.

**Agent.** Encrypt, upload, checksum.

**Frontend.** R2/S3 forms, last run, transferred bytes.

**Database.** Target kind plus endpoint plus secret ids.

**Security.** Keys never only in the bucket. SSE is extra, not sufficient.

**CLI/API.** `nodalctl backup target create --kind r2`.

**Tests.** MinIO in virt job, not every commit.

**Recovery tests.** Restore from object target to a new UUID on the same node.

**Acceptance.** Encrypted object appears. Restore boots. Incremental second run transfers less when the engine supports it.

**User capability.** You can protect workloads to R2/S3 from CE without another backup product.

**Complexity.** Very Large. Agent: 2 to 3 weeks. Human: 1 to 2 days.

---

## Phase 24. Backup verification and file restore

**Objective.** Verify jobs, restore tests, file-level restore where possible.

**Why now.** Destinations exist. Trust requires more than a checksum at write.

**Dependencies.** Phases 11 and 23. Guest/CT Files help file-level (Phases 6, 20).

**Architecture.** Open-verify (receive to scratch or `qemu-img check`). Scheduled restore-to-throwaway. File-level from received dataset or nbd/libguestfs.

**Control plane.** Verify jobs, last-tested timestamp.

**Agent.** Scratch restore, file extract.

**Frontend.** Verify badge, file restore picker.

**Database.** Verify results on artifacts.

**Security.** Throwaway workloads isolated.

**CLI/API.** `nodalctl backup verify`, `backup restore-file`.

**Tests.** Failed checksum marked unverified.

**Recovery tests.** Restore test must not touch the source workload.

**Acceptance.** A backup without catalog plus verify is shown as unverified.

**User capability.** You can prove a backup works before disaster.

**Complexity.** Medium. Agent: 1 week. Human: half day.

---

## Phase 25. LVM and LVM-thin

**Objective.** LVM-thin as a local pool driver.

**Why now.** VolumeHandle already defined. Users who refuse ZFS still get thin snaps.

**Dependencies.** Phase 3.

**Architecture.** VG/thin pool. Thin LV for disks. Honest metadata warnings. No fake send.

**Control plane.** LVM wizard.

**Agent.** LVM D-Bus or validated argv. `lvcreate` thin.

**Frontend.** Create thin pool.

**Database.** `vg_uuid`, `lv_uuid`.

**Security.** No accidental VG export.

**CLI/API.** `nodalctl storage lvm create`.

**Tests.** Capability: no incremental send.

**Recovery tests.** Missing PV: pool unavailable.

**Acceptance.** Thin snap works. Metadata percent visible.

**User capability.** You can use LVM-thin without ZFS.

**Complexity.** Medium. Agent: 1 week. Human: half day.

---

## Phase 26. NFS, SMB, and iSCSI datastores

**Objective.** Network storage as **compute/library** backends (backup-as-destination already in Phase 11).

**Why now.** Classes exist. Cluster later will use shared locators.

**Dependencies.** Phase 3.

**Architecture.** Mount or login, then Directory-like ops or block handle. Target UUID, not path.

**Control plane.** Datastore objects, reachability.

**Agent.** Mount, iSCSI login, health.

**Frontend.** Add NFS/SMB/iSCSI.

**Database.** `datastore` locators plus secret ids.

**Security.** Credentials in secrets. Do not store passwords in unit files world-readable.

**CLI/API.** `nodalctl storage nfs add`.

**Tests.** Fake mount.

**Recovery tests.** Share down: volumes unavailable, not deleted.

**Acceptance.** ISO library on NFS. VM disk on iSCSI or NFS file.

**User capability.** You can put images and disks on the NAS.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 27. Advanced networking

**Objective.** VLANs, bonds, firewall policies, overlays, per-workload traffic visibility.

**Why now.** Simple nets exist. CE section 10 remainder except WireGuard (Phase 28).

**Dependencies.** Phase 4 watchdog (re-test).

**Architecture.** Stacked VLAN in this phase. VLAN-aware bridge if we can do it safely. Bonds: `active-backup` then LACP. nftables `bridge` family for guest policy. Overlay for workload L2/L3 between nodes (prep, usable after Phase 30). Metrics per tap/veth.

**Control plane.** Policy objects.

**Agent.** `bridge vlan`, bond netdev, nftables.

**Frontend.** VLAN/bond/policy editors. Danger path reused.

**Database.** `network_policies`, bond/vlan objects.

**Security.** Policies cannot drop management INPUT.

**CLI/API.** `nodalctl network vlan add`, `policy apply`.

**Tests.** Watchdog still wins.

**Recovery tests.** VLAN-aware apply with rollback.

**Acceptance.** Access port on VID 20. Bond shown. Workload policy denies a pair.

**User capability.** You can do homelab VLANs without hand-editing.

**Complexity.** Large. Agent: 2 weeks. Human: 1 day.

**Follow-on.** Full SDN controller: post-1.0 CE.

---

## Phase 28. WireGuard and remote nodes

**Objective.** Encrypted node connectivity. Remote worker topology.

**Why now.** Before cluster join so the second node has a path.

**Dependencies.** Phase 9 TLS for RPC. Phase 4.

**Architecture.** WireGuard via the host-platform network persistence adapter (Debian 13: systemd-networkd WireGuard). mTLS still authenticates. Tunnel carries RPC/migrate later. Remote node: CP on A, agent on B.

**Control plane.** Peer config, join still Phase 30.

**Agent.** WireGuard netdev, `OpenSession` dial-out.

**Frontend.** Remote node helper.

**Database.** Peer public keys.

**Security.** Keys as secrets. Not a guest SDN by default.

**CLI/API.** `nodalctl cluster wg show` (pre-join peers).

**Tests.** Loopback WG.

**Recovery tests.** Tunnel down: node NotReady, guests keep running.

**Acceptance.** Agent on another machine heartbeats to CP.

**User capability.** You can place the control plane and a worker on different boxes.

**Complexity.** Medium. Agent: 1 week. Human: half day.

---

## Phase 29. Host platform compatibility and installer

**Objective.** Make additional **host** distributions additive. Keep the same public one-line URL. Ship a Debian installer ISO for bare metal. Qualify Ubuntu LTS as a second host through the host-platform mechanism, not a hardcoded one-off branch.

**Why now.** Debian 13 one-line install and updates are proven (Phases 1 and 12). The ISO solves a different problem: a machine with no OS yet. Do not move ISO work into Phase 1. Do not redo the bootstrap as a giant script.

**Dependencies.** Phase 12 packaging and the Phase 1 bootstrap plus `internal/hostos` seam.

**Architecture.** Formalize the host-platform interface used by package install/update, repo configuration, network persistence, firewall integration differences, kernel/module handling, GPU driver install, bootloader rollback, and distro-specific recovery. Shared Linux primitives stay shared. Add an `internal/hostos/ubuntu` adapter only after the interface is explicit. Debian 13 remains Tier 1. Ubuntu LTS becomes Tier 1 only after qualification tests pass. Other distributions are not claimed. Adding a later host means a new adapter plus tests, not a control-plane rewrite.

The public command stays `curl -fsSL https://get.no-dal.com | sudo sh`. The small bootstrap dispatches: Debian 13 path already shipped; Ubuntu LTS path added here if qualified. Unsupported hosts still fail closed and list supported platforms.

ISO (path C): mkosi or Debian-installer wrapping the **same** `nodal` metapackage and first-run `/setup`. After reboot the user sees the same browser setup as the one-line path. Ubuntu host install uses that distro's native packages from the same source tree. Adopt existing management networking. Do not dual-write Netplan and networkd on the same host.

**Control plane.** Stores and displays `host_platform`. Feature/update flows call adapter methods, not `apt` by name. No Ubuntu-only fields in workload or storage APIs.

**Node agent.** Selects the adapter from os-release. Unsupported hosts still fail closed unless an explicit experimental flag exists. Inventory continues to report host OS separately from guests, CTs, and OCI images.

**Frontend.** Host OS and support tier on the node page. Installer docs. Unsupported-host error copy.

**Database.** `host_platform` id, version, family, support_tier. Already sketched in Phase 1.

**Security.** ISO and packages signed. Same setup token. Experimental mode, if added, is audited and not the default.

**CLI/API.** `nodalctl node show` includes host platform. `nodalctl update` remains adapter-backed.

**Tests.** Hostos interface unit tests with a fake adapter. Ubuntu package/network persistence tests on a dedicated runner, not every commit. ISO boots in a VM.

**Recovery tests.** Failed install does not leave a half-joined cluster. Ubuntu adapter must not rewrite a Debian host's network persistence. Unsupported distro still refuses enroll.

**Acceptance.**
- Spare PC: boot Debian ISO, reboot, printed or documented URL, same `/setup` as Phase 1.
- One-line installer on Ubuntu LTS works **or** this phase is not complete if Ubuntu was intended and not qualified. If Ubuntu qualification fails, document the gaps and keep Debian as the only Tier 1 host. Do not fake support.
- A third distro can be added later by implementing one adapter and a test matrix. No major CP/agent rewrite required. The get.no-dal.com URL does not change.
- Workload, storage, network, backup, and cluster APIs are unchanged across hosts.
- Docs distinguish host OS, VM guest OS, system-container distribution, and OCI image. Docs list paths A, B, and C as the same installation.

**User capability.** You can install from ISO on a blank machine, or use the same one-line command on a second qualified host, without migrating architecture.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 to 2 days (Debian ISO plus Ubuntu lab). Uncertainty: Netplan adopt, Ubuntu packaging, DKMS differences.

**Follow-on.** Automated provisioning at CE level is this ISO plus cloud-init of the host, not EE. arm64 host: post-1.0 CE. Further host distros: post-1.0 CE after the same qualification process.

---

## Phase 30. Cluster join

**Objective.** Add a second node without rebuilding workloads.

**Why now.** IDs and RPC were cluster-shaped from Phase 1.

**Dependencies.** Phases 1, 9, 28 recommended.

**Architecture.** Join token, cluster CA, mTLS, `OpenSession`. One Postgres writer. Second node is a worker. Cluster inventory view. Hostname is not identity.

**Control plane.** Join APIs, refuse second writer.

**Agent.** Enroll over TLS.

**Frontend.** Settings, Cluster, Add Node, join code.

**Database.** Second `nodes` row.

**Security.** Single-use token, revoke node.

**CLI/API.** `nodalctl cluster join`.

**Tests.** Token reuse fails.

**Recovery tests.** Split-brain: two CP processes cannot both lease.

**Acceptance.** Two nodes in inventory. Existing VM on node A untouched.

**User capability.** You have a two-node cluster of one mental model.

**Complexity.** Large. Agent: 2 weeks. Human: 1 day.

---

## Phase 31. Placement and maintenance

**Objective.** Automatic / specific node / node group. Affinity. Drain/maintenance with migrate (migrate engine Phase 32).

**Why now.** Two nodes exist.

**Dependencies.** Phase 30. Storage class from Phase 3. GPU caps from Phase 14.

**Architecture.** Scheduler inputs: CPU, RAM, storage class, GPU, network, health, maintenance, priority, anti-affinity. Maintenance mode: list workloads to move.

**Control plane.** Placement, groups.

**Agent.** Feature Hello already exists.

**Frontend.** Placement radio. Maintenance wizard.

**Database.** `node_groups`, placement fields.

**Security.** Policy here is CE placement, not EE org governance.

**CLI/API.** `nodalctl node maintain`.

**Tests.** Scheduler fixtures.

**Recovery tests.** Placement never starts a second copy on the wrong node.

**Acceptance.** Create with automatic lands on a node that has the GPU/class.

**User capability.** You can drain a node after Phase 32 migrate exists. This phase can enqueue migrate jobs.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 32. Workload migration

**Objective.** Offline migrate, then live migrate for VMs.

**Why now.** ABI freeze and qemu-ga were designed in Phases 7 to 8.

**Dependencies.** Phases 8, 15 or shared storage (26), 30, 31.

**Architecture.** Dest builds identical ABI. Offline: stop, send volumes, start. Live: QMP migrate. CT: offline only in 1.0 (CRIU post-1.0). OCI: recreate on dest from images plus volume move.

**Control plane.** Migrate operation.

**Agent.** Incoming QEMU, volume transfer.

**Frontend.** Migrate action plus progress.

**Database.** Ownership epoch.

**Security.** `compute.migrate`.

**CLI/API.** `nodalctl workload migrate`.

**Tests.** Fake migrate. Physical: live VM ping.

**Recovery tests.** Failed live migrate leaves source running.

**Acceptance.** VM live-migrates between two nodes on shared or copied storage. CT offline-migrates.

**User capability.** You can empty a node for hardware work.

**Complexity.** Very Large. Agent: 3 weeks. Human: 2 days. Uncertainty: high.

---

## Phase 33. Cluster storage and disaster recovery

**Objective.** Cross-node restore as a normal workflow. Storage replication. DR runbook.

**Why now.** Backup catalog was cluster-shaped since Phase 11.

**Dependencies.** Phases 11, 23, 30.

**Architecture.** Restore to `target_node_id`. ZFS replica where used. Shared NFS/iSCSI already IDs. DR: restore entire node set from object storage plus metadata export.

**Control plane.** Restore placement.

**Agent.** Pull artifacts.

**Frontend.** Restore, node picker.

**Database.** Artifact locality vs pull URL.

**Security.** Dest node `secret.use` only for that restore.

**CLI/API.** `nodalctl backup restore --node`.

**Tests.** Source node down, restore on B.

**Recovery tests.** That is the test.

**Acceptance.** Documented DR: lose node A, restore workloads on B from R2/local.

**User capability.** A dead node is not a dead catalog if backups exist.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 34. HA foundations and rolling updates

**Objective.** Control-plane failover foundations. Cluster-aware rolling package updates.

**Why now.** Multi-node exists. Must not become a second product.

**Dependencies.** Phases 12, 30.

**Architecture.** Single writer. Promote/replica Postgres or move CP plus DSN. Fencing defined. Rolling: one node drain (31/32), update, next. Workloads keep running.

**Control plane.** HA status, rolling plan.

**Agent.** Update as Phase 12 per node.

**Frontend.** Cluster update wizard.

**Database.** Leader lease already exists. HA state.

**Security.** Promotion is privileged.

**CLI/API.** `nodalctl cluster update`.

**Tests.** Kill CP, standby takes lease (lab).

**Recovery tests.** Guests never stop because CP moved.

**Acceptance.** Rolling update documented. HA is foundations (leader plus replica), not magic multi-master.

**User capability.** You can update a 3-node cluster without a global outage.

**Complexity.** Very Large. Agent: 2 to 3 weeks. Human: 2 days.

---

## Phase 35. Feature modules

**Objective.** Settings, Features: install OCI, GPU services, K8s, distributed storage, AI without forcing all on a small box.

**Why now.** Vision section 8. Modules already have phases. This is the installer UX and dependency check.

**Dependencies.** Phase 1 `capabilities` row.

**Architecture.** Feature flags plus package sets. Default small. Heavy features opt-in. The base `nodal` metapackage from Phase 1 must stay light. GPU, Kubernetes, distributed storage, optional backends, and AI are extra packages enabled here, not silent Phase 1 dependencies.

**Control plane.** Feature API.

**Agent.** Package install via typed update, not scripts.

**Frontend.** Feature toggles.

**Database.** `features`.

**Security.** No root script from the Store here.

**CLI/API.** `nodalctl feature enable oci`.

**Tests.** Enabling a feature does not start K8s on a tiny node without confirm.

**Recovery tests.** Disable does not delete workloads without confirm.

**Acceptance.** Fresh install is light. GPU services optional.

**User capability.** A home server stays small.

**Complexity.** Small. Agent: 2 to 3 days. Human: 1 hour.

---

## Phase 36. No-dal Store

**Objective.** Declarative app install. Community apps. Upgrade, rollback, backup/restore hooks, GPU requirements, AI action **declarations**.

**Why now.** Stacks (22) and volumes exist. Trust pipeline is Phase 37.

**Dependencies.** Phases 21, 22, 35.

**Architecture.** Manifest YAML (vision section 13). No root helper scripts. Registry. Lifecycle. Hooks call existing backup/compute APIs.

**Control plane.** Catalog, install job maps to stack/workload create.

**Agent.** No script runner.

**Frontend.** Store grid, deploy form (node, CPU, RAM, pool, GPU, network).

**Database.** `store_packages`, `installations`.

**Security.** Unsigned Community: warn. Still no arbitrary exec.

**CLI/API.** `nodalctl app install`.

**Tests.** Manifest schema. Reject `run: bash`.

**Recovery tests.** Failed install rolls back objects.

**Acceptance.** One official-sample or Community sample app installs via manifest only.

**User capability.** You can deploy an app without hunting helper scripts.

**Complexity.** Very Large. Agent: 2 to 3 weeks. Human: 2 days.

---

## Phase 37. Store trust pipeline

**Objective.** Verified and Official classes. Signatures, provenance, vuln scan, permission analysis, network exposure, secret-handling checks, prohibited behavior, update testing.

**Why now.** Store without trust is a script market.

**Dependencies.** Phase 36.

**Architecture.** Signing keys. CI-like verifier. Classification: Community / Verified / Official.

**Control plane.** Verification records.

**Agent.** None (or isolated scanner module).

**Frontend.** Badges.

**Database.** `package_signatures`, `scan_results`.

**Security.** Verify before install when policy is Verified-only.

**CLI/API.** `nodalctl app verify`.

**Tests.** Tampered signature rejected.

**Recovery tests.** Revoked signing key stops new installs.

**Acceptance.** Tamper fails closed. Scan report visible.

**User capability.** You can require Verified apps.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 38. Optional Kubernetes

**Objective.** Kubernetes as an **optional feature**, never the foundation.

**Why now.** Vision section 8. Users who want it can install it. Others never see it.

**Dependencies.** Phase 35. Cluster (30) recommended.

**Architecture.** Module deploys a distro or connects to one. Workloads remain No-dal-visible where we wrap them. No requirement to know K8s to run a VM.

**Control plane.** Feature plus status.

**Agent.** Install/join kubelet only when enabled.

**Frontend.** Features, Kubernetes.

**Database.** Feature state.

**Security.** Isolated from default RBAC until enabled.

**CLI/API.** `nodalctl feature enable kubernetes`.

**Tests.** Default install has no kube process.

**Recovery tests.** Disable path documented.

**Acceptance.** VMs/CTs work with the module uninstalled.

**User capability.** You can add K8s without rebuilding No-dal.

**Complexity.** Very Large. Agent: 3 weeks. Human: 2 days.

---

## Phase 39. Optional distributed storage

**Objective.** Ceph-class (or equivalent) as an optional module.

**Why now.** Vision sections 3 and 9. VolumeHandle `rbd`/`nbd` kinds.

**Dependencies.** Phases 3, 35, 30.

**Architecture.** New pool driver. Compute unchanged.

**Control plane.** Pool type `distributed`.

**Agent.** Client plus optional OSD feature. 1.0 minimum is consume an external cluster. OSD bring-up is a named sub-deliverable of this same phase, not a new unnamed bucket. If OSD bring-up is incomplete, the phase is not done.

**Frontend.** Attach distributed pool.

**Database.** Pool driver = distributed.

**Security.** Keys in secrets.

**CLI/API.** `nodalctl storage distributed attach`.

**Tests.** Fake RBD handle.

**Recovery tests.** Cluster down: volumes unavailable.

**Acceptance.** A VM disk can be an RBD (or documented equivalent).

**User capability.** You can point No-dal at distributed block storage.

**Complexity.** Very Large. Agent: 3 weeks. Human: 2 days.

---

## Phase 40. Automation engine

**Objective.** Deterministic policies/workflows. Not an LLM loop.

**Why now.** Vision Automate must become real policies. Must exist **before** AI Automate (Phase 42).

**Dependencies.** Phase 16 events/metrics. Compute/backup APIs.

**Architecture.** Rules: if pool greater than 85 percent then enqueue migrate of low-priority (calls Phase 31/32 APIs). Approval optional.

**Control plane.** Policy store, evaluator.

**Agent.** No policy engine. Only Execute.

**Frontend.** Automation page.

**Database.** `policies`, `policy_runs`.

**Security.** Policies run as service identity. Same RBAC.

**CLI/API.** `nodalctl policy apply`.

**Tests.** Evaluator fixtures.

**Recovery tests.** Policy cannot `Host.Exec`.

**Acceptance.** A storage-pressure policy creates a real operation the user can see.

**User capability.** The platform can act without a model.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 41. AI Ask

**Objective.** Provider-neutral read-only assistant with infrastructure context.

**Why now.** APIs and timeline exist. Last major product family.

**Dependencies.** Phases 16, 40 (for later Operate). Ask can start after 16.

**Architecture.** BYO: OpenAI, Anthropic, Gemini, Ollama, local, OpenAI-compatible, private endpoints. No vendor required. Context retrieval with `metrics.read` / `events.read`. No mutate.

**Control plane.** Gateway, provider secrets, permission profile `ask`.

**Agent.** None.

**Frontend.** Ask panel.

**Database.** `ai_providers`, `ai_profiles`.

**Security.** Prompts/logs redacted. No secrets in context.

**CLI/API.** `nodalctl ai ask`.

**Tests.** Profile without read cannot query.

**Recovery tests.** Provider down: platform still works.

**Acceptance.** "Why did this workload restart?" cites events/metrics. Offline install has no AI and still works.

**User capability.** You can diagnose with BYO-AI.

**Complexity.** Large. Agent: 1 to 2 weeks. Human: 1 day.

---

## Phase 42. AI Plan, Operate, Automate (Feature-Complete Beta)

**Objective.** Structured actions with preview, approval, audit. App-specific actions from Store manifests.

**Why now.** Action catalog is the existing API. Automation engine exists.

**Dependencies.** Phases 36 (`ai_actions`), 40, 41.

**Architecture.** Plan = proposed operation list. Operate = execute with policy. Automate = bind model-proposed rules to Phase 40 policies after approval. Never a shell tool.

**Control plane.** Plan store, approval, `actor_type=ai`.

**Agent.** Unchanged Execute.

**Frontend.** Plan preview, approve.

**Database.** `ai_plans`.

**Security.** Destructive still confirm. Profiles: local model read-only vs operator model. Credential isolation.

**CLI/API.** `nodalctl ai plan`, `ai approve`.

**Tests.** Plan cannot call missing permissions. No exec RPC.

**Recovery tests.** Partial plan failure stops, audit remains.

**Acceptance.** A request such as install a database on a named node becomes a reviewable plan of existing APIs. **Feature-Complete Beta reached.**

**User capability.** AI is an operator layer, not a root shell.

**Complexity.** Very Large. Agent: 2 to 3 weeks. Human: 2 days.

---

## Phase 43. CE 1.0 release

**Objective.** Harden, document, soak, ship CE 1.0.

**Why now.** Beta exists.

**Dependencies.** Phase 42.

**Architecture.** No new primitives. License-activation **surface** (Settings, License) that talks to Cloud or a licensing API **only when the user enters a key**. Grace if unreachable. Workloads never stop. Docs: one-line install, manual repo install, ISO, uninstall, recovery, backup, cluster, Store, AI.

**Control plane.** License settings stub that does nothing unless a key is present.

**Agent.** None.

**Frontend.** Docs links, license page honest about CE.

**Database.** Optional `license_state` empty.

**Security.** No private repo credentials. No EE blobs.

**CLI/API.** Freeze API compatibility notes.

**Tests.** Full CI plus virt suite plus physical checklist.

**Recovery tests.** Repeat Homelab and cluster DR.

**Acceptance.** CE 1.0 definition in this document is true. Changelog. Signed packages. **No-dal CE 1.0 reached.**

**User capability.** You can run No-dal as your infrastructure platform.

**Complexity.** Large. Agent: 1 to 2 weeks polish. Human: 1 to 2 weeks soak (not coding).

---

## Feature coverage matrix

Format: Capability. Vision. Phase. First milestone. Notes.

- Control plane / node agent. Sections 3-4. 0-1. MVP. Architecture plan HOW.
- Single-node first-class. Section 5. 1-8. Dogfood.
- Cluster join / inventory. Sections 5, 31. 30. Beta.
- Placement / groups / maintenance. Section 6. 31. Beta.
- Affinity / priority. Section 6. 31. Beta.
- VMs lifecycle. Section 7. 7-8. Dogfood.
- VM disks/NICs/cloud-init/console. Section 7. 8. Dogfood.
- VM snapshots/clone/templates/import/export. Section 7. 10, 18. Homelab / Beta.
- VM PCI/USB/GPU. Section 7. 14, 18. After Homelab.
- VM migrate. Section 7. 32. Beta.
- System containers. Section 7. 5-6, 10. Dogfood.
- CT devices/GPU. Section 7. 14, 18. After Homelab.
- OCI plus stacks plus Compose. Section 7. 21-22. Beta.
- Modular features. Section 8. 35. Beta.
- Kubernetes optional. Section 8. 38. Beta.
- Directory / ZFS / LVM / NFS / SMB / iSCSI. Section 9. 3, 15, 25, 26. Dogfood to Beta.
- Storage classes / health / capacity forecast. Section 9. 3, 16. Dogfood / after Homelab.
- Replication. Section 9. 15, 33. Beta.
- Distributed storage optional. Sections 3, 9. 39. Beta.
- Bridges / isolated / NAT / DHCP / DNS / LAN. Section 10. 4. Dogfood.
- VLAN / bonds / firewall / policies / overlay / metrics. Section 10. 27. Beta.
- WireGuard / remote nodes. Sections 10, 4. 28. Beta.
- SDN controller. Section 10. Post-1.0 CE. Explicit.
- Snapshots vs backup. Section 11. 10-11. Homelab.
- Local / NFS / SMB backup dest. Section 11. 11. Homelab.
- S3 / R2 / B2 / MinIO / encrypt / incremental. Section 11. 23. Beta.
- Verify / restore test / file restore / cross-node / DR. Section 11. 24, 33. Beta.
- Dedup where practical. Section 11. 23. Beta. Honest skip if not practical, documented in 23 acceptance.
- Store plus manifest plus trust plus lifecycle. Sections 12-14. 36-37. Beta.
- Paid marketplace fees. Section 15. Cloud. Excluded.
- AI Ask/Plan/Operate/Automate plus profiles plus app actions. Sections 16-21. 41-42. Beta.
- Users/groups/roles/tokens/MFA/secrets/audit/certs/rate limits. Section 22. 1, 9, 13. MVP to after Homelab.
- Encryption in transit. Section 22. 9. Homelab.
- Encryption at rest. Section 22. 13, 15. After Homelab.
- SSO. Section 22. EE. Excluded.
- Observability plus timeline plus alerts plus logs. Section 23. 2, 16. MVP / after Homelab.
- UX guided/advanced/expert, search, palette, responsive. Section 24. 17. After Homelab.
- Terminal/Files dual console, host/CT/VM. Section 25. 6, 8, 19-20. Dogfood / Beta.
- API plus CLI. Section 26. 1 ongoing. MVP.
- Install packages / ISO / host platform compatibility. Section 27. 1, 12, 29. MVP / Dogfood / Beta. One-line bootstrap plus manual repo path in Phase 1. ISO in Phase 29. Same `nodal` metapackage. Debian 13 first. Ubuntu LTS via host adapter in Phase 29. Further hosts additive after qualification.
- Uninstall without workload wipe. Section 27 plus recovery. 1, 12. MVP. `apt remove` stops services. Data wipe is a separate confirmed action.
- Host OS vs VM guest vs CT image vs OCI image. Sections 3, 7, 27. 1, 5, 8, 21, 29. MVP / Dogfood / Beta. Four surfaces. Narrow host matrix. Broad guests and images.
- Updates / channels / rollback / rolling. Section 28. 12, 34. Homelab / Beta.
- CE useful, no node cap. Section 29. All.
- Cloud hosted services. Section 30. Cloud. Excluded.
- Guest Agent. Section 25, section 35 ndl-ce purpose. 19-20. Beta.
- EE upgrade surface. Section 36. 43. 1.0. Surface only.
- Hardware inventory (CPU/RAM/DIMM/disk/SMART/NIC/GPU/PCI/USB/IOMMU/temp/firmware). Section 23 plus user brief. 2. MVP.
- Em dash CI. Project rule. 0. Pre-MVP.

---

## Risks and resolution phases

- **QEMU supervisor complexity.** Resolve: Phase 7. Proof: acceptance list. Fallback: decision record, possible hidden launcher without libvirt identity. Blocks: Phase 8+.
- **liblxc lifecycle.** Resolve: Phase 5. Proof: unit outlives agent. Fallback: stay the course unless liblxc is unusable. Blocks: 6, 10 CT snaps.
- **Network lockout.** Resolve: Phase 4. Re-test: 9, 27, 28. Fallback: isolated-only default. Blocks: Dogfood.
- **ZFS DKMS vs kernel.** Resolve: Phases 12, 15. Proof: preflight refuse. Fallback: Directory only.
- **GPU IOMMU / vendor skew.** Resolve: Phase 14. Proof: group listing, no ACS default. Fallback: inventory-only.
- **Guest Agent OS matrix.** Resolve: Phase 19. Proof: Linux full, Windows subset. Fallback: Console plus qemu-ga. Blocks: 20.
- **Backup catalog identity.** Resolve: Phase 11. Proof: restore new UUID. Blocks: 23, 33.
- **Object backup crypto.** Resolve: Phase 23. Proof: restore after encrypt. Fallback: local-only until fixed.
- **Clustering split-brain.** Resolve: Phase 30, 34. Proof: single writer lease. Blocks: 31-34.
- **OCI runtime choice.** Resolve: Phase 21 start. Proof: pick containerd (or documented equivalent) in-phase. Blocks: 22, 36.
- **Store script injection.** Resolve: Phase 36-37. Proof: no exec in manifests. Fallback: disable Store.
- **AI shell bypass.** Resolve: Phase 41-42. Proof: no shell tool. Foundation forbids Exec from Phase 0.
- **HTTPS mis-issue.** Resolve: Phase 9. Proof: WSS plus cookies. Blocks: Homelab.
- **Host-platform leakage.** Resolve: Phases 0 to 1 seam, prove in Phase 29. Proof: Debian-specific apt/networkd/GRUB live only in `internal/hostos/debian`. Fallback: keep Debian-only Tier 1 until an adapter exists. Blocks: claiming Ubuntu or other hosts.
- **Unsigned curl-pipe installer.** Resolve: Phase 1. Proof: small HTTPS script, signed repo, package signatures, no extra unsigned payloads, fail-closed host check. Fallback: manual repo path is fully supported. Blocks: treating a giant remote shell as the product.

---

## Timeline estimates

These are **not** calendar guarantees. They assume an AI-assisted, iterative workflow and a human who can test on real hardware.

Effort bands per phase: Small (hours to 1 day agent), Medium (3 to 7 agent-days), Large (1 to 3 agent-weeks), Very Large (3 to 6 agent-weeks). Human testing is extra. Soak is extra and mostly waiting.

**Implementation time** (sum of phase agent work, overlapping where noted):
- Aggressive: about 20 to 28 agent-weeks if gates pass first try
- Realistic: about 35 to 50 agent-weeks with rework
- Conservative: about 55 to 80 agent-weeks including QEMU or cluster redesign

**Active human testing:**
- Aggressive: 3 to 5 weeks of focused lab days
- Realistic: 6 to 10 weeks
- Conservative: 12+ weeks

**Soak / dogfood (wall clock, not coding):**
- After Dogfood Host: 2 weeks minimum
- After Homelab Migration Candidate: 2 to 4 weeks before moving important guests
- After Feature-Complete Beta: 4 weeks before CE 1.0 tag

**Overall wall calendar (nights/weekends plus agents, one human):**
- Aggressive: about 5 to 7 months to CE 1.0 if you do not pause
- Realistic: about 9 to 13 months including soak
- Conservative: about 15 to 20 months if Phase 7 fails once and clustering is painful

Do not treat CE 1.0 as done when MVP looks pretty.

---

## Testing and CI (preserved)

GitHub is authoritative. Fast static CI every commit: lint, types, unit, privilege matrix, fake-agent interrupted jobs, Postgres migrations, codegen diff, em dash `file:line`, bootstrap script size/sanity. No nested virt on `ubuntu-latest`. Virt suite: `workflow_dispatch` plus nightly labeled runner. Physical checklists at Dogfood, Homelab, and 1.0.

**Installer tests (Phase 1 onward, not optional later):**
- Clean Debian 13 amd64 one-line (or identical script vs a test signed repo)
- Supported amd64 detection
- Unsupported distribution rejection (installs nothing)
- Unsupported architecture rejection
- Repository setup and signing verification
- Dependency installation (core only)
- Database initialization
- systemd unit start
- Bootstrap health check and management URL discovery
- First-admin `/setup`
- Installer rerun / idempotency
- Interrupted installation retry
- Package installation failure
- Existing PostgreSQL or No-dal config conflict (warn or fail closed, no silent overwrite)
- Upgrade from an older No-dal package (Phase 12)
- Uninstall without deleting workload data dirs / disks / backups

Development helper scripts may exist. They must not be the only tested install path.

---

## Product rules (preserved)

CE is useful and not crippled. No-dal owns orchestration. Linux primitives underneath. Agent is not AI. UI is an API client. Workloads survive management failure. Single-node first. Cluster evolves. Security first. Native backups. S3-compatible first-class. GPU in CE. Store is declarative. AI uses structured actions. Terminal/Files first-class. Guided does not remove Advanced. CE does not need EE or Cloud. Installation is a product surface: one command on a supported host, then browser setup. Manual and ISO paths remain and converge on the same packages.

---

## Reviewer findings applied

Coverage review: named CLI/API/RBAC/audit/cloud-init/DHCP/DNS/storage classes/backup engine/automation/license surface/dual console/hooks/verification pipeline. Removed blob words without nouns. SDN, arm64 **host**, and extra host distros parked as **post-1.0 CE**, not vanished.

Order review: QEMU gate before VMs. HTTPS after Dogfood, before Homelab. Homelab bundle is 9+10+11+12 (HTTPS, snapshots, backup restore, updates). GPU/ZFS do not gate Homelab. Local restore proven in Phase 11. Clustering after S3 is acceptable because the catalog is cluster-shaped.

Host-platform review: Debian 13 stays the early-phase host. Phase 29 renamed to Host platform compatibility and installer. Phase order unchanged. Guest, CT, and OCI images stay independent of the host matrix.

Install review: one-line bootstrap and manual repo path added to Phase 1. ISO stays Phase 29. Same `nodal` metapackage. Bootstrap stays small. Unsupported hosts fail closed. Uninstall does not wipe workloads.

---

## Final validation

1. Vision re-read. 2. Architecture plan re-read. 3. Roadmap compared. 4. CE capabilities numbered. 5. No undefined later bucket. 6. Safety mechanisms kept (typed agent, rollback, Observe-first, guest survival). 7. CE independent of Cloud/EE. 8. MVP is Phase 2. 9. Later phases stack. 10. CE 1.0 is the broad product. 11. HTTPS is Phase 9 before Homelab. 12. S3/R2 is Phase 23. 13. GPU assignment is Phase 14. 14. OCI is 21-22. 15. Store is 36-37. 16. Guest Agent is 19-20. 17. Cluster/migrate/HA are 30-34. 18. AI is 41-42. 19. UX modes are Phase 17. 20. Risks have phases. 21. This plan must contain zero em dash characters.
