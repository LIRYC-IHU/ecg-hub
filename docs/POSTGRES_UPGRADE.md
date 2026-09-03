# Upgrading PostgreSQL (16 → 18)

PostgreSQL cannot open a data directory written by a different major version.
Changing the image tag alone does not migrate anything: the new server finds no
cluster it can use, **initialises an empty one**, and the application starts
against a blank database while the old data sits untouched in the volume. The
stack looks healthy. Everything is gone from the UI.

The 18 images also changed where the cluster lives. It is now a
major-version subdirectory, so the mount moves up one level:

```yaml
# before (16)                          # after (18)
volumes:                               volumes:
  - postgres-data:/var/lib/postgresql/data   - postgres-data:/var/lib/postgresql
```

That is what makes a future `pg_upgrade --link` possible without crossing a
mount boundary. It also means a volume that already holds a 16 cluster at its
root cannot simply be remounted one level up — the upgrade goes through a dump.

## Procedure

Everything below runs from the directory holding `docker-compose.yml`. Replace
`$DB_USER` with the value from your `.env`.

### 1. Dump, with the old image still running

```bash
docker compose exec -T db pg_dumpall -U "$DB_USER" > ecghub-pg16-$(date +%F).sql
```

Check the dump before going any further — a truncated dump discovered after the
volume is gone is not a backup:

```bash
ls -lh ecghub-pg16-*.sql          # megabytes, not bytes
tail -3 ecghub-pg16-*.sql         # ends with "PostgreSQL database dump complete"
grep -c "CREATE TABLE" ecghub-pg16-*.sql
```

### 2. Stop the stack and put the old volume out of reach

```bash
docker compose down               # NOT `down -v` — that deletes the volume
docker volume ls | grep postgres
```

Keep the old volume until the new database has been checked. It is the only
rollback that exists.

### 3. Start 18 on a fresh volume

Switch the image tag and the mount path (see above), point the service at a
**new** volume name, then:

```bash
docker compose up -d db
docker compose logs -f db         # wait for "database system is ready"
```

### 4. Restore

```bash
cat ecghub-pg16-*.sql | docker compose exec -T db psql -U "$DB_USER" -d postgres
```

### 5. Verify before starting the application

```bash
docker compose exec -T db psql -U "$DB_USER" -d ecghub -c \
  "select (select count(*) from patients) patients,
          (select count(*) from ecgs) ecgs,
          (select count(*) from ecg_hub_users) users,
          (select count(*) from roles) roles"
```

Those numbers must match what the old database held. Only then:

```bash
docker compose up -d
```

Keep the old volume for a few days. Delete it deliberately, never as part of a
`down -v`.

## If the empty cluster is already running

The old data is either still in the volume (under `data/`, alongside the new
`18/`) or in a dangling volume:

```bash
docker volume ls -qf dangling=true | while read v; do
  echo "== $v"
  docker run --rm -v "$v:/d" alpine sh -c \
    'for p in /d/PG_VERSION /d/data/PG_VERSION; do [ -f $p ] && echo "  $p = $(cat $p)"; done; du -sh /d'
done
```

A volume reporting `PG_VERSION = 16` is the old cluster. Mount it read-only in a
`postgres:16-alpine` container, dump it, and resume at step 4:

```bash
docker run --rm -v <old-volume>:/var/lib/postgresql/data -e POSTGRES_PASSWORD=x \
  --name pg16-rescue -d postgres:16-alpine
docker exec -t pg16-rescue pg_dumpall -U "$DB_USER" > rescue.sql
docker rm -f pg16-rescue
```

If no volume holds a 16 cluster, the database is gone. ECG **files** live on a
separate volume and are unaffected: re-ingesting them through Uploads rebuilds
the patients and ECG rows from the files themselves. Settings — users, roles,
modules, connectors, webhooks, API keys — live only in the database and have to
be entered again.

## Avoiding this

Scheduled dumps are the database maintainers' responsibility — this project
ships no backup tooling. Two constraints to hand them: `pg_dump` refuses to
talk to a newer major version, so its client has to keep step with the server;
and the dump has to be taken at the same point as the ECG volumes, since
metadata without its files restores to rows pointing at nothing.
