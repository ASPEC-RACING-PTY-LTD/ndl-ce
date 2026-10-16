# Recovery

## Lost host: recover from the platform backup

If the host running NDL-CE is lost, a new host can take over everything from
the [platform backup](platform-backup.md): accounts, settings, secrets,
workload and game server definitions, storage and network mappings, backup
targets and policies, and the catalogue of restore points. Workload data then
comes back from the workload backups.

You need:

- a host with NDL-CE installed, the **same version or newer** than the one that
  made the backup, and **not yet set up**;
- its one-time setup token (printed by the installer);
- **new** Cloudflare credentials for the bucket: an API token with R2 access, or
  an R2 access key id and secret, plus the account id and the bucket name.
  Revoke the lost host's credentials; they are not needed and are replaced;
- the username and password of an Owner of the lost NDL-CE.

### Steps

1. Open the new host. On the setup screen choose **Recover an existing
   NDL-CE**. It is offered only while the host is unclaimed.
2. Enter the setup token, account id, bucket and the new credentials. An API
   token is turned into R2 S3 credentials (its id and the SHA-256 of its
   value). NDL-CE looks for the backup under `ndl-platform/`.
3. Enter an Owner's username and password. They unlock the newest snapshot
   (or, if that envelope does not open, the kept fallback). A wrong password
   is refused and repeated failures are throttled. A backup from a newer
   NDL-CE is refused: install the same or a newer version.
4. Map the lost host's storage pools and networks onto this one. The old
   paths and bridges are filled in.
5. **Restore**. From here on setup and further recoveries are locked.

### What restore does

1. Replaces the fresh database with the backup in a single transaction. If
   anything fails, nothing was changed and you can try again.
2. Applies this version's migrations.
3. Gives this host a fresh identity: it enrols with its own node id and
   certificates, everything that pointed at the lost host's nodes now points
   at it, and the old nodes are revoked.
4. Applies the pool paths and network bridges you chose.
5. Holds every workload and game server stopped, remembering how each was
   set, so nothing starts without its data.
6. Revokes every restored session.
7. Keeps the **new** bucket connection: the restored target for this bucket
   gets the new endpoint and credentials (its encryption key stays), other
   restored targets on the same bucket are marked superseded, and the
   connection is read back as saved and tested by listing the platform backup.
   The result is shown before any workload is restored.
8. Installs the lost host's backup repository key, so its restore points
   can be decrypted.
9. Restarts the control plane. Sign in with your Owner account.

Access to the bucket and the ability to decrypt stay separate throughout: the
bucket credentials find and download the backup, and only an Owner's password
opens it.

### Restoring the workloads

After recovery, **Backups → Recovered from a platform backup** lists every
workload with its newest offsite restore point and, before anything runs,
**the changes that were not backed up**: commits that were never pushed and
uncommitted files in its Git repositories. An Owner chooses **Restore all
workloads** or restores them one by one. For each workload:

1. A system container is created again from its definition. For a Smart or
   Custom backup that starts from its base image; a Full backup is the whole
   root. A VM gets its disk back.
2. Its data is restored from the newest offsite restore point, on top of the
   base image (paths never leave the workload's root).
3. It is started, and what Smart left out is put back:
   - Git repositories are cloned again and checked out at the exact recorded
     commit. Credentials are tried in order: the workload's own (its
     `~/.git-credentials`, `~/.ssh`, `ch` login, kept in its backup), then
     the host's CodeHold login (`ch clone`), then a token the Owner gives for
     that clone, which is used once and never stored. If none work it stops
     and asks. A commit that was never pushed cannot be cloned: the newest
     pushed ancestor is checked out instead, with a warning.
   - Images come back from a kept copy, else by their recorded digest
     (`repo@sha256:...`) and are tagged as before. Locally built images are
     rebuilt from the Dockerfile at the checked-out commit. If a digest is
     gone, the Owner is asked before the tag is used instead.
   - Compose projects are started with the images above (no pulls by tag,
     no builds at start).
4. It gets back the power it had: running workloads are left running,
   stopped ones are stopped again.

