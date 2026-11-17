# ADR 0001: Modular monolith in Go

- Status: accepted
- Date: 2026-11-17

## Context
Two clients (Flutter mobile, React web), a small team, minimal budget, and a large domain (patients, records, pharmacy, inpatient, billing, compliance).

## Decision
One Go binary and one PostgreSQL database, organised as modules under `internal/<module>/` with strict boundaries (domain → service → store → http). Use the standard library for HTTP and logging; add dependencies deliberately.

## Consequences
- Cheap to run (one container) and simple to debug and deploy.
- Boundaries are enforced by convention and code review, so they must be kept (see `CLAUDE.md`).
- A module can be extracted into its own service later if it needs independent scaling.
- Microservices are rejected for now: operational cost outweighs benefit at this size.
