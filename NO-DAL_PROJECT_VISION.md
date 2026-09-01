# No-dal

**Website:** no-dal.com\
**Project type:** Open-source infrastructure, virtualization,
orchestration, application and AI operations platform\
**Status:** Concept / architecture definition

> platform built on proven low-level technologies, with its own control
> plane, node architecture, user experience, application ecosystem,
> backup system, clustering model and AI operator layer.

------------------------------------------------------------------------

## 1. Vision

No-dal exists to make running infrastructure dramatically simpler
without sacrificing the power expected from serious virtualization and
server-management platforms.

Today, users commonly have to jump between hypervisor dashboards,
helper-script repositories, Docker tools, Kubernetes dashboards, backup
products, object-storage tooling, shell sessions, documentation and
external AI assistants just to operate a homelab or small infrastructure
environment.

No-dal should bring those workflows together.

The goal is a platform that can begin as a simple single-node home
server and grow naturally into a multi-node cluster without forcing the
user to rebuild their environment or learn an entirely different
management model.

Core philosophy:

-   Modern, polished UI from day one.
-   Open source at the core.
-   Useful without a paid subscription.
-   Single-node and clustered environments use the same mental model.
-   Modular features instead of forcing every component onto every
    installation.
-   First-class VMs, system containers and OCI/application containers.
-   Native storage, networking, backup, monitoring and automation.
-   Native S3-compatible backup support, including Cloudflare R2.
-   A verified application marketplace rather than dependence on random
    helper scripts.
-   Bring-your-own-AI support with safe, permissioned infrastructure
    operations.
-   Proven Linux virtualization primitives underneath rather than
    reinventing KVM or container runtimes.
-   The platform owns the orchestration and experience.

------------------------------------------------------------------------

## 2. What No-dal Is Not

No-dal should **not** become:

-   A collection of patches modifying another platform's frontend.
-   A Kubernetes distribution pretending every home server needs
    Kubernetes.
-   A new hypervisor implementation written from scratch.
-   A glorified Docker Compose frontend.
-   A platform that deliberately cripples its community edition to force
    subscriptions.
-   A system where AI is simply given unrestricted root shell access.
-   A marketplace that blindly executes arbitrary third-party shell
    scripts.

No-dal can use established open technologies such as Linux, KVM/QEMU, as
its underlying product.

------------------------------------------------------------------------

## 3. Platform Layers

A conceptual stack:

``` text
Physical Hardware
        │
        ▼
Linux Host
        │
        ├── KVM/QEMU            Virtual machines
        ├── System Containers   LXC/Incus-style workloads
        ├── OCI Runtime         Application containers
        ├── ZFS/LVM/etc.        Local storage
        ├── Ceph/etc.           Optional distributed storage
        ├── Linux networking    Bridges/VLANs/etc.
        └── WireGuard           Secure node connectivity
        │
        ▼
No-dal Node Agent
        │
        ▼
No-dal Control Plane
        │
        ├── Compute
        ├── Storage
        ├── Networking
        ├── Clustering
        ├── Backups
        ├── Applications
        ├── Monitoring
        ├── Identity/RBAC
        ├── Automation
        ├── Marketplace
        └── AI Operator
        │
        ▼
Web UI / API / CLI
```

The exact underlying technologies should remain implementation decisions
rather than becoming the identity of No-dal.

------------------------------------------------------------------------

## 4. Control Plane and Node Architecture

The control plane and managed hosts should be separate concepts.

Each physical No-dal server runs a lightweight **node agent**. The
control plane maintains the desired state and coordinates the
environment. Node agents perform privileged host-level operations.

``` text
                 No-dal Control Plane
                         │
              ┌──────────┼──────────┐
              │          │          │
              ▼          ▼          ▼
           Agent A    Agent B    Agent C
              │          │          │
           Node A      Node B      Node C
              │          │          │
         KVM/Containers/Storage/Networking
```

This architecture should make possible:

-   Single-node deployments.
-   Multi-node clusters.
-   Remote nodes.
-   Centralized management.
-   Scheduling and placement.
-   Health reporting.
-   Maintenance mode.
-   Workload migration.
-   Rolling platform updates.
-   High availability.
-   Event collection.
-   Central policy enforcement.
-   Future large-scale fleet management.

The web UI should be a client of the same supported API used by
automation and CLI tooling. Core functionality must not exist only
inside frontend code.

------------------------------------------------------------------------

## 5. Single Node to Cluster

No-dal should make clustering an evolution, not a separate product.

A user should be able to install No-dal on one old PC, use it for
months, then add another server through a simple workflow such as:

``` text
Settings → Cluster → Add Node

Join Code:
X7F2-K9PQ

[ Join ]
```

The existing node should become part of the cluster without rebuilding
its workloads.

Example:

