# Helix integration

Helix is an AI operator that runs on its own machine and manages projects:
it fixes bugs, deploys updates, watches services and responds to incidents.
This document is the contract between NDL-CE and Helix. The first part is for
whoever sets Helix up in NDL-CE. The rest is for whoever builds Helix's side,
and can be handed to them as it is.

## How it works

- Helix is not a user and holds no role. It authenticates with an `hlx_`
  credential that works only on the Helix API, `/api/v1/helix/v1/...`.
  The rest of the NDL-CE API refuses it, and so do the Helix settings.
- A SuperAdmin chooses which machines Helix may reach and what it may do on
  each one. NDL-CE checks every request against those grants. Helix cannot
  see, change or widen them.
- Changes that are destructive, and every change on a machine marked
  protected, wait for a SuperAdmin's approval. An approval is for one exact
  request, once.
- Everything Helix does or tries is in the audit log, as the service user
  `svc-helix`, with the credential's name and address.
- The kill switch refuses every Helix request at once and cancels what Helix
  has running. Admins can engage it; only a SuperAdmin can release it.

## Setting it up in NDL-CE

Open **IAM > Helix**. Only SuperAdmins see the settings (`helix.manage`).
Admins see the kill switch only (`helix.kill`).

1. **Create a credential.** Name it and choose when it expires (0 means
   never). The `hlx_...` key is shown once: give it to Helix and store it as
   a secret there.
2. **Allowed addresses.** Enter Helix's address, for example the IP of its
   container, or a range. Requests from anywhere else are refused. Empty
   allows any address.
3. **Machines.** Tick what Helix may do on each machine (see Capabilities).
   Mark critical machines **Protected**. Leave **Destructive unattended** off
   unless you want destructive operations to run without approval on that
   machine. **Run as** is the account commands run as, followed by any other
   accounts Helix may ask for: `root` or `deploy, www-data`.
4. **Allow Helix to connect.** The integration is off until you turn it on.

Pending approvals appear on the same page, with the exact request and why it
needs approval. **Approve** lets Helix run that request once within an hour.
**Deny** refuses it. Either way Helix finds out from the event feed or by
asking for the approval.

**Revoke** a credential to end it on Helix's next request. **Stop Helix
now** engages the kill switch. You can also revoke every credential at the
same time.

## For Helix: connecting

| | |
| --- | --- |
| Base URL | `https://<ndl-ce host>/api/v1/helix/v1` |
| Authentication | `Authorization: Bearer hlx_<64 hex characters>` |
| Format | JSON requests and responses, UTF-8 |
| Errors | `{"error": "plain sentence"}` with the status codes below |

If the NDL-CE host is behind Cloudflare Access, also send
`CF-Access-Client-Id` and `CF-Access-Client-Secret` from an Access service
token. Talk to NDL-CE over HTTPS, and verify its certificate.

Store the credential as a secret. Never log it, put it in a URL, commit it or
show it to a model's context. Responses carry `Cache-Control: no-store`.

### Status codes

| Code | Meaning | What Helix should do |
| --- | --- | --- |
| 200 | Done | Use the result |
| 202 | Accepted: queued (power, deploy, rollback), or `approval_required` | Follow the operation, or wait for the approval (below) |
| 400 | The request is malformed | Fix it; do not retry as is |
| 401 | The credential is missing, wrong, revoked or expired | Stop and alert a human. Do not retry in a loop |
| 403 | Not granted, the integration is off, the kill switch is engaged, or the address is not allowed | Stop that line of work. Never try another route to the same result |
| 404 | The machine, project, deployment or approval is not shared with Helix (or does not exist; the two look the same) | Re-read `/machines` |
| 409 | An approval does not fit (used, denied, expired, or for a different request), or the project is already deploying | Re-read the state |
| 422 | It cannot be done here (a VM has no commands, Docker Management is off, the machine is not running) | Report it |
| 429 | Over the rate limit or too many operations at once (`Retry-After: 60`) | Back off |
| 502 | NDL-CE could not reach the machine or the node agent | Retry later with backoff |
| 503 | The command was stopped by the kill switch | Stop |
| 504 | The command did not finish in its time limit | Make it shorter, or deploy it as a project |

### Limits

