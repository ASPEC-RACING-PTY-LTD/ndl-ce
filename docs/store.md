# Store

The Store installs declarative manifests. It is not a root script
runner. Official sample `store/official/sample-web.yaml` maps to an OCI
application. Manifests with `run`, `bash`, `helper`, or `exec` keys are
refused.

## Trust

Phase 37 signatures fail closed on tamper. Unsigned Community warns.
Verified-only refuses unsigned packages. A revoked signing key stops
new installs and does not delete running workloads. CVE scanner
unavailable is shown on the scan report.

```text
nodalctl app list
nodalctl app import FILE
nodalctl app install --id ID
nodalctl app verify --id ID
```

`ai_actions` on a manifest are declarations for Phase 42 plans. They
are not executed by import.
