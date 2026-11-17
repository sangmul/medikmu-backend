# Medikmu Backend — rules for Claude

Go API serving two clients: **medikmu_mobile** (Flutter, patients) and **medikmu-web** (React, doctors/clinics/hospitals).
Health data: treat every change as security-sensitive. Read `docs/architecture.md` before adding a module.

## Commands
- `make up` — API + Postgres + Redis + MinIO with hot reload (needs Docker)
- `make test` — `go test -race ./...` (run before every commit)
- `make lint` — golangci-lint; `make fmt` — gofmt
- `make migrate-up` / `make migrate-new name=<x>` — migrations (golang-migrate)

## Architecture rules
- **Modular monolith.** One binary, modules under `internal/<module>/` (`auth`, `patients`, `appointments`, ...).
  Do not split into services.
- Module layout: `domain/` (types, interfaces, no HTTP/SQL) → `service/` (use cases) → `store/` (SQL) → `http/` (handlers).
  Dependencies point inward. A module never imports another module's `store` or `http`; call its `service` interface.
- Shared plumbing lives in `internal/platform/` (config, httpx, db, logging). Keep it small and domain-free.
- Use the stdlib (`net/http` ServeMux patterns, `log/slog`) until a dependency clearly earns its place. Ask before adding one.
- Configuration comes only from env vars via `internal/platform/config`. Add new vars to `.env.example` too.

## API rules (spec-first)
- `api/openapi.yaml` is the source of truth. **Change the spec first**, then the code, in the same PR.
- Follow `docs/api-conventions.md` (paths, errors, pagination, auth).
- Errors use the envelope `{"error":{"code","message"}}` via `httpx.Error`. Messages for end users are in Indonesian; `code` is a stable English snake_case.

## Data & security rules (non-negotiable)
- Every tenant-owned table has `facility_id`; every query on it filters by the caller's facility. No exceptions.
- Authorization is checked in the service layer (role + facility), never only in the handler.
- Reading or changing patient medical data writes an `audit_logs` row.
- Never log PII, tokens, passwords or medical content. Log IDs only.
- Passwords: argon2id or bcrypt. Secrets only from env; never commit `.env`.
- SQL: parameterized queries only. Migrations are forward-only in production; add a new migration, never edit an applied one.
- Indonesian context: PDP Law and health-record rules apply. See `docs/security-compliance.md`.

## Code style
- Idiomatic Go, `gofmt`/`goimports`, wrap errors with `%w`, pass `context.Context` first, no globals.
- Table-driven tests. Unit-test services with fakes; test stores against a real Postgres (docker), not mocks.
- Small PRs. Conventional commit prefixes: `feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`.

## Git
- Branch from `main`; never push to `main` directly; open a PR.
- Commit identity for this repo is the personal account (see global git config). Do not change git config.

## Documentation
- Update `docs/` when behavior or architecture changes. Record significant decisions as an ADR in `docs/adr/`.
- Endpoint plan and phasing: `docs/endpoints-roadmap.md`. Deploy: `docs/deployment.md`.