| | |
| --- | --- |
| Reads | 600 per minute per credential |
| Changes (commands, actions, deploys, power) | 60 per minute per credential |
| Changes running at once | 8 (event waits do not count) |
| Command run time | 120 seconds; default 60 |
| Command size | 16 KiB |
| Command output | 256 KiB (the rest is dropped) |
| Logs | 5000 lines |
| Events kept | 7 days |
| Event long poll | 30 seconds |

`GET /whoami` returns the current limits.

## Capabilities

Each machine shared with Helix has some of these. Every capability includes
`read`.

| Capability | Allows |
| --- | --- |
| `read` | Status, resources, metrics, network counters, Docker containers and their health, systemd services, deployment projects and deployments, and events about the machine |
| `logs` | Docker container logs, service journals and deployment logs |
| `write` | Start, stop and restart Docker containers; start, stop, restart and reload systemd services |
| `execute` | Run commands, as the grant's run-as account or one of its other accounts |
| `deploy` | Deploy the machine's deployment projects and roll them back; read their logs |
| `power` | Start, stop and restart the machine itself |

**Machine types.** `lxc` machines (system containers) support everything.
`vm` machines and app containers support `read` and `power` only: NDL-CE has
no command, Docker or service access inside them.

## Approvals

A change needs approval when:

- the machine is **protected**: every change (`write`, `execute`, `deploy`,
  `power`) needs it; reading and logs never do; or
- the change is **destructive** and the grant does not allow destructive
  operations unattended. Destructive means:
  - stopping or restarting the machine;
  - stopping a container, or stopping or disabling a service;
  - any rollback;
  - a command that looks like it deletes data, wipes disks, shuts down,
    changes accounts or the firewall, deletes Docker data or storage, drops
    databases, rewrites Git history, kills init, overwrites system files,
    stops SSH, networking or Docker, or runs hidden or downloaded code
    (`base64 -d | sh`, `eval`, `curl ... | bash`).

The command check catches mistakes. It is not a sandbox, so treat it as a
safety net, never as permission. Do not rephrase a command to avoid it:
NDL-CE logs every command, and a SuperAdmin will see it.

The flow:

1. Helix sends the change as usual. NDL-CE answers `202` with:

   ```json
   { "status": "approval_required",
     "approval": { "id": "0b6f...", "op": "exec", "machine_id": "...", "machine_name": "app",
       "summary": "run as root in /srv/app: docker system prune -af",
       "reasons": ["the command looks destructive: deleting Docker containers, images or volumes"],
       "status": "pending", "requested_at": "...", "requested_by": "helix", "expires_at": "..." } }
   ```

   Nothing has run. Sending the same request again returns the same pending
   approval; it does not create another.
2. Wait. Watch the event feed for `helix.approval_approved` or
   `helix.approval_denied` with that `approval_id`, or poll
   `GET /approvals/{id}` no more than once a minute. A pending approval
   expires after 24 hours.
3. When it is `approved`, send **exactly the same request** again with
   `"approval_id": "<id>"` in the JSON body. It runs once. An approved
   request expires one hour after the decision. A changed request, a reused
   approval or a denied one gets `409`.

Approval states: `pending`, `approved`, `denied`, `used`, `expired`.
`note` holds the SuperAdmin's comment, if any.

## Endpoints

All paths are under `/api/v1/helix/v1`. `{id}` is a machine id unless the
path says otherwise.

### GET /whoami

The calling credential, the limits and the API version. Use it as a health
check at startup.

```json
{ "api_version": 1, "credential": { "id": "...", "name": "helix", "prefix": "hlx_3f9a1c", "expires_at": "..." },
  "machines": 3, "global_events": true, "ip": "10.0.3.15",
  "limits": { "reads_per_minute": 600, "changes_per_minute": 60, "concurrent_operations": 8,
    "exec_timeout_max_sec": 120, "event_retention_hours": 168 } }
```

### GET /machines

Every machine Helix can read.

```json
{ "machines": [ {
  "id": "1595ac36-...", "name": "app", "type": "lxc", "status": "running", "reason": "",
  "desired_power": "running", "node_id": "...", "cpus": 4, "memory_bytes": 8589934592, "disk_bytes": 68719476736,
  "nics": [ { "network_id": "...", "mac": "...", "ipv4": "192.168.2.11", "ipv4_address": "", "ipv6_address": "" } ],
  "autostart": true, "pending_restart": false, "image": "debian/13/amd64/default", "created_at": "...",
  "exec_capable": true,
  "grant": { "caps": ["read", "logs", "write", "execute", "deploy"], "protected": false,
    "destructive_unattended": false, "run_as": "root", "exec_users": ["root", "deploy"] } } ] }
```

