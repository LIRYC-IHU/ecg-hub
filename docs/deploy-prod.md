# Production deployment (Linux, host networking)

`docker-compose.yml` runs the production stack with **host networking**, which is
the recommended mode for ECG Hub: it binds every listener directly on the host
(no port mapping, no NAT), so the backend sees real client IPs and FTP passive
mode works without `PublicHost`/passive-range juggling.

> Host networking is Linux-only. On Docker Desktop (Mac/Windows) it is a no-op —
> use the bridge dev stack (`docker-compose.dev.yml`) for local work.

## Ports bound on the host

With host networking these listeners bind straight to the host — make sure they
are free and open them in the firewall as needed:

| Port | Service | Exposure |
|------|---------|----------|
| 80 / 443 | nginx (SPA + API + Swagger) | clients / clinicians |
| 4444 | Go backend HTTP | loopback only (nginx proxies it) |
| FTP control + passive `30000-30010` | FTP ingestion | ECG devices |
| 4242 | DICOM SCP | ECG/PACS devices |
| 30003 | ECTP (Nihon Kohden) | NK devices |
| 9091 | Prometheus metrics | scraper only — keep off the public interface (`METRICS_ENABLED`, `METRICS_PORT`) |

Bind the backend HTTP and metrics ports to a private interface via your firewall;
only 80/443 and the device ports need to face the network.

## Prerequisites

1. **TLS certificate** at `./certs/server.crt` + `./certs/server.key`
   (Let's Encrypt, internal PKI, or the hospital CA). nginx terminates TLS.
2. **`.env`** with production secrets:
   - `AUTH_ENCRYPTION_KEY` — strong, unique (the server refuses to start otherwise).
   - `DATABASE_URL` reachable **from the host**, e.g.
     `postgres://ecghub_user:...@127.0.0.1:5432/ecghub?sslmode=disable`
     (host networking → use `127.0.0.1`, not `host.docker.internal`).
   - `FTP_USERNAME` / `FTP_PASSWORD`, connector credentials, etc.
3. **PostgreSQL 16** — `docker-compose.yml` ships a `db` service that runs one
   on the host network (`127.0.0.1:5432`, credentials from `DB_USER` /
   `DB_PASSWORD` / `DB_NAME`). Comment that service out — along with the
   backend's `depends_on` and the `postgres-data` volume — when the site
   provides its own instance, and point `DATABASE_URL` at it. Either way, set up
   backups (see `docs/backup.md`).

### Injecting the environment

The backend service lists every variable it reads under `environment:` instead
of loading a file, so the values can come from a secret manager rather than from
a `.env` sitting on the host:

```sh
infisical run --env=prod -- docker compose -f docker-compose.yml up -d
```

Plain `docker compose` reads the same names from `.env` in the project
directory. A missing variable resolves to an empty string, and the server fails
fast on the ones that must not be empty.

### Non-Linux hosts

`network_mode: host` is a no-op on Docker Desktop (macOS, Windows). To run the
stack there, remove `network_mode: host` from `backend` and `frontend`,
uncomment the `ports:` blocks in `docker-compose.yml`, and switch nginx to the
bridge config (`nginx.conf`, upstream `backend:4444`). FTP passive mode then
needs the advertised host and the passive range set in Admin > Modules > FTP.

## Deploy

```sh
docker compose -f docker-compose.yml up -d --build
docker compose -f docker-compose.yml logs -f backend
```

Add metrics and/or scheduled backups by stacking the overlays:

```sh
docker compose -f docker-compose.yml -f docker-compose.metrics.yml \
  -f docker-compose.backup.yml --profile backup up -d --build
```

## FTP on the privileged port 21

The backend drops all Linux capabilities (`cap_drop: ALL`). Default FTP/DICOM/ECTP
ports are > 1024, so nothing extra is needed. **If a device requires FTP on port
21**, in `docker-compose.yml` uncomment on the `backend` service:

```yaml
    cap_add:
      - NET_BIND_SERVICE
```

and set the FTP port to `21` in Admin → Modules → FTP.

## After first start

- Open `https://<host>/` → complete the initial admin setup.
- Configure modules (FTP/DICOM), connectors, HL7, auth (OIDC/LDAP) from the admin UI.
- Verify a test ingestion end-to-end, and that an audit log + a backup are produced.

## Notes

- nginx uses `nginx.host.conf` (static upstream `127.0.0.1:4444`). The bridge
  variant `nginx.conf` (Docker DNS `backend:4444`) is kept for reference.
- `no-new-privileges` + `cap_drop: ALL` are applied to both services
  (`NET_BIND_SERVICE` added back to nginx for 80/443).