A workload that needs input shows what it needs (a token for a repository,
approval to use a tag) and continues from there without restoring its data
again. A failed one can be tried again; what the failed attempt made is
cleared first.

## Control plane down

Stopping or killing `ndl-control` leaves the host unchanged.
Workloads are not bound to the control plane or the agent
(`BindsTo` / `PartOf` / `Requires` are forbidden on workload units).
`systemctl stop ndl-control` does not stop guests.

A second `systemctl start` of an already-running `nodal-ct@` /
`nodal-vm@` unit is a no-op. See [workload-lifecycle.md](workload-lifecycle.md)
for live CPU/memory apply versus Pending Restart.

## Replay setup

The agent socket and process can also stop without tearing down
workloads that have already been started by systemd.

## Replay setup

`/setup` is a single-use first-admin claim. After an admin exists,
replaying setup is refused. There is no factory password to reuse.

## recover-admin

`nodalctl recover-admin` is a local host operation. It requires
root (or equivalent) on the machine. It is not a remote reset and
it is not a default credential. It resets the named user's password,
revokes that user's sessions and API tokens, and deletes their
`mfa_methods` rows so a lost last authenticator cannot lock the
administrator out of the appliance.

## Identity that persists

`cluster_id` and `node_id` are minted on first boot and stored on
disk under `/var/lib/ndl` and in the `nodal` database. Removing or
restarting packages does not mint new identities. Uninstall does
not delete those paths. A later destroy command is required to wipe
them.

Package upgrades, `ndl-control` restarts, overlay reinstalls, and
`EnsureRoles` refresh the built-in role permission catalog only.
They do not reset users, passwords, role bindings, sessions, or MFA
configuration. Setup claim stays single-use: it creates the first
owner only when no admin exists. See [management.md](management.md).

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

qemu-img convert is refused while the source or destination disk is attached
to a running unit, and when applied or unit state cannot be proven stopped.
Live disk mutation is an error, not a warning.

## TLS (Phase 9)

Private keys live under `/var/lib/ndl/certs` mode 0600. PostgreSQL stores
fingerprint and mode only. The API never returns a private key.

Enabling TLS requires `X-Nodal-Confirm: enable-tls`. Restart `ndl-control`
after generate or import so the process binds HTTPS. A bad import keeps the
last good certificate pair and does not fall back to open HTTP.

When TLS is enabled, port 80 and the previous HTTP listen address redirect
to HTTPS. ACME HTTP-01 tokens are served on `/.well-known/acme-challenge/`
without redirect. Event streams and IO websockets refuse cleartext.

Let's Encrypt (public HTTP-01) and step-ca (private ACME directory URL) use
the same ACME fields. Directory probe failure is recorded as `failed`, not
as an issued certificate.

Self-signed trust is the SHA-256 fingerprint shown in the UI. It is not a
public CA.

Console uses `compute.console` and a short-lived ticket. It does not grant
`terminal.open`. VM Terminal and Files require `nodal_ga` state `ok`. Missing
or disconnected guest agents disable those tabs with a reason. Console remains
usable without a guest agent.

### Recovery matrix (guest Terminal and Files)

1. Install the No-dal Guest Agent inside a Linux VM (or use a fixture guest channel).
2. `nodal_ga` reports `ok`.
3. Terminal Here opens a guest PTY at the Files cwd.
4. Upload Here writes inside guest `/`, not the host qcow2.
5. Audit records `vm:/...`.
6. Agent or guest disconnect disables Terminal and Files with a reason.
7. Serial console still works.
8. `not_installed` and `unavailable` do not enable the tabs.

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

## Snapshots (Phase 10)

Snapshot is not backup. Directory VM snapshots are external qcow2 overlays
with a chain cap of 16. `qemu-img` is refused while the disk is attached to
a running unit; live overlay uses QMP. Directory system containers do not
pretend to be ZFS.

Rollback requires `X-Nodal-Confirm: rollback` and a stopped disk for
Directory overlays. Flatten is an offline convert of the current tip.

### Recovery matrix (snapshot)

1. Create a VM overlay snapshot while the guest is stopped, or via QMP if running.
2. Make a change inside the guest.
3. Stop if needed, rollback to the snapshot, start.
4. The workload UUID is unchanged. This is not a backup restore.

