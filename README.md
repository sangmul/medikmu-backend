# Medikmu Backend

Go API for the Medikmu platform: the mobile app (patients) and the web app (doctors, clinics, hospitals).

## Quick start

Requires Docker and Go 1.26+.

```bash
make up          # API on http://localhost:8080 with hot reload
curl localhost:8080/healthz
make test
```

| Service | URL |
|---|---|
| API | http://localhost:8080 |
| Postgres | localhost:5432 (`medikmu` / `medikmu`) |
| Redis | localhost:6379 |
| API docs (Swagger UI) | http://localhost:8080/docs |

Migrations run automatically on `make up`. To run them by hand against the compose database, use `make migrate-up`
(install [golang-migrate](https://github.com/golang-migrate/migrate): `brew install golang-migrate`).

## Layout

```
cmd/api/              entrypoint
internal/platform/    config, http plumbing (shared, domain-free)
internal/<module>/    feature modules (domain, service, store, http)
api/openapi.yaml      API contract, source of truth
migrations/           SQL migrations
deploy/               production compose, Caddyfile, backup script
docs/                 architecture, deployment, conventions, ADRs
```

## Documentation

- [Architecture](docs/architecture.md)
- [API conventions](docs/api-conventions.md)
- [Endpoint roadmap](docs/endpoints-roadmap.md)
- [Development with ngrok](docs/dev-ngrok.md)
- [Deployment and cost](docs/deployment.md) (production, later)
- [Security and compliance](docs/security-compliance.md)
- [Decisions (ADR)](docs/adr/)
- Working with Claude Code: [CLAUDE.md](CLAUDE.md)
