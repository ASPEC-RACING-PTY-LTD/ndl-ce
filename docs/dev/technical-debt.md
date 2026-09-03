# Technical debt ledger

Non-blocking findings from autonomous CE implementation.
Review before Dogfood Host, Homelab Migration Candidate, Feature-Complete Beta, and CE 1.0.

## Phase 4

- MEDIUM. nft INPUT default-drop is deferred to Phase 27.
  Why not blocking: isolated NAT already uses typed nft; default-drop is the later host firewall phase.
- LOW. Management NIC detection uses the default-route interface.
  Why not blocking: matches Phase 4 safety goal on the single-NIC appliance.
- LOW. Network apply confirm HMAC is keyed with the cluster UUID.
  Why not blocking: confirm is still required; stronger binding can wait.

## Phase 5

- MEDIUM. Agent does not independently confine CT rootfs paths to storage roots.
  Why not blocking: the API joins pool locators; defense-in-depth can land with later storage backends.
- MEDIUM. Agent does not re-enforce privileged/idmap policy. HTTP RBAC does.
  Why not blocking: southbound is peer-cred to the control plane only.
- LOW. Some control-plane rootfs joins still use path.Join. Phase 6 jail join uses storage.JoinUnder.
- LOW. AppArmor is set unconfined when securityfs is missing (Docker e2e).
- LOW. Official simplestreams index is HTTPS-only; the tarball is gpgv-verified.
- LOW. lxcfs is skipped under container virtualization.

## Phase 6

- MEDIUM. Agent CapabilityBoundingSet includes CAP_SYS_ADMIN, CAP_SETFCAP, and CAP_SYS_PTRACE for typed lxc-attach of unprivileged system containers.
  Why not blocking: there is still no generic host execution RPC; DevicePolicy remains closed; NoNewPrivileges remains yes.
- LOW. Browser tickets use the `ndl.ticket.` WebSocket subprotocol because browsers cannot set `X-Nodal-Ticket`. Query-string tickets are rejected.
- LOW. Folder archive download was left for Phase 17 and remains deferred: Phase 17 Agent is None, so no new zip RPC.
- LOW. VM Terminal and Files were deferred to Phase 20. Why not blocking: Phase 20 now enables those tabs when nodal_ga is ok.
- LOW. Docker Desktop overlay backing identity changes across host reboot, so Directory pools on this e2e guest can show unavailable. Real disk UUIDs are the product path.

## Phase 7

- MEDIUM. Shared `ndl-qemu` uid can rewrite sibling frozen argv. Why not blocking: the prototype owns one VM; Phase 8 isolation can add per-VM credentials.
- LOW. PCI addresses are pinned at 0x5/0x6 in last-applied rather than from `query-pci`. Why not blocking: ABI is stable for the gate; live query can land with product VMs.
- LOW. Host reboot is proven via `systemctl enable` of `nodal-vm@`, not a live guest reboot. Why not blocking: the unit is independent of CP/agent and enabled for `nodal-workloads.target`.
- LOW. Adding other-execute on storage/workload parents uses a temporary root chown because the agent bounding set has no CAP_FOWNER. Why not blocking: CAP_CHOWN is already required; this does not add a new capability.
- MEDIUM. Launcher re-validation does not re-pin `filename=` under the storage root or force sockets onto the per-VM runtime dir. Why not blocking: compile already jails those fields; AppArmor workloads `r` blocks QEMU from rewriting argv when a parent profile exists.
- MEDIUM. Lab start does not re-check an existing volume is `vm-disk` before chown to `ndl-qemu`. Why not blocking: the lab path is admin-only and the prototype creates its own volume.
- LOW. Debian `qemu-system-x86` does not ship `/etc/apparmor.d/usr.bin.qemu-system-x86_64`, so the local snippet is a no-op until a parent profile exists. Why not blocking: TCG/QMP were proven; confinement is still the unit user plus closed devices.
- LOW. `Observed.PID` is unused; runtime identity is systemd MainPID / `running_as`.
- LOW. `EnableAutostart` trusts the caller UUID. The RPC already requires a UUID.

