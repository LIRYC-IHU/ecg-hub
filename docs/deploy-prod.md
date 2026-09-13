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

## Patient identifier domain — read before deploying

ECG Hub attaches an ECG to a patient on the **patient identifier alone**. An ECG
arrives carrying an identifier, that identifier keys an HL7 query to the HIS,
and the demographics come back from the HIS. Nothing matches on name, date of
birth or sex — deliberately, because demographic matching is how the wrong
patient gets a trace.

That design has one prerequisite, and it is on the installation rather than on
the software:

> **The patient identifier must be unique across everything this instance
> serves.**

The `patients.patient_id` column is globally unique. If two sites can issue the
same number for two different people, the second ECG attaches to the first
person's record — silently, with no mismatch to detect, because there are no
demographics being compared. There is no assigning-authority column to separate
them.

So, before deploying across more than one site, establish which of these holds:

- **One identifier domain across all sites.** Deploy one instance. (AP-HP is
  this case: patient identifiers are unique across the whole institution, not
  per site.)
- **Per-site identifier domains.** Deploy one instance per domain, or prefix the
  identifier at ingestion so the combined value is unique. Do not point two
  domains at one instance.

If you cannot state which case you are in, stop and find out. This is the most
plausible severe-harm path in the system.

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

Already handled **on the bridge network**. Under `network_mode: host` there are
no port mappings and this does not apply — see the next section.

The compose file publishes `${FTP_PORT:-21}:2121`: dockerd runs
as root and performs the privileged bind, while the backend keeps binding 2121
as a non-root user inside the container.

Leave the FTP port in Admin > Modules > FTP at **2121** — that is the container
side of the mapping, which is what the module binds. Set **Public port** to 21
so the page shows devices the port they should dial.

Do not add `cap_add: NET_BIND_SERVICE`, and do not `setcap` the binary: a
capability the bounding set cannot grant makes `execve` itself fail, and the
container crash-loops with `exec /app/ecg-hub: operation not permitted` before
running a line of code.

## Host networking, and the device whitelist

The bridge deployment above cannot identify devices. A container on a bridge
network has its own network namespace, so `/proc/net/arp` inside it lists the
other containers and never the hardware on the site network — and every device
connection arrives from the Docker gateway, so they all look like one address.
The MAC-address whitelist (Admin > Devices) then identifies nothing and, failing
open, lets every device through.

Two ways out. Either mount the host's neighbour tables read-only and keep the
bridge:

```yaml
backend:
  volumes:
    - /proc/net/arp:/host/proc/net/arp:ro
    - /proc/net/route:/host/proc/net/route:ro
  environment:
    HOST_PROC_NET: /host/proc/net
```

or run the whole stack with `network_mode: host`, where the backend shares the
host's namespace and sees both the real client addresses and the real ARP table
with no extra mount. `HOST_PROC_NET` is then unnecessary. `docker-compose.host.yml`
is that variant:

```sh
docker compose -f docker-compose.host.yml up -d
```

It differs from the bridge file in four places, and each is forced:

- `backend` and `frontend` take `network_mode: host`. nginx then reaches the
  API on loopback, which is why it mounts `nginx/nginx.host.conf` instead —
  the same file with `127.0.0.1:4444` as the upstream and no Docker resolver.
- `db` stays on its own bridge network, published on `127.0.0.1:5432`. Point
  `DATABASE_URL` there rather than at `db`: there is no Docker DNS in the host
  namespace.
- `SERVER_HOST` and `METRICS_HOST` are set to `127.0.0.1`. Without the port
  mappings, an unset bind address puts the API on the site network past nginx,
  and publishes the unauthenticated metrics endpoint with it.
- Nothing publishes ports, so nothing binds 21 — see below.

Host networking changes three things in this document:

- **There are no port mappings.** `${FTP_PORT:-21}:2121` is ignored, so nothing
  performs the privileged bind on 21 any more — see below.