``` text
Cluster: Home

NODE             CPU        RAM        STORAGE
server-01        24T        64 GB       8 TB
server-02        16T        32 GB       4 TB
server-03        32T       128 GB      16 TB

TOTAL            72T       224 GB      28 TB
```

Users can continue to think about individual nodes when necessary while
also being able to treat the cluster as a pool of available
infrastructure.

------------------------------------------------------------------------

## 6. Workload Placement and Scheduling

When creating workloads, No-dal should support:

``` text
Placement

● Automatic
○ Specific node
○ Node group
```

Automatic placement can eventually consider:

-   Available CPU.
-   Memory.
-   Storage capacity.
-   Storage class.
-   GPU/device requirements.
-   Network availability.
-   Affinity and anti-affinity rules.
-   Node health.
-   Maintenance status.
-   Workload priority.
-   Redundancy requirements.
-   Licensing or policy constraints.

Maintenance mode should eventually support workflows such as:

``` text
server-01 entering maintenance mode

Migrating:
Jellyfin     → server-03
PostgreSQL   → server-02
Immich       → server-03

Node is now safe to shut down.
```

------------------------------------------------------------------------

## 7. Compute

No-dal should provide first-class support for multiple workload types.

### Virtual Machines

Using proven Linux virtualization technology such as KVM/QEMU.

Expected capabilities include:

-   Create/edit/delete.
-   Start/stop/reboot.
-   Templates.
-   Clone.
-   Snapshots.
-   Console access.
-   CPU and memory configuration.
-   Dynamic resource changes where supported.
-   Disk management.
-   Network interfaces.
-   PCI/GPU passthrough.
-   USB/device passthrough.
-   Migration.
-   Backup/restore.
-   Cloud-init.
-   Import/export.

### System Containers

Lightweight system-level containers should be a core feature, not an
afterthought.

Expected capabilities:

-   Create from templates/images.
-   CPU/RAM limits.
-   Storage allocation.
-   Network configuration.
-   Device passthrough.
-   Snapshots.
-   Backup/restore.
-   Migration.
-   Console/shell access.

### OCI/Application Containers

No-dal should also support application/container workloads without
forcing the user to deploy a separate container-management product.

Potential functionality:

-   Images and registries.
-   Containers.
-   Compose-style stacks.
-   Volumes.
-   Networks.
-   Secrets.
-   Environment variables.
-   Health checks.
-   Logs.
-   Updates.
-   Resource limits.

------------------------------------------------------------------------

## 8. Modular Platform Features

No-dal should avoid making every deployment run every enterprise
feature.

Example:

``` text
Features

Virtual Machines       Installed
System Containers      Installed
OCI Containers         Installed
Kubernetes             [ Install ]
Distributed Storage    [ Install ]
GPU Services           [ Install ]
AI Services            [ Install ]
```

A simple home server should remain lightweight.

A larger installation can progressively enable more advanced
capabilities.

### Kubernetes

Kubernetes should be supported as an optional feature/integration rather
than being the foundation that all No-dal installations are forced to
use.

The user should not need Kubernetes knowledge merely to run a VM,
Jellyfin server or small container workload.

------------------------------------------------------------------------

## 9. Storage

Storage should be understandable without hiding advanced functionality.

Potential support includes:

-   Local disks.
-   ZFS.
-   LVM/LVM-thin.
-   Directory storage.
-   NFS.
-   SMB.
-   iSCSI.
-   S3-compatible object storage where appropriate.
-   Optional distributed storage such as Ceph.
-   Storage pools/classes.
-   Replication.
-   Health monitoring.
-   SMART/device information.
-   Capacity forecasting.
-   Dataset/volume management.

Storage configuration should integrate directly with compute,
applications and backup policies.

------------------------------------------------------------------------

## 10. Networking

Networking should support both simple and advanced users.

Potential capabilities:

-   Linux bridges.
-   Bonds.
-   VLANs.
-   Virtual networks.
-   DHCP/static configuration.
-   DNS configuration.
-   Firewall rules.
-   Network policies.
-   NAT.
-   Overlay networking between nodes.
-   WireGuard-based secure node connectivity.
-   SDN functionality as the platform matures.
-   Per-workload traffic visibility.
-   Network health and throughput monitoring.

Common operations should not require users to understand every low-level
Linux networking command.

------------------------------------------------------------------------

## 11. Native Backup and Disaster Recovery

Backup should be part of No-dal itself rather than requiring an entirely
separate backup product for normal use.

First-class backup destinations should include:

-   Local storage.
-   NFS.
-   SMB.
-   Generic S3-compatible storage.
-   Cloudflare R2.
-   AWS S3.
-   Backblaze B2.
-   MinIO.

The architecture should aim to support:

-   Encryption before upload.
-   Compression.
-   Deduplication where practical.
-   Incremental backups.
-   Snapshot-based backups.
-   Retention policies.
-   Scheduled backups.
-   Replication.
-   Backup verification.
-   Restore testing.
-   Workload-level restore.
-   File-level restore where possible.
-   Cross-node restore.
-   Disaster recovery.

