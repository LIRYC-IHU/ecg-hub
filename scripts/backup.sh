#!/bin/sh
# ECG Hub backup — database and ECG files, captured together.
#
# Restore with scripts/restore.sh <backup-dir>.
#
# The database and the files are one record: metadata without its files restores
# to rows pointing at nothing, and files without their rows are unreachable. The
# dump is taken first and the files second, so a file ingested mid-run is present
# on disk without a row — recoverable (it lands in the unidentified queue on
# re-ingestion). The reverse order loses the file outright.
#
# Usage: scripts/backup.sh [backup-root]      default ./backups
#
# Config via env:
#   DATABASE_URL   source database (required)
#   DATA_DIR       parent of ecg/ + ecg-quarantine/ (default ./data)
#   KEEP           number of backups to retain, 0 keeps all (default 14)
set -eu

BACKUP_ROOT="${1:-./backups}"
DATA_DIR="${DATA_DIR:-./data}"
KEEP="${KEEP:-14}"
: "${DATABASE_URL:?DATABASE_URL is required}"

STAMP=$(date -u +%Y%m%d-%H%M%S)
DEST="${BACKUP_ROOT}/${STAMP}"
mkdir -p "$DEST"

# A partial backup must not look like a complete one: the directory is only
# renamed into place once every step has succeeded.
TMP="${DEST}.incomplete"
rm -rf "$TMP"
mv "$DEST" "$TMP"

echo "[backup] dumping database..."
pg_dump --format=custom --no-owner --no-privileges \
  --file="${TMP}/db.dump" "$DATABASE_URL"

for vol in ecg ecg-quarantine; do
  if [ -d "${DATA_DIR}/${vol}" ]; then
    echo "[backup] archiving ${vol}..."
    tar -czf "${TMP}/${vol}.tar.gz" -C "$DATA_DIR" "$vol"
  else
    echo "[backup] ${DATA_DIR}/${vol} absent, skipped"
  fi
done

mv "$TMP" "$DEST"
echo "[backup] done: ${DEST} ($(du -sh "$DEST" | cut -f1))"

# Rotation. Only complete backups are counted, so a failed run never evicts a
# good one.
if [ "$KEEP" -gt 0 ]; then
  ls -1d "${BACKUP_ROOT}"/*/ 2>/dev/null \
    | grep -v '\.incomplete/$' \
    | sort -r \
    | tail -n "+$((KEEP + 1))" \
    | while read -r old; do
        echo "[backup] rotating out ${old}"
        rm -rf "$old"
      done
fi
