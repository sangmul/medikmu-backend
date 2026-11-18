# Deployment and cost

> **Scope:** this document is about **production** hosting, which is not decided yet. During development the API is
> exposed through ngrok: see [dev-ngrok.md](dev-ngrok.md) and [ADR 0004](adr/0004-dev-via-ngrok.md).

Goal: the cheapest setup that is safe for health data, and easy to move later. Prices below are rough monthly
estimates from memory; **check the providers' current pricing before buying.**

## Options compared

| Option | Rough cost / month | Pros | Cons |
|---|---|---|---|
| **A. One VPS + Docker Compose** (recommended) | ~US$5–15 (Rp 80–250k) | Cheapest, full control, Postgres included, predictable bill | You operate it: updates, backups, monitoring |
| B. Serverless (Cloud Run / Fly.io) + managed DB | ~US$15–40+ | Less ops, scales to zero | Managed Postgres is the expensive part; cold starts; cost grows with usage |
| C. Full cloud (AWS ECS + RDS) | ~US$50–150+ | Most scalable, compliance tooling | Too expensive for a pre-revenue startup |

## Recommendation: start with A, keep the door open

One VPS (2 vCPU / 2–4 GB RAM) runs everything in `deploy/docker-compose.prod.yml`: Caddy (automatic HTTPS), the API, Postgres, Redis.
Move to B or C only when traffic or revenue justifies it. Because the app is a Docker image configured by env vars with
S3-compatible storage, moving is a redeploy, not a rewrite.

### Where to host (data residency)
Health data is sensitive personal data under Indonesia's PDP Law, and SatuSehat integration may matter later.
Prefer an **Indonesian or Singapore** region. Candidates to price-check: Biznet Gio, IDCloudHost, Dewaweb (Indonesia);
DigitalOcean / Vultr / Hetzner Singapore-region offerings where available. Get legal advice on residency requirements before launch.

### Supporting services (all have free or very cheap tiers)
| Need | Suggestion | Cost |
|---|---|---|
| Object storage (lab files, PDFs) | Cloudflare R2 or Backblaze B2 (S3-compatible) | free tier, then cents/GB |
| DB backups | `deploy/backup.sh` → R2/B2 via rclone, daily, 30-day retention | ~free |
| DNS + CDN + WAF | Cloudflare free plan | free |
| Push notifications | Firebase Cloud Messaging | free |
| Error tracking | Sentry free tier or GlitchTip (self-host) | free |
| Uptime monitoring | UptimeRobot / Better Stack free | free |
| Email / OTP | Start with a transactional email free tier; WhatsApp/SMS OTP costs per message | per use |

## First deploy (outline)
1. Create a VPS (Ubuntu LTS), add your SSH key, disable password login, enable the firewall (allow 22, 80, 443 only), enable unattended upgrades.
2. Install Docker. Point `api.<your-domain>` DNS at the server.
3. Copy `deploy/docker-compose.prod.yml`, `deploy/Caddyfile`, `deploy/backup.sh` to `/opt/medikmu/`.
4. Create `/opt/medikmu/.env.prod` with `API_DOMAIN`, `API_IMAGE`, `POSTGRES_PASSWORD`, `JWT_SECRET` (`openssl rand -hex 32`). Never commit it.
5. `docker compose -f docker-compose.prod.yml --env-file .env.prod up -d`
6. Run migrations (e.g. `docker run --rm --network <compose_network> -v /opt/medikmu/migrations:/migrations migrate/migrate -path /migrations -database "$DATABASE_URL" up`), set up the backup cron, and **do a test restore** before real data arrives.

## CI/CD (next step)
GitHub Actions builds the image on `main`, pushes it to GHCR, then SSHes to the VPS to `docker compose pull && up -d`.
Add this once the first deploy works by hand.

## Staging
A second tiny VPS (or the same one on another port/domain) with fake data. Never test with real patient data.

## When to outgrow it
Sustained CPU/RAM pressure, a need for zero-downtime deploys, or hospital customers requiring SLAs → move Postgres to a managed
service first (biggest risk reduction), then run multiple API instances behind a load balancer.