Example:

``` text
Jellyfin

Protection
✓ Snapshot every 6 hours
✓ Daily backup → R2
✓ Keep 14 daily
✓ Keep 8 weekly
✓ Encrypt before upload

Last backup: 03:04
Size: 18.7 GB
Transferred: 640 MB
```

Restoring a workload to another compatible No-dal node should eventually
be a normal supported workflow.

------------------------------------------------------------------------

## 12. No-dal Store

The Store should be a first-class part of the platform.

The objective is to remove the need for users to search through
helper-script repositories, random GitHub projects and outdated
tutorials for common applications.

Example:

``` text
STORE

Media
┌──────────┐ ┌──────────┐ ┌──────────┐
│ Jellyfin │ │ Immich   │ │ Plex     │
│ Verified │ │ Verified │ │ Verified │
│ Install  │ │ Install  │ │ Install  │
└──────────┘ └──────────┘ └──────────┘

Infrastructure
┌──────────┐ ┌──────────┐ ┌──────────┐
│Postgres  │ │ Redis    │ │ MinIO    │
│ Verified │ │ Verified │ │ Verified │
└──────────┘ └──────────┘ └──────────┘
```

Installing an application should feel like:

``` text
Deploy Jellyfin

Node
[ Home-01 ▼ ]

CPU
[ 4 ]

Memory
[ 8 GB ]

Storage
[ Media Pool ▼ ]

GPU
[ NVIDIA GPU ▼ ]

Network
[ Media VLAN ▼ ]

[ Deploy ]
```

No-dal handles the underlying deployment.

------------------------------------------------------------------------

## 13. Application Manifest

Applications should use a declarative No-dal package/manifest format
rather than being arbitrary shell scripts.

Conceptually:

``` yaml
name: jellyfin
version: 10.x

resources:
  cpu: 4
  memory: 8GB

storage:
  - name: config
    persistent: true

devices:
  gpu:
    optional: true

ports:
  - 8096

deployment:
  supported:
    - container
    - vm
```

The eventual specification should cover:

-   Application metadata.
-   Versioning.
-   Resource requirements.
-   Supported architectures.
-   Storage.
-   Networking.
-   Ports.
-   Environment variables.
-   Secrets.
-   Devices.
-   GPU support.
-   Dependencies.
-   Health checks.
-   Upgrade procedures.
-   Backup hooks.
-   Restore hooks.
-   Migration compatibility.
-   AI actions.
-   Permissions.
-   Security requirements.

The package format should be designed carefully enough that third
parties can build against it without coupling themselves to No-dal's
internal implementation.

------------------------------------------------------------------------

## 14. Marketplace Trust Model

Possible application classifications:

### Community

Community-created application package.

### Verified

No-dal validation has confirmed defined security, packaging and
compatibility requirements.

### Official

Maintained by No-dal or the upstream application publisher.

Verification should eventually include automated checks such as:

-   Manifest validation.
-   Image provenance.
-   Vulnerability scanning.
-   Permission analysis.
-   Network exposure analysis.
-   Secret-handling validation.
-   Prohibited behavior checks.
-   Update testing.
-   Signature verification.

No-dal should avoid creating a model where installing an app effectively
means running an unknown Bash script as root.

------------------------------------------------------------------------

## 15. Free and Paid Marketplace

The marketplace can support both free and commercial software.

The basic Store remains available to community users.

Potential commercial model:

-   Free applications.
-   Paid applications.
-   Paid enterprise integrations.
-   No-dal takes a transparent marketplace transaction fee.
-   Developers can publish commercial software through the platform.

The marketplace should never become necessary just to perform ordinary
infrastructure operations.

------------------------------------------------------------------------

# 16. Native AI Operator

AI should be designed into No-dal as an infrastructure operator layer
rather than added later as a generic chat window.

Users should be able to bring their own AI provider.

Potential providers:

-   OpenAI.
-   Anthropic.
-   Google Gemini.
-   Ollama.
-   Local models.
-   OpenAI-compatible APIs.
-   Enterprise/private endpoints.

No-dal itself should not require one specific AI vendor.

------------------------------------------------------------------------

## 17. AI With Infrastructure Context

A major advantage of native AI is that No-dal already understands the
infrastructure.

Instead of a user copying logs and commands into an external assistant,
the AI can be granted controlled access to:

-   Nodes.
-   Workloads.
-   Configuration.
-   Metrics.
-   Logs.
-   Events.
-   Change history.
-   Storage state.
-   Network state.
-   Backup state.
-   Application metadata.
-   Health checks.
-   Hardware information.

A user could ask:

> Why did Plex go down at 3:17 AM?

No-dal AI could correlate the environment and answer:

