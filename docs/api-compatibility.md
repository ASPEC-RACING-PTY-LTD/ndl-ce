# API compatibility (CE 1.0)

The CE 1.0 northbound contract is OpenAPI `api/openapi/nodal.v1.yaml`
version 1.0.0, generated UI types, and `nodalctl`.

Compatibility for this freeze:

- Existing `/api/v1` paths remain. Additive fields and routes may appear
  after 1.0. Removing or renaming a frozen path is a breaking change.
- RBAC stays deny-by-default. New permissions must not appear as silent
  grants on viewer.
- Secrets never appear in list JSON (license keys, Cephx, registry
  passwords, Store private keys, AI provider keys).
- The agent proto still has no `Host.Exec`. Plans and policies cannot
  add one.

This freeze does not claim wire compatibility with a future EE API.