`status` is `running`, `stopped`, `starting`, `stopping` or `error`, among
others. `grant` is what Helix may do here: plan with it instead of finding
out from `403`s.

### GET /machines/{id}

One machine, in the same shape.

### GET /machines/{id}/metrics?minutes=60

CPU and memory series for the last `minutes` (1 to 1440), and the latest
values.

```json
{ "machine_id": "...", "minutes": 60, "status": "available",
  "latest": { "cpu_percent": 12.5, "memory_used_bytes": 2147483648, "memory_limit_bytes": 8589934592, "memory_percent": 25 },
  "series": [ { "name": "workload.<id>.cpu.busy_ratio", "status": "available", "points": [ { "time": "...", "value": 0.12 } ] } ] }
```

`cpu.busy_ratio` is 0 to 1 of the machine's CPU limit. `status` is
`unavailable` or `collecting` while there is no data yet.

### GET /machines/{id}/network

The configured interfaces, and for a running `lxc` the kernel counters since
it started. Sample twice to get a rate.

```json
{ "machine_id": "...", "nics": [ ... ], "sampled_at": "...",
  "interfaces": [ { "name": "eth0", "rx_bytes": 3493219031, "rx_packets": 0, "rx_errors": 0, "rx_dropped": 0,
    "tx_bytes": 4981525405, "tx_packets": 0, "tx_errors": 0, "tx_dropped": 0 } ] }
```

### GET /machines/{id}/containers

Docker containers on an `lxc` machine (Docker Management must be on in
NDL-CE).

```json
{ "machine_id": "...", "containers": [ {
  "id": "9c1e...", "name": "app-web-1", "project": "app", "service": "web", "image": "ghcr.io/x/app:latest",
  "state": "running", "status": "Up 3 hours (healthy)", "health": "healthy", "health_reason": "",
  "healthcheck": "healthy", "exit_code": 0, "oom_killed": false, "restarting": false, "restart_count": 0,
  "restart_loop": false, "started_at": "...", "uptime": "3h", "ports": [ ... ], "cpu_percent": 1.2,
  "memory_bytes": 104857600, "memory_limit": 0, "working_dir": "/srv/app", "error": "" } ] }
```

`health` is NDL-CE's judgement: `healthy`, `degraded`, `critical`, or
`unknown`. `healthcheck` is Docker's own healthcheck status, if the image
has one.

### GET /machines/{id}/containers/{cid}/logs?tail=200

Needs `logs`. `{cid}` is the container id (at least 12 characters) or its
name. Returns `{ "machine_id", "container", "logs" }`.

### POST /machines/{id}/containers/{cid}/{action}

Needs `write`. `action` is `start`, `stop` or `restart`. `stop` is
destructive. Body: `{}` or `{ "approval_id": "..." }`. Returns
`{ "ok": true, "message": "", "container": "app-web-1", "action": "restart" }`.

### GET /machines/{id}/services

systemd services on an `lxc` machine.

```json
{ "machine_id": "...", "failed": 1, "services": [
  { "unit": "nginx.service", "load": "loaded", "active": "active", "sub": "running", "description": "A high performance web server" } ] }
```

### GET /machines/{id}/services/{unit}/logs?lines=200

Needs `logs`. The journal for one unit (`nginx` or `nginx.service`).
Returns `{ "machine_id", "unit", "logs", "exit_code" }`.

### POST /machines/{id}/services/{unit}/{action}

Needs `write`. `action` is `start`, `stop`, `restart` or `reload`. `stop` is
destructive. Body: `{}` or `{ "approval_id": "..." }`. Returns
`{ "unit", "action", "exit_code", "output" }`. A non-zero `exit_code` means
systemctl refused; `output` says why.

### POST /machines/{id}/exec

Needs `execute`. Runs one command with `bash` in an `lxc` machine and waits
for it.

```json
{ "command": "cd /srv/app && git status --short && docker compose ps",
  "cwd": "/srv/app", "run_as": "deploy", "timeout_sec": 60, "approval_id": "" }
```