``` text
Node-02 experienced an NVMe timeout at 03:16:48.

The Plex container entered an I/O wait state and failed its
health check. It recovered after the storage device reset
41 seconds later.

Recommended actions:

1. Run NVMe diagnostics.
2. Move Plex metadata to Pool-SSD-02.
3. Enable automatic workload failover.

[ Review Plan ]
```

------------------------------------------------------------------------

## 18. AI Actions

AI should operate through a permissioned internal action API rather than
arbitrary root shell access.

For example:

``` text
workload.create
workload.resize
workload.restart
workload.migrate

network.attach
network.modify

backup.create
backup.restore

storage.mount

app.install
app.update
```

A request such as:

> Install PostgreSQL on node-02, give it 8 GB RAM, put its data on the
> fast pool, expose it only to VLAN 20 and back it up nightly to R2.

could become:

``` text
Proposed Plan

1. Create PostgreSQL workload on node-02.
2. Allocate 8 GB RAM.
3. Attach fast-storage/postgres-data.
4. Attach VLAN 20 network policy.
5. Deploy verified PostgreSQL package.
6. Create nightly backup policy targeting R2.

[ Cancel ] [ Approve & Execute ]
```

The platform validates the requested actions before execution.

------------------------------------------------------------------------

## 19. AI Modes

### Ask

Read-only investigation and assistance.

Example:

> Why is Jellyfin buffering?

The AI can inspect permitted metrics, GPU usage, network state, storage
latency and application logs.

### Plan

The AI creates a proposed infrastructure change but performs nothing.

Example:

> Move Jellyfin to node-03 and give it the NVIDIA GPU.

### Operate

The AI executes actions within its granted permissions, with approval
requirements determined by policy.

Example:

> Restart every unhealthy container.

### Automate

Users define longer-lived intent or policies.

Example:

> If this storage pool exceeds 85%, move eligible low-priority workloads
> to node-02.

Automation should ultimately be represented by deterministic No-dal
policies/workflows rather than requiring an LLM to continuously make
unrestricted decisions.

------------------------------------------------------------------------

## 20. AI Permission Profiles

Different AI providers/models can have different privileges.

Example:

``` text
Local Qwen

✓ Read infrastructure
✓ View logs
✓ Diagnose problems
✗ Modify workloads
✗ Delete anything


Operator Model

✓ Read
✓ Create
✓ Modify
✓ Restart
✗ Delete storage
✗ Modify security policies
```

Sensitive operations should support:

-   Explicit approval.
-   Permission boundaries.
-   Risk classification.
-   Dry runs.
-   Plan previews.
-   Audit trails.
-   Rollback where technically possible.
-   Destructive-operation protection.
-   Credential isolation.

AI actions must be attributable and auditable.

------------------------------------------------------------------------

## 21. Application-Specific AI Actions

Store packages may optionally expose safe application-specific
operations.

For example, a PostgreSQL package could define:

``` yaml
ai_actions:
  - backup_database
  - inspect_slow_queries
  - create_user
  - rotate_password
  - check_replication
```

These actions should have defined inputs, outputs, permissions and
safety classifications.

Verified packages can have their AI actions reviewed as part of
verification.

This gives the AI semantic understanding of an application without
giving it unrestricted shell access.

------------------------------------------------------------------------

## 22. Identity, RBAC and Security

Security must be part of the architecture rather than a post-release
addition.

Expected areas:

-   Local users.
-   Groups.
-   Roles.
-   Fine-grained permissions.
-   API tokens.
-   Service identities.
-   Node identities.
-   Short-lived credentials where possible.
-   MFA.
-   Session management.
-   Audit logs.
-   Secret management.
-   Encryption at rest.
-   Encryption in transit.
-   Certificate management.
-   Signed packages.
-   Secure bootstrap.
-   Node enrollment.
-   Revocation.
-   Rate limiting.
-   Brute-force protection.
-   Security event history.

Enterprise identity features can include SSO/SAML/OIDC integrations
while avoiding unnecessary restrictions on normal community
functionality.

------------------------------------------------------------------------

## 23. Observability

No-dal should have native visibility into the infrastructure.

Potential capabilities:

-   CPU.
-   Memory.
-   GPU.
-   Disk.
-   Storage latency.
-   Network throughput.
-   Temperatures.
-   Workload health.
-   Node health.
-   Backup status.
-   Application health.
-   Cluster events.
-   Historical metrics.
-   Logs.
-   Alerts.
-   Change timeline.

The event/change timeline is especially important for AI-assisted
diagnostics.

A user should be able to answer:

> What changed before this broke?

without manually correlating five different systems.

------------------------------------------------------------------------

## 24. User Experience

The UI is one of No-dal's primary reasons for existing.

It should feel like a modern product rather than an old enterprise
administration interface.

Possible dashboard:

