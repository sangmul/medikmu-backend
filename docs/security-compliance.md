# Security and compliance

> This is an engineering checklist, not legal advice. Have a lawyer confirm obligations before handling real patient data.

## Context
Medikmu stores medical records, which are **specific personal data** under Indonesia's Personal Data Protection Law (UU PDP, UU 27/2022).
Electronic medical records (Rekam Medis Elektronik) are also regulated by the Ministry of Health (Permenkes 24/2022), and facilities
may need to integrate with **SatuSehat**. The web app already models these areas under `features/compliance`.

## Engineering baseline

### Implemented (each is covered by a test unless noted)
- Passwords hashed with argon2id; login does not reveal whether an email exists (same error, equalized timing).
- Short-lived access JWTs (HS256; algorithm, issuer and expiry enforced; `alg=none` rejected) and rotating opaque refresh
  tokens stored only as SHA-256 hashes. Reusing an already-used refresh token revokes the whole session.
- Authorization (role + facility, or account ownership) enforced in the service layer; tenant isolation tested at the service,
  HTTP and Postgres levels. Another tenant's data is a 404, never a 403.
- Audit log for patient reads and writes and for facility/team/invite changes (`audit_logs`); reads of patient data are
  fail-closed. Audit entries contain IDs only, never names, emails, search text or tokens.
- Invite tokens: 256-bit, one-time, stored hashed, 7-day expiry, bound to the invited email, atomic single use, and every
  failure looks the same.
- Rate limiting of login, register and refresh per client IP (in-memory; see limitations).
- Exact-origin CORS; request bodies capped at 1 MiB; unknown JSON fields rejected (no mass assignment); parameterized SQL only.
- Responses with personal data or tokens are `Cache-Control: no-store`; NIK is masked in lists.
- The public placeholder `JWT_SECRET` is refused outside plain local development; `make .env` generates a random one (not covered by a Go test).
- Postgres and Redis ports bind to `127.0.0.1` in the dev stack (verified by reading `docker-compose.yml`, not by a test).

### Planned (not done yet; do not assume they exist)
- **Per-account login lockout or backoff.** Only per-IP limiting exists, so credential stuffing across many IPs is not slowed down.
- **HTTPS and HSTS.** Production Caddy terminates TLS but HSTS is not configured; ngrok terminates TLS in development.
- **Revoke sessions on password change** (there is no password change or reset endpoint yet).
- **Tamper-resistant audit log** (append-only enforced by the database) and retention rules.
- **Encryption at rest** (disk encryption, encrypted backups) and **tested restores** (`deploy/backup.sh` is written but never run against a real backup target).
- **Dependency and image scanning in CI** (`govulncheck`, Dependabot): CI currently runs vet, tests, lint, OpenAPI lint and a Docker build only.
- Secret rotation process; error tracking that scrubs PII.

## Known limitations (accepted for now)
- **Removed staff keep access for up to 15 minutes.** Access tokens are stateless JWTs: removing someone from a
  facility stops them from refreshing (the membership is re-checked, covered by a test), but a token already issued
  works until it expires (`ACCESS_TOKEN_TTL`, default 15m). If that window is too long for a customer, shorten the TTL
  or add a per-request membership check (one indexed query) or a token deny-list in Redis.
- **Rate limiting is in-memory and per process** (`httpx.Limiter`). Fine for one instance; use Redis before running several.
- **Audit log is append-only by convention**, not enforced by the database. Add a trigger or restricted DB role before launch.
- **Development runs through a public ngrok URL** ([ADR 0004](adr/0004-dev-via-ngrok.md)): fake data only.
- **Admin actions are audited after the change is made**, not in the same transaction. If auditing fails the request
  returns an error although the change was applied. Reads of patient data are fail-closed (no audit, no data).

## Privacy features to plan for
- Patient consent records (`consent`), with purpose and timestamp.
- Patient right of access and export of their own data.
- Data retention and deletion policy (medical records have mandatory retention periods; deletion is not simply `DELETE`).
- Incident response: who is told, how fast (the PDP Law has breach-notification duties).

## Before launch
- [ ] Legal review of data residency, consent text and terms
- [ ] Penetration test or at least an OWASP ASVS L1 review
- [ ] Restore drill from backup
- [ ] Privacy policy and data-processing agreements with facilities
