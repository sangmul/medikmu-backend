# ADR 0003: Start on a single VPS with Docker Compose

- Status: proposed (target provider not yet chosen)
- Date: 2026-11-17

## Context
Startup with a minimal budget. Health data requires care with residency and backups.

## Decision
Run Caddy, the API, Postgres and Redis on one VPS in an Indonesian or Singapore region using `deploy/docker-compose.prod.yml`. Use free-tier R2/B2 for files and backups, Cloudflare free for DNS/WAF. Revisit when traffic, revenue or customer SLAs demand managed services.

## Consequences
- Lowest monthly cost (roughly US$5–15 plus near-free supporting services).
- We own patching, monitoring and restores; backups must be tested.
- Single point of failure until Postgres moves to a managed service.
- Portability is preserved: Docker image, env-var config, S3-compatible storage.

## Open
Pick the provider after comparing current prices and residency terms (see `docs/deployment.md`).