## Phase 8

- MEDIUM. Shared `ndl-qemu` uid can read/write sibling VolumeHandle disks because the AppArmor local snippet allows `/var/lib/ndl/storage/** rwk`. Why not blocking: per-VM credentials would add a fragile identity model; Dogfood Host isolation is unit plus closed devices plus typed launch.
- LOW. Debian `qemu-system-x86` still may not ship a parent AppArmor profile, so the local snippet can remain a no-op. Why not blocking: confinement is still the ndl-qemu user plus closed devices; enforcing AppArmor is physical/appliance validation.
- LOW. Host reboot autostart is systemd enablement, not a live guest reboot in Cloud CI. Why not blocking: the unit is independent of CP/agent.
- LOW. Browser graphical console confirms a ticketed VNC unix session and does not decode a full RFB framebuffer. Serial is the interactive compatibility console. Why not blocking: console works without a guest agent; a richer VNC client can land later.
- LOW. PCI live match compares persisted slot addresses to `query-pci` slots. QEMU may also show bridges and implicit devices. Why not blocking: assigned virtio/VGA/serial slots are still pinned.
- LOW. Cloud-image validation beyond qcow2 magic remains best-effort from Phase 3. Why not blocking: QEMU start fails honestly if the artifact is not usable; the library file is not mutated.
- MEDIUM. TAP devices are created at prepare and not always deleted on a clean stop; cleanup runs on failed prepare and VM delete. Why not blocking: names are `nv` plus hex and stay on the VM bridge; leaked TAPs do not attach a second QEMU. Later lifecycle polish can delete on stop.
- LOW. `ip link delete` allowlist still accepts any `nv*` name if last-applied is tampered. Why not blocking: TAP names are compiled, and delete already refuses unmanaged names that are not derived.

## Phase 9

- MEDIUM. ACME HTTP-01 issuance is a directory probe plus stored pending/failed state, not a completed Let's Encrypt issuance in Cloud. Why not blocking: generate and import are the Dogfood TLS path; ACME for public names and step-ca is documented and probed honestly.
- MEDIUM. After generate/import, HTTPS redirect starts on ndl-control restart (`restart_required`), not in the same process. Why not blocking: confirm is required, cookies/WS harden immediately, and the UI tells the operator to restart.
- LOW. Binding :443 requires CAP_NET_BIND_SERVICE on ndl-control. Why not blocking: the unit is still non-root.

## Phase 10

- MEDIUM. Live overlay uses QMP `blockdev-snapshot-sync`; Cloud tests prove the stopped qemu-img overlay path and that live qemu-img is refused. Why not blocking: the product path is typed QMP or offline overlay, never live qemu-img.
- LOW. qemu-ga fsfreeze is best-effort and does not block snapshot. Why not blocking: roadmap calls freeze best-effort.

## Phase 11

- MEDIUM. Backup of additional data disks is refused. Why not blocking: restore cannot apply extra attached disks; a boot-only 202 would invent a complete copy.
- MEDIUM. Nightly backup of a running guest creates a qcow2 overlay each run, so the chain cap of 16 can fill if overlays are not flattened. Why not blocking: the backup artifact is a standalone qemu-img convert of the frozen parent; the live chain is a separate snapshot concern; flatten remains an explicit operator action.
- LOW. qemu-img convert of a backup is not run in Cloud CI (qemu-img is absent). Why not blocking: ConvertOffline is the same typed path as Phase 8/10 flatten; SkipHostCmds plus frozen-parent source assertions cover the control plane.
- MEDIUM. NFS and SMB destinations are catalogued and used only when the locator is already a local directory. Cloud does not mount remote filesystems. Why not blocking: faking a remote copy would violate honesty; Phase 26 owns NFS/SMB as compute datastores.
- LOW. Backup compression is the qcow2 image as stored. No extra gzip layer. Why not blocking: full copy is an honest incremental-not-available engine.
- LOW. Homelab restore-and-boot of a deleted VM on Debian 13 hardware is not proven in this Cloud VM. Why not blocking: fixture restore new UUID plus start is covered; appliance boot remains physical validation.

