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
- Follow-up: completed ACME issuance (Let's Encrypt / step-ca HTTP-01) remains documented debt
