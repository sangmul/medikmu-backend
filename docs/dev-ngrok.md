# Development with ngrok

During development the API runs on a developer machine (or a dedicated device) and is exposed to the mobile
and web apps through an [ngrok](https://ngrok.com) tunnel. There is no server to rent yet; see
[ADR 0004](adr/0004-dev-via-ngrok.md). Production hosting is still open ([ADR 0003](adr/0003-deploy-single-vps.md)).

```
mobile / web ──HTTPS──> https://<name>.ngrok-free.app ──ngrok──> api :8080 (Docker) ─> Postgres
```

## One-time setup
1. Create a free ngrok account and copy your **authtoken** (dashboard → Getting started → Your Authtoken).
2. Reserve a **static domain** (dashboard → Domains). Free accounts get one; it keeps the URL stable, so the apps do not
   need a new base URL every day.
3. `cp .env.example .env`, then set in `.env`:
   ```
   NGROK_AUTHTOKEN=<token>
   NGROK_DOMAIN=<name>.ngrok-free.app
   TRUST_PROXY=true
   CORS_ALLOWED_ORIGINS=http://localhost:5173,https://<web-dev-origin>
   JWT_SECRET=<openssl rand -hex 32>
   ```
   `.env` is git-ignored. Never commit the authtoken.

## Run it
Everything in Docker (recommended, same machine runs API and tunnel):
```bash
make tunnel          # API + Postgres + Redis + ngrok; migrations run automatically
curl https://<name>.ngrok-free.app/healthz
open https://<name>.ngrok-free.app/docs      # interactive API docs (non-production only)
```
Inspect and replay requests at http://localhost:4040 while the tunnel is up. Stop with `make down`.

### Dedicated tunnel device
When another device will run ngrok (the API on a different machine), install the ngrok agent there, then:
```bash
ngrok config add-authtoken <token>
cp deploy/ngrok/ngrok.yml ~/medikmu-ngrok.yml     # set `domain` and `addr` (e.g. 192.168.1.20:8080)
ngrok config check --config ~/medikmu-ngrok.yml
ngrok start medikmu-api --config <default-ngrok.yml> --config ~/medikmu-ngrok.yml
```
The default config file path is printed by `ngrok config check`. Make sure the API host's firewall allows
the tunnel device to reach port 8080, and set `TRUST_PROXY=true` on the API.

## Pointing the apps at it
- **Mobile (Flutter):** base URL `https://<name>.ngrok-free.app/api/v1`. No CORS needed.
- **Web (React):** same base URL, and add the web app's origin to `CORS_ALLOWED_ORIGINS`.
  Browsers hit ngrok's free-tier interstitial page unless requests send `ngrok-skip-browser-warning: 1`
  (any value). The API allows that header in CORS preflight; set it in the web client's fetch wrapper.
  Non-browser clients (mobile, curl) are not shown the interstitial.
- Generate typed clients from `https://<name>.ngrok-free.app/openapi.yaml`.

## Things to know
- **Anyone with the URL can reach the API.** Only use fake data. Do not enter real patient data while developing on ngrok.
  Rate limiting (20/min per client IP on login, register and refresh) and JWT auth still apply.
- **Free-tier limits** (bandwidth, one static domain, interstitial) are fine for development, not for a pilot with real clinics.
- The URL changes if you do not use a static domain; the apps then need the new base URL.
- `/docs` and `/openapi.yaml` are served only when `APP_ENV` is not `production`.
- Probes: `/healthz` (process up) and `/readyz` (database reachable).

## Moving to real hosting later
Nothing here is ngrok-specific in the code: the API is a Docker image configured by env vars. To go live, follow
[deployment.md](deployment.md) (VPS + Caddy), point the apps at the new domain, set `APP_ENV=production`, and drop `make tunnel`.
