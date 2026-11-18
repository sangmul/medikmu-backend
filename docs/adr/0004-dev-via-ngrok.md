# ADR 0004: Use ngrok to expose the API during development

- Status: accepted
- Date: 2026-11-18
- Relates to: [0003](0003-deploy-single-vps.md) (production hosting, still open)

## Context
The mobile and web apps need a public HTTPS URL for the API now, but there is no budget decision yet for a server.
A dedicated device will run the tunnel during the development period.

## Decision
During development, run the API in Docker on a developer machine or a dedicated device and expose it with an ngrok
static domain. Provide `make tunnel` (compose profile `tunnel`) and an agent config (`deploy/ngrok/ngrok.yml`) for the
dedicated device. Add what ngrok requires of the API: exact-origin CORS (allowing `ngrok-skip-browser-warning`),
`TRUST_PROXY` for the real client IP, and rate limiting on credential endpoints.

## Consequences
- Zero hosting cost and a stable URL for client developers.
- The API is reachable by anyone with the URL: development data only, never real patient data.
- Free-tier limits and the browser interstitial apply; not suitable for a pilot with real clinics.
- Production hosting (ADR 0003) must be decided before any real patient data is stored. Moving off ngrok needs only a
  new domain and `APP_ENV=production`; no code changes.