- **`PublicHost` is no longer required.** The server sees the host's own
  address and advertises it correctly in PASV.
- **`DOCKER-USER` no longer applies.** With no DNAT there is nothing to filter
  there; the rules belong in `INPUT`, where `ufw` works normally again.
- **What used to confine a port is gone.** On the bridge network the compose
  file publishes `127.0.0.1:9091` for metrics and does not publish 4444 at all;
  in the host namespace both are as reachable as the process binds them. That
  is what `SERVER_HOST` and `METRICS_HOST` are for.

### Port 21 without a port mapping

The backend runs as a non-root user under `cap_drop: ALL`, so it binds 2121 and
nothing else. Redirect in the kernel:

```sh
iptables -t nat -A PREROUTING -p tcp --dport 21 -j REDIRECT --to-port 2121
```

Persist it (`iptables-persistent`, an nftables rule, or a systemd unit) or it is
gone at the next reboot. `PREROUTING` covers traffic arriving on an interface
but not the host talking to its own address — add the matching `OUTPUT` rule if
you want `ftp localhost 21` to work for testing.

`make ftp-ports` does this and persists it, and is safe to run twice — it
collapses the rule to exactly one whatever it starts from. Applying it by hand
and then again after a reboot is how a chain ends up with two copies, which
nothing complains about and nobody sees. `make ftp-ports-check` reports without
changing anything.

The alternative is no NAT at all:

```sh
sysctl -w net.ipv4.ip_unprivileged_port_start=21   # persist in /etc/sysctl.d/
```

and set the FTP port to 21 in Admin > Modules. Simpler, but it lowers the
privileged-port floor for every process on the host, not just this one.

**Do not** bridge 21 to 2121 with `socat`, `haproxy` or an nginx `stream` block.
Those terminate the connection and open a new one, so every device arrives from
the host itself: the whitelist sees a single identity for the whole site, and
approving one device approves all of them. `REDIRECT` and `DNAT` rewrite only
the destination and leave the source address — which is what the whitelist reads
— untouched.

## FTP passive mode

`PublicHost` in Admin > Modules > FTP is **required**. On a bridge network the
server would otherwise advertise its `172.x` address in PASV, which no external
client can route, and every transfer would reset right after `227`.

The passive range width is the concurrent-transfer ceiling — one port is held
per in-flight data connection. Measured on the previous 11-port range, uploads
began timing out at 10 in parallel. The published 30000-30100 gives 101.

The range spans ECTP's 30003 without harm: ftpserverlib retries the next port
when a bind fails, so a data connection that draws it moves on.

Forward the same range on every router and firewall between the devices and
this host, and keep it clear of 30003 (ECTP).

**It will not open itself.** A NAT helper (`nf_conntrack_ftp`) normally reads
the `227 Entering Passive Mode` reply on the control channel and opens the data
port on demand — which is why plain FTP through a home router usually just
works. Enable FTPS and that reply is encrypted: the helper sees nothing and
opens nothing. The control port keeps working because it is forwarded
explicitly, so the symptom is a session that logs in, answers `PWD` and `PASV`,
and then hangs for twenty seconds on `MLSD` or the first transfer. Nothing in
the server logs says why — from its side the connection simply never arrives.

Measured on the pilot, 2026-09-10: with a listener up on 30099, port 21 from
outside connected and 30099 timed out. Two of those tests are the whole
diagnosis.

## Firewall

`ufw` does **not** filter the published ports: Docker's DNAT rules run before
the INPUT chain. Filtering belongs in `DOCKER-USER`, e.g.

```sh
iptables -I DOCKER-USER -i eth0 ! -s 10.0.0.0/8 -p tcp --dport 21 -j DROP
```

Note also that on a bridge network the backend sees the Docker gateway address
rather than the real client IP, so FTP logs and anything keyed on client IP
reflect that. Under `network_mode: host` it sees the real addresses.

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