## Backups (Phase 11)

Backups are independent copies. A snapshot is not a backup. The engine
snapshots, then converts the frozen disk into a standalone qcow2 on the
target with a SHA-256 checksum. Overlay backing files are flattened into
the artifact, so the copy does not depend on live pool files.
Destinations are local directories, plus NFS and SMB as backup targets
only. NFS/SMB stay unavailable until the locator is an existing local
directory. The API never returns target passwords.
Target create and directory probes go through the typed agent.

Directory system containers copy the mounted rootfs live when ZFS send
is not available. Running containers stay running. The copy is
crash-consistent. Optional guest hooks
`/etc/ndl/hooks/backup-pre` and `/etc/ndl/hooks/backup-post` can flush
application state. Stopped containers are copied without hooks. ZFS
pools still use snapshot plus send. Extra attached disks are skipped
in the plan.

Restore `new` mints a new workload UUID. Restore `replace` requires
`X-Nodal-Confirm: restore` and overwrites the existing boot disk after stop.
Retention prunes backup artifacts, not live overlay files needed for the
snapshot chain.

`nodalctl backup run` and `nodalctl backup restore` are the CLI paths.

### Recovery matrix (backup)

1. Create a VM and a local backup target.
2. Run a backup. A snapshot exists and an artifact with checksum is catalogued.
3. Delete the VM configuration (volumes may remain) or keep it.
4. Restore `new` to a new UUID and start the guest on the same node.
5. The restored disk boots from the independent copy if the target is intact.
6. Restore `replace` without confirm is refused.
7. An NFS locator that is not a local directory remains unavailable. That is not a successful backup.

## Cluster disaster recovery (Phase 33)

Cross-node restore is a normal workflow. `POST /api/v1/backups/artifacts/{id}/restore`
accepts `target_node_id`. Empty dest is this control node. Restore `new` onto a
worker records the catalog (`DesiredNodeID` / `NodeID`) and does not start a
second copy on the control unix agent when that dest agent is not connected.
Restore `replace` stays on the current node.

Artifact rows store locality (`local`, `object`, or `pull`) and an optional
pull URL. The pull URL is a locator without credentials. Dest `secret.use`
is audited only when this control node materializes an object artifact.

`GET /api/v1/backups/dr-export` writes the backup catalog plus node and
workload identity. It never includes target passwords or encryption keys.
Shared NFS/iSCSI volumes already have IDs; ZFS send artifacts still restore
with `zfs recv` in a later path, not qemu-img.

`nodalctl backup restore --artifact ID --mode new --node ID` and
`nodalctl backup dr-export` are the CLI paths.

### Recovery matrix (cluster DR)

1. Node A holds a VM. A backup artifact exists on local disk or object storage (R2/S3).
2. Node A is down or revoked. The catalog and artifacts remain.
3. Restore `new` with `--node` set to node B (this control node in the Cloud fixture).
4. The restored workload UUID is new. Disks are not copied onto the wrong node.
5. Restore onto a worker whose agent is not connected stays `unavailable` with that reason.
6. DR export JSON has locators and object keys and does not contain passwords.

## Platform updates (Phase 12)

Control-plane package updates use the signed No-dal Debian repository that
install used. They do not use a one-off script. Split packages mean
`ndl-control` can restart without `BindsTo` on `nodal-vm@` units, so guests
keep running.

On a host that is not Debian 13 amd64 the Update API reports Unsupported
and does not pretend apt succeeded. Check is always a dry run. Apply and
rollback require `X-Nodal-Confirm`. Checkpoint writes `/var/lib/ndl` plus
a PostgreSQL dump when the Debian adapter can run those typed argv lists.

Kernel rollback is GRUB previous-entry (`grub-reboot 1`), not a QEMU guest
action. Store packages are declarative; helper scripts are rejected.

`nodalctl update check` and `nodalctl update apply --confirm apply-update`
are the CLI paths.

### Recovery matrix (platform update)