- `command`: anything bash accepts, up to 16 KiB. It runs non-interactively,
  with no terminal and no standard input.
- `cwd`: an absolute folder other than `/`; default `/tmp`.
- `run_as`: the grant's `run_as` (default) or one of its `exec_users`.
- `timeout_sec`: 1 to 120; default 60. For longer work, deploy a project.

```json
{ "exit_code": 0, "output": "...", "duration_ms": 1840, "run_as": "deploy", "cwd": "/srv/app" }
```

Standard output and error are combined. A command that exits non-zero still
answers `200`: check `exit_code`. On `502`, `503` or `504` the body also has
`error`, plus whatever `output` there was.

### POST /machines/{id}/power/{action}

Needs `power`. `action` is `start`, `stop` or `restart`. `stop` and
`restart` are destructive. Answers `202`; follow the machine's `status` and
the `machine.*` events.

### GET /projects

Deployment projects on machines Helix can read. A project is a folder in an
`lxc` machine plus the commands that update it. A SuperAdmin sets them up
under Docker > Deployments.

```json
{ "projects": [ { "id": "...", "name": "app", "machine_id": "...", "working_dir": "/srv/app",
  "commands": ["git pull origin main && docker compose up -d --build --force-recreate"],
  "branch": "main", "local_commit": "4f2c9a1...", "remote_commit": "8d0e7b3...", "behind": 2, "ahead": 0,
  "dirty": false, "last_deploy_id": "...", "last_deploy_status": "succeeded", "can_deploy": true } ] }
```

### POST /projects/{id}/deploy

Needs `deploy` on the project's machine. Runs the project's own commands (the
SuperAdmin's, not Helix's). Body: `{}` or `{ "approval_id": "..." }`.
Answers `202` with the deployment. `409` if the project is already deploying.

### POST /projects/{id}/rollback

Needs `deploy`, and approval unless the grant allows destructive operations
unattended.

```json
{ "deployment_id": "<a deployment of this project>" }
```

or `{ "commit": "4f2c9a1" }` (7 to 40 hex characters). With
`deployment_id`, NDL-CE rolls back to the commit that deployment started
from. The rollback is a deployment that:

1. fetches if the commit is not there,
2. runs `git reset --hard <commit>` in the project folder (this discards
   local changes),
3. runs the project's commands without their `git pull`, `git fetch`,
   `ch pull` or `ch fetch` steps.

Answers `202` with the deployment. The project's next normal deploy pulls the
branch forward again.

### GET /deployments/{id}

A deployment on a machine Helix can read:

```json
{ "id": "...", "project_id": "...", "project_name": "app", "workload_id": "...", "status": "running",
  "trigger": "helix", "actor": "helix (helix)", "queued_at": "...", "started_at": "...", "finished_at": null,
  "working_dir": "/srv/app", "run_as": "deploy", "commands": [ { "run": "..." } ],
  "commit_before": "4f2c9a1...", "commit_after": "", "exit_code": null, "failed_step": 0, "error": "",
  "steps": [ { "command": "...", "started_at": "...", "finished_at": "...", "exit_code": 0 } ],
  "step_count": 1, "current_step": 1, "log_bytes": 2048, "duration_sec": 12 }
```

`status`: `queued`, `waiting`, `starting`, `running`, `succeeded`, `failed`,
`cancelled`, `timed-out` or `interrupted`. `trigger` is `helix` for
Helix's deploys and `helix-rollback` for its rollbacks.

### GET /deployments/{id}/log?offset=0

Needs `logs` or `deploy`. Reads the log from a byte offset:
`{ "data": "...", "offset": 0, "next": 2048, "size": 2048, "done": false }`.
Call again with `offset=next` until `done` is true.

### GET /approvals/{id}

One approval, in the shape shown under Approvals.

### GET /events?after=&wait=&limit=

The event feed. See the next section.

## Events

Events are how Helix learns about failures and changes without polling
everything. Each has a `seq` that only goes up. Helix keeps the last `next`
it saw and asks for what came after it.

```http
GET /api/v1/helix/v1/events?after=1041&wait=25&limit=100
```

