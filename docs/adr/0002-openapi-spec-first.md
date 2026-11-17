# ADR 0002: OpenAPI, spec-first

- Status: accepted
- Date: 2026-11-17

## Context
The API is consumed from Dart (Flutter) and TypeScript (React). The web app already isolates data access behind repository interfaces meant to be swapped for an API implementation.

## Decision
`api/openapi.yaml` is the source of truth. Change the spec first, then implement. Generate server types (`oapi-codegen`) and typed clients for Dart and TypeScript from it. Serve interactive docs (Swagger UI or Scalar) at `/docs`. CI lints the spec.

## Consequences
- Clients and server cannot silently drift; contract changes are visible in PRs.
- Slightly more upfront work than annotating handlers (`swaggo`), repaid by generated clients.
- Server code generation is introduced when the first real module lands; until then the spec is hand-maintained and linted.