## Phase 12

- MEDIUM. Homelab Migration Candidate requires Phases 9-12 on Debian 13 hardware. This Cloud VM is Ubuntu and has no installed product. Why not blocking: the Update API refuses honestly; signed-repo upgrade remains appliance validation.
- LOW. GRUB previous-kernel is documented typed argv and is not executed during a control-plane package apply. Why not blocking: CP bumps must not reboot guests or the host; kernel rollback is an operator recovery step.
- LOW. apt-get/pg_dump/tar are not run in Cloud CI. Why not blocking: SkipHostCmds plus argv unit tests cover the typed adapter; executing apt on Ubuntu would fake Debian success.
- LOW. Store compatibility is an honest unsupported hook. Why not blocking: roadmap assigns the real check to Phase 36.

## Phase 13

- MEDIUM. WebAuthn is not implemented. Why not blocking: roadmap allows TOTP as the working MFA; UI and API say not implemented.
- MEDIUM. Directory volume unlock is honest 422. LUKS/ZFS native encryption need those backends. Why not blocking: ZFS create-time encryption is Phase 15; faking unlock would violate honesty.
- LOW. Cluster destroy stays not implemented after AAL 2. Why not blocking: CE must not wipe the appliance in this phase.
- LOW. recover-admin host-key path is not executed in Cloud CI (not root, not an installed product). Why not blocking: SQL includes DELETE FROM mfa_methods; unit tests prove login after DeleteUserMFA.

## Phase 14

- MEDIUM. Physical GPU, IOMMU, VFIO bind, and DKMS are not available in this Cloud VM. Why not blocking: API/RBAC, group listing, exclusive claims, typed driverctl argv, and LXC config without /dev/dri are proven; hardware bind remains appliance validation.
- LOW. Render assignment uses the planned `/dev/dri/renderD128` locator. Why not blocking: the node is optional in LXC config; Cloud has no DRM device to prove the live node number.
- LOW. QEMU vfio-pci slots start at 0x1a rather than a live query-pci allocation. Why not blocking: locators are not identity; Phase 8 already pins compiled PCI slots.

## Phase 15

- MEDIUM. Physical ZFS pools, zvols, dataset mounts, and zfs send streams are not available in this Cloud VM. Why not blocking: GUID import, force refuse, capability matrix, pulled-disk unavailable-with-nil-capacity, zvol vs dataset locators, and typed argv are proven; hardware ZFS remains appliance validation.
- MEDIUM. zfs recv restore of a send artifact is honest 422. Why not blocking: BackupSource send is this phase; qemu-img must not consume a ZFS stream; recv restore can land with backup hardening.
- MEDIUM. zfs send captures stdout in-process rather than streaming to the dest file. Why not blocking: SkipHostCmds is the Cloud path; a streaming RunTo belongs on the appliance for large zvols.
- LOW. ZFS create-time native encryption is not implemented. Why not blocking: Phase 15 is pools, zvols, datasets, snapshot/send; encryption remains a follow-up, not a fake unlock.
- LOW. by-id aliases of the root disk are not EvalSymlinks-compared. Why not blocking: inventory MountHint=/ refuses /dev/sdX and prefix children; extra disks remain the product path.

## Phase 16

- MEDIUM. Live journald follow and local SMTP delivery are not executed in this Cloud VM. Why not blocking: typed argv, SkipHostCmds, and webhook delivery are proven; appliance journald and SMTP remain physical/local validation.
- LOW. Per-workload traffic is TAP/iface counters (`net.iface.*`), not cgroup netcls identity. Why not blocking: host net plus TAP names are honest; cgroup accounting can land later.
- LOW. Capacity forecast is a linear fit of `storage.avail_bytes` samples. Why not blocking: fewer than four points stays Collecting and does not invent hours-to-zero.

