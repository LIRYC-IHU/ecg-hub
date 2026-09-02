# ECG Hub

**Vendor-neutral ECG ingestion and management hub for hospitals.**

ECG Hub sits between bedside ECG devices (Philips, GE, Nihon Kohden, DICOM
modalities…) and the hospital IT system. Devices push their recordings to the
hub over the protocols they already speak (FTP, DICOM C-STORE, ECTP); the hub
parses every vendor format into a common model, enriches it with patient
identity from the HIS via HL7, stores the original files safely, and gives
clinicians a modern web UI to browse, view and export ECGs — while optionally
forwarding a copy to an external PACS.

> ⚠️ **Non-diagnostic use.** ECG Hub is a data-management and visualisation
> tool. It is not a medical device and must not be used as the basis for
> diagnosis.

---

## Contributing & security

Bug reports and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).
Found a vulnerability? Do not open an issue: follow [SECURITY.md](SECURITY.md).

## How it works

```
 ECG devices                        ECG Hub                             Hospital IT
┌────────────┐   FTP :2121   ┌───────────────────────────────────┐
│ Philips    │──────────────▶│ Ingestion pipeline                │  HL7 QRY/ADT :2575
│ GE MUSE    │  DICOM :4242  │  • vendor module parses the file  │◀───────────────────▶ HIS / CommServer
│ Nihon K.   │──────────────▶│  • metadata → PostgreSQL          │
│ DICOM      │   ECTP        │  • original file → volume         │  Webhooks (HTTP POST)
└────────────┘──────────────▶│  • no patient ID? → unidentified  │───────────────────▶ external systems
                             │  • unparseable?   → quarantine    │
                             │                                   │  Proxy connectors
                             │ Web UI + REST API (:4444)         │───────────────────▶ PACS (DICOM C-STORE,
                             └───────────────────────────────────┘                      ECTP/FTP forward)
```

1. **Ingestion** — built-in FTP server, DICOM C-STORE SCP and ECTP listener
   receive files. A router matches each file to a **vendor module** (Philips,
   GE, Nihon Kohden, DICOM, …) which validates and parses it. Modules can be
   hot-started/stopped from the admin UI; remote gRPC modules running as
   separate containers are supported (EPIC-010, in progress under `modules/`).
2. **Storage** — original files land on a dedicated volume (`/data/ecg`),
   metadata in PostgreSQL. Files that cannot be parsed go to a separate
   **quarantine** volume for manual review. A janitor watches a soft size cap
   (`storage.max_size`) and _never_ deletes clinical files unless rotation is
   explicitly enabled.
3. **Patient identity** — ECGs arriving without a usable patient ID enter the
   **unidentified** workflow: an operator assigns them to a patient (guarded
   by name + DOB + ID cross-checks) and the file is re-ingested. An HL7
   scheduler queries the HIS to enrich pending ECGs, with retries and an
   exhausted state, all visible in the UI.
4. **Distribution** — outbound **proxy connectors** forward a copy of incoming
   ECGs to external systems (PACS via DICOM C-STORE, ECTP/FTP endpoints) with
   retries; **webhooks** notify third parties after identification; batch
   **export** produces ZIPs in the formats each module can convert to
   (PDF/XML/native). WebSocket events push ingestion updates to the UI live.
5. **Security & audit** — local, OIDC and LDAP auth providers, role-based
   permissions, API keys, and a full audit trail. All provider/module
   credentials are stored AES-256-GCM-encrypted in the database.

## The web UI

| Area                                             | What you do there                                                                                                    |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| `/setup`                                         | First-launch creation of the local admin account                                                                     |
| `/login`                                         | Local / OIDC / LDAP sign-in                                                                                          |
| `/` Patients                                     | Master–detail patient list → ECG list → interactive waveform viewer (WebGL), PDF/format download, HL7 status per ECG |
| `/uploads`                                       | Manual file upload and recent arrivals                                                                               |
| `/quarantine`                                    | Review quarantined + unidentified files, assign to patients, re-ingest                                               |
| `/modules-config`                                | Enable/disable vendor modules and ingestion servers (FTP, DICOM), hot control, per-module settings                   |
| `/hl7`                                           | HL7 connection settings, pending/exhausted queues, retry policy                                                      |
| `/webhooks`, `/api-keys`                         | Outbound integrations                                                                                                |
| `/users`, `/app-users`, `/roles`, `/auth-config` | Accounts, roles/permissions, auth providers                                                                          |
| `/audit`                                         | Who did what, when                                                                                                   |
| `/system`                                        | Storage usage, runtime status, branding                                                                              |

Everything operational — auth providers, modules, FTP/DICOM/HL7 settings,
connectors, webhooks — is configured **from the UI and stored in the
database**. `config.yaml` only carries infrastructure bootstrap (see below).

## Repository layout

