# Contributing to No-dal Community Edition

This repository is the open-source infrastructure platform. It is not
the marketing site.

## Phase order

Implement one numbered phase from `ndl-ce_complete_roadmap.plan.md` at
a time. Phase 0 is foundation only.

## Commands

From the repository root:

```text
go test ./...
go vet ./...
gofmt -w .
node scripts/ci/check-em-dashes.mjs
node scripts/ci/check-bootstrap-size.mjs
node scripts/ci/check-package-structure.mjs
node scripts/ci/check-ci-policy.mjs
node scripts/ci/check-codegen.mjs
node scripts/generate-openapi.mjs
buf generate
buf lint
```

From `ui/`:

```text
pnpm install
pnpm lint
pnpm typecheck
pnpm test
```

## Rules

- No Unicode em dash characters.
- No generic host command RPC.
- The control plane is unprivileged. The agent is the typed privileged
  process.
- Do not add Ubuntu as a supported host until Phase 29.
- Do not put PostgreSQL or service-user logic in the bootstrap script.
- UI and CLI consume the supported API. Do not hide important actions
  in the frontend only.
- Do not add fake infrastructure data.
