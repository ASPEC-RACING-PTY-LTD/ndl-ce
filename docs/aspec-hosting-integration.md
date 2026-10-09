# ASPEC Hosting integration: the No-dal provisioning API

This document is for whoever builds the ASPEC Hosting side (the repository at
`D:/ASPEC Hosting`). It explains what No-dal Community Edition (NDL-CE) now
offers, and exactly what ASPEC Hosting has to add so its `Provisioner` can
create, run, suspend and destroy customer game servers and Linux containers
on an NDL-CE host.

Nothing in the ASPEC Hosting repository has been changed. Everything under
"What to build in ASPEC Hosting" is still to do there.

## How the two systems fit

ASPEC Hosting owns customers, services, plans, invoices and the decision to
deploy. NDL-CE owns the machine: it runs the game server containers and LXC
system containers and stores their data. They meet at one boundary that
already exists in ASPEC Hosting:

```ts
// src/server/provisioning.ts (ASPEC Hosting)
export type Provisioner = {
  key: string;
  deploy(input: { serviceId: string; nodeId: string; policy: WorkloadPolicy }): Promise<DeployResult>;
  destroy(input: { serviceId: string; nodeId: string | null }): Promise<DeployResult>;
  suspend(input: { serviceId: string; nodeId: string }): Promise<DeployResult>;
  resume(input: { serviceId: string; nodeId: string }): Promise<DeployResult>;
};
```

Today `getProvisioner()` only returns `UnconfiguredProvisioner`. The work in
ASPEC Hosting is to add an `ndl-ce` adapter that implements this type by
calling the NDL-CE API described below.

```
ASPEC worker job (service.provision, service.game_change, service.deprovision, ...)
  -> NdlProvisioner (new, in ASPEC Hosting)
      -> HTTPS + Bearer token
          -> NDL-CE  /api/v1/provisioning/services/{external_id}
              -> NDL game servers (Docker) or LXC system containers on that host
```

One ASPEC `nodes` row corresponds to one NDL-CE host.

## The NDL-CE API

Base URL: the host's NDL-CE address, for example `https://ndl.example.com`.
All paths below are under `/api/v1`. Requests and responses are JSON.

### Authentication

Every call sends `Authorization: Bearer ndl_...`.

Set it up once per NDL-CE host:

1. In NDL-CE, IAM, create a local user for the integration (for example
   `aspec-hosting`) with the **Operator** role. Operator can create, start,
   stop and delete workloads and game servers; it cannot change IAM roles,
   certificates or platform updates.
2. Sign in as that user, open API Access and create a token. Copy the
   `ndl_...` value; it is shown once.
3. If the NDL-CE host sits behind Cloudflare Access, also create an Access
   service token and send `CF-Access-Client-Id` and `CF-Access-Client-Secret`
   with every request.
4. On the NDL-CE host, enable Game Servers in Add Features and make sure
   Docker is installed (Add Features, Docker). `GET /provisioning/capacity`
   reports `game_runtime.ready: false` with the reason until it is.

Store the URL and token encrypted in ASPEC Hosting (it already encrypts
secrets with `APP_ENCRYPTION_KEY`). Never put them in `.env` and never send
them to the browser.

### The external id

Every service is addressed by ASPEC Hosting's own id, the `external_id` in the
path. Use the ASPEC `services.id` (a UUID). Allowed characters: letters,
digits, `.`, `_`, `:`, `-`, 1 to 128 long.

All calls are idempotent per external id, so a retried job never creates a
second server:

- `PUT` for an id that already exists returns the existing service (200).
- `DELETE` for an id that does not exist succeeds with `"existed": false`.

### Service object

Every service endpoint returns this shape:

```json
{
  "external_id": "6f1c...",
  "kind": "game",
  "resource_id": "b2d4...",
  "template": "ndl-minecraft-paper",
  "state": "active",
  "detail": "running",
  "desired_power": "running",
  "suspended": false,
  "ports": [{ "name": "game", "container_port": 25565, "host_port": 25565, "protocol": "tcp" }],
  "ipv4": "",
  "error": "",
  "error_detail": "",
  "labels": { "organisation": "..." },
  "created_at": "2026-10-10T09:00:00Z",
  "updated_at": "2026-10-10T09:00:00Z"
}
```