``` text
NO-DAL                                      Healthy

Compute                    Storage
21 workloads               5.8 TB / 12 TB
████████░░ 73% CPU          ██████░░░░ 61%

Nodes
┌─────────────────────────────────────────┐
│ server-01                     ● Healthy │
│ Ryzen 9                                 │
│ CPU 21%        RAM 48 GB / 64 GB        │
└─────────────────────────────────────────┘

Workloads

● Jellyfin       System Container    Running
● PostgreSQL     System Container    Running
● Windows        VM                  Running
● Immich         OCI                 Running
○ Test-Ubuntu    VM                  Stopped
```

Design principles:

-   Clear terminology.
-   Progressive disclosure of advanced options.
-   Fast common workflows.
-   Consistent actions.
-   Excellent mobile/tablet responsiveness where practical.
-   Search everywhere.
-   Useful command palette.
-   Live status without requiring constant refreshes.
-   Strong empty states/onboarding.
-   No requirement to SSH for routine operations.
-   Advanced users can still reach detailed configuration.

------------------------------------------------------------------------

## 25. Browser-Native Console and File Management

Console and filesystem access are core No-dal product experiences, not
secondary troubleshooting tools.

A user should be able to perform routine terminal and file-management
work entirely from the browser without needing a separate SSH client,
SCP client, SFTP application or external file-transfer utility.

## Dual Console Model

No-dal should provide two console paths where appropriate.

### Compatibility Console

A conventional low-level console path for maximum compatibility,
including graphical or serial access where required.

This remains available for boot troubleshooting, installers, guests
without integration support and other cases where direct console access
is necessary.

### No-dal Terminal

A first-class browser terminal designed to behave like a modern terminal
application.

Expected functionality:

-   Reliable copy and paste.
-   Multi-line paste.
-   Native browser clipboard integration.
-   Large scrollback history.
-   Search and text selection.
-   Configurable font sizing.
-   Full-screen mode.
-   Correct dynamic resizing.
-   Multiple terminal sessions/tabs.
-   Reconnect after temporary browser/network interruption.
-   Clear connection/session state.
-   Keyboard shortcuts.
-   Context actions on recognised filesystem paths.
-   Optional confirmation before large or potentially dangerous
    multi-line pastes.
-   Drag-and-drop file upload into the current working directory.
-   Direct browser downloads.
-   Tight integration with the No-dal Files interface.

The terminal should use a proper PTY-backed transport where possible
rather than attempting to emulate terminal behaviour through a graphical
framebuffer console.

For system containers, the node agent can provide native terminal
sessions. For virtual machines, enhanced terminal integration can be
provided through a No-dal Guest Agent or another explicitly supported
guest transport. The compatibility console remains available when
enhanced integration is unavailable.

## Browser-Native File Transfer

File transfer should use normal browser upload and download experiences.

Example:

``` text
root@jellyfin:/opt/jellyfin/config#

[ Upload Here ] [ Open Files ]
```

Dragging `config.json` from the user's desktop onto the terminal can
produce:

``` text
Upload file?

config.json
12.4 KB

Destination:
/opt/jellyfin/config/config.json

Owner: root
Permissions: 0644

[ Cancel ] [ Upload ]
```

The browser uploads the file through No-dal's authenticated
file-transfer API. The user should not need to understand SCP, SFTP or
shell transfer commands.

Downloads should similarly be delivered directly through the browser.

Recognised paths in terminal output can expose contextual actions:

``` text
/var/log/jellyfin.log

Open in Files
Download
Copy Path
Properties
```

## Dedicated Files Interface

Each compatible node and workload should expose a dedicated **Files**
experience.

``` text
Jellyfin > Files

/opt/jellyfin/config

Name              Size       Owner       Modified
config.json       12 KB      root        05:41
network.xml        4 KB      root        05:32
cache/             -         jellyfin    04:58

[ Upload ] [ New Folder ] [ Terminal Here ]
```

Expected capabilities:

-   Browse directories.
-   Upload files, multiple files and folders.
-   Download files directly through the browser.
-   Download folders/collections through an appropriate archive or
    streaming mechanism.
-   Create files and directories.
-   Rename, move and copy.
-   Delete.
-   View metadata.
-   Change ownership where authorized.
-   Change permissions where authorized.
-   Search, sort and filter.
-   Transfer progress.
-   Conflict/overwrite handling.
-   Large-file handling.
-   Resumable transfers where practical.
-   Checksums/integrity verification where useful.
-   Open a terminal in the current directory.
-   Open the terminal's current directory in Files.

The UI should call this feature **Files** rather than exposing
implementation-specific transfer terminology.

## Terminal and Files Integration

Terminal and Files should behave as two views into the same workload.

-   **Upload Here** uploads directly into the terminal's current working
    directory.
-   **Open Files** opens Files at the terminal's current working
    directory.
-   **Terminal Here** opens a shell already positioned in the directory
    currently shown in Files.
-   Recognised terminal paths can be opened or downloaded through Files.
-   File-transfer progress can remain visible while the user continues
    using the terminal.

## File Access Architecture

