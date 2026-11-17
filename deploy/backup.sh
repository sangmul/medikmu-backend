#!/usr/bin/env bash
# Daily Postgres backup to S3-compatible storage (Cloudflare R2 / Backblaze B2).
# Cron (on the VPS):  0 2 * * *  /opt/medikmu/backup.sh >> /var/log/medikmu-backup.log 2>&1
# Requires: docker, rclone configured with a remote named "backup".
set -euo pipefail

STAMP="$(date +%Y%m%d-%H%M%S)"
FILE="/tmp/medikmu-${STAMP}.sql.gz"

docker compose -f /opt/medikmu/docker-compose.prod.yml exec -T db \
  pg_dump -U medikmu medikmu | gzip > "$FILE"

rclone copy "$FILE" "backup:medikmu-backups/db/"
rm -f "$FILE"

# Keep 30 days.
rclone delete --min-age 30d "backup:medikmu-backups/db/"
echo "backup ${STAMP} ok"
