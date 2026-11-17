# Security and compliance

> This is an engineering checklist, not legal advice. Have a lawyer confirm obligations before handling real patient data.

## Context
Medikmu stores medical records, which are **specific personal data** under Indonesia's Personal Data Protection Law (UU PDP, UU 27/2022).
Electronic medical records (Rekam Medis Elektronik) are also regulated by the Ministry of Health (Permenkes 24/2022), and facilities
may need to integrate with **SatuSehat**. The web app already models these areas under `features/compliance`.

## Engineering baseline
- HTTPS everywhere (Caddy, automatic certificates). HSTS on.
- Passwords hashed with argon2id/bcrypt; rate-limit login and OTP; lockout with backoff.
- Short-lived access tokens, rotating hashed refresh tokens, revoke on password change.
- Authorization (role + facility) enforced in the service layer. Tenant isolation tested.
- Audit log for every read/write of patient data (`audit_logs`); retained and tamper-resistant (append-only).
- Encrypt at rest: disk encryption on the VPS, encrypted backups; files stored with private ACLs and signed URLs.
- No PII, tokens or medical content in logs or error reports.
- Secrets only in env/secret store; rotate on staff changes.
- Backups tested by restoring them, regularly.
- Dependency and image scanning in CI (`govulncheck`, Dependabot).

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
