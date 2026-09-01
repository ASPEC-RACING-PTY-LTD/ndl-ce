# Phase records

Implementation evidence for accepted CE phases. This is not product UI.

## Phase 7

- Commit: 52f8d0e
- Package: 0.1.6
- Result: ACCEPTED
- Coverage: PROVEN IN CLOUD (compiler, fake QMP, unit tests). PROVEN USING TCG on appliance via phase7-accept. KVM not required for the gate.

## Phase 8

- Feature commit: 7ef11c3
- Audit follow-up: this change on cursor/phase-8-virtual-machines-6e26
- Package: 0.1.7
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, buf lint, em dash, codegen drift
- Acceptance this environment can perform: compiler, API/RBAC, live qemu-img fail-closed, missing volume refuse, password seed survival, console tickets, delete preserves volumes, no raw argv
- Not physically validated here: Debian APT upgrade, cloud-image/ISO boot, CP/agent stop with a live guest, KVM, enforcing AppArmor, host reboot
- Audits: architecture gate holds (direct QEMU/QMP, systemd-owned, no libvirt, no Host.Exec). Storage/console HIGH live-disk fail-open and missing-volume create were fixed before accept. Shared `ndl-qemu` isolation remains documented debt.
- Follow-up: TAP stop cleanup, richer browser VNC, per-VM credentials (not Phase 8)

## Phase 9

- Package: 0.1.8
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen
- Coverage: generate/import, Secure cookies, cleartext WS refuse, ACME directory probe, UI certificates page, last-good keep on mismatch, confirm header
- Not physically validated here: Let's Encrypt HTTP-01 on a public name, step-ca issuance, binding :443 on Debian
- Honest ACME: directory probe only; status is pending or failed, never a fake issued certificate
## Phase 10

- Package: 0.1.9
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: overlay create/live qemu-img refuse/chain cap, API VM snapshot, Directory CT hidden, viewer deny
- Coverage: Directory qcow2 external overlay, rollback, flatten, Snapshots UI (not labeled Backup)
- Not physically validated: live QMP blockdev-snapshot-sync, qemu-ga freeze, rollback of a guest that installed a bad package

## Phase 11

- Package: 0.1.10
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: local fake target, snapshot then standalone convert, restore new UUID, replace confirm, CT refuse, NFS unavailable unless mounted, forbidden NFS /etc, viewer deny, retention prunes artifacts not live snaps, nightly tick, mkdir/stat via typed agent
- Coverage: PROVEN IN CLOUD (API/RBAC/checksum copy/fail-closed qemu-img path). FIXTURE for live QEMU copy. NOT PHYSICALLY VALIDATED: Debian APT restore-and-boot, real NFS/SMB mounts, qemu-ga consistent backup of a running guest
- Honest NFS/SMB: stored as backup targets; status is unavailable unless the locator is an existing local directory. No fake remote success.
- Follow-up: S3/R2 Phase 23, verify jobs Phase 24, NFS/SMB as compute datastores Phase 26, cross-node Phase 33

## Phase 12

- Package: 0.1.11
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: dry-run check, confirm headers, viewer deny, GET uses status not index refresh, unsupported host honesty, checkpoint does not fake pg_dump, store hook Phase 36, apply JSON has no apt/stop, Debian argv allowlist, agent SkipHostCmds
- Coverage: PROVEN IN CLOUD (API/RBAC/host-neutral contract/Debian adapter refuse on Ubuntu). FIXTURE for apt-get/pg_dump/tar. NOT PHYSICALLY VALIDATED: Debian 13 signed-repo upgrade, CP bump with a live guest, GRUB previous-kernel reboot, Homelab Migration Candidate on hardware
- Honest host: Ubuntu Cloud reports Unsupported. No fake package-manager success.
- Homelab Migration Candidate: not claimed. Phases 9-12 have not passed on Debian 13 hardware in this environment.
- Follow-up: cluster rolling updates Phase 34, Ubuntu host adapter Phase 29, Store compatibility Phase 36

## Phase 13

