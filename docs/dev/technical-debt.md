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

- MEDIUM. Agent CapabilityBoundingSet now includes CAP_SYS_ADMIN for typed lxc-attach setns only.
  Why not blocking: there is still no generic host execution RPC; DevicePolicy remains closed.
- LOW. Browser tickets use the `ndl.ticket.` WebSocket subprotocol because browsers cannot set `X-Nodal-Ticket`. Query-string tickets are rejected.
- LOW. Folder archive download was left for Phase 17.
- LOW. VM Terminal and Files remain Phase 20 and return 422.
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