For hosts and system containers, filesystem operations can be mediated
by the No-dal node agent using explicit filesystem APIs.

For virtual machines, the full experience can be provided through the
No-dal Guest Agent or another supported guest integration.

``` text
Browser
   │
   ├── Terminal session
   └── File upload/download
          │
          ▼
     No-dal API
          │
          ▼
   Permission / Audit Layer
          │
          ▼
      Node Agent
          │
          ├── Host filesystem
          ├── System-container filesystem
          └── Guest Agent → VM filesystem
```

No-dal should not require direct browser-to-host SSH/SFTP credentials
for its native experience.

## File and Terminal Permissions

Terminal and file access should use explicit permissions:

``` text
terminal.open

files.read
files.download
files.upload
files.create
files.modify
files.delete
files.permissions
files.ownership
```

This allows administrators to grant terminal access without necessarily
granting unrestricted file download or destructive filesystem
operations.

## Auditing

File activity should be auditable.

``` text
05:42  User uploaded config.json
       → jellyfin:/opt/jellyfin/config/config.json

05:44  User downloaded jellyfin.log
       ← jellyfin:/var/log/jellyfin/jellyfin.log
```

Relevant terminal session creation, privileged actions and file
modifications should integrate with No-dal's broader audit/event system.

## Product Principle

> **A user should not need to leave No-dal for routine terminal or
> filesystem administration simply because the built-in tools are
> missing basic desktop-quality functionality.**

Routine file management, upload/download and terminal work should be
possible entirely from the browser.

------------------------------------------------------------------------

# 26. API and CLI

Everything important should have a supported API.

The UI, CLI and automation should use the same underlying platform
capabilities.

Potential CLI:

``` bash
nodalctl node list
nodalctl workload list
nodalctl workload create
nodalctl app install jellyfin
nodalctl backup run jellyfin
nodalctl cluster status
```

Naming can be finalized later, but the CLI should be considered a
first-class interface.

------------------------------------------------------------------------

## 27. Installation

No-dal should eventually provide a straightforward bare-metal
installation experience.

Long term, the ideal user experience is:

1.  Install No-dal.
2.  Open the web interface.
3.  Complete first-run setup.
4.  Configure storage/networking.
5.  Create a VM/container or install an application.
6.  Add more nodes whenever needed.

Potential distribution methods can include:

-   Dedicated installer ISO.
-   Supported installation onto a base Linux distribution.
-   Automated provisioning for larger deployments.

The project should eventually own and test the complete supported host
environment rather than expecting users to assemble undocumented
combinations of packages.

------------------------------------------------------------------------

## 28. Updates

Platform updates should be safe and understandable.

Potential capabilities:

-   Release channels.
-   Update previews.
-   Changelogs.
-   Pre-update health checks.
-   Cluster-aware rolling updates.
-   Maintenance orchestration.
-   Automatic rollback where practical.
-   Backup/checkpoint before major upgrades.
-   Signed releases.
-   Compatibility validation for Store applications.

No-dal should avoid creating an environment where routine platform
updates regularly break third-party applications.

------------------------------------------------------------------------

# 29. Business and Open-Source Model

The open-source/community version should be genuinely useful.

### Community

The intention is to include core infrastructure functionality without
artificial node limits designed purely to force upgrades.

Potential community capabilities:

-   Nodes.
-   VMs.
-   Containers.
-   Clustering.
-   Storage.
-   Networking.
-   Backups.
-   Store.
-   Community applications.
-   API.
-   Bring-your-own-AI.

### Marketplace Revenue

No-dal can take a transparent fee from paid marketplace transactions.

### Enterprise Subscription

Enterprise subscriptions can focus on things organizations actually pay
for, such as:

-   Commercial support.
-   SLAs.
-   Enterprise identity integration.
-   Advanced audit/compliance.
-   Fleet management.
-   Advanced policy.
-   Priority support/security response.
-   Enterprise deployment tooling.
-   Optional hosted management services.

The community product should not become a deliberately frustrating demo.

------------------------------------------------------------------------

## 30. Potential Future Cloud Services

Optional hosted services could eventually include:

-   Hosted control plane.
-   Fleet visibility.
-   Remote access/relay.
-   Alert delivery.
-   Centralized enterprise policy.
-   Backup orchestration.
-   Marketplace licensing.
-   Managed update channels.

Self-hosting should remain a first-class deployment model.

------------------------------------------------------------------------

# 31. Initial Development Strategy

Do not attempt to build every item in this document simultaneously.

The architecture should account for the broader vision, while
development proceeds through tightly scoped stages.

## Foundation

First establish:

-   Supported host OS strategy.
-   Core service architecture.
-   Node agent.
-   Control-plane API.
-   Authentication.
-   Node enrollment.
-   Event model.
-   Job/task engine.
-   State reconciliation model.
-   Secure RPC between control plane and nodes.
-   Database model.
-   Extension/module boundaries.

