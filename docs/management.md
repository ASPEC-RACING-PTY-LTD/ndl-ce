# Management

Management is the administrator area of the No-DAL portal. It is not a
personal account page. Sidebar items and `/api/v1` routes both enforce
permissions. Hiding a nav link is not the access control.

## Roles

Built-in roles stay immutable. Authz uses the in-process catalog, not
custom `roles.permissions` rows, so custom role editing is not offered.

| Role | Title | Meaning |
| --- | --- | --- |
| `viewer` | User | Read the appliance. No Management. |
| `operator` | Admin | Operate workloads, features, updates, groups, and API tokens. |
| `admin` | Owner | Super-admin. Users, roles, license, audit, and security policy. |

The last enabled Owner cannot be deleted, disabled, or demoted. Groups
still cannot bind `admin`.

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

Setup claim creates the first Owner only when `CountAdmins` is 0 and
setup is still open. Replaying setup, `EnsureRoles` on package start,
and additive schema migrations do not rewrite existing users, passwords,
roles, sessions, or MFA rows.

Password recovery remains `nodalctl recover-admin` on the host. It is
not a Management API.

## Personal security

Authenticator enrollment stays at `/settings/mfa` and in the Account
menu. Cluster MFA policy lives under Management → Security.