## Phase 17

- LOW. Folder archive download is still not implemented. Why not blocking: Phase 17 Agent is None; zip would need a typed Files RPC, not a UI fake.
- LOW. Tablet/phone layout is CSS-only in Cloud. Why not blocking: chrome wraps and tap targets grow at 64rem; physical devices are not in this environment.
- LOW. Expert JSON is a read-only preview of the same create body. Why not blocking: a free-form editor that posts extra keys would violate the same-API contract and Host.Exec ban.

## Phase 18

- MEDIUM. Template deploy clones the current source disk when a snapshot overlay is missing. Why not blocking: create records a snapshot when overlay succeeds; Cloud fixtures still clone with new UUIDs and MAC.
- MEDIUM. Clone of additional data disks is refused. Backup, restore, bundle export, and migrate refuse the same extra disks. Why not blocking: boot disk clone is the Phase 18 product path; extra attached disks would otherwise share a writable volume UUID.
- MEDIUM. Live QMP usb-host hotplug is not executed in this Cloud VM. Why not blocking: frozen argv usb-host plus device_add/device_del are typed; missing QMP returns a live-session error instead of inventing attach success.
- LOW. Generic PCI attach reuses exclusive GPU assignment rows for IOMMU claims. Why not blocking: ParseGPUID and group listing still apply; GPU inventory remains on the GPU API.
- LOW. Clone boot of a guest OS is not proven here. Why not blocking: clone materializes a new volume via qemu-img convert and starts the systemd unit through the existing VM lifecycle path.

## Phase 19

- MEDIUM. Live virtio-serial org.nodal.guest.0 is proven with a unix fixture, not a booted guest. Why not blocking: Cloud has no KVM guest OS; the protocol, agent mux, and Linux PTY banner are covered.
- LOW. Windows PTY is honestly unimplemented. Why not blocking: roadmap allows a Files/shutdown/IP subset; Linux Terminal is the Phase 20 acceptance path.
- LOW. ndl-guest is an optional guest package and is not a host metapackage dependency. Why not blocking: VMs start without it.
- LOW. GET /guest never emits stale in Cloud tests. Why not blocking: missing reply is not_installed and missing socket is unavailable; stale is for later observation aging.

## Phase 20

- MEDIUM. Terminal Here / Upload Here are proven through HTTP plus a fake guest/IO, not a booted virtio-serial guest. Why not blocking: Cloud has no KVM guest OS; jail is guest:/ and product tabs stay off unless nodal_ga is ok.
- LOW. Windows PTY remains unimplemented, so a Windows VM with nodal_ga ok can still fail Terminal honestly. Why not blocking: Files still work; Console remains the Windows shell path.

## Phase 21

- MEDIUM. containerd pull/run is proven with FakeRuntime and SkipHostCmds, not a live ctr on this Cloud VM. Why not blocking: Cloud has no containerd; unavailable health stays honest; Debian 13 appliance validation remains.
- MEDIUM. `ctr image pull --user user:pass` puts registry password on argv. Why not blocking: that is the typed ctr interface; last-applied and API JSON redact passwords.
- MEDIUM. Port publish, CNI/bridge attach, cgroup limits, and SecretRefs are stored as desired state and not applied by `ctr run` in this runtime. Why not blocking: faking published ports would invent connectivity; Compose/CNI can land with Phase 22 stacks.
- LOW. Health HTTP probe is configured and visible as collecting/not_configured; live probe against a running task is not executed here. Why not blocking: inventing healthy would violate honesty.
- LOW. OCI clone is refused. Why not blocking: recreate-from-image plus volume move belongs with migrate phases.

## Phase 22

