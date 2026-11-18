# Client guide (mobile and web)

How to talk to the Medikmu API. The contract is `api/openapi.yaml` (interactive: `/docs` outside production).
Everything below describes behavior that is implemented and tested.

## Base URL and headers
- Base: `https://<name>.ngrok-free.app/api/v1` during development ([dev-ngrok.md](dev-ngrok.md)).
- JSON in, JSON out: `Content-Type: application/json`. Unknown request fields are rejected with 400 (no silent ignoring).
- Authenticated calls: `Authorization: Bearer <access_token>`.
- **Web only:** send `ngrok-skip-browser-warning: 1` on every request, or ngrok's free tier answers with an HTML page.
  The origin of the web app must be listed in the API's `CORS_ALLOWED_ORIGINS`. Mobile apps need neither.
- Development data only: the dev URL is public. Never type real patient data into it.

## Authentication
Tokens: **access** (JWT, 15 minutes) and **refresh** (opaque, 30 days). Both come from `register`, `login`, `refresh`
and `switch-facility`.

| Call | Notes |
|---|---|
| `POST /auth/register` | Creates a patient account and signs in. 409 `email_taken` / `phone_taken`. |
| `POST /auth/login` | Wrong password and unknown email give the same 401 `invalid_credentials`. |
| `POST /auth/refresh` | Returns a **new pair**. The old refresh token stops working immediately. |
| `POST /auth/logout` | Revokes the whole session. Idempotent. |
| `POST /auth/switch-facility` | Moves the session into a facility you belong to (see below). |

### Refresh without logging users out (read this)
Refresh tokens rotate. If a refresh token that was **already used** is presented again, the server assumes it was
stolen and **revokes the entire session**. So a client that fires two refreshes at once (two requests both getting 401
at the same moment) will sign the user out.

- Keep **one** refresh in flight. Queue the other requests behind it, then retry them with the new access token.
- Always **save the new refresh token** before using the new access token. Losing it means logging in again.
- Refresh a little early (for example at 80% of `expires_in`) instead of waiting for a 401.
- Store refresh tokens in secure storage (Keychain/Keystore on mobile). Not in `localStorage` if you can avoid it.

### Which facility am I in?
A user is a patient by default and can also be staff of facilities. An access token carries at most one active
facility; staff endpoints (`/patients`, `/team`, ...) need it, patient endpoints (`/family-members`, `/me/profile`) do not.

- `login`: if the user belongs to **exactly one** facility it becomes active. Otherwise there is none: call
  `GET /me` (it lists `memberships`) and then `POST /auth/switch-facility` with the chosen `facility_id`.
- `switch-facility` body: `{"refresh_token": "...", "facility_id": "<uuid>"}`; an empty `facility_id` returns to the personal
  (patient) context. It rotates the refresh token like `refresh` does. A rejected switch (403) does **not** consume it.
- A staff endpoint called without an active facility returns 403.

## Onboarding flows
**A doctor or clinic starts (web):**
1. `POST /auth/register`
2. `POST /facilities` `{name, kind: solo_doctor|clinic|hospital}`: you become its admin (max 5 per account).
3. `POST /auth/switch-facility` with the new facility `id`.
4. `POST /invites` `{email, role}`: the response contains a one-time `token`. No email is sent yet: the admin shares the token.

**Staff joins:**
1. The invitee registers (or logs in) with **the invited email address**.
2. `POST /invites/accept` `{token}`. Every failure is the same 400 `invite_invalid` (wrong email, used, revoked, expired).
3. `POST /auth/switch-facility` with the returned `facility_id`.

**Removed staff:** their existing access token keeps working until it expires (at most 15 minutes) but cannot be refreshed.

## Errors
Always this shape; branch on `code`, show `message` (Indonesian) to users:
```json
{ "error": { "code": "validation_failed", "message": "Data tidak valid.", "details": { "phone": "nomor telepon harus 9-15 digit" } } }
```
`details` (field name → reason) is present on 422.

| Status | Meaning | Typical codes |
|---|---|---|
| 400 | malformed JSON or unknown field | `invalid_request`, `invite_invalid` |
| 401 | missing/invalid/expired token or bad credentials | `unauthorized`, `invalid_token`, `invalid_credentials` |
| 403 | signed in but not allowed (wrong role, or no active facility) | `forbidden` |
| 404 | not found, **or belongs to someone else** (never reveals which) | `not_found`, `patient_not_found` |
| 409 | conflict | `email_taken`, `phone_taken`, `nik_taken`, `already_member`, `invite_pending`, `last_admin`, `facility_limit`, `family_limit` |
| 422 | validation failed (see `details`) | `validation_failed` |
| 429 | too many requests: wait `Retry-After` seconds | `rate_limited` |
| 500 | server error | `internal_error` |

On 401 with a valid session: refresh once, then retry the request once.

## Rate limits
`/auth/login`, `/auth/register` and `/auth/refresh` allow about 20 requests per minute per client IP (bursts of 10).
Honor `Retry-After`. Do not retry in a tight loop.

## Pagination
List endpoints that can grow (`GET /patients`) use cursors: `?limit=20&cursor=<opaque>` → `{ "data": [...], "next_cursor": "..." | null }`.
Pass `next_cursor` back unchanged; stop when it is `null`. `limit` is 1–100 (default 20). Small per-account lists
(`/family-members`, `/team`, `/invites`) return everything under `data`.

## Data handling
- Dates are `YYYY-MM-DD`; timestamps are RFC 3339 in UTC. Format for Asia/Jakarta on the client.
- IDs are UUIDs. Treat them as opaque strings.
- **NIK** (national ID): 16 digits; spaces and dashes are ignored. Patient lists return `nik_masked` (`3174 **** **** 8901`) and
  only the detail view returns the full number. Do not log or cache it. Responses carrying personal data are `Cache-Control: no-store`.
- Phone numbers of accounts and patients are validated (9-15 digits) and normalized to digits, keeping a leading `+`, so
  `0812-3456-7890` and `081234567890` are the same number (a duplicate gives 409 `phone_taken`). A facility's phone is stored as typed.
- Patient records are created with `"consent": true`; the server rejects the request otherwise.
- Searching patients (`?q=`) matches name, MRN prefix and phone digits.

## Not built yet (do not depend on it)
Password reset and OTP, email delivery of invites, file upload, push notifications, appointments and booking,
`Idempotency-Key`, and BPJS card activity status. See [endpoints-roadmap.md](endpoints-roadmap.md).
