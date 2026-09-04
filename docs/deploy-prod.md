# Production deployment

`docker-compose.yml` runs the production stack on a **bridge network**, publishing
only the ports devices and clients need. Containers reach each other by service
name (`db`, `backend`), so nothing depends on the host's network layout.

## Published ports

| Host port | Service | Exposure |
|-----------|---------|----------|
| 80 | nginx (SPA + API) | clients / clinicians |
| `${FTP_PORT:-21}` → 2121 | FTP control | ECG devices |
| 30000-30100 | FTP passive range | ECG devices |
| 4242 | DICOM C-STORE SCP | ECG / PACS devices |
| 30003 | ECTP (Nihon Kohden) | NK devices |
| 127.0.0.1:9091 | Prometheus metrics | scraper only |

The backend's HTTP port (4444) is **not published**: nginx reaches it over the
Docker network. PostgreSQL is not published either — only `backend` talks to it.

TLS for the web UI is terminated upstream (an external Traefik forwards plain
HTTP to port 80). The certificates mounted at `/certs` are for **FTPS and DICOM
TLS**, not for nginx.

Three things must agree or a port silently goes nowhere: the container side of
each mapping above, the value in Admin > Modules, and the firewall or router
rule in front. The FTP port is the exception — see below.

## Prerequisites

1. **Certificates for FTPS and DICOM TLS** in `./certs`, named `fullchain.pem`
   and `privkey.pem` (certbot's own names, so mounting
   `/etc/letsencrypt/live/<host>` there needs no configuration). Override with
   `TLS_CERT_FILE` / `TLS_KEY_FILE`. Enabling TLS on a module without a usable
   certificate is refused at start-up with the reason stated, rather than
   accepted and then failing every client.

   certbot writes `live/` as symlinks into `archive/`, root-owned and `0700`.
   A container running as uid 10001 cannot follow them, so copy the pair out in
   a deploy hook and chown it to that uid rather than mounting `live/` directly.
   `docs/TLS_CERTS.md` has the hook and the renewal details.

2. **`.env`** (or a secret manager) with:
   - `DATABASE_URL` — reachable **from inside the network**, so the service
     name, not the loopback:
     `postgres://ecghub_user:...@db:5432/ecghub?sslmode=disable`
   - `JWT_SECRET`, `AUTH_ENCRYPTION_KEY` — the server refuses to start on weak
     or placeholder values unless `APP_ENV=development`.
   - `DB_USER`, `DB_PASSWORD`, `DB_NAME` when using the bundled database.

3. **PostgreSQL 18.** `docker-compose.yml` ships a `db` service. Comment it out —
   along with the backend's `depends_on` and the `postgres-data` volume — when
   the site provides its own instance, and point `DATABASE_URL` at it. Either
   way, backups are the database maintainers' responsibility. Whatever they run
   has to capture the database and the ECG volumes at the same point: metadata
   without its files restores to rows pointing at nothing.

   Upgrading an existing install: 18 cannot open a 16 cluster and initialises an
   empty one instead, so the stack comes up healthy against a blank database
   while the old data sits untouched in the volume. Dump first —
   `docs/POSTGRES_UPGRADE.md`.

### Injecting the environment

The backend lists every variable it reads under `environment:` rather than
loading a file, so values can come from a secret manager:

```sh
infisical run --env=prod -- docker compose up -d
```

Plain `docker compose` reads the same names from `.env`. A missing variable
resolves to an empty string, and the server fails fast on the ones that must not
be empty.

## Deploy

```sh
docker compose up -d --build
docker compose logs -f backend
```

Changing the network mode or a published port needs `docker compose down` first;
those cannot be applied to a running stack.

## FTP on the privileged port 21

Already handled. The compose file publishes `${FTP_PORT:-21}:2121`: dockerd runs
as root and performs the privileged bind, while the backend keeps binding 2121
as a non-root user inside the container.

Leave the FTP port in Admin > Modules > FTP at **2121** — that is the container
side of the mapping, which is what the module binds. Set **Public port** to 21
so the page shows devices the port they should dial.

Do not add `cap_add: NET_BIND_SERVICE`, and do not `setcap` the binary: a
capability the bounding set cannot grant makes `execve` itself fail, and the
container crash-loops with `exec /app/ecg-hub: operation not permitted` before
running a line of code.

## FTP passive mode

`PublicHost` in Admin > Modules > FTP is **required**. On a bridge network the
server would otherwise advertise its `172.x` address in PASV, which no external
client can route, and every transfer would reset right after `227`.

The passive range width is the concurrent-transfer ceiling — one port is held
per in-flight data connection. Measured on the previous 11-port range, uploads
began timing out at 10 in parallel. The published 30000-30100 gives 101.

The range spans ECTP's 30003 without harm: ftpserverlib retries the next port
when a bind fails, so a data connection that draws it moves on.

Forward the same range on the router, and keep it clear of 30003 (ECTP).

## Firewall

`ufw` does **not** filter the published ports: Docker's DNAT rules run before
the INPUT chain. Filtering belongs in `DOCKER-USER`, e.g.

```sh
iptables -I DOCKER-USER -i eth0 ! -s 10.0.0.0/8 -p tcp --dport 21 -j DROP
```

Note also that the backend sees the Docker gateway address rather than the real
client IP, so FTP logs and anything keyed on client IP reflect that.

The FTP module throttles failed logins — a delay that grows with the count, per
source address, exported as `ftp_auth_failures_total`. It does not lock an
address out, precisely because of the shared gateway address above: every device
would sit in one bucket and a scanner could take ingestion offline. The throttle
makes a password sweep pointless; it does not make a publicly reachable FTP port
a good idea. Keep the port on the clinical network or behind the `DOCKER-USER`
rule above.

## After first start

- Open `https://<host>/` → complete the initial admin setup at `/setup`.
- Configure modules (FTP/DICOM), connectors, HL7 and auth (OIDC/LDAP) from the
  admin UI. Everything operational lives in the database, not in `config.yaml`.
- Verify a test ingestion end-to-end and that an audit entry is written.

## Notes

- `no-new-privileges` and `cap_drop: ALL` apply to both services; nginx gets
  back the capabilities it needs to bind port 80 and drop to its own user.
- Object storage is optional (`STORAGE_BACKEND=s3`). The local volume then acts
  as an upload spool, so it must stay mounted and sized for the longest bucket
  outage worth riding out.
