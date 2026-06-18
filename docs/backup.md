# Backup & restore

ECG Hub stores clinical data in two places — **both must be backed up together**:

1. **PostgreSQL** — patients, ECG metadata, audit log, module/connector config, users/roles.
2. **ECG file volumes** — the raw stored ECGs (`data/ecg`) and quarantined files (`data/ecg-quarantine`).

A database backup alone is **not** a valid backup: the DB references files on disk. The
tooling here captures both in one timestamped folder.

> ⚠️ Medical data. Keep at least one **off-site / off-host** copy, and verify restores
> periodically. A backup you have never restored is a hypothesis, not a backup.

## What you get

`scripts/backup.sh` writes to `./backups/<timestamp>/`:

| File | Content |
|------|---------|
| `db.dump` | `pg_dump` custom format (restore with `pg_restore`) |
| `ecg.tar.gz` | `data/ecg` |
| `ecg-quarantine.tar.gz` | `data/ecg-quarantine` |
| `manifest.txt` | creation time + file list |

`./backups/` is git-ignored — backups are never committed.

## Manual backup (from the host)

Requires `pg_dump` (the `postgresql` client package) on the host.

```sh
# DATABASE_URL must point at the DB as reachable from the host.
# From the host this is usually localhost, not host.docker.internal.
export DATABASE_URL="postgres://ecghub_user:admin@localhost:5432/ecghub?sslmode=disable"
sh scripts/backup.sh
```

Config (env, all optional except `DATABASE_URL`):

| Var | Default | Meaning |
|-----|---------|---------|
| `DATABASE_URL` | — | DB connection string (required) |
| `BACKUP_DIR` | `./backups` | output directory |
| `DATA_DIR` | `./data` | parent of `ecg/` + `ecg-quarantine/` |
| `RETENTION_DAYS` | `14` | delete backups older than N days (`0` = keep all) |

## Scheduled backup (optional service)

An opt-in compose service runs the backup on a loop. It is **profile-gated**, so it never
starts unless you ask for it:

```sh
docker compose -f docker-compose.dev.yml -f docker-compose.backup.yml --profile backup up -d
```

Tune via env (e.g. in `.env`):

| Var | Default | Meaning |
|-----|---------|---------|
| `BACKUP_INTERVAL` | `86400` | seconds between backups (86400 = 24h) |
| `BACKUP_RETENTION_DAYS` | `14` | retention for the service |

The service uses `DATABASE_URL` from `.env`. Inside a container, `host.docker.internal`
resolves to the host (the compose file maps it on Linux too). If you run Postgres **as a
compose service** instead, point `DATABASE_URL` at `db:5432`.

## Restore

> 🔴 Destructive: overwrites the current database and ECG files. Stop the backend first.

```sh
export DATABASE_URL="postgres://ecghub_user:admin@localhost:5432/ecghub?sslmode=disable"
sh scripts/restore.sh ./backups/20260618-103749
# type "yes" at the prompt
```

`restore.sh` runs `pg_restore --clean --if-exists` (drops and recreates objects) and
extracts the ECG archives back into `DATA_DIR`.

To inspect a dump without restoring:

```sh
pg_restore --list ./backups/<timestamp>/db.dump | head
```

## Production checklist

- [ ] Off-site copy of `./backups` (rsync/object storage), encrypted in transit & at rest.
- [ ] `pg_dump`/`pg_restore` **major version matches the server** (the optional service
      pins `postgres:16-alpine` — change it if your server is not PG 16).
- [ ] A restore has been performed on a staging copy and the app verified.
- [ ] Retention aligned with your data-retention / RGPD policy.
- [ ] Backups monitored (alert if no successful backup in N hours).
