#!/bin/sh
# ECG Hub restore — restores a backup created by backup.sh.
# DESTRUCTIVE: overwrites the database and ECG files. Type "yes" to proceed.
#
# Usage: scripts/restore.sh <backup-dir>   e.g. scripts/restore.sh ./backups/20260618-031500
#
# Config via env:
#   DATABASE_URL   target database (required)
#   DATA_DIR       parent of ecg/ + ecg-quarantine/ (default ./data)
set -eu

BACKUP_PATH="${1:?usage: restore.sh <backup-dir>}"
DATA_DIR="${DATA_DIR:-./data}"
: "${DATABASE_URL:?DATABASE_URL is required}"

[ -f "${BACKUP_PATH}/db.dump" ] || { echo "no db.dump found in ${BACKUP_PATH}"; exit 1; }

printf 'This will OVERWRITE the database and ECG files. Type "yes" to continue: '
read -r confirm
[ "$confirm" = "yes" ] || { echo "aborted."; exit 1; }

echo "[restore] restoring database..."
pg_restore --clean --if-exists --no-owner --no-privileges \
  --dbname="$DATABASE_URL" "${BACKUP_PATH}/db.dump"

if [ -f "${BACKUP_PATH}/ecg.tar.gz" ]; then
  echo "[restore] restoring ECG files..."
  mkdir -p "$DATA_DIR"
  tar -xzf "${BACKUP_PATH}/ecg.tar.gz" -C "$DATA_DIR"
fi
if [ -f "${BACKUP_PATH}/ecg-quarantine.tar.gz" ]; then
  echo "[restore] restoring quarantine files..."
  mkdir -p "$DATA_DIR"
  tar -xzf "${BACKUP_PATH}/ecg-quarantine.tar.gz" -C "$DATA_DIR"
fi

echo "[restore] done."