- MEDIUM. Stack apply orchestrates existing OCI RPC; live multi-container containerd apply is not proven on this Cloud VM. Why not blocking: FakeOCI covers import, privileged reject, and partial resume; honest collecting/unavailable statuses remain.
- MEDIUM. Compose import supports a service/image/env/ports/named-volumes subset; full Compose feature surface (depends_on, profiles, build, tmpfs, secrets files) is not claimed. Why not blocking: imported members are PATCH-editable No-dal objects; Compose is not runtime SoT.
- LOW. Stack delete does not cascade-delete OCI workloads. Why not blocking: workloads remain first-class objects reachable from member links.
- LOW. Shared stack-scoped networks beyond attaching an existing network_id are not introduced. Why not blocking: Phase 4 networks remain the attachment surface.
- LOW. Named compose volumes always allocate Directory volumes even if the selected pool is ZFS. Why not blocking: Directory is the documented compose volume class; ZFS dataset mounts can wait for a later storage pass.
- LOW. Apply is create-once per member; later desired edits do not recreate the OCI unit. Why not blocking: the linked workload remains the running object and is editable through the workload API.

## Phase 23

- MEDIUM. Live MinIO/R2 is not in this Cloud job. Why not blocking: FakeObject plus MemoryTransport prove encrypt-before-upload, restore to a new UUID, and no_check_bucket honesty; virt MinIO is the intended live job.
- MEDIUM. Multipart resume does not persist a partial MPU upload id to disk. Why not blocking: objects above 8MiB still complete in one agent call; a sidecar resume file can land without changing the destination model.
- LOW. qcow2 dirty-bitmap incrementals are not implemented. Why not blocking: ZFS send -i is wired and transfers less when the prior snapshot exists; qcow2 remains a full encrypted copy.
- LOW. ZFS object restore still returns 422 (zfs recv is a later backup phase). Why not blocking: qcow2 object restore to a new UUID is the proven boot path.
- LOW. Dedup across artifacts is not a second product UX. Why not blocking: compression plus ZFS incrementals are the CE engines; a dedicated dedup product would violate the phase.

## Phase 24

- MEDIUM. Live qemu-img check and libguestfs/nbd extract are not proven on this Cloud VM. Why not blocking: SkipHostCmds and missing guestfish stay unverified/unavailable; FakeVerify covers checksum mismatch and throwaway isolation.
- LOW. Nightly scheduled throwaway verify is not ticked yet. Why not blocking: on-demand open and throwaway verify exist; operators can prove a backup before disaster.
- LOW. File restore is capped at 1MiB and returns base64. Why not blocking: the picker is for config files; large extract can wait for a streaming download.

## Phase 25

- MEDIUM. Live lvm2 is not in this Cloud job. Why not blocking: SkipHostCmds plus FakeLVM prove no incremental send, vgexport refuse, missing PV unavailable, and thin snap recording; extra-disk appliance is the intended live job.
- LOW. Filesystem classes mkfs.ext4+mount on thin LVs are SkipHostCmds in Cloud. Why not blocking: VM disks are thin LVs (raw); CT mount is typed argv when lvm2 is present.
- LOW. LVM backup is a full qemu-img convert of the snapshot LV, not a send stream. Why not blocking: IncrementalSend stays false; inventing zfs-send-equivalent would violate the phase.
- LOW. lvconvert --merge rollback is not proven against a live origin. Why not blocking: snapshot create is the acceptance gate; merge refuses a running workload.

## Phase 26

- MEDIUM. Live NFS/SMB/iSCSI is not in this Cloud job. Why not blocking: fake mount plus SkipHostCmds prove share down stays unavailable, passwords stay off argv, and incremental send stays false; a NAS appliance is the intended live job.
- LOW. Phase 11 NFS/SMB backup destinations remain catalog-only unless the locator is already a local directory. Why not blocking: compute mounts are a different object; conflating them would invent a backup copy.
- LOW. iSCSI snapshots stay false. Why not blocking: a raw LUN is not a qcow2 overlay chain; inventing snapshots would violate honesty.
- LOW. SMB credentials are a 0600 file under /var/lib/ndl/secrets/datastore, not a systemd unit. Why not blocking: that is the security requirement; Cloud does not write the file when SkipHostCmds is set.

## Phase 27