- Package: 0.1.12
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (TOTP enroll/challenge/verify, re-enroll of enabled MFA is 409, API tokens cannot enroll, viewer audit 403, operator MFA, token permissions cannot exceed creator, service principal password login denied, AAL 2 step-up for cluster.destroy, groups cannot bind admin, verify lockout, login fail-closed if MFA state is unreadable, recover-admin SQL deletes mfa_methods). FIXTURE for recover-admin host-key path. NOT PHYSICALLY VALIDATED: hardware TOTP device, WebAuthn, LUKS/ZFS volume unlock
- Honest MFA: TOTP works. WebAuthn is not implemented. Directory volume unlock is 422, not a fake LUKS open. Cluster destroy remains not implemented.
- Follow-up: WebAuthn, real LUKS/ZFS unlock after Phase 15, license activation UI Phase 43

## Phase 14

- Package: 0.1.13
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (gpu=all refused, HDMI audio listed in IOMMU group, two exclusive claims fail, ACS override 422, viewer 403, VFIO requires snapshot and stopped VM, Ubuntu runtime unsupported, CT create without GPU omits /dev/dri, typed driverctl argv, NVIDIA_VISIBLE_DEVICES never all). FIXTURE for driverctl/apt-get/DKMS (SkipHostCmds). NOT PHYSICALLY VALIDATED: real NVIDIA/AMD/Intel GPU, IOMMU groups on hardware, VFIO bind/unbind, DKMS, /dev/dri inside a running CT
- Honest GPU: none detected stays none detected. Runtime install is Unsupported on this Ubuntu Cloud host. Store GPU picker is Phase 36.
- Follow-up: OCI GPU consume claims in Phase 21, MIG if NVML offers it, licensed vGPU post-1.0 CE

## Phase 15

- Package: 0.1.14
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (force import 422, GUID import, root disk refuse, ZFS incremental_send true / Directory false, pulled-disk rows remain unavailable with nil capacity, zvol vs dataset volume create, viewer 403, Ubuntu runtime unsupported, ZFS snapshot mechanism, flatten 422, zvol QEMU raw path, typed zpool/zfs argv never -f). FIXTURE for zpool/zfs execution (SkipHostCmds). NOT PHYSICALLY VALIDATED: real zpool create/import, zvol VM boot, dataset CT root, zfs send stream on disk
- Honest ZFS: hosts without ZFS keep Directory as default. Missing userland is Unavailable/not installed, not a fake Available pool. zpool import -f is refused.
- Follow-up: physical zvol+dataset acceptance, zfs recv restore, native encryption

## Phase 16

- Package: 0.1.15
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (empty/stale series not zeros, typed journalctl argv, force-refuse of non-allowlisted units, webhook URL redaction, viewer cannot create alerts, viewer timeline omits audit, SMTP not_configured without host, SMART not_reported, capacity collecting without samples, hourly downsample without invented buckets). FIXTURE for journalctl execution (SkipHostCmds). NOT PHYSICALLY VALIDATED: live journald follow on Debian appliance, local SMTP delivery, real disk latency histograms, SMART from smartctl on hardware
- Honest observability: missing samples stay collecting/unavailable/stale. Webhook URLs are secrets. SMTP is optional and local.
- Follow-up: appliance journald, SMTP send, per-workload cgroup traffic identity beyond TAP iface names

## Phase 17

- Package: 0.1.16
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (PATCH /me ux_level, expert ack one-time, viewer plus Expert still 403 on create, palette hides Create VM for viewer, Guided/Advanced share VM create body). FIXTURE: none required (no agent). NOT PHYSICALLY VALIDATED: tablet/phone layout on hardware, live operator walkthrough on Debian 13 appliance
- Honest UX: Expert does not grant permissions. Missing health stays unavailable. Empty workloads stay empty.
- Follow-up: folder archive download still needs a typed Files RPC (Agent: None in this phase), physical tablet polish

## Phase 18