```
backend/            Go backend (Echo + GORM + PostgreSQL)
  cmd/ecg-hub/      entrypoint
  internal/         api, auth, ingestion, module, hl7, dicom, connector,
                    export, storage, metrics, webhook, events, …
frontend/           React 19 + TypeScript + Vite + Tailwind 4 + TanStack Query
  src/pages/        Login / Setup
  src/components/   patient, ecg, admin, uploads, layout, ui
  src/ecg-viewer/   WebGL waveform viewer
modules/            remote gRPC vendor modules (EPIC-010, WIP)
proto/              gRPC contracts for remote modules
docs/               deploy-prod.md, backup.md, epics/, grafana/
scripts/            backup / restore tooling
```

## Configuration

Three layers, by design:

1. **`.env` — secrets & bootstrap** (never committed). Copy from
   `.env.example`. Required: `DATABASE_URL`, `JWT_SECRET`,
   `AUTH_ENCRYPTION_KEY`, `APP_ENV`. Production is the **default**: the server
   refuses to start with weak/placeholder secrets unless
   `APP_ENV=development`.
2. **`config.yaml` — infrastructure only.** Copy from `config.example.yaml`.
   ```yaml
   server: { tls: false } # TLS here only for bare-metal; behind nginx keep false
   database: { max_open_conns: 10, max_idle_conns: 5 }
   storage:
     {
       volume_path: /data/ecg,
       quarantine_path: /data/ecg-quarantine,
       max_size: 50Gi,
       allow_rotation: false,
     }
   export: { workers: 2, tmp_ttl: 2h }
   metrics: { enabled: true, port: 9091 } # dedicated Prometheus scrape port
   ```

   The listen port is fixed at 4444 (Dockerfile, nginx and compose all assume
   it), and three knobs come from the environment instead of this file:
   `METRICS_ENABLED`, `METRICS_PORT` and `WEBHOOKS_RETENTION_DAYS` — a container
   can move the scrape endpoint or change delivery retention without templating
   a mounted file.
3. **Database (via the admin UI)** — everything else: auth providers, modules,
   FTP/DICOM/HL7, connectors, webhooks. Hot-reloaded, no restart needed.

## Setup

### Prerequisites

- Docker + Docker Compose (prod & dev stacks)
- Go ≥ 1.26 and Node ≥ 20 only for local (non-Docker) development
- A reachable PostgreSQL (the compose stacks expect an external DB —
  see `DATABASE_URL`; helper scripts in `backend/createDATABAE.sh`)

### Quick start (dev)

```bash
make init          # creates .env and config.yaml from the examples
# edit .env: DATABASE_URL, JWT_SECRET, AUTH_ENCRYPTION_KEY, APP_ENV=development
make dev           # bridge stack: backend (air hot-reload) + frontend (vite) + nginx
```

Open the app, go to **`/setup`** to create the local admin account, then
configure modules/HL7/auth from the admin pages.

### Production

```bash
make docker        # docker compose up -d — host networking (Linux only)
```

Host networking is deliberate: FTP passive mode and real client IPs work
without NAT juggling. Read **`docs/deploy-prod.md`** before deploying (ports
bound on the host, TLS/Traefik notes) and **`docs/backup.md`** — PostgreSQL
and the ECG volumes must be backed up _together_ (`docker-compose.backup.yml`,
`scripts/restore.sh`).

Default ports: API `4444` · FTP `2121` (+ passive `30100-30199`) · HL7 `2575`
· DICOM `4242` · metrics `9091`.

### Observability (optional overlay)

```bash
docker compose -f docker-compose.yml -f docker-compose.metrics.yml up -d
```

Adds Prometheus (`:9090`), Grafana (`:3000`, admin/admin, dashboard
auto-provisioned from `docs/grafana/`), cAdvisor and node-exporter. All four
bind `127.0.0.1` only — cAdvisor and node-exporter expose the whole host with
no authentication — so reach them over an SSH tunnel:

```bash
ssh -L 3000:localhost:3000 -L 9090:localhost:9090 <host>
```

`METRICS_ENABLED=true` is required on the backend, or `:9091` serves nothing.

To size a deployment, `backend/loadtest/capacity-probe.sh` reports the CPU cost
of a single ECG — the number that extrapolates to a daily volume. It reads
`/metrics` directly and needs none of the overlay above:

```bash
METRICS=http://<host>:9091/metrics ./loadtest/capacity-probe.sh \
  'JOBS=8 ./loadtest/ftp-stress.sh /path/to/ecgs 2121 <host>'
```

The metrics port publishes storage volumes, queue depths and module health with
no authentication. Keep it on the loopback or the management network; reaching
it from a workstation is an SSH tunnel, not a published port. The
backend exposes ~40 application metrics (ingestion, modules, HL7, DICOM,
connectors, storage, export, HTTP/DB) on the dedicated metrics port —
controlled by the `metrics:` section of `config.yaml`.

## Development notes

- `make` builds backend + frontend locally; `make swagger` regenerates the
  OpenAPI docs; `make clean` / `make fclean` / `make re` do what they say.
- Backend tests: `cd backend && go test ./...`
- Roadmap and design history live in `docs/epics/` (connector pack, metrics,
  DICOM proxy, UX redesign, HL7 hardening, auth refactor, gRPC modules,
  input-validation & security hardening, …).