1. Confirm `nodal-vm@` has no BindsTo/PartOf on ndl-control or ndl-agent.
2. Apply a control-plane package bump from the signed repo.
3. Guests stay running. ndl-control may restart.
4. Roll back the `ndl-control` package to the recorded previous version.
5. After a kernel package update, use GRUB previous-kernel (`grub-reboot 1`) then reboot the host if the new kernel is unusable.
6. Homelab Migration Candidate requires Phases 9 through 12 on Debian 13 hardware. Cloud fixture coverage is not that gate.

## HA foundations and rolling updates (Phase 34)

The cluster has one Postgres writer. A second control-plane process cannot
hold the lease while it is live. Replica DSN is a secret and is never
returned. Replica status stays `not_configured` until a DSN is stored, then
`unavailable` until operator-managed streaming replication is proven. This
is not multi-master.

Fencing is operator isolation of the old writer. STONITH is not implemented.
`POST /api/v1/cluster/ha/fence` with confirm expires the lease. Promote then
takes it. Guests keep running because workload units are not bound to
ndl-control.

Rolling update drains one node (maintenance; guests stay running), applies
the Phase 12 update on this control node, then the next. Worker package
apply stays unavailable until the dest agent is connected.

`nodalctl cluster ha`, `nodalctl cluster fence --confirm fence`,
`nodalctl cluster promote --confirm promote`, and
`nodalctl cluster update --confirm cluster-update` are the CLI paths.

### Recovery matrix (HA and rolling)

1. Two holders: the second AcquireLease fails while the first lease is live.
2. Fence or expire the lease. The standby takes it. Guests stay running.
3. Rolling update does not call guest stop.
4. Worker update steps stay unavailable when dest agent is not connected.
5. Replica DSN never appears in HA JSON.

## Feature modules (Phase 35)

The `nodal` metapackage stays light. GPU, Kubernetes, distributed
storage, AI, and Docker Management are extra packages. `GET /api/v1/features`
lists core VM/CT as installed and optional modules as off.

`nodalctl feature enable oci` installs `nodal-feature-oci` through the
typed HostUpdate path. Enabling Kubernetes on a node at or below 8 GiB
RAM needs `X-Nodal-Confirm: enable-k8s`. Kubelet is not started.
Disable needs confirm when workloads exist and does not delete them.

### Recovery matrix (features)

1. Fresh `apt-get install nodal` does not pull GPU, Kubernetes, Ceph, AI, or Docker Management.
2. Tiny-node Kubernetes enable without confirm is refused. Kubelet stays down.
3. Disable with confirm leaves OCI workloads running.
4. Docker Management stays off until `nodalctl feature enable docker`. Discovery
   does not run while the module is disabled.

## No-dal Store (Phase 36)

Store packages are YAML manifests. `run: bash` and helper script keys are
rejected. Install maps to stack plus OCI create. Failed install deletes
the stack and workloads it created. Unsigned Community packages warn.
Signatures are Phase 37.

`nodalctl app list`, `nodalctl app import FILE`, and
`nodalctl app install --id ID` are the CLI paths.

### Recovery matrix (Store)

1. Official sample-web installs from the bundled manifest only.
2. A manifest with `run: bash` is refused.
3. A failed OCI pull rolls back stack and workload rows.

## Store trust (Phase 37)

Packages are signed with cluster Ed25519 keys. Tampered YAML fails
closed. Revoking a key stops new installs that need that signature.
Running workloads are left running. `verified-only` refuses unsigned
Community packages. The scan report is visible; CVE scanning is
unavailable unless a scanner is installed.

`nodalctl app verify --id ID` and `nodalctl app policy` are the CLI
paths.

### Recovery matrix (Store trust)

1. Tampered manifest signature is refused.
2. Revoked signing key refuses new installs.
3. Unsigned Community is refused when policy is verified-only.

## Optional Kubernetes (Phase 38)

Kubernetes is an optional feature. Default install has no kubelet
process. Virtual machines and system containers do not require it.
`nodalctl feature enable kubernetes` installs the optional package and
does not start kubelet. `nodalctl kubernetes start --confirm start-kubelet`
is a separate typed action.

Disable leaves workloads running. Stop kubelet does not stop VMs.

### Recovery matrix (Kubernetes)

