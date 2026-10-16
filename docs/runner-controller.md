# Runner provisioning interfaces

This describes how software other than CodeHold's own host controller can
provision Actions runners: for example an orchestrator such as ndl-ce that
creates LXCs. CodeHold has no ndl-ce integration yet; these interfaces are
what one would use, and each works on its own.

There are three ways in, from least to most integrated:

1. **Install a remote runner** with the agent's command line (no CodeHold
   API calls needed beyond creating a registration token).
2. **Use the administration API** to create and manage runners.
3. **Act as a host controller**: own a set of local runners and keep them in
   the state CodeHold asks for.

All three use the same runner records, scheduling, credentials and job
execution as the runners added in the UI.

## Security model (applies to all three)

- Only server administrators manage runners. API calls use an administrator's
  session or personal access token.
- A registration token is one-time and short-lived (1 hour; 15 minutes for
  controller-issued ones). It is exchanged for a runner credential
  (`chr_...`) that is unique to the runner, stored only as a hash on the
  server, and rotates every 30 days by handover (see
  [actions.md](actions.md#credentials-and-access)).
- A runner credential grants no repository access, is not an account and
  cannot call any API other than the runner protocol. Repository access is a
  read-only token for one repository, issued per job and revoked when the job
  ends (or after 30 minutes).
- Secrets are passed by environment variable, file or stdin, never as
  command-line arguments.
- Host controllers have their own credentials (`chc_...`), enrolled with a
  one-time token (`chce_...`) and rotated the same way. Only a controller
  touches Docker; CodeHold never does.

## 1. The agent command line

```sh
# Non-interactive registration: the token comes from the environment.
CODEHOLD_REGISTRATION_TOKEN=chreg_... codehold-runner register \
  --url https://git.example.com --name lxc-build-1 --labels linux,go \
  --config /etc/codehold-runner/config.json --work-dir /var/lib/codehold-runner
codehold-runner run --config /etc/codehold-runner/config.json
```

- `--token-file FILE` reads the token from a file instead.
- `--discover` (instead of `--url`) finds CodeHold on the local /24 networks
  by probing `/api/v1/health`.
- Behind Cloudflare Access, add `--cloudflare-access` and set
  `CF_ACCESS_CLIENT_ID` and `CF_ACCESS_CLIENT_SECRET`.
- `codehold-runner run --registration-file FILE --url URL` waits until a
  token appears in FILE, registers with it and deletes it; it does the same
  later if a new token appears (registering again). This is how a supervisor
  provisions a runner without ever passing a secret on a command line.
- `run` exits with status **78** when CodeHold rejects the runner's
  credentials (revoked, expired or removed), so a supervisor can tell and
  register it again; other failures exit with status 1.

The binary is served by CodeHold at
`/downloads/codehold-runner-linux-{amd64,arm64}`; `/install/runner.sh`
installs it as a systemd service.

## 2. The administration API

All under `/api/v1`, with an administrator's credentials.

| Method and path | Purpose |
| --- | --- |
| `POST /admin/actions/registrations` | Create a registration token for a remote runner. Body: `name`, `scope` (`instance`, `organisation`, `repository`), `organisation` (slug), `repository` (path), `labels`, `limits` (`concurrency`, `cpus`, `memory_mb`, `isolation`, `image`), `cloudflare_access`. Returns `token` (shown once), `registration.expires_at` and ready-made `commands`. |
| `POST /admin/actions/local-runners` | Create a local runner on a host controller: same body plus `controller_id`. |
| `GET /admin/actions/runners` | List runners with `status`, `kind`, `online`, `busy`, `credential` (issued, rotates, expires, pending), host facts and, for local runners, `desired` and `local` (the controller's report). |
| `PATCH /admin/actions/runners/{id}` | Change `name`, `labels`, `limits`, `draining`, `disabled`. |
| `POST /admin/actions/runners/{id}/start`, `/stop` | Start or stop a local runner. |
| `POST /admin/actions/runners/{id}/rotate` | Rotate its credential at the next contact. |
| `POST /admin/actions/runners/{id}/revoke` | Cut it off at once. |
| `POST /admin/actions/runners/{id}/reregister` | A new one-time token bound to the existing runner (remote), or registration through its controller (local). |
| `DELETE /admin/actions/runners/{id}` | Remove (a local runner is torn down by its controller first; `?force=1` forgets it at once). |
| `GET /admin/actions/runners/{id}/logs`, `/jobs` | Recent agent and controller log lines; job history. |
| `GET /admin/actions/jobs` | Queued and running jobs. `POST /actions/jobs/{id}/cancel` cancels one. |
| `GET /admin/actions/controllers` | Host controllers with status, capacity and reservations. |
| `POST /admin/actions/controllers/enrollments` | A one-time enrollment token for a new controller (`name`) or to reconnect one (`controller_id`). |
| `POST /admin/actions/controllers/{id}/rotate`, `/revoke`; `PATCH`, `DELETE /admin/actions/controllers/{id}`; `GET .../logs` | Manage a controller. |

## 3. The host controller protocol

A host controller owns local runners. CodeHold keeps their desired state; the
controller makes its hosts match and reports what it sees. CodeHold's own
controller (`codehold-runner controller`) does this with Docker Compose; an
orchestrator could do it with LXCs or VMs instead, as long as each runner ends
up as a `codehold-runner` agent registered with the token CodeHold hands out.

### Enroll

```
POST /api/v1/actions/controller/enroll
{"token": "chce_...", "name": "...", "hostname": "...", "version": "...", "os": "linux", "arch": "amd64"}
-> 201 {"controller_id": "...", "controller_token": "chc_...", "name": "..."}
```

Keep `controller_token` secret (mode 0600). With a token created for an
existing controller (`controller_id`), the same controller gets a new
credential and keeps its runners.

### Sync

The controller calls this in a loop with `Authorization: Bearer chc_...`:

```
POST /api/v1/actions/controller/sync
{
  "wait": true,
  "version": "...", "hostname": "...", "os": "linux", "arch": "amd64",
  "engine": "Docker 28.5.2, Compose 2.40.3",
  "cpus": 16, "memory_mb": 65536,
  "image": "...", "network": "...", "runner_url": "http://codehold",
  "error": "",
  "logs": ["controller log lines since the last sync"],
  "runners": [
    {"id": "...", "state": "running", "error": "", "project": "...", "auth_failed": false, "logs": ["..."]}
  ]
}
-> 200
{
  "runners": [
    {"id": "...", "name": "build-1", "state": "running", "busy": 0, "spec": "4f0c...",
     "limits": {"concurrency": 2, "cpus": 2, "memory_mb": 4096, "isolation": "container", "image": "debian:13"},
     "registration_token": "chreg_..."}
  ],
  "credential": {"token": "chc_...", "expires_at": "..."}
}
```

- With `wait: true` CodeHold holds the request (up to 25 seconds) until the
  desired state changes. Send `wait: false` on the first call and after
  changing anything, so the report reaches CodeHold at once.
- `cpus` and `memory_mb` are the host's capacity. CodeHold reserves each
  runner's per-job limits times its jobs at a time and refuses runners that
  do not fit.
- Report each runner with `state` `provisioning`, `running`, `stopped`,
  `error` (with `error`) or `removed`. Set `auth_failed` when its agent exited
  with status 78: CodeHold then hands out a new registration token.
- In the reply, `state` is `running`, `stopped` or `removed`. Removal is
  confirmed by reporting `removed`; only then does CodeHold forget the runner.
  Tear down anything it owns that is not in the reply (runners removed while
  the controller was away).
- `spec` changes when the runner's containers must be recreated (limits or
  name). Recreating kills running jobs; wait until `busy` is 0.
- `registration_token`, when present, is a one-time token (valid 15 minutes,
  the same one on every sync until used) to give to the runner's agent, for
  example through `run --registration-file`. Never log it.
- `credential`, when present, is the controller's replacement credential:
  save it, keep the old one as a fallback, and use the new one from the next
  request on. The first request with it confirms it and retires the old one.
  If the new one is ever rejected, fall back to the previous one.
- `401` means the controller's credential is not valid (`code`
  `credential_revoked`, `credential_expired` or `runner_unknown`): it needs
  a new enrollment token.

### The Docker Compose controller as a reference

`internal/runner/controller.go` is the reference implementation: one
Compose project per runner (`codehold-runner-<id>`), labelled
`codehold.controller`, `codehold.runner.id` and `codehold.spec`; a private
`docker:28-dind` engine per runner reached through a socket on a shared
volume; the registration token copied into the agent's configuration volume
as a tar stream on stdin and the agent restarted.
