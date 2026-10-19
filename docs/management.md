# Management

Management is the administrator area of the No-DAL portal. It is not a
personal account page. Sidebar items and `/api/v1` routes both enforce
permissions. Hiding a nav link is not the access control.

## Roles

Role ids match the names the UI shows. Built-in roles:

| Role | Title | Meaning |
| --- | --- | --- |
| `user` | User | Read the appliance. No Management. |
| `admin` | Admin | Operate workloads, features, updates, groups, and API tokens. |
| `superadmin` | SuperAdmin | The highest role. Users, roles, license, audit, and security policy. |
| `automation` | Automation | The identity flows run as. Read-only; never assigned to a person. |

Migration 0076 renamed the old ids in place (`admin` to `superadmin`,
`operator` to `admin`, `viewer` to `user`); bindings, group roles and
permissions were kept. For one release the API still accepts the old ids
and logs each use.

The last enabled SuperAdmin cannot be deleted, disabled, or demoted.
Groups still cannot bind `superadmin`. A custom role's id follows its
name (lowercase, other characters as dashes).

### Root SuperAdmin

The account that set the appliance up is the root SuperAdmin (stored in
`iam_settings.root_user_id`). Other SuperAdmins cannot manage it. Only
root may rank roles above SuperAdmin, and root holds those roles'
permissions. Change it only from the host:

    nodalctl set-root --username USER

### Automation creators and approvals

IAM > Access control > Automation creators sets who may create and edit
flows (default: Admin and above). Admins may change it until a SuperAdmin
saves it; after that only SuperAdmins can, unless "Let admins change this"
is on.

Flows run as the system without permission checks. A flow version that
holds a protected step (deletes, migrate, prune, scripts, Docker recreate
or update, storage pressure) and that a SuperAdmin did not build waits on
IAM > Approvals; the version running now keeps running. HTTP steps that
reach ndl-ce's own API are refused below SuperAdmin when saved and checked
again when they run. SuperAdmins choose which other roles get
`approvals.view` and `approvals.decide` on the Approvals page.

## APIs

| Method | Path | Permission |
| --- | --- | --- |
| GET | `/api/v1/users` | `users.read` |
| POST | `/api/v1/users` | `users.create` |
| PATCH | `/api/v1/users/{id}` | `users.update` |
| DELETE | `/api/v1/users/{id}` | `users.delete` |
| POST | `/api/v1/users/{id}/password` | `users.update` |
| POST | `/api/v1/users/{id}/sessions/revoke` | `users.sessions.revoke` |
| POST | `/api/v1/users/{id}/tokens/revoke` | `users.sessions.revoke` |
| GET | `/api/v1/roles` | `roles.manage` |
| GET/PATCH | `/api/v1/settings/security` | `settings.security.manage` |

List JSON never includes password hashes, MFA secrets, or API token
values. Token secrets remain one-time on create.

## Bootstrap

Setup claim creates the first SuperAdmin (the root account) only when `CountAdmins` is 0 and
setup is still open. Replaying setup, `EnsureRoles` on package start,
and additive schema migrations do not rewrite existing users, passwords,
roles, sessions, or MFA rows.

Password recovery remains `nodalctl recover-admin` on the host. It is
not a Management API.

## Personal security

Authenticator enrollment stays at `/settings/mfa` and in the Account
menu. Cluster MFA policy lives under Management → Security.