`state` is what ASPEC Hosting acts on:

| state          | meaning                                                                 |
| -------------- | ----------------------------------------------------------------------- |
| `provisioning` | Being created, downloading or installing, or starting after install.     |
| `active`       | Running.                                                                 |
| `stopped`      | Installed and not running, and not suspended (for example `start:false`).|
| `suspended`    | Stopped through `/suspend`.                                              |
| `failed`       | Did not install or start. `error` is a plain sentence for staff or customers; `error_detail` is the raw output for support. |

`ports` (game servers) lists the published ports; customers connect to
`<node hostname>:<host_port>`. `ipv4` (containers) is the container address.

### Endpoints

| Method and path | Does |
| --- | --- |
| `GET /provisioning/capacity` | Host CPU, memory, storage pools, what is allocated, and whether game servers can run. |
| `GET /game-servers/catalogue` | The game templates this host can install (`items[].id` is the `template_id`). |
| `PUT /provisioning/services/{external_id}` | Create a game server or container. Idempotent. |
| `GET /provisioning/services/{external_id}` | Current state. Poll this. |
| `GET /provisioning/services` | Every provisioned service. |
| `POST /provisioning/services/{external_id}/suspend` | Stop it and mark it suspended. |
| `POST /provisioning/services/{external_id}/resume` | Start it again. |
| `POST /provisioning/services/{external_id}/replace` | Change a game server to another template. Needs `X-Nodal-Confirm: replace-service`. Deletes the current server and its files. |
| `DELETE /provisioning/services/{external_id}` | Destroy it and its data. Needs `X-Nodal-Confirm: destroy-service`. |

#### Create a game server

```http
PUT /api/v1/provisioning/services/6f1c...
Authorization: Bearer ndl_...
Content-Type: application/json

{
  "kind": "game",
  "name": "GS-1001",
  "template_id": "ndl-minecraft-paper",
  "cpus": 2,
  "memory_mb": 4096,
  "disk_gb": 20,
  "env": { "EULA": "true" },
  "labels": { "organisation": "org-uuid", "public_code": "GS-1001" }
}
```

Responses:

- `201` with the service object, `state: "provisioning"`.
- `200` if this external id already exists with the same kind and template.
- `409` if it exists with a different template (use `/replace`), with the
  current service in `service`.
- `422` if the request cannot run, with `error` (plain) and, where useful,
  `errors` and `detail`. Examples: Docker is not installed, a required key
  (EULA, GSLT, license key) is missing, the template does not exist.

The install runs in the background and can take minutes (game downloads).
Poll `GET` until `state` is `active` or `failed`. When the install finishes and
the service should be running, the `GET` call starts it, so keep polling
through `stopped` to `active`.

Template-specific settings go in `env`; the catalogue item lists what each
template requires (`requirements`, `variables`).

#### Create a Linux container (LXC)

```json
{
  "kind": "container",
  "name": "GS-1002",
  "image_pin": "debian/13/amd64/default",
  "cpus": 2,
  "memory_mb": 2048,
  "disk_gb": 16,
  "pool_id": "",
  "network_id": "",
  "start": true
}
```

`pool_id` and `network_id` are optional; NDL-CE picks the default pool and
network when empty. The container is unprivileged and starts when created
unless `start` is `false`.

#### Errors

Errors are `{"error": "plain sentence"}` with a fitting status. A refused
create leaves nothing behind and records nothing, so the call can be retried
after fixing the cause.

## What to build in ASPEC Hosting

1. **A provisioner key.** Add `"ndl-ce"` to `PROVISIONERS` in
   `src/server/settings.ts`, so `/admin/settings` can select it.

2. **Per-node connection details.** One NDL-CE host per ASPEC node. Add a
   migration (new file in `drizzle/`, never edit an applied one) with, on
   `nodes`: `api_url text` and `api_token_encrypted text` (and optionally
   `access_client_id`, `access_client_secret_encrypted` for Cloudflare
   Access). Edit them in `/admin` with the super administrator permission and
   encrypt with `APP_ENCRYPTION_KEY`.