## First Useful Compute Release

A first genuinely useful release should focus on:

-   Single-node installation.
-   Modern web UI.
-   Node overview.
-   VM lifecycle.
-   System-container lifecycle.
-   Storage basics.
-   Networking basics.
-   Console access.
-   Metrics.
-   Logs/events.
-   User authentication.
-   Basic RBAC.
-   Updates.

The goal should be that a user can operate a useful virtualization host
without falling back to another virtualization-management platform.

## Clustering

Then:

-   Node joining.
-   Cluster inventory.
-   Placement.
-   Migration.
-   Cluster-aware storage/networking.
-   Maintenance mode.
-   Health.
-   HA foundations.

## Native Backups

Then:

-   Backup engine.
-   S3-compatible targets.
-   Cloudflare R2.
-   Retention.
-   Encryption.
-   Restore.
-   Cross-node restore.

## Store

Then:

-   Application manifest specification.
-   Registry.
-   Community packages.
-   Verification pipeline.
-   Application lifecycle.
-   Upgrade/rollback.
-   Backup integration.

## AI

Once the platform exposes stable, safe actions:

-   Provider-neutral AI gateway.
-   Read-only Ask mode.
-   Infrastructure context retrieval.
-   Plan generation.
-   Permissioned action API.
-   Approval workflow.
-   Operate mode.
-   Application AI actions.
-   Automation/policy integration.

AI should consume stable platform capabilities rather than becoming a
shortcut around unfinished APIs.

------------------------------------------------------------------------

# 32. Architectural Principles to Protect

These principles should survive implementation pressure:

2.  **Do not reinvent KVM/QEMU or other proven low-level primitives
    without a compelling reason.**
3.  **The control plane and node agent are separate responsibilities.**
4.  **The web UI is a client, not the platform.**
5.  **All important operations belong behind supported APIs.**
6.  **Single-node operation is first-class.**
7.  **Clustering is an evolution of single-node operation.**
8.  **Optional enterprise features must not make small installations
    unnecessarily heavy.**
9.  **The Store uses declarative, inspectable packages rather than
    arbitrary root scripts.**
10. **Backups are native and object storage is first-class.**
11. **AI is provider-neutral.**
12. **AI operates through explicit permissions and structured actions.**
13. **AI-generated infrastructure changes are inspectable and
    auditable.**
14. **Security boundaries are designed before convenience features.**
15. **Community No-dal should remain genuinely useful.**
16. **The platform should simplify infrastructure without hiding
    important state from advanced users.**

------------------------------------------------------------------------

# 33. The No-dal Identity

The name derives naturally from **nodal**, meaning relating to nodes or
connection points, while the **No-dal** spelling gives the project a
distinct identity.

Primary domain:

**no-dal.com**

Additional registered domains can redirect to the canonical domain.

Possible product terminology:

-   No-dal Node
-   No-dal Cluster
-   No-dal Store
-   No-dal AI
-   No-dal Enterprise
-   No-dal Agent
-   No-dal Community

The brand should be able to stand independently rather than positioning
itself permanently as an alternative to a particular competitor.

A playful marketing concept can exist around the name:

> **Your infrastructure. Your rules. Everything else? No deal.**

This should remain marketing personality rather than defining the
technical identity of the project.

------------------------------------------------------------------------

# 34. North Star

No-dal succeeds when someone can take a machine, install the platform,
deploy and protect workloads, add applications, understand problems, and
later add more machines **without needing to stitch together half a
dozen unrelated administration products**.

At the small end, it should make self-hosting dramatically easier.

At the large end, the same architecture should be capable of growing
into a serious clustered infrastructure and enterprise management
platform.

The long-term idea is simple:

> **One open platform for running, connecting, protecting and operating
> your infrastructure.**

------------------------------------------------------------------------

# 35. Repository Naming and Product Separation

No-dal should use a clean repository naming convention that separates
the public platform, enterprise code and the marketing/cloud product.

Core repositories:

``` text
no-dal/no-dal
no-dal/ndl-ce
no-dal/ndl-ee
```

## `no-dal`

Purpose:

-   Main No-dal marketing website.
-   Public product information.
-   No-dal Cloud account experience.
-   Business/enterprise signup flows.
-   Contact-sales workflows.
-   Customer account management.
-   Subscription/licensing integration later.
-   Optional hosted services later.
-   Administrative backend for the No-dal commercial/web presence.

This repository represents the No-dal public/commercial web product
rather than the open-source infrastructure platform itself.

## `ndl-ce`

Meaning:

**No-dal Community Edition**

Purpose:

-   Public open-source No-dal infrastructure platform.
-   Core control plane.
-   Node agent.
-   Web management UI.
-   CLI. 
-   Guest agent components where appropriate.
-   Compute, storage, networking, backup, clustering, Store and AI
    platform capabilities.
-   Community-focused documentation and development.

