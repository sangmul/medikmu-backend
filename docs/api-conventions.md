# API conventions

The contract lives in `api/openapi.yaml`. Change it first.

## Basics
- Base path `/api/v1`. Breaking changes require `/api/v2`; additive changes do not.
- JSON, `snake_case` fields, UUID ids, RFC 3339 UTC timestamps, dates as `YYYY-MM-DD`.
- Plural nouns: `/patients`, `/patients/{id}/records`. Actions that are not CRUD use a verb sub-resource: `POST /visits/{id}/check-in`.
- Patient-scoped endpoints for the mobile app live under `/me/...`.

## Auth
- `Authorization: Bearer <access_token>`. Facility context comes from the token; switch with `POST /auth/switch-facility`.
- 401 = not authenticated, 403 = authenticated but not allowed.

## Errors
Always the same envelope; `code` is stable and machine-readable, `message` is user-facing Indonesian.

```json
{ "error": { "code": "invalid_credentials", "message": "Email atau kata sandi salah." } }
```

| Status | When |
|---|---|
| 400 | malformed request (`invalid_request`) |
| 401 / 403 | auth (`unauthorized`, `forbidden`) |
| 404 | not found, or not visible to the caller |
| 409 | conflict (`slot_taken`) |
| 422 | validation failed (field errors in `error.details`) |
| 429 | rate limited |
| 500 | `internal_error` (never leak internals) |

## Pagination
Cursor-based: `?limit=20&cursor=<opaque>` → `{ "data": [...], "next_cursor": "..." | null }`.

## Idempotency
Create endpoints that must not duplicate (bookings, payments) accept `Idempotency-Key`.

## Versioning of clients
Generate typed clients from the spec: Dart (`openapi-generator`) for mobile, TypeScript (`openapi-typescript`) for web.