- MEDIUM. Live VLAN-aware bridging, LACP, and nft bridge-family forwarding are not proven on this Cloud VM. Why not blocking: SkipHostCmds plus fixtures prove VID 20 access argv, bond files, policy refuse of management INPUT, and that the Phase 4 watchdog still restores a failed LAN-bridge probe.
- LOW. VXLAN overlay is local prep and does not form a multi-node mesh. Why not blocking: roadmap says usable after Phase 30.
- LOW. VLAN-aware filtering is applied with typed `bridge vlan` on an access port rather than rewriting an existing isolated bridge netdev in place. Why not blocking: stacked VLAN netdev plus access PVID is the homelab path; rewriting a live bridge would risk the management NIC.

## Phase 28

- MEDIUM. Live WireGuard between two machines is not in this Cloud job. Why not blocking: SkipHostCmds plus loopback keypairs prove netdev files, 0600 private keys, handshake 0 stays NotReady, and guests keep running when the session is stale.
- LOW. Remote Execute over the tunnel is recorded as listen_addr and TCP dial exists, but workload APIs still use the local unix agent. Why not blocking: roadmap join and scheduling remain Phase 30; heartbeat plus NotReady is the Phase 28 acceptance.
- LOW. Pairing tokens are pre-join secrets, not cluster join tokens. Why not blocking: join tokens stay empty until Phase 30.

## Phase 29

- MEDIUM. The Debian installer ISO is config-only in this Cloud job and is not booted. Why not blocking: mkosi.conf pins Debian 13 and the nodal metapackage; a spare PC boot is the intended live job.
- LOW. Ubuntu LTS is not Tier 1. Why not blocking: the roadmap allows documenting qualification gaps instead of faking support. Netplan dual-write, packaging, and DKMS remain the blockers.

## Phase 30

- MEDIUM. Live two-box join over TLS and WireGuard is not in this Cloud job. Why not blocking: HTTP join, token reuse, writer lease, and inventory are proven with Memory plus an ephemeral CA.
- LOW. Worker Execute still uses the local unix agent on the control node. Why not blocking: placement and remote apply remain Phase 31.
- LOW. Cluster CA is issued and stored on disk; northbound HTTPS is still the existing appliance certificate. Join client TLS keeps system roots and adds the cluster CA so appliance certificates still verify after node.crt is written. Why not blocking: wrapping Execute in mTLS is follow-up.

## Phase 31

- MEDIUM. Worker apply still is not wired; placement records DesiredNodeID and refuses local start. Why not blocking: that is the recovery gate against a second copy on the control node.
- LOW. Maintenance queues migrate operations when dest agent is missing. Why not blocking: Phase 32 runs queued jobs when a dest runtime is present.

## Phase 32

- MEDIUM. Dest agent Execute over the WireGuard tunnel is not wired in this Cloud job. Why not blocking: fake migrate proves ownership transfer and the live-fail recovery gate; using the control unix agent as dest would start a second copy.
- LOW. Live migrate of VMs compiled with -cpu host is refused. Why not blocking: the architecture records host CPU as a single-node default; offline migrate still moves the guest.
- LOW. Physical two-box live ping is not in this Cloud job. Why not blocking: QMP migrate and incoming defer are unit-tested; the roadmap names fake migrate as the Cloud test.

## Phase 33

- MEDIUM. Dest agent pull of object artifacts is not wired. Why not blocking: restore onto a worker records the catalog and refuses to copy disks onto the control node; dest Execute remains the same honesty gate as migrate.
- LOW. ZFS send artifacts still refuse qemu-img restore. Why not blocking: Phase 23 already stores send streams; zfs recv restore is a later storage path.
- LOW. Live R2 restore after losing a physical node is not in this Cloud job. Why not blocking: source-down restore onto the control fixture plus DR export without credentials cover the documented runbook.

## Phase 34