```json
{ "next": 1043, "events": [
  { "seq": 1042, "id": "...", "type": "docker.container_unhealthy", "severity": "warning", "at": "2026-11-22T10:15:00Z",
    "machine_id": "1595ac36-...",
    "data": { "container_id": "9c1e...", "name": "app-web-1", "project": "app", "health": "critical",
      "reason": "restarting", "state": "restarting", "restart_count": 7, "exit_code": 137 } } ] }
```

- **Start:** call without `after`. NDL-CE returns `{ "events": [], "next": N }`,
  the current position, so Helix starts from now. Store `next` durably and
  resume from it after a restart. Events are kept for 7 days.
- **Long poll:** with `wait` (up to 30 seconds), NDL-CE holds the request
  until an event arrives or the time runs out. Then call again at once with
  `after=next`. Use a client timeout a little longer than `wait`.
- **Always continue from `next`**, even when `events` is empty: it moves
  past events Helix is not allowed to see.
- **Visibility:** events about a machine (`machine_id` set) are delivered
  only for machines Helix can read. Other events are delivered only when
  **Send events that are not about one machine** is on.
- Delivery is at least once in order. Use `id` to drop duplicates.

### Event types

| Type | Severity | About | `data` |
| --- | --- | --- | --- |
| `machine.started`, `machine.restarted` | info | machine | `workload_id`, `name`, `kind` |
| `machine.stopped`, `machine.deleted` | warning | machine | the same |
| `machine.down` | critical | machine | `name`, `status`, `reason`: it should run and does not |
| `machine.up` | info | machine | after `machine.down` |
| `machine.resource_high` | warning | machine | `resource` (`cpu` or `memory`), `percent`, `threshold` |
| `machine.resource_recovered` | info | machine | the same |
| `docker.container_unhealthy` | warning | machine | `container_id`, `name`, `project`, `health`, `reason`, `state`, `restart_count`, `exit_code` |
| `docker.container_recovered` | info | machine | the same |
| `deploy.succeeded` | info | machine | `deployment_id`, `project_id`, `project`, `status`, `trigger`, `error`, `commit_before`, `commit_after` |
| `deploy.failed`, `deploy.timed-out`, `deploy.interrupted` | warning | machine | the same |
| `deploy.cancelled` | info | machine | the same |
| `backup.failed` | warning | machine | `workload_id`, `run_id`, `status`, `error` |
| `helix.approval_requested` | info | machine | `approval_id`, `summary` |
| `helix.approval_approved`, `helix.approval_denied` | info | machine | `approval_id`, `by`, `note` |
| `uptime.down` | critical | global | `monitor_id`, `name`, `target`, `state`, `status`, `error`, `failures` |
| `uptime.recovered` | info | global | the same |
| `alert.firing` | warning | global | `rule_id`, `name`, `metric`, `op`, `threshold`, `value` |
| `storage.pool_usage_high` | warning | global | `pool_id`, `pool_name`, `percent`, `band` |
| `migration.failed` | warning | global | the migration's details |
| `platform.recovered` | info | global | NDL-CE recovered its platform |
| `security.signin_refused` | warning | global | `action`, `ip`, `detail`: a refused NDL-CE sign-in or MFA check |
| `security.helix_credential_refused` | warning | global | `ip`, `reason`: someone used a wrong or revoked `hlx_` key |
| `security.helix_address_refused` | warning | global | `ip`, `credential`: a valid key from an address that is not allowed |

Machine checks (`machine.down`, resources, containers) run every minute.
Thresholds come from the settings page (90% by default). A problem is
reported once when it starts and once when it clears. A problem that is
already there when NDL-CE starts is reported once.

New event types may appear. Ignore types you do not know.

## Building Helix's side

### The client

1. Read the base URL and the credential from Helix's secret store.
2. Call `GET /whoami` at startup. On `401` or `403`, stop and tell a human.
   Do not retry in a loop.
3. Wrap every call: send the header, apply a client timeout (30 seconds;
   140 for `exec`; `wait + 10` for events), map status codes as in the table
   above, and honour `Retry-After` on `429`.
4. Keep one event loop running: `GET /events?after=<cursor>&wait=25`, handle
   the events, save the new cursor, repeat. Back off (1 s up to 60 s) on
   network errors and `5xx`.

### Tools for the model

Give the model one tool per endpoint, not a general HTTP tool, so it cannot
reach anything else. For example:

| Tool | Endpoint |
| --- | --- |
| `list_machines` | `GET /machines` |
| `machine_status` | `GET /machines/{id}`, `/metrics`, `/network` |
| `list_containers` / `container_logs` / `container_action` | `/containers...` |
| `list_services` / `service_logs` / `service_action` | `/services...` |
| `run_command` | `POST /machines/{id}/exec` |
| `machine_power` | `POST /machines/{id}/power/{action}` |
| `list_projects` / `deploy_project` / `rollback_project` | `/projects...` |
| `deployment_status` / `deployment_log` | `/deployments...` |
| `approval_status` | `GET /approvals/{id}` |

Each tool should return NDL-CE's answer as it is, including `error` and
`approval_required`, so the model sees the real outcome.

### Rules for the agent

- **The grants are the boundary.** If NDL-CE refuses something (`403`),
  report it and stop. Do not look for another route to the same result.
- **Approvals mean wait.** On `approval_required`, tell the humans what is
  waiting and why, and continue only once it is approved, with the same
  request and the `approval_id`. Never retry a destructive change any other
  way.
- **Read before you change.** Check status, logs and recent deployments,
  and prefer the smallest fix: restart one container before a whole
  machine; deploy or roll back a project before hand-editing files.
- **Commands are short and checkable.** Keep them under 120 seconds,
  idempotent where possible, and check `exit_code`. Longer work belongs in a
  deployment project.
- **Never print secrets** from logs or files into chats, tickets or commits.
- **The kill switch is final.** On `403` saying the kill switch is engaged,
  or `503` stopped by the kill switch, stop everything and wait for a human.

### Incident response loop

1. An event arrives (for example `docker.container_unhealthy` or
   `machine.down`).
2. Gather: `GET /machines/{id}`, `/containers`, the container's `/logs`,
   `/metrics`, and the latest deployments in `/projects`.
3. Decide: was it caused by a recent deployment (compare its time and
   `commit_after`), resources, or a crash?
4. Act within the grant: restart the container, or roll back to the
   deployment's `commit_before`, or deploy a fix.
5. Verify: wait for `docker.container_recovered` or `machine.up`, or check
   again after a short wait.
6. Report what happened and what was done.

## What NDL-CE guarantees

- An `hlx_` credential opens only `/api/v1/helix/v1`. It cannot open the
  settings (grants, credentials, approvals, kill switch), any other NDL-CE
  route, a terminal, the file manager or the host.
- Machines that are not granted do not exist for Helix: they answer like
  missing machines, and their events are not delivered.
- Nothing runs on the NDL-CE host itself. Commands run inside the chosen
  system container through the node agent, as the account the grant allows.
- Every request, allowed or refused, is audited with the credential and
  address. Refused credentials and addresses are also reported as events, at
  most once a minute per address.
- Credentials are stored as hashes. Revoking one takes effect on the next
  request.
- The kill switch refuses every Helix request, cancels running commands and
  event waits, cancels queued and running deployments that Helix started,
  and expires every open approval. It stays engaged until a SuperAdmin
  releases it, and NDL-CE never releases it by itself.
- Turning the integration off has the same immediate effect, without the
  kill switch's record.

## For NDL-CE administrators: the settings API

These routes use a person's session or `ndl_` API token, never an `hlx_`
credential. All but the kill switch need `helix.manage`, which only
SuperAdmins hold.

| Method and path | Does |
| --- | --- |
| `GET /api/v1/helix` | Everything on the settings page |
| `PUT /api/v1/helix/settings` | `enabled`, `allowed_ips`, `global_events`, `thresholds` |
| `PUT /api/v1/helix/machines` | Replace `grants` |
| `POST /api/v1/helix/credentials` | `name`, `ttl_days`; returns `token` once |
| `POST /api/v1/helix/credentials/{id}/revoke` | Revoke |
| `POST /api/v1/helix/approvals/{id}/approve` or `/deny` | Decide, with an optional `note` |
| `GET /api/v1/helix/kill` | Kill switch state (`helix.kill`) |
| `POST /api/v1/helix/kill` | Engage: `reason`, `revoke_credentials` (`helix.kill`) |
| `POST /api/v1/helix/kill/release` | Release |
| `GET /api/v1/helix/activity` | The latest 200 Helix audit entries |

## Versioning

This is version 1 (`api_version` in `/whoami`). Fields and event types may be
added. Removing or changing one will use a new path, `/api/v1/helix/v2`.