- Package: 0.1.17
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (clone new UUIDs and MAC, failed import deletes the volume and creates no workload, viewer 403 on import, USB must be in inventory, USB remains listed after attach, PCI list omits GPUs, secure boot without secboot firmware is conflict, no user QEMU argv). FIXTURE: qemu-img convert via Backup.CopyBackup / SkipHostCmds. NOT PHYSICALLY VALIDATED: clone boots a guest, live QMP usb-host hotplug, USB/VFIO hardware, Debian APT upgrade
- Honest USB/PCI: none detected stays none detected. Secure Boot without host firmware is conflict, not a fake OVMF.
- Follow-up: template deploy from frozen snapshot when the source VM is deleted; live QMP USB on appliance; physical VFIO of non-GPU PCI

## Phase 19

- Package: 0.1.18
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (org.nodal.guest.0 in frozen argv, fake Linux guest PTY banner, Windows subset shutdown/IP/files and no PTY, missing agent is not_installed not healthy, product VM Terminal/Files stay 422, Console still creates a ticket). FIXTURE: unix guest channel. NOT PHYSICALLY VALIDATED: virtio-serial in a real Linux/Windows guest, qemu-ga ping on hardware, Debian APT of ndl-guest
- Honest guest state: missing sockets are unavailable; no reply is not_installed; qemu-ga remains freeze/shutdown
- Follow-up: live virtio-serial on Debian 13; Windows PTY if a later subset lands in this phase on hardware

## Phase 20

- Package: 0.1.19
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (VM Terminal/Files when nodal_ga is ok, 422 when not_installed, permission split, disconnect disables with a reason, console still works, audit vm:/, jail guest:/). FIXTURE: unix guest channel / fake IO. NOT PHYSICALLY VALIDATED: Terminal Here / Upload Here inside a booted Linux guest on virtio-serial
- Honest IO: tabs stay disabled until nodal_ga is ok; agent down is unavailable, not healthy
- Follow-up: live virtio-serial Terminal/Files on Debian 13; Windows PTY remains unimplemented

## Phase 21

- Package: 0.1.20
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (private registry pull with stored creds via FakeRuntime, health collecting/not_configured visible, reject host bind /, privileged admin-only, GPU render/compute/encode for kind oci and VFIO still VM-only, nodal-oci@ independent of CP). FIXTURE: FakeRuntime + SkipHostCmds. NOT PHYSICALLY VALIDATED: containerd pull/run on Debian 13, live health HTTP probe
- Runtime choice: containerd via allowlisted `/usr/bin/ctr`. Cloud has no containerd; SkipHostCmds and FakeRuntime stay honest unavailable.
- Follow-up: live containerd on appliance; Compose/stacks remain Phase 22

## Phase 22

- Package: 0.1.21
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (compose fixture import to stacks/stack_members, privileged reject for operator, host bind / reject, anonymous volume reject, apply creates kind=oci workloads, partial apply resumes idempotently, member health stays collecting/unavailable never fake healthy, PATCH member image/env before apply, compute.create-only token cannot auto-create Directory volumes). FIXTURE: FakeOCI + fakeStorage. NOT PHYSICALLY VALIDATED: multi-container apply against live containerd on Debian 13
- Follow-up: shared stack networks beyond existing network_id attach; live containerd stack apply on appliance; S3 backups remain Phase 23
- Audit follow-up: storage.volume.create is required when import creates named volumes; stack members are PATCH-editable No-dal objects before apply

## Phase 23

- Package: 0.1.22
- Result: ACCEPTED WITH NON-BLOCKING FOLLOW-UP
- Tests: `go test ./...`, `go vet ./...`, UI lint/typecheck/vitest, em dash, codegen, buf lint
- Coverage: PROVEN IN CLOUD (R2/S3/MinIO fixture encrypt-before-upload, ciphertext is NDLE not plaintext, secrets never returned, no_check_bucket does not invent available, HTTPS required except MinIO fixtures, restore mints a new UUID, ZFS send -i second run transfers less). FIXTURE: FakeObject + MemoryTransport + fakeZFS. NOT PHYSICALLY VALIDATED: live MinIO/R2 multipart against a real bucket; qcow2 dirty-bitmap incrementals; live QEMU boot after object restore
- Follow-up: backup verification remains Phase 24; live MinIO virt job; multipart resume sidecar; qcow2 bitmap incrementals
- Audit follow-up: client-side encryption is required; bucket SSE is extra, not sufficient; SkipNetwork without transport stays unavailable