This is the main public infrastructure codebase.

## `ndl-ee`

Meaning:

**No-dal Enterprise Edition**

Purpose:

-   Private enterprise-specific capabilities.
-   Commercial extensions.
-   Enterprise identity and governance features.
-   Advanced compliance/audit capabilities.
-   Enterprise fleet-management capabilities.
-   Commercial support integrations.
-   Other enterprise-only functionality that does not belong in the
    community codebase.

The Community Edition should remain genuinely useful and should not be
intentionally crippled to force adoption of Enterprise Edition.

## Naming Convention

`NDL` is an engineering/repository namespace.

The user-facing product names remain:

-   **No-dal**
-   **No-dal Community Edition**
-   **No-dal Enterprise Edition**
-   **No-dal Cloud**

Potential future repository names can follow the same prefix when a
separate repository is genuinely justified:

``` text
ndl-docs
ndl-sdk
ndl-apps
ndl-agent
```

Avoid prematurely splitting the project into many repositories. Prefer a
monorepo for Community Edition until there is a concrete technical or
organizational reason to split components.

------------------------------------------------------------------------

# 36. Community to Enterprise Upgrade Model

No-dal Enterprise Edition should extend Community Edition rather than
operate as a separate infrastructure platform or require users to
reinstall their environment.

A Community Edition installation should be upgradeable to Enterprise by
entering a valid No-dal Enterprise license key. Existing nodes, workloads,
storage, networking, backups and configuration remain in place.

Conceptually:

``` text
No-dal Community Edition
        |
        | Enterprise license activation
        v
No-dal licensing service
        |
        | signed, short-lived entitlement
        v
No-dal package/update service
        |
        | authorized signed Enterprise artifacts
        v
Local Enterprise capabilities enabled
```

## Licensing and Entitlements

The license key should be used for initial activation rather than as a
permanent credential for every request. Activation can register the
installation and establish a revocable machine or installation identity.

The platform should then use short-lived credentials and signed
entitlement information to determine which Enterprise capabilities are
available.

Expected properties include:

-   License activation through No-dal Cloud or a dedicated licensing API.
-   Revocable installation identities.
-   Short-lived authentication credentials where practical.
-   Signed entitlement documents or tokens.
-   Clear organization, subscription and update-channel information.
-   Grace periods for temporary loss of connectivity to licensing
    services.
-   Existing infrastructure must continue operating safely when licensing
    services are temporarily unreachable.
-   License expiry must never abruptly stop running customer workloads.

## Enterprise Artifact Delivery

Enterprise source code remains private. Customer installations must never
receive private repository credentials, source archives or access to the
private development repository as part of normal Enterprise operation.

Enterprise functionality that executes locally can be distributed as
compiled, versioned and cryptographically signed artifacts or modules.
The exact packaging mechanism remains an implementation decision.

The delivery model should provide:

-   Authenticated and authorized artifact downloads.
-   TLS for transport security.
-   Cryptographic signing of Enterprise artifacts.
-   Local signature verification before installation or execution.
-   Compatibility metadata for Community Edition and platform API
    versions.
-   Release channels and controlled rollout.
-   Revocation of compromised signing material or artifacts.
-   Safe rollback where technically possible.
-   No permanent decryption secret embedded in the open-source Community
    Edition codebase.

Encryption may be used as defense in depth, including per-installation or
ephemeral artifact encryption where useful, but it must not be treated as
a substitute for authentication, authorization and cryptographic signing.
Any capability that executes on customer hardware should be designed with
the assumption that its executable artifact can eventually be inspected.

## Local and Cloud-Assisted Enterprise Capabilities

Enterprise capabilities can use two models.

### Local Enterprise Capabilities

Features that need to continue working independently can execute locally
through signed Enterprise components. Examples can include advanced
identity integration, governance, compliance, policy, audit and fleet
management functionality.

### Cloud-Assisted Capabilities

Capabilities that genuinely benefit from a hosted service can remain
cloud-assisted. Examples can include licensing, organization management,
optional fleet visibility, support entitlement, managed update channels
and future hosted services.

No-dal should avoid making ordinary local infrastructure operation depend
on continuous Internet or Cloud availability.

## Product Experience

The upgrade should feel like enabling additional capabilities within the
same product:

``` text
Settings -> License

License Key
[ XXXX-XXXX-XXXX-XXXX ]

[ Activate ]

Edition:       Enterprise
Organization:  Example Pty Ltd
License:       Active
Updates:       Enabled
Support:       Active
```

There should be one underlying infrastructure environment and one workload
model. Enterprise extends the Community platform rather than creating a
second installation that drifts from it.

## Architectural Principle

> **Community Edition is the foundation. Enterprise Edition is an
> authenticated capability layer on top of that foundation.**

This model should preserve a straightforward path from a Community
installation to Enterprise while keeping Community genuinely useful and
keeping private Enterprise implementation source out of the public
codebase.

