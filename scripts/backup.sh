#!/bin/sh
# ECG Hub backup — Postgres dump + ECG file volumes.
# POSIX sh (runs from the host or the optional `backup` compose service).
#
# Config via env (all optional except DATABASE_URL, usually from .env):
#   DATABASE_URL     postgres://user:pass@host:5432/db   (required)
#   BACKUP_DIR       where to write backups               (default ./backups)
#   DATA_DIR         parent of ecg/ + ecg-quarantine/     (default ./data)
#   RETENTION_DAYS   delete backups older than N days     (default 14, 0 = keep all)
#
# See docs/backup.md.
set -eu

BACKUP_DIR="${BACKUP_DIR:-./backups}"
DATA_DIR="${DATA_DIR:-./data}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
: "${DATABASE_URL:?DATABASE_URL is required (export it or put it in .env)}"

ts="$(date +%Y%m%d-%H%M%S)"
dest="${BACKUP_DIR}/${ts}"
mkdir -p "$dest"

echo "[backup] ${ts} -> ${dest}"

# 1. Database — custom format so pg_restore can do a clean, selective restore.
echo "[backup] dumping database..."
pg_dump --format=custom --no-owner --no-privileges \
  --dbname="$DATABASE_URL" --file="${dest}/db.dump"

# 2. ECG file volumes (raw stored ECGs + quarantine).
if [ -d "${DATA_DIR}/ecg" ]; then
  echo "[backup] archiving ECG files..."
  tar -czf "${dest}/ecg.tar.gz" -C "$DATA_DIR" ecg
fi
if [ -d "${DATA_DIR}/ecg-quarantine" ]; then
  echo "[backup] archiving quarantine files..."
  tar -czf "${dest}/ecg-quarantine.tar.gz" -C "$DATA_DIR" ecg-quarantine
fi

# 3. Manifest for traceability.
{
  echo "created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "db_dump=db.dump"
  [ -f "${dest}/ecg.tar.gz" ] && echo "ecg_archive=ecg.tar.gz"
  [ -f "${dest}/ecg-quarantine.tar.gz" ] && echo "quarantine_archive=ecg-quarantine.tar.gz"
} > "${dest}/manifest.txt"

echo "[backup] done: $(du -sh "$dest" | cut -f1)"

# 4. Retention — prune backups older than RETENTION_DAYS.
if [ "$RETENTION_DAYS" -gt 0 ]; then
  echo "[backup] pruning backups older than ${RETENTION_DAYS}d..."
  find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -mtime "+${RETENTION_DAYS}" \
    -exec rm -rf {} + 2>/dev/null || true
fi