- MEDIUM. Streaming Postgres replica is not proven. Why not blocking: DSN is stored as a secret and status stays unavailable; this phase is single-writer foundations, not multi-master.
- MEDIUM. STONITH is not implemented. Why not blocking: fence is an operator isolation record that expires the lease; the architecture names fencing as defined, not automatic kill.
- LOW. Worker Phase 12 apply is not wired. Why not blocking: rolling records unavailable rather than running apt on the control node for the wrong host.

## Phase 35

- MEDIUM. Feature package apt apply is not run on this Ubuntu Cloud host. Why not blocking: HostUpdate returns unavailable and the catalog still records opt-in; Debian 13 signed-repo install is the product path.
- LOW. Kubernetes runtime is not started. Why not blocking: Phase 38 owns kubelet; this phase is installer UX and the tiny-node confirm gate.

## Phase 36

- MEDIUM. Live registry pull of the official sample image is not in this Cloud job. Why not blocking: fakeOCI proves manifest-to-stack mapping; the sample image pin is declarative.
- LOW. Store upgrade/rollback version graph is not implemented. Why not blocking: failed install rollback is the recovery gate; upgrade is a later catalog operation.

## Phase 37

- MEDIUM. Live CVE scanner is not installed on this control node. Why not blocking: the scan report is visible and records vulnerability as unavailable rather than a fake pass.
- LOW. Production No-dal signing CA is not shipped. Why not blocking: each cluster generates Official Ed25519 keys; private material stays in secrets and is never returned.

## Phase 38

- MEDIUM. Live kubelet is not started on this Ubuntu Cloud host. Why not blocking: start is a typed HostUpdate that returns unavailable and kubelet_started stays false; Debian 13 systemd is the product path.
- LOW. No-dal does not wrap arbitrary Kubernetes pods as first-class workloads yet. Why not blocking: VMs and CTs remain the default compute and do not require Kubernetes.

## CE 1.0 closeout (HIGH)

These stay HIGH until Debian 13 Homelab and cluster hardware gates pass.
They are not claimed as CE 1.0 complete.

- HIGH. Fail-closed host engines. SkipHostCmds is still the Cloud-safe
  default for QEMU, LXC, ZFS, LVM, NFS/SMB/iSCSI, WireGuard, backup
  convert, object copy, kubelet, and apt. SkipHostCmds must fail closed
  (unavailable or unverified) and must not invent success. Live QEMU
  migrate now errors under SkipHostCmds; other engines still need the
  same rule on the appliance.
  Why not blocking for license surface: Cloud fixtures stay honest when
  they report unavailable; appliance execution is the CE 1.0 gate.
- HIGH. Migrate dest agent is nil. `Server.Migrate` stays unset until
  dest Execute is wired. Live and offline migrate return
  dest-agent-not-connected and leave the source running. Two-box
  migrate is not reached.
  Why not blocking for license surface: the API refuses instead of
  starting a second copy on the control unix agent.
- LOW. Operate is CompilePlan keyword matching plus existing HTTP APIs,
  not a general planner. Restart, Store install, and policy create
  invoke those handlers. Why not blocking: Ask cannot mutate; Host.Exec
  shaped prompts stay 422.
- HIGH. Webhook SSRF residual. Literal loopback, link-local, and
  RFC1918 IPs are denied. Hostnames that are not IPs are still
  accepted, so DNS rebinding and metadata names remain a risk.
  Why not blocking for license surface: webhook URLs stay secrets in
  list JSON; network isolation on the appliance is the remaining gate.
- HIGH. Pairing reuse residual. Pairing tokens can be marked used on
  disk. A session that already has `LastSeenAt` can reconnect without
  the token. Pairing tokens are not join tokens; join tokens are
  single-use.
  Why not blocking for license surface: pairing is pre-join; join
  consume is a different object.
- HIGH. Official Store keys are self-issued. `ensureOfficialTrust`
  generates a per-cluster Ed25519 Official key. There is no production
  No-dal signing CA in this tree. Official is not a public trust root.
  Why not blocking for license surface: tamper still fails closed on
  the stored bytes; production CA is a later signing operation.








