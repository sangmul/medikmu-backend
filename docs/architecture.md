# Architecture

## Shape

A **modular monolith**: one Go binary, one PostgreSQL database, modules with strict boundaries.
It is cheap to run (one container), simple to debug, and a module can be extracted later if it ever needs to scale alone.

```
mobile (Flutter) ─┐
                  ├─ HTTPS ─> Caddy ─> api (Go) ─> PostgreSQL
web (React) ──────┘                         ├────> Redis (sessions, rate limit, queues)
                                            └────> S3-compatible storage (files, PDFs)
```

## Modules

Mirror the `features/` of the web and mobile apps.

| Phase | Modules |
|---|---|
| 1 Foundation | `auth`, `facilities` (tenants, team, invites), `files`, `notifications` |
| 2 Core care | `patients`, `doctors`, `appointments` (+ booking), `visits`, `records`, `prescriptions`, `labs` |
| 3 Patient app | `family`, `kia` (growth, immunization, antenatal), `messages` (chat), `medicines` |
| 4 Business | `billing`, `documents`, `dashboard` |
| 5 Advanced | `pharmacy`, `inpatient`, `emergency`, `accounting`, `compliance` (SatuSehat) |

Each module: `domain/` → `service/` → `store/` → `http/`. Modules talk through `service` interfaces, never through each other's tables.

## Multi-tenancy

- A **facility** is a solo doctor, clinic or hospital. Staff belong to facilities through `memberships (user, facility, role)`.
- A user may be a patient and staff at the same time; roles are per facility.
- Every tenant-owned row has `facility_id`. The JWT carries the active facility; the service layer enforces it.
- Patient-facing endpoints (`/me/...`) are scoped to the patient, not a facility.

## Auth

- Email + password (argon2id); short-lived access JWT (HS256, 15 min) + rotating opaque refresh token (30 days) stored as a SHA-256 hash.
- Implemented in `internal/auth`; other modules protect routes with `authhttp.RequireAuth` / `RequireFacility(roles...)` and read the caller via `authctx.From(ctx)`.
- Login activates the facility automatically only if the user belongs to exactly one; otherwise the client calls `POST /auth/switch-facility`.
- RBAC by role per facility. Authorization lives in services.
- Refresh token reuse detection: revoke the whole token family.

## Data

- PostgreSQL 16, `sqlc` for typed queries, `golang-migrate` for schema.
- IDs are UUIDs. Timestamps are `timestamptz` in UTC; clients format for Asia/Jakarta.
- Medical data changes are audited in `audit_logs`.
- Files (lab results, sketches, PDFs) go to S3-compatible storage; the database keeps only keys and metadata. Downloads use short-lived signed URLs.

## Real-time and async

- Chat: WebSocket `/ws`, fan-out through Redis pub/sub once there is more than one instance.
- Push notifications: FCM, sent from a small in-process worker queue first; extract only when needed.

## Observability

- Structured JSON logs (`slog`) with request IDs; `/healthz` for liveness.
- Add `/readyz` (database ping), Prometheus metrics, and error tracking (Sentry) in phase 1 hardening.