1. Fresh status shows kube_process false and kubelet_started false.
2. Enable without start leaves kubelet down.
3. Stop does not stop virtual machines.


## Identity completion (Phase 13)

TOTP is the working MFA method. WebAuthn is not implemented. Login returns
an MFA challenge instead of a session when TOTP is enabled. Recovery codes
are shown once at enroll. Service principals cannot password-login.

`nodalctl recover-admin` deletes `mfa_methods` for the named user so a lost
last authenticator does not strand the administrator. `nodalctl user mfa`
and `nodalctl group add` are the CLI paths.

Volume encryption unlock is honest 422 for Directory storage. LUKS and ZFS
native encryption are later backends. Cluster destroy stays not implemented
even after AAL 2 and confirm.

### Recovery matrix (lost MFA)

1. Enroll TOTP and confirm a code.
2. Lose the authenticator and recovery codes.
3. On the appliance as root, run `nodalctl recover-admin --username USER --password NEW`.
4. Password login succeeds without an MFA challenge.
5. Re-enroll TOTP if MFA is still required by policy.

## GPU assignment (Phase 14)

Workloads receive a GPU only when assigned. Create without a GPU does not
attach `/dev/dri`. `gpu=all` is refused. ACS override is refused as the
product default. Two exclusive claims on the same GPU or IOMMU group fail.

VFIO assignment requires a VM snapshot and a stopped guest. Bind uses typed
`driverctl set-override <BDF> vfio-pci`. Unassign runs `driverctl unset-override`
so the host driver can bind again. HDMI audio functions in the same IOMMU
group are listed and included in the VFIO host set.

GPU runtime packages are optional host-platform work. They are not Depends of
`ndl-agent`. NVIDIA_VISIBLE_DEVICES=all is never set.

Failed VFIO restore: unassign keeps the claim and the VM spec unchanged and
returns the error, so the GPU is never reported free while still bound to
vfio-pci. Fix the cause and unassign again; the retry uses the same typed
argv. If driverctl is missing, the status is failed with an honest reason,
not a fake unbound GPU.

Render, compute, and encode claims on system containers and OCI workloads
derive their device nodes from inventory. NVIDIA claims add the per-GPU
`/dev/nvidia<N>` reported by the driver plus `nvidiactl`, `nvidia-uvm`,
`nvidia-uvm-tools`, and `nvidia-modeset`; `nvidia-caps` is excluded. See
`docs/docker.md` for the exact device rules.

Unassign keeps the workload's other GPUs: the control plane rebuilds the
device list from the remaining claims, checks the regenerated config, and
rolls the claim rows and config back together if the agent fails. A running
container keeps its device nodes until it restarts; the unassign response
says when a restart is still needed.

Removing a GPU from the host: its claims stay recorded and the GPU page lists
them under "Assigned GPUs not on this node", so they can still be
unassigned. Unassign them before or after pulling the card, then restart the
affected workloads.

The workload Diagnostics page compares saved, expected, configured, and
guest GPU devices. Reapply rewrites the container config from the saved
claims without restarting. It fails when a node is missing on the host (the
GPU is removed or its driver is not loaded) and reports restart required
when the running container has not received every device.

## ZFS storage (Phase 15)

Import is by pool GUID. `zpool import -f` is refused. Create uses extra disks only,
never the host root disk. UUID remains desired identity; `zpool_guid`, dataset names,
and `/dev/zvol/...` are locators.

If a disk is pulled, the pool is faulted or unavailable. Desired pool and volume rows
remain. Capacity is not reported as zero.

Directory remains the default for hosts without ZFS. Missing ZFS userland is not a fake
Available pool.

## Observability (Phase 16)

Metrics live in agent SQLite, not PostgreSQL. Empty or stale series are not filled with zeros.
Logs use typed journalctl argv for allowlisted units only.

If the agent is down, the UI shows Stale or Unavailable, not invented charts.
Local webhook URLs are secrets. Optional SMTP stays not_configured until a host is set.

## License (Phase 43)

CE does not require a key. Management, License stores a key only when the
operator enters one. The licensing API is contacted only then. If that
API is unreachable, status is grace or unreachable. Workloads are not
stopped. Clearing the key returns the surface to absent CE.