3. **Template mapping.** Each `deployment_templates` row needs the NDL
   `template_id` it installs. Either add a column `ndl_template_id text`, or
   use the existing `image_ref` with a convention such as `ndl:<template_id>`
   (then `policy.approvedImage` carries it into `deploy`). Fill it from
   `GET /game-servers/catalogue` on the target host. Required template
   settings (EULA, tokens) map from `environment_variables` /
   `startup_defaults` into `env`.

4. **`NdlProvisioner`** implementing `Provisioner` in
   `src/server/provisioning.ts` (or a new file it imports), returned by
   `getProvisioner()` when the setting is `ndl-ce`:

   - `deploy({ serviceId, nodeId, policy })`
     1. Load the node's URL and token, the service's variant (memory, storage,
        CPU) and the installation's template. The current `deploy` input only
        carries `policy`; either widen the input (recommended:
        `{ serviceId, nodeId, policy, templateKey, memoryMb, storageGb, name, env }`)
        or read them from the database inside the adapter.
     2. `PUT /provisioning/services/{serviceId}` with `kind: "game"`,
        `template_id`, `memory_mb = policy.maxMemoryMb`,
        `cpus = ceil(policy.maxCpuMillicores / 1000)`, `disk_gb` from the
        variant, `name` = the service's public code.
     3. If it returns `409` because the service runs another template (a game
        change), call `POST .../replace` with `X-Nodal-Confirm: replace-service`
        and the new `template_id`.
     4. Poll `GET` every 5 seconds until `state` is `active` (return
        `{ ok: true, providerResourceId: resource_id }`) or `failed` (return
        `{ ok: false, error }`). Give up after 45 minutes with
        `{ ok: false, error: "The install did not finish in time." }`.
   - `suspend` / `resume`: `POST .../suspend` and `.../resume`; `ok` when the
     response is 200.
   - `destroy`: `DELETE` with `X-Nodal-Confirm: destroy-service`; `ok` on 200,
     including `existed: false`.
   - Network errors and 5xx: return `{ ok: false, error }` so the job can retry;
     every call is safe to repeat.

   This keeps ASPEC's existing rules intact: a service only becomes `active`
   after `deploy` returns `ok: true`, and a failed game change leaves the
   previous installation current.

5. **Capacity and health.** In the `node.health_scan` job, call
   `GET /provisioning/capacity` for each node:
   - request fails: `health = 'unreachable'`;
   - `game_runtime.ready` false: `health = 'degraded'` (show `reason`);
   - otherwise `healthy`, and update `node_capacity` (`memory_capacity_mb` from
     `memory_bytes / 2^20`, `storage_capacity_gb` from the pools'
     `usable_bytes`, `cpu_capacity_millicores` from `cpu_threads * 1000`).
   Set `provisioning_capable = true` on a node only after this succeeds once,
   so `regionsWithCapacity` opens ordering only when a host can really deploy.

6. **Showing it to customers.** In `/portal`, show the service's address as
   `<node hostname>:<host_port>` from `ports`, and on failure the plain
   `error`. Keep `error_detail` for staff in `/admin`.

7. **Game changes and data.** `/replace` deletes the old server and its files.
   ASPEC Hosting's `archive` data action still only records a dataset (as the
   project brief says); NDL-CE does not capture files across a replace yet. If
   archiving is needed later, NDL-CE game backups
   (`POST /game-servers/{resource_id}/backups`) can be taken before the
   replace, but they are deleted with the server today.

8. **Tests.** Mirror the existing ones: a failed provision never becomes
   active, a retried deploy does not create a second server (the API
   guarantees it), and destroy is idempotent.

## Limits to know

- The token is an Operator token: it can manage every workload on that NDL-CE
  host. Use dedicated hosts for customer workloads, keep the token server-side
  and rotate it from API Access.
- Suspend stops the workload; it does not block someone with NDL-CE access
  from starting it by hand.
- One NDL-CE host per ASPEC node. NDL-CE does not place across hosts for ASPEC.
- Customer workloads are unprivileged containers. NDL-CE refuses privileged
  containers for Operator tokens, which matches `assertSafeWorkloadPolicy`.
